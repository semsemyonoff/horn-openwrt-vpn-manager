package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
)

// multiNodePayload provides two VLESS nodes, which triggers multi-node outbound group generation.
const multiNodePayload = "vless://uuid1@h1.example.com:443?encryption=none#Node+1\nvless://uuid2@h2.example.com:443?encryption=none#Node+2\n"

// singleNodePayload provides one VLESS node, which produces a single outbound (no groups).
const singleNodePayload = "vless://uuid3@h3.example.com:443?encryption=none#Work+Server\n"

// TestIntegration_Run_with_route_rules exercises the full subscription pipeline:
//   - a default multi-node subscription (produces urltest + selector groups)
//   - a non-default single-node subscription with manual domain and IP routing rules
//
// It verifies that the generated config.json contains outbounds for both subscriptions
// and a route rule for the non-default subscription pointing to the correct outbound.
func TestIntegration_Run_with_route_rules(t *testing.T) {
	defaultSrv := newTestServer(t, multiNodePayload, http.StatusOK)
	defer defaultSrv.Close()
	workSrv := newTestServer(t, singleNodePayload, http.StatusOK)
	defer workSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"default": {
				Name:    "Default",
				URL:     defaultSrv.URL,
				Default: true,
			},
			"work": {
				Name: "Work",
				URL:  workSrv.URL,
				Route: &config.SubscriptionRoute{
					Domains: []string{"jira.example.com", "confluence.example.com"},
					IPCIDRs: []string{"203.0.113.0/24"},
				},
			},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	// ConfigDir too: a non-dry-run writes subs-tags.json there, and the default
	// is the on-device /etc/horn-vpn-manager.
	runner.ConfigDir = t.TempDir()

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Applier must be called exactly once (non-dry-run).
	if len(applier.applySingboxCalls) != 1 {
		t.Errorf("expected 1 ApplySingbox call, got %d", len(applier.applySingboxCalls))
	}
	if applier.applySingboxCalls[0] != filepath.Join(outDir, "config.json") {
		t.Errorf("unexpected config path: %s", applier.applySingboxCalls[0])
	}

	// Parse the generated config.
	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	outboundTags := collectOutboundTags(generated)

	// Default subscription (multi-node) must produce urltest + selector groups.
	if !outboundTags["default-auto"] {
		t.Errorf("expected default-auto outbound, got tags: %v", outboundTags)
	}
	if !outboundTags["default-manual"] {
		t.Errorf("expected default-manual outbound, got tags: %v", outboundTags)
	}

	// Work subscription (single node) must produce a single outbound.
	if !outboundTags["work-single"] {
		t.Errorf("expected work-single outbound, got tags: %v", outboundTags)
	}

	// Route section must contain two separate rules for work-single: one for
	// domain_suffix and one for ip_cidr. sing-box AND semantics require them
	// to be separate so traffic matching either condition is routed correctly.
	routeSection, _ := generated["route"].(map[string]any)
	if routeSection == nil {
		t.Fatal("expected route section in generated config")
	}
	rules, _ := routeSection["rules"].([]any)
	var workDomainRule, workIPRule bool
	for _, rule := range rules {
		ruleMap, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		if outbound, _ := ruleMap["outbound"].(string); outbound == "work-single" {
			if ds, _ := ruleMap["domain_suffix"].([]any); len(ds) > 0 {
				workDomainRule = true
				if _, hasIP := ruleMap["ip_cidr"]; hasIP {
					t.Error("domain rule must not contain ip_cidr (AND semantics would break matching)")
				}
			}
			if ic, _ := ruleMap["ip_cidr"].([]any); len(ic) > 0 {
				workIPRule = true
				if _, hasDomain := ruleMap["domain_suffix"]; hasDomain {
					t.Error("ip rule must not contain domain_suffix (AND semantics would break matching)")
				}
			}
		}
	}
	if !workDomainRule {
		t.Errorf("expected a domain_suffix route rule for work-single, got rules: %v", rules)
	}
	if !workIPRule {
		t.Errorf("expected an ip_cidr route rule for work-single, got rules: %v", rules)
	}

	// route.final must point to the default subscription's final outbound.
	finalTag, _ := routeSection["final"].(string)
	if !strings.HasPrefix(finalTag, "default-") {
		t.Errorf("expected route.final to start with 'default-', got %q", finalTag)
	}
}

