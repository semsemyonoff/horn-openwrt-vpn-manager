package singbox

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLoadTemplate_empty_path_returns_embedded(t *testing.T) {
	data, err := LoadTemplate("")
	if err != nil {
		t.Fatalf("LoadTemplate(\"\") error: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("expected non-empty embedded template")
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("embedded template is not valid JSON: %v", err)
	}
}

func TestLoadTemplate_nonexistent_path_returns_error(t *testing.T) {
	_, err := LoadTemplate("/nonexistent/path/template.json")
	if err == nil {
		t.Fatal("expected error for nonexistent template path")
	}
}

func TestRenderConfig_basic_single_node(t *testing.T) {
	tmpl := `{
  "log": {"level": "info"},
  "inbounds": [{"type": "tun", "tag": "tun-in"}],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "block", "tag": "block"}
  ],
  "route": {
    "rules": [],
    "final": "placeholder",
    "auto_detect_interface": true
  }
}`

	type nodeOb struct {
		Type       string `json:"type"`
		Tag        string `json:"tag"`
		Server     string `json:"server"`
		ServerPort int    `json:"server_port"`
		UUID       string `json:"uuid"`
	}

	outbounds := []any{
		nodeOb{Type: "vless", Tag: "sub-single", Server: "1.2.3.4", ServerPort: 443, UUID: "uuid1"},
	}

	data, err := RenderConfig([]byte(tmpl), outbounds, nil, "sub-single", "warn")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("result is not valid JSON: %v", err)
	}

	// log level overridden
	log, _ := cfg["log"].(map[string]any)
	if log["level"] != "warn" {
		t.Errorf("log.level = %v, want warn", log["level"])
	}

	// route.final set
	route, _ := cfg["route"].(map[string]any)
	if route["final"] != "sub-single" {
		t.Errorf("route.final = %v, want sub-single", route["final"])
	}

	// outbounds: node first, then non-deprecated static
	obs, _ := cfg["outbounds"].([]any)
	if len(obs) != 2 {
		t.Fatalf("expected 2 outbounds (node + direct), got %d", len(obs))
	}
	first, _ := obs[0].(map[string]any)
	if first["tag"] != "sub-single" {
		t.Errorf("first outbound tag = %v, want sub-single", first["tag"])
	}
	last, _ := obs[1].(map[string]any)
	if last["tag"] != "direct" {
		t.Errorf("last outbound tag = %v, want direct", last["tag"])
	}
}

func TestRenderConfig_multinode_groups(t *testing.T) {
	tmpl := `{"outbounds": [], "route": {"rules": [], "final": ""}}`

	type urltest struct {
		Type      string   `json:"type"`
		Tag       string   `json:"tag"`
		Outbounds []string `json:"outbounds"`
		URL       string   `json:"url"`
		Interval  string   `json:"interval"`
		Tolerance int      `json:"tolerance"`
	}
	type selector struct {
		Type      string   `json:"type"`
		Tag       string   `json:"tag"`
		Outbounds []string `json:"outbounds"`
		Default   string   `json:"default"`
	}
	type node struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}

	outbounds := []any{
		node{Type: "vless", Tag: "sub-node-abc12345"},
		node{Type: "vless", Tag: "sub-node-def67890"},
		urltest{
			Type:      "urltest",
			Tag:       "sub-auto",
			Outbounds: []string{"sub-node-abc12345", "sub-node-def67890"},
			URL:       "https://www.gstatic.com/generate_204",
			Interval:  "5m",
			Tolerance: 100,
		},
		selector{
			Type:      "selector",
			Tag:       "sub-manual",
			Outbounds: []string{"sub-auto", "sub-node-abc12345", "sub-node-def67890"},
			Default:   "sub-auto",
		},
	}

	data, err := RenderConfig([]byte(tmpl), outbounds, nil, "sub-manual", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	obs, _ := cfg["outbounds"].([]any)
	if len(obs) != 4 {
		t.Errorf("expected 4 outbounds, got %d", len(obs))
	}

	route, _ := cfg["route"].(map[string]any)
	if route["final"] != "sub-manual" {
		t.Errorf("route.final = %v, want sub-manual", route["final"])
	}
}

