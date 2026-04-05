package subscription_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
)

func testOpts() fetch.Options {
	return fetch.Options{Retries: 1, Timeout: 5e9, Parallelism: 2}
}

// --- isValidDomain ---

func TestIsValidDomain(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"example.com", true},
		{"sub.example.com", true},
		{"localhost", true},
		{"xn--nxasmq6b.com", true},
		{"a-b.example.com", true},
		{"", false},
		{"has space.com", false},
		{"-example.com", false},
		{"example-.com", false},
		{"example..com", false},
		{"example.com.", false}, // trailing dot produces empty label
		{"a" + string(rune(0)) + "b", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := subscription.IsValidDomain(tc.input)
			if got != tc.want {
				t.Errorf("IsValidDomain(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// --- isValidCIDR ---

func TestIsValidCIDR(t *testing.T) {
	cases := []struct {
		input string
		want  bool
	}{
		{"10.0.0.0/8", true},
		{"192.168.1.0/24", true},
		{"2001:db8::/32", true},
		{"192.0.2.1", true}, // plain IP
		{"::1", true},       // IPv6 plain
		{"", false},
		{"not-an-ip", false},
		{"10.0.0.0/33", false}, // prefix too long
		{"256.0.0.0/8", false},
		{"10.0.0.0/-1", false},
	}
	for _, tc := range cases {
		t.Run(tc.input, func(t *testing.T) {
			got := subscription.IsValidCIDR(tc.input)
			if got != tc.want {
				t.Errorf("IsValidCIDR(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// --- FetchRouteEntries ---

func TestFetchRouteEntries_NilRoute(t *testing.T) {
	result := subscription.FetchRouteEntries(context.Background(), "sub1", nil, testOpts())
	if result != nil {
		t.Errorf("expected nil for nil route, got %+v", result)
	}
}

func TestFetchRouteEntries_NoURLs(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"10.0.0.0/8"},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Domains) != 1 || result.Domains[0] != "example.com" {
		t.Errorf("Domains: got %v, want [example.com]", result.Domains)
	}
	if len(result.IPCIDRs) != 1 || result.IPCIDRs[0] != "10.0.0.0/8" {
		t.Errorf("IPCIDRs: got %v, want [10.0.0.0/8]", result.IPCIDRs)
	}
}

func TestFetchRouteEntries_DomainURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("# comment\ndl.example.com\nother.example.com\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		Domains:    []string{"manual.example.com"},
		DomainURLs: []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should have manual + downloaded, deduped and sorted.
	want := map[string]bool{
		"manual.example.com": true,
		"dl.example.com":     true,
		"other.example.com":  true,
	}
	if len(result.Domains) != len(want) {
		t.Errorf("Domains count: got %d want %d: %v", len(result.Domains), len(want), result.Domains)
	}
	for _, d := range result.Domains {
		if !want[d] {
			t.Errorf("unexpected domain %q", d)
		}
	}
}

func TestFetchRouteEntries_IPURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("203.0.113.0/24\n198.51.100.0/24\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		IPCIDRs: []string{"10.0.0.0/8"},
		IPURLs:  []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	want := map[string]bool{
		"10.0.0.0/8":      true,
		"203.0.113.0/24":  true,
		"198.51.100.0/24": true,
	}
	if len(result.IPCIDRs) != len(want) {
		t.Errorf("IPCIDRs count: got %d want %d: %v", len(result.IPCIDRs), len(want), result.IPCIDRs)
	}
	for _, c := range result.IPCIDRs {
		if !want[c] {
			t.Errorf("unexpected CIDR %q", c)
		}
	}
}

// TestFetchRouteEntries_InvalidEntriesFiltered checks that invalid entries in
// downloaded lists are silently skipped while valid entries pass through.
func TestFetchRouteEntries_InvalidEntriesFiltered(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("good.example.com\n-invalid.com\nhas space.com\nalso.good.com\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		DomainURLs: []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	want := map[string]bool{
		"good.example.com": true,
		"also.good.com":    true,
	}
	if len(result.Domains) != len(want) {
		t.Errorf("Domains count: got %d want %d: %v", len(result.Domains), len(want), result.Domains)
	}
	for _, d := range result.Domains {
		if !want[d] {
			t.Errorf("unexpected domain %q", d)
		}
	}
}

// TestFetchRouteEntries_Deduplication checks that duplicate entries across manual
// config and downloaded lists are removed.
func TestFetchRouteEntries_Deduplication(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// "shared.example.com" appears in both manual and downloaded.
		_, _ = w.Write([]byte("shared.example.com\nnew.example.com\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		Domains:    []string{"shared.example.com", "manual.example.com"},
		DomainURLs: []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// "shared.example.com" must appear exactly once.
	count := 0
	for _, d := range result.Domains {
		if d == "shared.example.com" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("shared.example.com appears %d times, want 1", count)
	}
	// Total should be 3 unique entries.
	if len(result.Domains) != 3 {
		t.Errorf("Domains count: got %d want 3: %v", len(result.Domains), result.Domains)
	}
}

// TestFetchRouteEntries_ManualWins verifies that when a manual entry and a
// downloaded entry are identical, the entry is preserved (dedup keeps first
// occurrence, which is the manual entry).
func TestFetchRouteEntries_ManualWins(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("10.0.0.0/8\n192.168.0.0/16\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		IPCIDRs: []string{"10.0.0.0/8"}, // same as in downloaded list
		IPURLs:  []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// "10.0.0.0/8" must appear exactly once.
	count := 0
	for _, c := range result.IPCIDRs {
		if c == "10.0.0.0/8" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("10.0.0.0/8 appears %d times, want 1", count)
	}
	// Total: 10.0.0.0/8 + 192.168.0.0/16 = 2 unique entries.
	if len(result.IPCIDRs) != 2 {
		t.Errorf("IPCIDRs count: got %d want 2: %v", len(result.IPCIDRs), result.IPCIDRs)
	}
}

// TestFetchRouteEntries_DownloadFailure verifies that a failed URL download is
// treated as a non-fatal warning: valid entries from other URLs are still returned.
func TestFetchRouteEntries_DownloadFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("good.example.com\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{
		DomainURLs: []string{
			"http://127.0.0.1:1", // unreachable
			srv.URL,
		},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	// Should still get entries from the reachable server.
	found := false
	for _, d := range result.Domains {
		if d == "good.example.com" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected good.example.com in Domains, got %v", result.Domains)
	}
}

// TestFetchRouteEntries_MultipleURLs checks that entries from multiple URLs are
// all collected and deduplicated together.
func TestFetchRouteEntries_MultipleURLs(t *testing.T) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("a.example.com\nb.example.com\n"))
	}))
	defer srv1.Close()

	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("b.example.com\nc.example.com\n"))
	}))
	defer srv2.Close()

	route := &config.SubscriptionRoute{
		DomainURLs: []string{srv1.URL, srv2.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts())
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	want := map[string]bool{
		"a.example.com": true,
		"b.example.com": true,
		"c.example.com": true,
	}
	if len(result.Domains) != len(want) {
		t.Errorf("Domains count: got %d want %d: %v", len(result.Domains), len(want), result.Domains)
	}
	for _, d := range result.Domains {
		if !want[d] {
			t.Errorf("unexpected domain %q", d)
		}
	}
}

// TestRunner_RouteRule_WithDownloadedEntries verifies the full subscription runner
// integrates FetchRouteEntries for non-default subscriptions that have domain_urls
// or ip_urls in their route config.
func TestRunner_RouteRule_WithDownloadedEntries(t *testing.T) {
	nodePayload := "vless://uuid1@h1.example.com:443?encryption=none&security=tls#Node+1\n"
	domainList := "dl.example.com\nextra.example.com\n"

	nodeSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(nodePayload))
	}))
	defer nodeSrv.Close()

	domainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(domainList))
	}))
	defer domainSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 2},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     nodeSrv.URL,
				Default: true,
			},
			"work": {
				Name: "Work",
				URL:  nodeSrv.URL,
				Route: &config.SubscriptionRoute{
					Domains:    []string{"manual.example.com"},
					DomainURLs: []string{domainSrv.URL},
				},
			},
		},
	}

	applier := &fakeRouteApplier{}
	runner := subscription.NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
}
