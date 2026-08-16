package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func TestLoad_singbox_connect_timeout(t *testing.T) {
	cases := []struct {
		name    string
		value   string
		want    string
		wantErr bool
	}{
		{name: "valid duration", value: `"connect_timeout": "3s",`, want: "3s"},
		{name: "absent", value: "", want: ""},
		{name: "explicitly empty", value: `"connect_timeout": "",`, want: ""},
		{name: "invalid duration", value: `"connect_timeout": "3 seconds",`, wantErr: true},
		{name: "unitless number", value: `"connect_timeout": "3",`, wantErr: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "config.json")
			writeFile(t, path, `{
				"singbox": {`+tc.value+`"log_level": "warn"},
				"subscriptions": {
					"s1": {"name": "S1", "url": "https://example.com/s1", "default": true}
				}
			}`)

			cfg, err := Load(path)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error for invalid connect_timeout")
				}
				if !strings.Contains(err.Error(), "connect_timeout") {
					t.Errorf("error should name the offending field: %v", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cfg.Singbox.ConnectTimeout != tc.want {
				t.Errorf("singbox.connect_timeout = %q, want %q", cfg.Singbox.ConnectTimeout, tc.want)
			}
		})
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

const testNodeURI = "vless://11111111-2222-3333-4444-555555555555@203.0.113.10:443?security=reality&sni=example.com&pbk=abc&fp=chrome&type=tcp#Personal"

func TestLoad_subscription_nodes(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"subscriptions": {
			"personal": {
				"name": "Personal",
				"default": true,
				"nodes": ["`+testNodeURI+`"]
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	sub := cfg.Subscriptions["personal"]
	if sub == nil {
		t.Fatal("subscription 'personal' not found")
	}
	if len(sub.Nodes) != 1 || sub.Nodes[0] != testNodeURI {
		t.Errorf("nodes = %v", sub.Nodes)
	}
	if sub.URL != "" {
		t.Errorf("url = %q, want empty", sub.URL)
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestSubscription_nodes_omitted_when_empty(t *testing.T) {
	data, err := json.Marshal(&Subscription{Name: "S1", URL: "https://example.com/s1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "nodes") {
		t.Errorf("empty nodes must be omitted, got %s", data)
	}
}

func TestValidateSubscriptions_source(t *testing.T) {
	f := false
	cases := []struct {
		name    string
		sub     *Subscription
		wantErr string
	}{
		{
			name: "url only",
			sub:  &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true},
		},
		{
			name: "nodes only",
			sub:  &Subscription{Name: "S1", Default: true, Nodes: []string{testNodeURI}},
		},
		{
			name: "explicitly empty url with nodes",
			sub:  &Subscription{Name: "S1", URL: "", Default: true, Nodes: []string{testNodeURI}},
		},
		{
			name:    "both url and nodes",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Nodes: []string{testNodeURI}},
			wantErr: "both url and nodes",
		},
		{
			name:    "neither on enabled subscription",
			sub:     &Subscription{Name: "S1", Default: true},
			wantErr: "neither url nor nodes",
		},
		{
			name:    "empty string in nodes",
			sub:     &Subscription{Name: "S1", Default: true, Nodes: []string{testNodeURI, ""}},
			wantErr: "empty node",
		},
		{
			name:    "unparsable node",
			sub:     &Subscription{Name: "S1", Default: true, Nodes: []string{"https://example.com/not-a-node"}},
			wantErr: "invalid node",
		},
		{
			name:    "disabled subscription with both",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Enabled: &f, Nodes: []string{testNodeURI}},
			wantErr: "both url and nodes",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := map[string]*Subscription{"s1": tc.sub}
			if !tc.sub.Default {
				subs["main"] = &Subscription{Name: "Main", URL: "https://example.com/main", Default: true}
			}
			cfg := &Config{Subscriptions: subs}

			err := cfg.ValidateSubscriptions()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), `"s1"`) {
				t.Errorf("error should name the offending subscription: %v", err)
			}
		})
	}
}

