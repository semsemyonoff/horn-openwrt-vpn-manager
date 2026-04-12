package subscription_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
)

func TestBuildRouteRules_NilRoute(t *testing.T) {
	rules := subscription.BuildRouteRules(nil, "work-manual")
	if rules != nil {
		t.Errorf("expected nil for nil route, got %+v", rules)
	}
}

func TestBuildRouteRules_EmptyRoute(t *testing.T) {
	rules := subscription.BuildRouteRules(&config.SubscriptionRoute{}, "work-manual")
	if len(rules) != 0 {
		t.Errorf("expected empty rules for empty route, got %+v", rules)
	}
}

func TestBuildRouteRules_DomainsOnly(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"jira.example.com", "confluence.example.com"},
	}
	rules := subscription.BuildRouteRules(route, "work-manual")
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	rule := rules[0]
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
	rules := subscription.BuildRouteRules(route, "work-single")
	if len(rules) != 1 {
		t.Fatalf("expected 1 rule, got %d", len(rules))
	}
	rule := rules[0]
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

// TestBuildRouteRules_DomainsAndCIDRs verifies that when both domains and IP CIDRs
// are configured, two separate rules are generated (one per condition type). sing-box
// applies AND semantics within a single rule, so combining them would break matching.
func TestBuildRouteRules_DomainsAndCIDRs(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"10.0.0.0/8"},
	}
	rules := subscription.BuildRouteRules(route, "corp-manual")
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules (one for domains, one for IPs), got %d", len(rules))
	}
	// First rule: domains only.
	if len(rules[0].DomainSuffix) != 1 || len(rules[0].IPCIDR) != 0 {
		t.Errorf("rule[0] should have domain_suffix only: %+v", rules[0])
	}
	if rules[0].Outbound != "corp-manual" {
		t.Errorf("rule[0] outbound: got %q want corp-manual", rules[0].Outbound)
	}
	// Second rule: IPs only.
	if len(rules[1].IPCIDR) != 1 || len(rules[1].DomainSuffix) != 0 {
		t.Errorf("rule[1] should have ip_cidr only: %+v", rules[1])
	}
	if rules[1].Outbound != "corp-manual" {
		t.Errorf("rule[1] outbound: got %q want corp-manual", rules[1].Outbound)
	}
}

// TestBuildRouteRules_SingleNodeOutbound verifies that single-node subscriptions
// (whose FinalTag is <id>-single) are correctly referenced.
func TestBuildRouteRules_SingleNodeOutbound(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"work.example.com"},
	}
	rules := subscription.BuildRouteRules(route, "work-single")
	if len(rules) == 0 {
		t.Fatal("expected non-empty rules")
	}
	if rules[0].Outbound != "work-single" {
		t.Errorf("Outbound: got %q want %q", rules[0].Outbound, "work-single")
	}
}

// TestBuildRouteRules_MultiNodeOutbound verifies that multi-node subscriptions
// (whose FinalTag is <id>-manual) are correctly referenced.
func TestBuildRouteRules_MultiNodeOutbound(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"corp.example.com"},
	}
	rules := subscription.BuildRouteRules(route, "corp-manual")
	if len(rules) == 0 {
		t.Fatal("expected non-empty rules")
	}
	if rules[0].Outbound != "corp-manual" {
		t.Errorf("Outbound: got %q want %q", rules[0].Outbound, "corp-manual")
	}
}

// TestBuildRouteRules_JSONShape verifies that domain and IP rules have the
// correct JSON structure and do not cross-contaminate fields.
func TestBuildRouteRules_JSONShape(t *testing.T) {
	route := &config.SubscriptionRoute{
		Domains: []string{"example.com"},
		IPCIDRs: []string{"192.0.2.0/24"},
	}
	rules := subscription.BuildRouteRules(route, "work-manual")
	if len(rules) != 2 {
		t.Fatalf("expected 2 rules, got %d", len(rules))
	}

	domainData, err := json.Marshal(rules[0])
	if err != nil {
		t.Fatalf("marshal domain rule: %v", err)
	}
	var dm map[string]any
	if unmarshalErr := json.Unmarshal(domainData, &dm); unmarshalErr != nil {
		t.Fatalf("unmarshal domain rule: %v", unmarshalErr)
	}
	if _, ok := dm["domain_suffix"]; !ok {
		t.Error("domain rule JSON missing domain_suffix field")
	}
	if _, ok := dm["ip_cidr"]; ok {
		t.Error("domain rule JSON must not have ip_cidr field")
	}
	if dm["outbound"] != "work-manual" {
		t.Errorf("domain rule JSON outbound: got %v want work-manual", dm["outbound"])
	}

	ipData, err := json.Marshal(rules[1])
	if err != nil {
		t.Fatalf("marshal ip rule: %v", err)
	}
	var im map[string]any
	if err := json.Unmarshal(ipData, &im); err != nil {
		t.Fatalf("unmarshal ip rule: %v", err)
	}
	if _, ok := im["ip_cidr"]; !ok {
		t.Error("ip rule JSON missing ip_cidr field")
	}
	if _, ok := im["domain_suffix"]; ok {
		t.Error("ip rule JSON must not have domain_suffix field")
	}
	if im["outbound"] != "work-manual" {
		t.Errorf("ip rule JSON outbound: got %v want work-manual", im["outbound"])
	}
}

// TestBuildRouteRules_InputNotMutated checks that BuildRouteRules copies slices
// rather than referencing the original config slices.
func TestBuildRouteRules_InputNotMutated(t *testing.T) {
	domains := []string{"a.example.com"}
	cidrs := []string{"10.0.0.0/8"}
	route := &config.SubscriptionRoute{Domains: domains, IPCIDRs: cidrs}
	rules := subscription.BuildRouteRules(route, "work-manual")
	if len(rules) == 0 {
		t.Fatal("expected non-empty rules")
	}
	rules[0].DomainSuffix[0] = "mutated"
	if domains[0] != "a.example.com" {
		t.Error("BuildRouteRules modified the original Domains slice")
	}
	rules[1].IPCIDR[0] = "mutated"
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

func (f *fakeRouteApplier) ApplySingbox(stagingPath, finalPath string) error {
	return os.Rename(stagingPath, finalPath)
}