func TestRenderConfig_route_rules_prepended(t *testing.T) {
	tmpl := `{
  "outbounds": [],
  "route": {
    "rules": [{"inbound": ["tun-in"], "action": "sniff"}],
    "final": ""
  }
}`
	type routeRule struct {
		DomainSuffix []string `json:"domain_suffix,omitempty"`
		Outbound     string   `json:"outbound"`
	}

	rules := []any{
		routeRule{DomainSuffix: []string{"jira.example.com"}, Outbound: "work-single"},
	}

	data, err := RenderConfig([]byte(tmpl), nil, rules, "work-single", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	route, _ := cfg["route"].(map[string]any)
	ruleList, _ := route["rules"].([]any)
	if len(ruleList) != 2 {
		t.Fatalf("expected 2 rules (sniff + generated), got %d", len(ruleList))
	}

	sniff, _ := ruleList[0].(map[string]any)
	if sniff["action"] != "sniff" {
		t.Errorf("first rule action = %v, want sniff", sniff["action"])
	}

	// Generated rule should follow the mandatory sniff action.
	first, _ := ruleList[0].(map[string]any)
	second, _ := ruleList[1].(map[string]any)
	ds, _ := second["domain_suffix"].([]any)
	if len(ds) == 0 || ds[0] != "jira.example.com" {
		t.Errorf("second rule domain_suffix = %v, want [jira.example.com]", ds)
	}
	if first["action"] != "sniff" {
		t.Errorf("first rule = %v, want sniff action", first)
	}
}

func TestRenderConfig_strips_legacy_placeholders(t *testing.T) {
	tmpl := `{
  "outbounds": [
    "__VLESS_OUTBOUNDS__",
    "__GROUP_OUTBOUNDS__",
    {"type": "direct", "tag": "direct"},
    {"type": "block", "tag": "block"}
  ],
  "route": {
    "rules": ["__ROUTE_RULES__"],
    "final": "__DEFAULT_TAG__"
  }
}`
	type node struct {
		Type string `json:"type"`
		Tag  string `json:"tag"`
	}

	outbounds := []any{node{Type: "vless", Tag: "sub-single"}}

	data, err := RenderConfig([]byte(tmpl), outbounds, nil, "sub-single", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	if strings.Contains(string(data), "__VLESS_OUTBOUNDS__") {
		t.Error("placeholder __VLESS_OUTBOUNDS__ still in output")
	}
	if strings.Contains(string(data), "__DEFAULT_TAG__") {
		t.Error("placeholder __DEFAULT_TAG__ still in output")
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	obs, _ := cfg["outbounds"].([]any)
	// 1 node + direct = 2; placeholders and legacy block stripped
	if len(obs) != 2 {
		t.Errorf("expected 2 outbounds, got %d", len(obs))
	}

	route, _ := cfg["route"].(map[string]any)
	ruleList, _ := route["rules"].([]any)
	// No generated rules, placeholder stripped, mandatory sniff retained.
	if len(ruleList) != 1 {
		t.Fatalf("expected 1 sniff rule after stripping placeholder, got %d", len(ruleList))
	}
	sniff, _ := ruleList[0].(map[string]any)
	if sniff["action"] != "sniff" {
		t.Errorf("rule action = %v, want sniff", sniff["action"])
	}
}

func TestRenderConfig_no_log_override_when_empty(t *testing.T) {
	tmpl := `{"log": {"level": "debug"}, "outbounds": [], "route": {"rules": [], "final": ""}}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	log, _ := cfg["log"].(map[string]any)
	if log["level"] != "debug" {
		t.Errorf("log.level = %v, want debug (should not be overridden when logLevel is empty)", log["level"])
	}
}

func TestRenderConfig_log_level_preserves_other_fields(t *testing.T) {
	tmpl := `{"log": {"level": "info", "output": "/var/log/sing-box.log"}, "outbounds": [], "route": {"rules": [], "final": ""}}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "warn")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	log, _ := cfg["log"].(map[string]any)
	if log["level"] != "warn" {
		t.Errorf("log.level = %v, want warn", log["level"])
	}
	if log["output"] != "/var/log/sing-box.log" {
		t.Errorf("log.output = %v, want /var/log/sing-box.log", log["output"])
	}
}

func TestRenderConfig_empty_plans_keeps_static_outbounds(t *testing.T) {
	tmpl := `{
  "outbounds": [{"type": "direct", "tag": "direct"}],
  "route": {"rules": [], "final": "direct"}
}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	obs, _ := cfg["outbounds"].([]any)
	if len(obs) != 1 {
		t.Errorf("expected 1 outbound (direct), got %d", len(obs))
	}
}

func TestRenderConfig_strips_singbox_113_deprecated_fields(t *testing.T) {
	tmpl := `{
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "domain_strategy": "ipv4_only",
      "sniff": true,
      "sniff_override_destination": false,
      "address": ["172.16.250.1/30"]
    }
  ],
  "outbounds": [
    {"type": "direct", "tag": "direct"},
    {"type": "block", "tag": "block"},
    {"type": "dns", "tag": "dns-out"}
  ],
  "route": {"rules": [], "final": "direct"}
}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	inbounds, _ := cfg["inbounds"].([]any)
	inbound, _ := inbounds[0].(map[string]any)
	for _, key := range []string{"domain_strategy", "sniff", "sniff_override_destination"} {
		if _, ok := inbound[key]; ok {
			t.Errorf("deprecated inbound field %q was not stripped", key)
		}
	}

	outbounds, _ := cfg["outbounds"].([]any)
	if len(outbounds) != 1 {
		t.Fatalf("expected only direct outbound after stripping legacy special outbounds, got %d", len(outbounds))
	}
	outbound, _ := outbounds[0].(map[string]any)
	if outbound["tag"] != "direct" {
		t.Errorf("outbound tag = %v, want direct", outbound["tag"])
	}

	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	if len(rules) != 1 {
		t.Fatalf("expected one sniff rule, got %d", len(rules))
	}
	rule, _ := rules[0].(map[string]any)
	if rule["action"] != "sniff" {
		t.Errorf("first route action = %v, want sniff", rule["action"])
	}
}

