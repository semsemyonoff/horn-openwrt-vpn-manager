package subscription_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
)

func TestBuildRouteRules_NilRoute(t *testing.T) {
	rule := subscription.BuildRouteRules(nil, "work-manual")
	if rule != nil {
		t.Errorf("expected nil for nil route, got %+v", rule)
	}
}

func TestBuildRouteRules_EmptyRoute(t *testing.T) {
	rule := subscription.BuildRouteRules(&config.SubscriptionRoute{}, "work-manual")
	if rule != nil {
		t.Errorf("expected nil for empty route, got %+v", rule)
	}
}

func TestBuildRouteRules_DomainsOnly(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"jira.example.com", "confluence.example.com"},
	}
	rule := subscription.BuildRouteRules(route, "work-manual")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	if len(rule.DomainSuffix) != 2 {
		t.Errorf("DomainSuffix len: got %d want 2", len(rule.DomainSuffix))
	}
	if rule.DomainSuffix[0] != "jira.example.com" {
		t.Errorf("DomainSuffix[0]: got %q want %q", rule.DomainSuffix[0], "jira.example.com")
	}
	if rule.DomainSuffix[1] != "confluence.example.com" {
		t.Errorf("DomainSuffix[1]: got %q want %q", rule.DomainSuffix[1], "confluence.example.com")
	}
	if len(rule.IPCIDR) != 0 {
		t.Errorf("IPCIDR len: got %d want 0", len(rule.IPCIDR))
	}
	if rule.Outbound != "work-manual" {
		t.Errorf("Outbound: got %q want %q", rule.Outbound, "work-manual")
	}
}

func TestBuildRouteRules_IPCIDRsOnly(t *testing.T) {
	route := &config.SubscriptionRoute{
		IPCIDRs: []string{"203.0.113.0/24", "198.51.100.0/24"},
	}
	rule := subscription.BuildRouteRules(route, "work-single")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	if len(rule.IPCIDR) != 2 {
		t.Errorf("IPCIDR len: got %d want 2", len(rule.IPCIDR))
	}
	if rule.IPCIDR[0] != "203.0.113.0/24" {
		t.Errorf("IPCIDR[0]: got %q want %q", rule.IPCIDR[0], "203.0.113.0/24")
	}
	if len(rule.DomainSuffix) != 0 {
		t.Errorf("DomainSuffix len: got %d want 0", len(rule.DomainSuffix))
	}
	if rule.Outbound != "work-single" {
		t.Errorf("Outbound: got %q want %q", rule.Outbound, "work-single")
	}
}

func TestBuildRouteRules_DomainsAndCIDRs(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"10.0.0.0/8"},
	}
	rule := subscription.BuildRouteRules(route, "corp-manual")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	if len(rule.DomainSuffix) != 1 {
		t.Errorf("DomainSuffix len: got %d want 1", len(rule.DomainSuffix))
	}
	if len(rule.IPCIDR) != 1 {
		t.Errorf("IPCIDR len: got %d want 1", len(rule.IPCIDR))
	}
	if rule.Outbound != "corp-manual" {
		t.Errorf("Outbound: got %q want %q", rule.Outbound, "corp-manual")
	}
}

// TestBuildRouteRules_SingleNodeOutbound verifies that single-node subscriptions
// (whose FinalTag is <id>-single) are correctly referenced.
func TestBuildRouteRules_SingleNodeOutbound(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"work.example.com"},
	}
	rule := subscription.BuildRouteRules(route, "work-single")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	if rule.Outbound != "work-single" {
		t.Errorf("Outbound: got %q want %q", rule.Outbound, "work-single")
	}
}

// TestBuildRouteRules_MultiNodeOutbound verifies that multi-node subscriptions
// (whose FinalTag is <id>-manual) are correctly referenced.
func TestBuildRouteRules_MultiNodeOutbound(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"corp.example.com"},
	}
	rule := subscription.BuildRouteRules(route, "corp-manual")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	if rule.Outbound != "corp-manual" {
		t.Errorf("Outbound: got %q want %q", rule.Outbound, "corp-manual")
	}
}

func TestBuildRouteRules_JSONShape(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"192.0.2.0/24"},
	}
	rule := subscription.BuildRouteRules(route, "work-manual")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	data, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("marshal route rule: %v", err)
	}
	var m map[string]interface{}
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal route rule: %v", err)
	}
	if _, ok := m["domain_suffix"]; !ok {
		t.Error("JSON missing domain_suffix field")
	}
	if _, ok := m["ip_cidr"]; !ok {
		t.Error("JSON missing ip_cidr field")
	}
	if m["outbound"] != "work-manual" {
		t.Errorf("JSON outbound: got %v want work-manual", m["outbound"])
	}
}

// TestBuildRouteRules_InputNotMutated checks that BuildRouteRules copies slices
// rather than referencing the original config slices.
func TestBuildRouteRules_InputNotMutated(t *testing.T) {
	domains := []string{"a.example.com"}
	cidrs := []string{"10.0.0.0/8"}
	route := &config.SubscriptionRoute{Domains: domains, IPCIDRs: cidrs}
	rule := subscription.BuildRouteRules(route, "work-manual")
	if rule == nil {
		t.Fatal("expected non-nil rule")
	}
	rule.DomainSuffix[0] = "mutated"
	if domains[0] != "a.example.com" {
		t.Error("BuildRouteRules modified the original Domains slice")
	}
	rule.IPCIDR[0] = "mutated"
	if cidrs[0] != "10.0.0.0/8" {
		t.Error("BuildRouteRules modified the original IPCIDRs slice")
	}
}

// TestRunner_RouteRule_NonDefault verifies that the runner generates route rules
// for non-default subscriptions that have a route config block.
func TestRunner_RouteRule_NonDefault(t *testing.T) {
	payload := "vless://uuid1@h1.example.com:443?encryption=none&security=tls#Node+1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     srv.URL,
				Default: true,
			},
			"work": {
				Name: "Work",
				URL:  srv.URL,
				Route: &config.SubscriptionRoute{
					Domains: []string{"corp.example.com"},
					IPCIDRs: []string{"10.0.0.0/8"},
				},
			},
		},
	}

	// Use a capturing applier from the internal package via a local fake.
	applier := &fakeRouteApplier{}
	runner := subscription.NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
}

// TestRunner_RouteRule_DefaultNoRule verifies that default subscriptions do not
// get route rules even when a route block is present.
func TestRunner_RouteRule_DefaultNoRule(t *testing.T) {
	payload := "vless://uuid1@h1.example.com:443?encryption=none&security=tls#Node+1\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(payload))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     srv.URL,
				Default: true,
				// Route block on default subscription must not generate a route rule.
				Route: &config.SubscriptionRoute{
					Domains: []string{"should-not-route.example.com"},
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

// fakeRouteApplier satisfies the subscription.Applier interface for route tests.
type fakeRouteApplier struct{}

func (f *fakeRouteApplier) ApplySingbox(configPath string) error { return nil }