func TestValidateSubscriptions_disabled_without_source(t *testing.T) {
	f := false
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"main":     {Name: "Main", URL: "https://example.com/main", Default: true},
			"disabled": {Name: "Off", Enabled: &f},
		},
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("disabled subscription without url or nodes must be accepted: %v", err)
	}
}

func TestLoad_subscription_fallback(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"subscriptions": {
			"personal": {
				"name": "Personal",
				"url": "https://example.com/personal",
				"default": true,
				"fallback": {
					"subscriptions": ["backup", "spare"],
					"blacklist_timeout": "1m"
				}
			},
			"backup": {"name": "Backup", "url": "https://example.com/backup"},
			"spare": {"name": "Spare", "url": "https://example.com/spare"}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	fb := cfg.Subscriptions["personal"].Fallback
	if fb == nil {
		t.Fatal("fallback not parsed")
	}
	if got := strings.Join(fb.Subscriptions, ","); got != "backup,spare" {
		t.Errorf("chain order = %q, want \"backup,spare\"", got)
	}
	if fb.BlacklistTimeout != "1m" {
		t.Errorf("blacklist_timeout = %q", fb.BlacklistTimeout)
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("unexpected validation error: %v", err)
	}
}

func TestSubscription_fallback_omitted_when_absent(t *testing.T) {
	data, err := json.Marshal(&Subscription{Name: "S1", URL: "https://example.com/s1"})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(data), "fallback") {
		t.Errorf("absent fallback must be omitted, got %s", data)
	}
}

func TestValidateSubscriptions_fallback(t *testing.T) {
	f := false
	// Every case declares the chain on "s1"; "backup" and "off" exist as targets.
	cases := []struct {
		name    string
		sub     *Subscription
		wantErr string
	}{
		{
			name: "single backup",
			sub:  &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"backup"}}},
		},
		{
			name: "backup on a non-default subscription",
			sub:  &Subscription{Name: "S1", URL: "https://example.com/s1", Fallback: &Fallback{Subscriptions: []string{"backup"}}},
		},
		{
			name: "inline nodes with a chain",
			sub:  &Subscription{Name: "S1", Default: true, Nodes: []string{testNodeURI}, Fallback: &Fallback{Subscriptions: []string{"backup"}}},
		},
		{
			name: "valid blacklist_timeout",
			sub:  &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"backup"}, BlacklistTimeout: "90s"}},
		},
		{
			name:    "empty chain",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{}},
			wantErr: "empty fallback chain",
		},
		{
			name:    "empty id in chain",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{""}}},
			wantErr: "empty id in its fallback chain",
		},
		{
			name:    "self reference",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"s1"}}},
			wantErr: "lists itself",
		},
		{
			name:    "duplicate backup",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"backup", "backup"}}},
			wantErr: `lists "backup" twice`,
		},
		{
			name:    "unknown backup",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"ghost"}}},
			wantErr: `unknown subscription "ghost"`,
		},
		{
			name:    "disabled backup",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"off"}}},
			wantErr: `disabled subscription "off"`,
		},
		{
			name:    "invalid blacklist_timeout",
			sub:     &Subscription{Name: "S1", URL: "https://example.com/s1", Default: true, Fallback: &Fallback{Subscriptions: []string{"backup"}, BlacklistTimeout: "1 minute"}},
			wantErr: "invalid fallback blacklist_timeout",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			subs := map[string]*Subscription{
				"s1":     tc.sub,
				"backup": {Name: "Backup", URL: "https://example.com/backup"},
				"off":    {Name: "Off", URL: "https://example.com/off", Enabled: &f},
			}
			if !tc.sub.Default {
				subs["main"] = &Subscription{Name: "Main", URL: "https://example.com/main", Default: true}
			}
			cfg := &Config{Subscriptions: subs}

			err := cfg.ValidateSubscriptions()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
			if !strings.Contains(err.Error(), `"s1"`) {
				t.Errorf("error should name the declaring subscription: %v", err)
			}
		})
	}
}

