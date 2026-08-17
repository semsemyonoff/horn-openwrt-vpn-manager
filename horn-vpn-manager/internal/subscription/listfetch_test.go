package subscription_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", nil, testOpts(), subscription.ListCacheOptions{})
	if result != nil {
		t.Errorf("expected nil for nil route, got %+v", result)
	}
}

func TestFetchRouteEntries_NoURLs(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"10.0.0.0/8"},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})
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

// TestFetchRouteEntries_CacheHit verifies that when a cached file exists and
// forceDownload is false, the URL is served from cache without making a request.
func TestFetchRouteEntries_CacheHit(t *testing.T) {
	cacheDir := t.TempDir()

	// Seed the cache manually.
	if err := subscription.WriteCachedList(cacheDir, "http://will-not-be-called/", "domains", []byte("cached.example.com\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("live.example.com\n"))
	}))
	defer srv.Close()

	// Replace the URL with the server URL but keep the cached file under the original key.
	// Instead, seed cache for srv.URL.
	if err := subscription.WriteCachedList(cacheDir, srv.URL, "domains", []byte("from-cache.example.com\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{Dir: cacheDir, TTL: time.Hour})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if called {
		t.Error("expected cache hit but server was called")
	}
	if len(result.Domains) != 1 || result.Domains[0] != "from-cache.example.com" {
		t.Errorf("Domains: got %v, want [from-cache.example.com]", result.Domains)
	}
}

// TestFetchRouteEntries_CacheFallback verifies that when a refresh fails and a
// cached file exists, the cache is used as a fallback. TTL 0 forces the refresh
// attempt; with a TTL the copy would be served without any request and the
// fallback path would never be reached.
func TestFetchRouteEntries_CacheFallback(t *testing.T) {
	cacheDir := t.TempDir()
	unreachable := "http://127.0.0.1:1"

	if err := subscription.WriteCachedList(cacheDir, unreachable, "domains", []byte("fallback.example.com\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	route := &config.SubscriptionRoute{DomainURLs: []string{unreachable}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{Dir: cacheDir, TTL: 0})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if len(result.Domains) != 1 || result.Domains[0] != "fallback.example.com" {
		t.Errorf("Domains: got %v, want [fallback.example.com]", result.Domains)
	}
}

// TestFetchRouteEntries_ForceDownload verifies that with forceDownload=true, the
// server is always called even when a cached file exists.
func TestFetchRouteEntries_ForceDownload(t *testing.T) {
	cacheDir := t.TempDir()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		_, _ = w.Write([]byte("live.example.com\n"))
	}))
	defer srv.Close()

	// Seed cache with stale data.
	if err := subscription.WriteCachedList(cacheDir, srv.URL, "domains", []byte("stale.example.com\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{Dir: cacheDir, ForceDownload: true})
	if result == nil {
		t.Fatal("expected non-nil result")
	}
	if !called {
		t.Error("expected server call with forceDownload=true but server was not called")
	}
	if len(result.Domains) != 1 || result.Domains[0] != "live.example.com" {
		t.Errorf("Domains: got %v, want [live.example.com]", result.Domains)
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

// seedStaleCache writes a cached list whose FetchedAt is old enough to be past
// any TTL a test sets, optionally with an ETag.
func seedStaleCache(t *testing.T, dir, url, kind, body, etag string) {
	t.Helper()
	meta := subscription.ListMeta{
		URL:       url,
		Kind:      kind,
		ETag:      etag,
		FetchedAt: time.Now().Add(-48 * time.Hour),
	}
	if err := subscription.SaveCachedList(dir, []byte(body), &meta); err != nil {
		t.Fatalf("SaveCachedList: %v", err)
	}
}

// TestFetchRouteEntries_StaleCachePicksUpChange is the regression test for the
// applied config carrying a previous revision of a route list: past the TTL the
// cached copy is revalidated, and a changed list reaches the rules on the very
// run that notices it — not one run later.
func TestFetchRouteEntries_StaleCachePicksUpChange(t *testing.T) {
	cacheDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("ETag", `"v2"`)
		_, _ = w.Write([]byte("api.narrowed.example\n"))
	}))
	defer srv.Close()

	seedStaleCache(t, cacheDir, srv.URL, "domains", "broad.example\n", `"v1"`)

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(),
		subscription.ListCacheOptions{Dir: cacheDir, TTL: time.Hour})

	if len(result.Domains) != 1 || result.Domains[0] != "api.narrowed.example" {
		t.Fatalf("Domains = %v, want the current revision [api.narrowed.example]", result.Domains)
	}
	// The refreshed copy and its new validator must land in the cache, or the
	// next run revalidates against a validator the server has already retired.
	if got := string(subscription.ReadCachedList(cacheDir, srv.URL, "domains")); got != "api.narrowed.example\n" {
		t.Errorf("cached list = %q, want the downloaded revision", got)
	}
	meta, ok := subscription.ReadListMeta(cacheDir, srv.URL, "domains")
	if !ok || meta.ETag != `"v2"` {
		t.Errorf("cached meta = %+v, want etag \"v2\"", meta)
	}
}

