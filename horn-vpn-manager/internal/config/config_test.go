package config

import (
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func TestLoad_valid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"fetch": {"retries": 5, "timeout_seconds": 30, "parallelism": 4},
		"routing": {
			"domains": {"url": "https://example.com/domains.lst"},
			"subnets": {
				"urls": ["https://example.com/sub1.lst"],
				"manual_file": "/tmp/manual.lst"
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Fetch.Retries != 5 {
		t.Errorf("retries = %d, want 5", cfg.Fetch.Retries)
	}
	if cfg.Fetch.TimeoutSeconds != 30 {
		t.Errorf("timeout = %d, want 30", cfg.Fetch.TimeoutSeconds)
	}
	if cfg.Fetch.Parallelism != 4 {
		t.Errorf("parallelism = %d, want 4", cfg.Fetch.Parallelism)
	}
	if cfg.Routing.Domains.URL != "https://example.com/domains.lst" {
		t.Errorf("domains.url = %q", cfg.Routing.Domains.URL)
	}
	if len(cfg.Routing.Subnets.URLs) != 1 {
		t.Errorf("subnets.urls length = %d, want 1", len(cfg.Routing.Subnets.URLs))
	}
	if cfg.Routing.Subnets.ManualFile != "/tmp/manual.lst" {
		t.Errorf("manual_file = %q", cfg.Routing.Subnets.ManualFile)
	}
}

func TestLoad_defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"routing": {
			"domains": {"url": "https://example.com/d.lst"}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Fetch.Retries != 3 {
		t.Errorf("default retries = %d, want 3", cfg.Fetch.Retries)
	}
	if cfg.Fetch.TimeoutSeconds != 15 {
		t.Errorf("default timeout = %d, want 15", cfg.Fetch.TimeoutSeconds)
	}
	if cfg.Fetch.Parallelism != 2 {
		t.Errorf("default parallelism = %d, want 2", cfg.Fetch.Parallelism)
	}
	if cfg.Routing.Subnets.ManualFile != "/etc/horn-vpn-manager/lists/manual-ip.lst" {
		t.Errorf("default manual_file = %q", cfg.Routing.Subnets.ManualFile)
	}
}

func TestLoad_validation_empty_routing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{"routing": {}}`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected validation error for empty routing and no subscriptions")
	}
}

func TestLoad_subscriptions_only(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"subscriptions": {
			"default": {
				"name": "Default",
				"url": "https://example.com/sub",
				"default": true
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(cfg.Subscriptions) != 1 {
		t.Errorf("subscriptions count = %d, want 1", len(cfg.Subscriptions))
	}
	sub := cfg.Subscriptions["default"]
	if sub == nil {
		t.Fatal("subscription 'default' not found")
	}
	if sub.Name != "Default" {
		t.Errorf("name = %q, want %q", sub.Name, "Default")
	}
	if sub.URL != "https://example.com/sub" {
		t.Errorf("url = %q, want %q", sub.URL, "https://example.com/sub")
	}
	if !sub.Default {
		t.Error("default = false, want true")
	}
	if !sub.IsEnabled() {
		t.Error("IsEnabled() = false for subscription with no enabled field")
	}
}

func TestLoad_subscription_disabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	f := false
	_ = f
	writeFile(t, path, `{
		"subscriptions": {
			"s1": {"name": "S1", "url": "https://example.com/s1", "enabled": false}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub := cfg.Subscriptions["s1"]
	if sub == nil {
		t.Fatal("subscription 's1' not found")
	}
	if sub.IsEnabled() {
		t.Error("IsEnabled() = true for explicitly disabled subscription")
	}
}

func TestLoad_singbox_section(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"singbox": {
			"log_level": "warn",
			"test_url": "https://www.gstatic.com/generate_204",
			"template": "/etc/horn-vpn-manager/sing-box.template.json"
		},
		"subscriptions": {
			"s1": {"name": "S1", "url": "https://example.com/s1"}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Singbox.LogLevel != "warn" {
		t.Errorf("singbox.log_level = %q, want %q", cfg.Singbox.LogLevel, "warn")
	}
	if cfg.Singbox.TestURL != "https://www.gstatic.com/generate_204" {
		t.Errorf("singbox.test_url = %q", cfg.Singbox.TestURL)
	}
	if cfg.Singbox.Template != "/etc/horn-vpn-manager/sing-box.template.json" {
		t.Errorf("singbox.template = %q", cfg.Singbox.Template)
	}
}

func TestLoad_subscription_route(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"subscriptions": {
			"work": {
				"name": "Work",
				"url": "https://example.com/work",
				"route": {
					"domains": ["jira.example.com"],
					"domain_urls": ["https://example.com/work-domains.lst"],
					"ip_cidrs": ["203.0.113.0/24"],
					"ip_urls": ["https://example.com/work-ips.lst"]
				}
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub := cfg.Subscriptions["work"]
	if sub.Route == nil {
		t.Fatal("route is nil")
	}
	if len(sub.Route.Domains) != 1 || sub.Route.Domains[0] != "jira.example.com" {
		t.Errorf("domains = %v", sub.Route.Domains)
	}
	if len(sub.Route.IPCIDRs) != 1 || sub.Route.IPCIDRs[0] != "203.0.113.0/24" {
		t.Errorf("ip_cidrs = %v", sub.Route.IPCIDRs)
	}
}

func TestValidateSubscriptions_empty_include_pattern(t *testing.T) {
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"s1": {Name: "S1", URL: "https://example.com/s1", Default: true, Include: []string{""}},
		},
	}
	if err := cfg.ValidateSubscriptions(); err == nil {
		t.Fatal("expected error for empty include pattern")
	}
}

func TestValidateSubscriptions_no_subscriptions(t *testing.T) {
	cfg := &Config{}
	if err := cfg.ValidateSubscriptions(); err == nil {
		t.Fatal("expected error for empty subscriptions")
	}
}

func TestValidateSubscriptions_no_default(t *testing.T) {
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"s1": {Name: "S1", URL: "https://example.com/s1"},
		},
	}
	if err := cfg.ValidateSubscriptions(); err == nil {
		t.Fatal("expected error when no default subscription defined")
	}
}

func TestValidateSubscriptions_multiple_defaults(t *testing.T) {
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"s1": {Name: "S1", URL: "https://example.com/s1", Default: true},
			"s2": {Name: "S2", URL: "https://example.com/s2", Default: true},
		},
	}
	if err := cfg.ValidateSubscriptions(); err == nil {
		t.Fatal("expected error when multiple default subscriptions defined")
	}
}

func TestValidateSubscriptions_disabled_default(t *testing.T) {
	f := false
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"s1": {Name: "S1", URL: "https://example.com/s1", Default: true, Enabled: &f},
		},
	}
	if err := cfg.ValidateSubscriptions(); err == nil {
		t.Fatal("expected error when default subscription is disabled")
	}
}

func TestValidateSubscriptions_valid(t *testing.T) {
	t1 := true
	f := false
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"main":     {Name: "Main", URL: "https://example.com/main", Default: true, Enabled: &t1},
			"disabled": {Name: "Off", URL: "https://example.com/off", Enabled: &f},
		},
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLoad_missing_file(t *testing.T) {
	_, err := Load("/nonexistent/config.json")
	if err == nil {
		t.Fatal("expected error for missing file")
	}
}

func TestLoad_invalid_json(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{invalid`)

	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}