func TestIsPlaceholder(t *testing.T) {
	tests := []struct {
		raw  string
		want bool
	}{
		{`"__PLACEHOLDER__"`, true},
		{`{"type": "direct"}`, false},
		{`[]`, false},
		{`  "  spaces  "  `, true},
		{`123`, false},
		{`true`, false},
	}
	for _, tt := range tests {
		got := isPlaceholder(json.RawMessage(tt.raw))
		if got != tt.want {
			t.Errorf("isPlaceholder(%q) = %v, want %v", tt.raw, got, tt.want)
		}
	}
}

func TestRenderConfig_preserves_unknown_route_fields(t *testing.T) {
	// A template with a route section containing fields we do not model
	// (geoip, geosite, etc.) must survive the render pass unchanged.
	tmpl := `{
  "outbounds": [],
  "route": {
    "rules": [],
    "final": "",
    "auto_detect_interface": true,
    "geoip": {"download_detour": "direct"},
    "geosite": {"download_detour": "direct"}
  }
}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	route, _ := cfg["route"].(map[string]any)
	if route["geoip"] == nil {
		t.Error("route.geoip was dropped by RenderConfig; unknown route fields must be preserved")
	}
	if route["geosite"] == nil {
		t.Error("route.geosite was dropped by RenderConfig; unknown route fields must be preserved")
	}
	if route["auto_detect_interface"] != true {
		t.Errorf("route.auto_detect_interface = %v, want true", route["auto_detect_interface"])
	}
}

func TestRenderConfig_preserves_unknown_top_level_fields(t *testing.T) {
	// A template with top-level keys outside the set we manage must survive
	// the render pass unchanged (e.g. certificate, endpoints, or any future
	// sing-box top-level section).
	tmpl := `{
  "log": {"level": "info"},
  "outbounds": [],
  "route": {"rules": [], "final": ""},
  "certificate": [{"certificate_path": "/etc/ssl/cert.pem"}],
  "endpoints": [{"type": "wireguard", "tag": "wg-out"}]
}`

	data, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err != nil {
		t.Fatalf("RenderConfig error: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if cfg["certificate"] == nil {
		t.Error("top-level certificate was dropped; unknown top-level fields must be preserved")
	}
	if cfg["endpoints"] == nil {
		t.Error("top-level endpoints was dropped; unknown top-level fields must be preserved")
	}
}

func TestRenderConfig_rejects_non_array_outbounds(t *testing.T) {
	tmpl := `{"outbounds": {"type": "direct"}, "route": {"rules": [], "final": "direct"}}`
	_, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err == nil {
		t.Fatal("expected error when template outbounds is not an array, got nil")
	}
}

func TestRenderConfig_rejects_non_array_route_rules(t *testing.T) {
	tmpl := `{"outbounds": [], "route": {"rules": "not-an-array", "final": "direct"}}`
	_, err := RenderConfig([]byte(tmpl), nil, nil, "direct", "")
	if err == nil {
		t.Fatal("expected error when template route.rules is not an array, got nil")
	}
}

func TestRenderConfig_uses_embedded_template(t *testing.T) {
	// Load empty path → should use embedded default template, which is valid JSON
	tmplData, err := LoadTemplate("")
	if err != nil {
		t.Fatalf("LoadTemplate: %v", err)
	}

	data, err := RenderConfig(tmplData, nil, nil, "direct", "warn")
	if err != nil {
		t.Fatalf("RenderConfig with embedded template: %v", err)
	}

	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	// Template has direct outbound and a mandatory sniff route rule.
	obs, _ := cfg["outbounds"].([]any)
	if len(obs) < 1 {
		t.Errorf("expected at least 1 outbound from default template, got %d", len(obs))
	}

	route, _ := cfg["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	first, _ := rules[0].(map[string]any)
	if first["action"] != "sniff" {
		t.Errorf("first route action = %v, want sniff", first["action"])
	}
}