// TestIntegration_DryRun_no_apply verifies dry-run mode writes config but does not call the applier.
func TestIntegration_DryRun_no_apply(t *testing.T) {
	srv := newTestServer(t, multiNodePayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Config must be written even in dry-run.
	if _, err := os.Stat(filepath.Join(outDir, "config.json")); err != nil {
		t.Errorf("expected config.json to be written in dry-run: %v", err)
	}

	// Node file must be written in dry-run.
	if _, err := os.Stat(filepath.Join(outDir, "main-nodes.txt")); err != nil {
		t.Errorf("expected main-nodes.txt to be written in dry-run: %v", err)
	}

	// Applier must NOT be called in dry-run.
	if len(applier.applySingboxCalls) != 0 {
		t.Errorf("expected no ApplySingbox calls in dry-run, got %d", len(applier.applySingboxCalls))
	}
}

// TestIntegration_PartialFailure_default_config_still_generated verifies that when a
// non-default subscription fails to download, the run continues and the default
// subscription's config is still generated and applied.
func TestIntegration_PartialFailure_default_config_still_generated(t *testing.T) {
	defaultSrv := newTestServer(t, multiNodePayload, http.StatusOK)
	defer defaultSrv.Close()
	failSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer failSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"default": {Name: "Default", URL: defaultSrv.URL, Default: true},
			"failed":  {Name: "Failed", URL: failSrv.URL},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.ConfigDir = t.TempDir()

	// Must succeed despite the failed non-default subscription.
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() should not error on non-default failure: %v", err)
	}

	// Applier must still be called for the default subscription's config.
	if len(applier.applySingboxCalls) != 1 {
		t.Errorf("expected 1 ApplySingbox call, got %d", len(applier.applySingboxCalls))
	}

	// Config should contain default subscription's outbounds.
	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	outboundTags := collectOutboundTags(generated)

	hasDefault := outboundTags["default-auto"] || outboundTags["default-manual"] || outboundTags["default-single"]
	if !hasDefault {
		t.Errorf("expected default subscription outbound in config, got tags: %v", outboundTags)
	}
}

// TestIntegration_RoutingAndSubscriptions_coexist verifies that a config with both
// routing and subscriptions sections runs the subscriptions pipeline cleanly without
// generating routing state files (domains.lst, subnets.lst).
func TestIntegration_RoutingAndSubscriptions_coexist(t *testing.T) {
	srv := newTestServer(t, multiNodePayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Routing: config.Routing{
			Domains: config.Domains{URL: "https://example.com/domains.lst"},
			Subnets: config.Subnets{URLs: []string{"https://example.com/subnets.lst"}},
		},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.DryRun = true

	// Subscriptions pipeline must run cleanly without attempting to download routing lists.
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// sing-box config must be written.
	if _, err := os.Stat(filepath.Join(outDir, "config.json")); err != nil {
		t.Errorf("expected config.json in output dir: %v", err)
	}

	// Routing list files must NOT be created by the subscriptions pipeline.
	for _, name := range []string{"domains.lst", "subnets.lst", "vpn-ip-list.lst"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err == nil {
			t.Errorf("subscriptions pipeline must not write routing list file %s", name)
		}
	}
}

// TestIntegration_Run_inline_nodes_no_http verifies an inline-node subscription
// performs no HTTP request. The inline subscription is the default one, so it is
// processed first in phase 1, where a download would otherwise be unconditional;
// the only request the run may make is the one for the URL-backed subscription.
func TestIntegration_Run_inline_nodes_no_http(t *testing.T) {
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(singleNodePayload))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"personal": {
				Name:    "Self-hosted",
				Default: true,
				Nodes:   []string{"vless://uuid-self@vps.example.com:443?encryption=none#Self+Hosted"},
			},
			"provider": {Name: "Provider", URL: srv.URL},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.ConfigDir = t.TempDir()

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected exactly 1 HTTP request (provider only), got %d", got)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	tags := collectOutboundTags(generated)
	for _, tag := range []string{"personal-single", "provider-single"} {
		if !tags[tag] {
			t.Errorf("expected outbound %q, got tags: %v", tag, tags)
		}
	}

	routeSection, _ := generated["route"].(map[string]any)
	if routeSection == nil {
		t.Fatal("expected route section in generated config")
	}
	if final, _ := routeSection["final"].(string); final != "personal-single" {
		t.Errorf("route.final = %q, want personal-single", final)
	}
}

// TestIntegration_Run_inline_nodes_only verifies a config whose every subscription
// is inline renders and applies without any network access at all.
func TestIntegration_Run_inline_nodes_only(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"personal": {
				Name:    "Self-hosted",
				Default: true,
				Nodes: []string{
					"vless://uuid-self1@vps1.example.com:443?encryption=none#VPS+1",
					"vless://uuid-self2@vps2.example.com:443?encryption=none#VPS+2",
				},
			},
		},
	}

	outDir := t.TempDir()
	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.ConfigDir = t.TempDir()

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(applier.applySingboxCalls) != 1 {
		t.Fatalf("expected 1 ApplySingbox call, got %d", len(applier.applySingboxCalls))
	}

	// Two inline nodes must produce the same group shape as a downloaded multi-node sub.
	tags := collectOutboundTags(readConfig(t, filepath.Join(outDir, "config.json")))
	for _, tag := range []string{"personal-auto", "personal-manual"} {
		if !tags[tag] {
			t.Errorf("expected outbound %q, got tags: %v", tag, tags)
		}
	}
}