// TestFetchRouteEntries_StaleCacheRevalidates covers the cheap half: past the
// TTL an unchanged list costs a 304 and keeps the stored bytes, and its age is
// reset so the next run inside the TTL does not ask again.
func TestFetchRouteEntries_StaleCacheRevalidates(t *testing.T) {
	cacheDir := t.TempDir()

	var requests, conditional int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("If-None-Match") == `"v1"` {
			conditional++
			w.WriteHeader(http.StatusNotModified)
			return
		}
		_, _ = w.Write([]byte("live.example\n"))
	}))
	defer srv.Close()

	seedStaleCache(t, cacheDir, srv.URL, "domains", "cached.example\n", `"v1"`)

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}
	cache := subscription.ListCacheOptions{Dir: cacheDir, TTL: time.Hour}

	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), cache)
	if len(result.Domains) != 1 || result.Domains[0] != "cached.example" {
		t.Fatalf("Domains = %v, want the kept cached copy", result.Domains)
	}
	if conditional != 1 {
		t.Errorf("conditional requests = %d, want 1", conditional)
	}

	// Second run: the 304 refreshed the age, so nothing is requested at all.
	result = subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), cache)
	if len(result.Domains) != 1 || result.Domains[0] != "cached.example" {
		t.Fatalf("Domains on second run = %v", result.Domains)
	}
	if requests != 1 {
		t.Errorf("requests after the second run = %d, want 1 — the refreshed age was not stored", requests)
	}
}

// TestFetchRouteEntries_NotModifiedWithoutCachedCopy covers a 304 answered for a
// list whose file is gone: the entries are dropped rather than silently read as
// an empty list.
func TestFetchRouteEntries_NotModifiedWithoutCachedCopy(t *testing.T) {
	cacheDir := t.TempDir()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	}))
	defer srv.Close()

	seedStaleCache(t, cacheDir, srv.URL, "domains", "cached.example\n", `"v1"`)
	if err := os.Remove(filepath.Join(cacheDir, subscription.ListCacheFilename(srv.URL, "domains"))); err != nil {
		t.Fatalf("remove cached list: %v", err)
	}

	route := &config.SubscriptionRoute{
		Domains:    []string{"manual.example"},
		DomainURLs: []string{srv.URL},
	}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(),
		subscription.ListCacheOptions{Dir: cacheDir, TTL: time.Hour})

	if len(result.Domains) != 1 || result.Domains[0] != "manual.example" {
		t.Errorf("Domains = %v, want only the manual entry", result.Domains)
	}
}

// TestFetchRouteEntries_FreshCacheIsServedWithoutRequest pins the point of
// --cached-lists: a copy the prefetch just wrote costs no request at all.
func TestFetchRouteEntries_FreshCacheIsServedWithoutRequest(t *testing.T) {
	cacheDir := t.TempDir()

	called := false
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		_, _ = w.Write([]byte("live.example\n"))
	}))
	defer srv.Close()

	if err := subscription.WriteCachedList(cacheDir, srv.URL, "domains", []byte("fresh.example\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(),
		subscription.ListCacheOptions{Dir: cacheDir, TTL: time.Hour})

	if called {
		t.Error("a copy younger than the TTL was revalidated")
	}
	if len(result.Domains) != 1 || result.Domains[0] != "fresh.example" {
		t.Errorf("Domains = %v, want [fresh.example]", result.Domains)
	}
}