func TestValidateSubscriptions_fallback_chain_order_preserved(t *testing.T) {
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"main": {
				Name: "Main", URL: "https://example.com/main", Default: true,
				Fallback: &Fallback{Subscriptions: []string{"b2", "b1"}},
			},
			"b1": {Name: "B1", URL: "https://example.com/b1"},
			"b2": {Name: "B2", URL: "https://example.com/b2"},
		},
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := strings.Join(cfg.Subscriptions["main"].Fallback.Subscriptions, ","); got != "b2,b1" {
		t.Errorf("validation must not reorder the chain, got %q", got)
	}
}

func TestValidateSubscriptions_fallback_disabled_declarer_unvalidated(t *testing.T) {
	f := false
	cfg := &Config{
		Subscriptions: map[string]*Subscription{
			"main": {Name: "Main", URL: "https://example.com/main", Default: true},
			"off": {
				Name: "Off", URL: "https://example.com/off", Enabled: &f,
				Fallback: &Fallback{Subscriptions: []string{"ghost", "ghost"}},
			},
		},
	}
	if err := cfg.ValidateSubscriptions(); err != nil {
		t.Fatalf("a disabled subscription's chain is never generated and must not be validated: %v", err)
	}
}

func TestValidateSubscriptions_fallback_cycles(t *testing.T) {
	f := false
	cases := []struct {
		name    string
		subs    map[string]*Subscription
		wantErr string
	}{
		{
			name: "two-node cycle",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b"}}},
				"b": {Name: "B", URL: "https://example.com/b", Fallback: &Fallback{Subscriptions: []string{"a"}}},
			},
			wantErr: "a -> b -> a",
		},
		{
			name: "three-node cycle",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b"}}},
				"b": {Name: "B", URL: "https://example.com/b", Fallback: &Fallback{Subscriptions: []string{"c"}}},
				"c": {Name: "C", URL: "https://example.com/c", Fallback: &Fallback{Subscriptions: []string{"a"}}},
			},
			wantErr: "a -> b -> c -> a",
		},
		{
			name: "cycle not involving the entry point",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b"}}},
				"b": {Name: "B", URL: "https://example.com/b", Fallback: &Fallback{Subscriptions: []string{"c"}}},
				"c": {Name: "C", URL: "https://example.com/c", Fallback: &Fallback{Subscriptions: []string{"b"}}},
			},
			wantErr: "b -> c -> b",
		},
		{
			name: "linear chain is not a cycle",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b"}}},
				"b": {Name: "B", URL: "https://example.com/b", Fallback: &Fallback{Subscriptions: []string{"c"}}},
				"c": {Name: "C", URL: "https://example.com/c"},
			},
		},
		{
			name: "diamond is not a cycle",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b", "c"}}},
				"b": {Name: "B", URL: "https://example.com/b", Fallback: &Fallback{Subscriptions: []string{"d"}}},
				"c": {Name: "C", URL: "https://example.com/c", Fallback: &Fallback{Subscriptions: []string{"d"}}},
				"d": {Name: "D", URL: "https://example.com/d"},
			},
		},
		{
			name: "cycle broken by a disabled subscription",
			subs: map[string]*Subscription{
				"a": {Name: "A", URL: "https://example.com/a", Default: true, Fallback: &Fallback{Subscriptions: []string{"b"}}},
				"b": {Name: "B", URL: "https://example.com/b", Enabled: &f, Fallback: &Fallback{Subscriptions: []string{"a"}}},
			},
			// "a" referencing the disabled "b" is rejected by validateFallback,
			// with the more specific message.
			wantErr: `disabled subscription "b"`,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{Subscriptions: tc.subs}
			err := cfg.ValidateSubscriptions()
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error = %v, want it to contain %q", err, tc.wantErr)
			}
		})
	}
}

func TestLoad_manual_file_only_routing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	writeFile(t, path, `{
		"routing": {
			"subnets": {
				"manual_file": "/etc/horn-vpn-manager/lists/manual-ip.lst"
			}
		}
	}`)

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error for manual_file-only routing: %v", err)
	}
	if cfg.Routing.Subnets.ManualFile != "/etc/horn-vpn-manager/lists/manual-ip.lst" {
		t.Errorf("manual_file = %q", cfg.Routing.Subnets.ManualFile)
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