// mixedProtocolPayload interleaves a VLESS and a hysteria2 line the way a
// provider payload would after this feature ships.
const mixedProtocolPayload = "vless://uuid-m@vless.example.com:443?encryption=none#VLESS+Node\n" +
	inlineHY2NodeA + "\n"

// TestIntegration_Run_inline_hysteria2_with_route_rules is the acceptance case
// this feature was built for: the self-hosted hysteria2 node expressed as inline
// nodes on its own subscription, with its own route rules, instead of a
// hand-written outbound in the sing-box template.
//
// The URI omits the port and carries a colon inside the auth, so this also pins
// the round trip of both spec quirks all the way into the generated config.
func TestIntegration_Run_inline_hysteria2_with_route_rules(t *testing.T) {
	cfg := &config.Config{
		Fetch:   config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Singbox: config.Singbox{ConnectTimeout: "3s"},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", Default: true, Nodes: []string{inlineNodeA}},
			"api": {
				Name:  "API over QUIC",
				Nodes: []string{inlineHY2NodeA},
				Route: &config.SubscriptionRoute{
					Domains: []string{"api.anthropic.com"},
					IPCIDRs: []string{"203.0.113.0/24"},
				},
			},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))

	ob := outboundByTag(generated, "api-single")
	if ob == nil {
		t.Fatalf("api-single outbound missing, tags: %v", collectOutboundTags(generated))
	}
	for _, tc := range []struct {
		field string
		want  any
	}{
		{"type", "hysteria2"},
		{"server", "hy2a.example.com"},
		{"server_port", float64(443)}, // spec default, the URI carried no port
		{"password", "user:pa:ss"},    // whole userinfo, colon and all
		{"connect_timeout", "3s"},
	} {
		if ob[tc.field] != tc.want {
			t.Errorf("api-single %s = %v (%T), want %v", tc.field, ob[tc.field], ob[tc.field], tc.want)
		}
	}

	obfs, _ := ob["obfs"].(map[string]any)
	if obfs == nil || obfs["type"] != "salamander" || obfs["password"] != "obfspw" {
		t.Errorf("api-single obfs = %v, want a salamander block with the URI's password", ob["obfs"])
	}
	// sing-box requires tls on a hysteria2 outbound; it runs over QUIC.
	tls, _ := ob["tls"].(map[string]any)
	if tls == nil || tls["enabled"] != true || tls["server_name"] != "hy2a.example.com" {
		t.Errorf("api-single tls = %v, want enabled with the URI's sni", ob["tls"])
	}
	// Inbound-only fields must never reach an outbound.
	for _, forbidden := range []string{"ignore_client_bandwidth", "masquerade"} {
		if _, ok := ob[forbidden]; ok {
			t.Errorf("api-single carries inbound-only field %q", forbidden)
		}
	}
	// Bandwidth hints were not in the URI, so congestion control stays BBR.
	for _, absent := range []string{"up_mbps", "down_mbps"} {
		if _, ok := ob[absent]; ok {
			t.Errorf("api-single carries %q, want absent so sing-box keeps BBR", absent)
		}
	}

	routeSection, _ := generated["route"].(map[string]any)
	if routeSection == nil {
		t.Fatal("expected route section in generated config")
	}
	rules, _ := routeSection["rules"].([]any)
	var domainRule, ipRule bool
	for _, rule := range rules {
		m, ok := rule.(map[string]any)
		if !ok {
			continue
		}
		if outbound, _ := m["outbound"].(string); outbound != "api-single" {
			continue
		}
		if ds, _ := m["domain_suffix"].([]any); len(ds) > 0 {
			domainRule = true
		}
		if ic, _ := m["ip_cidr"].([]any); len(ic) > 0 {
			ipRule = true
		}
	}
	if !domainRule || !ipRule {
		t.Errorf("expected domain and ip_cidr rules for api-single, got rules: %v", rules)
	}
}