// TestFetchRouteEntries_DuplicateURLs pins that the same URL listed twice maps
// its results back by position: keying them by URL loses one and renders it as
// an empty list.
func TestFetchRouteEntries_DuplicateURLs(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("dup.example\n"))
	}))
	defer srv.Close()

	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL, srv.URL}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(), subscription.ListCacheOptions{})

	if len(result.Domains) != 1 || result.Domains[0] != "dup.example" {
		t.Errorf("Domains = %v, want [dup.example]", result.Domains)
	}
}

// TestFetchRouteEntries_ForceDownloadFallsBackToCache pins that --download-lists
// bypasses the cache on the way out but still falls back to it when the download
// fails: without the fallback the subscription loses its route rules entirely
// and its domains fall through to route.final.
func TestFetchRouteEntries_ForceDownloadFallsBackToCache(t *testing.T) {
	cacheDir := t.TempDir()
	unreachable := "http://127.0.0.1:1"

	if err := subscription.WriteCachedList(cacheDir, unreachable, "domains", []byte("fallback.example\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}

	route := &config.SubscriptionRoute{DomainURLs: []string{unreachable}}
	result := subscription.FetchRouteEntries(context.Background(), "sub1", route, testOpts(),
		subscription.ListCacheOptions{Dir: cacheDir, ForceDownload: true, TTL: time.Hour})

	if len(result.Domains) != 1 || result.Domains[0] != "fallback.example" {
		t.Errorf("Domains = %v, want [fallback.example]", result.Domains)
	}
}

// TestFetchRouteEntries_RunCacheResolvesOnce pins that a route list shared by two
// subscriptions is resolved once per run. Phase 2 resolves subscriptions
// concurrently, so without the memo the URL is fetched twice — and if the list
// changes mid-run, one fetch can answer 304 with the old revision while the
// other returns the new one, putting two revisions of the same list into one
// generated config.
func TestFetchRouteEntries_RunCacheResolvesOnce(t *testing.T) {
	var mu sync.Mutex
	var requests int
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		mu.Lock()
		requests++
		n := requests
		mu.Unlock()
		if n == 1 {
			<-release // hold the first request so the second subscription overlaps it
		}
		_, _ = fmt.Fprintf(w, "rev%d.example\n", n)
	}))
	defer srv.Close()

	cache := subscription.ListCacheOptions{Run: subscription.NewListRunCache()}
	route := &config.SubscriptionRoute{DomainURLs: []string{srv.URL}}

	results := make([][]string, 2)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			r := subscription.FetchRouteEntries(context.Background(), fmt.Sprintf("sub%d", i), route, testOpts(), cache)
			results[i] = r.Domains
		}(i)
	}
	time.Sleep(50 * time.Millisecond) // let the second goroutine reach the claim
	close(release)
	wg.Wait()

	if requests != 1 {
		t.Errorf("requests = %d, want 1 — the shared list was fetched more than once", requests)
	}
	if len(results[0]) != 1 || len(results[1]) != 1 || results[0][0] != results[1][0] {
		t.Errorf("subscriptions got different revisions: %v vs %v", results[0], results[1])
	}
}

// TestFetchRouteEntries_RunCacheNoDeadlock pins the publish-then-wait ordering:
// two subscriptions claiming the same two URLs in opposite order must not block
// on each other.
func TestFetchRouteEntries_RunCacheNoDeadlock(t *testing.T) {
	srvA := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("a.example\n"))
	}))
	defer srvA.Close()
	srvB := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("b.example\n"))
	}))
	defer srvB.Close()

	cache := subscription.ListCacheOptions{Run: subscription.NewListRunCache()}
	routes := []*config.SubscriptionRoute{
		{DomainURLs: []string{srvA.URL, srvB.URL}},
		{DomainURLs: []string{srvB.URL, srvA.URL}},
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		var wg sync.WaitGroup
		for i, route := range routes {
			wg.Add(1)
			go func(i int, route *config.SubscriptionRoute) {
				defer wg.Done()
				got := subscription.FetchRouteEntries(context.Background(), fmt.Sprintf("sub%d", i), route, testOpts(), cache)
				if len(got.Domains) != 2 {
					t.Errorf("sub%d domains = %v, want both lists", i, got.Domains)
				}
			}(i, route)
		}
		wg.Wait()
	}()

	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatal("deadlock: two subscriptions claiming the same URLs in opposite order blocked on each other")
	}
}
