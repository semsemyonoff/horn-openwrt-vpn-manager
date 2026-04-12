package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