// TestIntegration_Run_mixed_protocol_subscription pins that one downloaded
// payload carrying both protocols produces both nodes under a single shared
// urltest/selector pair — the decode filter, the dispatcher and the group
// generation all have to agree for this to hold.
func TestIntegration_Run_mixed_protocol_subscription(t *testing.T) {
	srv := newTestServer(t, mixedProtocolPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	outbounds, _ := generated["outbounds"].([]any)

	// Exactly one urltest and one selector, holding both node tags in payload order.
	var urltest, selector map[string]any
	var nodeTags []string
	var types []string
	for _, raw := range outbounds {
		m, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		switch m["type"] {
		case "urltest":
			if urltest != nil {
				t.Fatalf("more than one urltest group generated: %v", collectOutboundTags(generated))
			}
			urltest = m
		case "selector":
			if selector != nil {
				t.Fatalf("more than one selector group generated: %v", collectOutboundTags(generated))
			}
			selector = m
		case "vless", "hysteria2":
			tag, _ := m["tag"].(string)
			nodeTags = append(nodeTags, tag)
			types = append(types, m["type"].(string))
		}
	}

	if want := []string{"vless", "hysteria2"}; !slices.Equal(types, want) {
		t.Fatalf("node outbound types = %v, want %v", types, want)
	}
	if urltest == nil || selector == nil {
		t.Fatalf("expected one urltest and one selector group, tags: %v", collectOutboundTags(generated))
	}
	if got := outboundList(t, urltest); !slices.Equal(got, nodeTags) {
		t.Errorf("urltest members = %v, want both node tags %v", got, nodeTags)
	}
	if got := outboundList(t, selector); !slices.Equal(got, append([]string{"main-auto"}, nodeTags...)) {
		t.Errorf("selector members = %v, want main-auto plus %v", got, nodeTags)
	}
	if final := routeFinal(t, generated); final != "main-manual" {
		t.Errorf("route.final = %q, want main-manual", final)
	}
}

// TestIntegration_Run_bounded_subscription_parallelism pins that phase 2 never
// runs more subscriptions at once than fetch.parallelism. Each processSub does
// its own bounded fetching, so an unbounded fan-out multiplies out to one
// decode goroutine and up to parallelism requests per subscription — on a
// router that is the memory spike, not the download, that hurts.
//
// The handler holds each request briefly so overlap is observable: with the
// bound the peak can never exceed parallelism, without it every subscription
// is in flight at once.
func TestIntegration_Run_bounded_subscription_parallelism(t *testing.T) {
	const parallelism = 2

	var mu sync.Mutex
	var inFlight, peak int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		inFlight++
		if inFlight > peak {
			peak = inFlight
		}
		mu.Unlock()

		time.Sleep(50 * time.Millisecond)

		mu.Lock()
		inFlight--
		mu.Unlock()

		id := strings.TrimPrefix(r.URL.Path, "/")
		w.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(w, "vless://uuid-%s@%s.example.com:443?encryption=none#Node+%s\n", id, id, id)
	}))
	defer srv.Close()

	// Inline default: phase 1 must not contribute a request, so every observed
	// request comes from the phase 2 pool.
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: parallelism},
		Subscriptions: map[string]*config.Subscription{
			"default": {
				Name:    "Default",
				Default: true,
				Nodes:   []string{"vless://uuid-self@vps.example.com:443?encryption=none#Self+Hosted"},
			},
		},
	}
	for i := range 6 {
		id := fmt.Sprintf("sub%d", i)
		cfg.Subscriptions[id] = &config.Subscription{Name: id, URL: srv.URL + "/" + id}
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mu.Lock()
	got := peak
	mu.Unlock()
	if got > parallelism {
		t.Errorf("peak concurrent subscription requests = %d, want at most %d", got, parallelism)
	}
	// The other direction: a pool that never actually runs two subscriptions at
	// once would satisfy the bound above without measuring anything. Each
	// request is held for 50 ms, so two workers cannot avoid overlapping.
	if got != parallelism {
		t.Errorf("peak concurrent subscription requests = %d, want exactly %d", got, parallelism)
	}

	// All six are still processed.
	tags := collectOutboundTags(readConfig(t, filepath.Join(runner.OutDir, "config.json")))
	for i := range 6 {
		tag := fmt.Sprintf("sub%d-single", i)
		if !tags[tag] {
			t.Errorf("expected outbound %q, got tags: %v", tag, tags)
		}
	}
}

// collectOutboundTags returns a set of outbound tags from a parsed sing-box config.
func collectOutboundTags(cfg map[string]any) map[string]bool {
	tags := make(map[string]bool)
	outbounds, _ := cfg["outbounds"].([]any)
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]any); ok {
			if tag, ok := m["tag"].(string); ok {
				tags[tag] = true
			}
		}
	}
	return tags
}

// readConfig reads and parses a JSON config file, failing the test on error.
func readConfig(t *testing.T, path string) map[string]any {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("parse %s: %v", path, err)
	}
	return m
}
