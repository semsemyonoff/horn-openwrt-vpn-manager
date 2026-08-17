package subscription_test

import (
	"bytes"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/hysteria2"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/singbox"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

const testURL = "https://www.gstatic.com/generate_204"

// buildOpts are the explicit-value options used by tests that do not exercise
// defaulting or connect_timeout.
var buildOpts = subscription.BuildOptions{Interval: "5m", Tolerance: 100, TestURL: testURL}

// vlessOB returns the plan's i-th node outbound as the concrete VLESS type.
// NodeOutbounds is []any because each protocol owns its outbound struct, so a
// test asserting on VLESS fields has to state which type it expects.
func vlessOB(t *testing.T, plan *subscription.OutboundPlan, i int) *vless.Outbound {
	t.Helper()
	if i >= len(plan.NodeOutbounds) {
		t.Fatalf("NodeOutbounds[%d] out of range: len %d", i, len(plan.NodeOutbounds))
	}
	ob, ok := plan.NodeOutbounds[i].(*vless.Outbound)
	if !ok {
		t.Fatalf("NodeOutbounds[%d] is %T, want *vless.Outbound", i, plan.NodeOutbounds[i])
	}
	return ob
}

func TestBuildOutbounds_SingleNode(t *testing.T) {
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
	}
	plan, err := subscription.BuildOutbounds("default", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Single-node mode must use <id>-single tag.
	if plan.FinalTag != "default-single" {
		t.Errorf("FinalTag: got %q want %q", plan.FinalTag, "default-single")
	}
	if len(plan.NodeOutbounds) != 1 {
		t.Fatalf("NodeOutbounds len: got %d want 1", len(plan.NodeOutbounds))
	}
	ob := vlessOB(t, plan, 0)
	if ob.Tag != "default-single" {
		t.Errorf("outbound Tag: got %q want %q", ob.Tag, "default-single")
	}
	if len(plan.NodeTags) != 1 || plan.NodeTags[0] != "default-single" {
		t.Errorf("NodeTags: got %v want [default-single]", plan.NodeTags)
	}
	if ob.Type != "vless" {
		t.Errorf("outbound Type: got %q want %q", ob.Type, "vless")
	}
	if ob.Server != "host1.example.com" {
		t.Errorf("Server: got %q want %q", ob.Server, "host1.example.com")
	}
	if ob.ServerPort != 443 {
		t.Errorf("ServerPort: got %d want 443", ob.ServerPort)
	}
	if ob.UUID != "uuid1" {
		t.Errorf("UUID: got %q want %q", ob.UUID, "uuid1")
	}

	// No group outbounds for single-node.
	if plan.URLTestGroup != nil {
		t.Error("URLTestGroup should be nil for single-node subscription")
	}
	if plan.SelectorGroup != nil {
		t.Error("SelectorGroup should be nil for single-node subscription")
	}

	// TagNames must have the single entry.
	if name, ok := plan.TagNames["default-single"]; !ok {
		t.Error("TagNames missing 'default-single'")
	} else if name != "Node 1" {
		t.Errorf("TagNames[default-single]: got %q want %q", name, "Node 1")
	}
}

func TestBuildOutbounds_MultiNode(t *testing.T) {
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
		"vless://uuid3@host3.example.com:8443?security=reality&pbk=abc123#Node+3",
	}
	plan, err := subscription.BuildOutbounds("default", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Multi-node mode must use <id>-manual as FinalTag.
	if plan.FinalTag != "default-manual" {
		t.Errorf("FinalTag: got %q want %q", plan.FinalTag, "default-manual")
	}
	if len(plan.NodeOutbounds) != 3 {
		t.Fatalf("NodeOutbounds len: got %d want 3", len(plan.NodeOutbounds))
	}

	// NodeTags must stay aligned with NodeOutbounds: it is the only way to read
	// a tag back once the outbound is opaque.
	if len(plan.NodeTags) != len(plan.NodeOutbounds) {
		t.Fatalf("NodeTags len %d != NodeOutbounds len %d", len(plan.NodeTags), len(plan.NodeOutbounds))
	}
	for i, tag := range plan.NodeTags {
		if got := vlessOB(t, plan, i).Tag; got != tag {
			t.Errorf("NodeTags[%d] = %q but outbound carries %q", i, tag, got)
		}
	}

	// Each node must be tagged <id>-node-<8char-hash>.
	for _, tag := range plan.NodeTags {
		if !strings.HasPrefix(tag, "default-node-") {
			t.Errorf("node tag %q should start with 'default-node-'", tag)
		}
		hash := strings.TrimPrefix(tag, "default-node-")
		if len(hash) != 8 {
			t.Errorf("node hash should be 8 chars, got %d in tag %q", len(hash), tag)
		}
	}

	// URLTest group must exist and reference all node tags.
	if plan.URLTestGroup == nil {
		t.Fatal("URLTestGroup should not be nil for multi-node subscription")
	}
	if plan.URLTestGroup.Tag != "default-auto" {
		t.Errorf("URLTestGroup Tag: got %q want %q", plan.URLTestGroup.Tag, "default-auto")
	}
	if plan.URLTestGroup.Type != "urltest" {
		t.Errorf("URLTestGroup Type: got %q want %q", plan.URLTestGroup.Type, "urltest")
	}
	if len(plan.URLTestGroup.Outbounds) != 3 {
		t.Errorf("URLTestGroup Outbounds len: got %d want 3", len(plan.URLTestGroup.Outbounds))
	}
	if plan.URLTestGroup.URL != testURL {
		t.Errorf("URLTestGroup URL: got %q want %q", plan.URLTestGroup.URL, testURL)
	}
	if plan.URLTestGroup.Interval != "5m" {
		t.Errorf("URLTestGroup Interval: got %q want %q", plan.URLTestGroup.Interval, "5m")
	}
	if plan.URLTestGroup.Tolerance != 100 {
		t.Errorf("URLTestGroup Tolerance: got %d want 100", plan.URLTestGroup.Tolerance)
	}

	// Selector group must exist with auto as first outbound and default.
	if plan.SelectorGroup == nil {
		t.Fatal("SelectorGroup should not be nil for multi-node subscription")
	}
	if plan.SelectorGroup.Tag != "default-manual" {
		t.Errorf("SelectorGroup Tag: got %q want %q", plan.SelectorGroup.Tag, "default-manual")
	}
	if plan.SelectorGroup.Type != "selector" {
		t.Errorf("SelectorGroup Type: got %q want %q", plan.SelectorGroup.Type, "selector")
	}
	if plan.SelectorGroup.Default != "default-auto" {
		t.Errorf("SelectorGroup Default: got %q want %q", plan.SelectorGroup.Default, "default-auto")
	}
	if len(plan.SelectorGroup.Outbounds) != 4 { // auto + 3 nodes
		t.Errorf("SelectorGroup Outbounds len: got %d want 4", len(plan.SelectorGroup.Outbounds))
	}
	if plan.SelectorGroup.Outbounds[0] != "default-auto" {
		t.Errorf("SelectorGroup first outbound: got %q want %q", plan.SelectorGroup.Outbounds[0], "default-auto")
	}

	// TagNames must include all tags.
	for _, tag := range []string{"default-auto", "default-manual"} {
		if _, ok := plan.TagNames[tag]; !ok {
			t.Errorf("TagNames missing %q", tag)
		}
	}
	for _, tag := range plan.NodeTags {
		if _, ok := plan.TagNames[tag]; !ok {
			t.Errorf("TagNames missing node tag %q", tag)
		}
	}
}

func TestBuildOutbounds_TagsAreStable(t *testing.T) {
	// Running BuildOutbounds twice with the same URIs must produce the same tags.
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
	}
	p1, err := subscription.BuildOutbounds("sub", uris, subscription.BuildOptions{TestURL: testURL})
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	p2, err := subscription.BuildOutbounds("sub", uris, subscription.BuildOptions{TestURL: testURL})
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	for i, tag := range p1.NodeTags {
		if tag != p2.NodeTags[i] {
			t.Errorf("node %d tag mismatch: %q vs %q", i, tag, p2.NodeTags[i])
		}
	}
}

func TestBuildOutbounds_Defaults(t *testing.T) {
	// Empty interval and zero tolerance must fall back to defaults.
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls#A",
		"vless://uuid2@host2.example.com:443?security=tls#B",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, subscription.BuildOptions{TestURL: testURL})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.URLTestGroup.Interval != "5m" {
		t.Errorf("default interval: got %q want 5m", plan.URLTestGroup.Interval)
	}
	if plan.URLTestGroup.Tolerance != 100 {
		t.Errorf("default tolerance: got %d want 100", plan.URLTestGroup.Tolerance)
	}
}

func TestBuildOutbounds_ConnectTimeout(t *testing.T) {
	single := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
	}
	multi := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
	}

	t.Run("emitted on every node outbound", func(t *testing.T) {
		for name, uris := range map[string][]string{"single-node": single, "multi-node": multi} {
			t.Run(name, func(t *testing.T) {
				opts := subscription.BuildOptions{Interval: "5m", Tolerance: 100, TestURL: testURL, ConnectTimeout: "3s"}
				plan, err := subscription.BuildOutbounds("sub", uris, opts)
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if len(plan.NodeOutbounds) != len(uris) {
					t.Fatalf("NodeOutbounds len: got %d want %d", len(plan.NodeOutbounds), len(uris))
				}
				for i := range plan.NodeOutbounds {
					ob := vlessOB(t, plan, i)
					if ob.ConnectTimeout != "3s" {
						t.Errorf("%s: ConnectTimeout = %q, want %q", ob.Tag, ob.ConnectTimeout, "3s")
					}
					m := marshalToMap(t, ob)
					if m["connect_timeout"] != "3s" {
						t.Errorf("%s: connect_timeout in JSON = %v, want 3s", ob.Tag, m["connect_timeout"])
					}
				}
			})
		}
	})

	t.Run("empty value omits the field", func(t *testing.T) {
		plan, err := subscription.BuildOutbounds("sub", multi, buildOpts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for i, tag := range plan.NodeTags {
			m := marshalToMap(t, plan.NodeOutbounds[i])
			if _, ok := m["connect_timeout"]; ok {
				t.Errorf("%s: connect_timeout present in JSON with empty option: %v", tag, m["connect_timeout"])
			}
		}
	})

	t.Run("not set on group outbounds", func(t *testing.T) {
		opts := subscription.BuildOptions{Interval: "5m", Tolerance: 100, TestURL: testURL, ConnectTimeout: "3s"}
		plan, err := subscription.BuildOutbounds("sub", multi, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		for name, group := range map[string]any{"urltest": plan.URLTestGroup, "selector": plan.SelectorGroup} {
			if _, ok := marshalToMap(t, group)["connect_timeout"]; ok {
				t.Errorf("%s group: connect_timeout is a dial field and must not appear on a group", name)
			}
		}
	})

	t.Run("does not affect dedup", func(t *testing.T) {
		dup := "vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1"
		uris := []string{dup, "vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2", dup}
		opts := subscription.BuildOptions{Interval: "5m", Tolerance: 100, TestURL: testURL, ConnectTimeout: "3s"}
		plan, err := subscription.BuildOutbounds("sub", uris, opts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(plan.NodeOutbounds) != 2 {
			t.Errorf("NodeOutbounds len: got %d want 2", len(plan.NodeOutbounds))
		}
	})
}

func marshalToMap(t *testing.T, v any) map[string]any {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	return m
}

func TestBuildOutbounds_TLSBlock(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&sni=server.example.com&fp=chrome#TLS+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	if ob.TLS == nil {
		t.Fatal("TLS block should not be nil for security=tls")
	}
	if !ob.TLS.Enabled {
		t.Error("TLS.Enabled should be true")
	}
	if ob.TLS.ServerName != "server.example.com" {
		t.Errorf("TLS.ServerName: got %q want %q", ob.TLS.ServerName, "server.example.com")
	}
	if ob.TLS.UTLS == nil {
		t.Fatal("TLS.UTLS should not be nil when fp is set")
	}
	if ob.TLS.UTLS.Fingerprint != "chrome" {
		t.Errorf("TLS.UTLS.Fingerprint: got %q want %q", ob.TLS.UTLS.Fingerprint, "chrome")
	}
}

func TestBuildOutbounds_RealityBlock(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:8443?security=reality&pbk=mypubkey&sid=myshortid&sni=www.example.com#Reality+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	if ob.TLS == nil {
		t.Fatal("TLS block should not be nil for security=reality")
	}
	if ob.TLS.Reality == nil {
		t.Fatal("TLS.Reality should not be nil when security=reality and pbk is set")
	}
	if !ob.TLS.Reality.Enabled {
		t.Error("TLS.Reality.Enabled should be true")
	}
	if ob.TLS.Reality.PublicKey != "mypubkey" {
		t.Errorf("TLS.Reality.PublicKey: got %q want %q", ob.TLS.Reality.PublicKey, "mypubkey")
	}
	if ob.TLS.Reality.ShortID != "myshortid" {
		t.Errorf("TLS.Reality.ShortID: got %q want %q", ob.TLS.Reality.ShortID, "myshortid")
	}
}

func TestBuildOutbounds_WSTransport(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&sni=cdn.example.com&type=ws&path=%2Fws&host=cdn.example.com#WS+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	if ob.Transport == nil {
		t.Fatal("Transport should not be nil for ws type")
	}

	// Marshal to JSON to verify the transport shape.
	data, err := json.Marshal(ob.Transport)
	if err != nil {
		t.Fatalf("marshal transport: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal transport: %v", err)
	}
	if m["type"] != "ws" {
		t.Errorf("transport type: got %v want ws", m["type"])
	}
	if m["path"] != "/ws" {
		t.Errorf("transport path: got %v want /ws", m["path"])
	}
	headers, ok := m["headers"].(map[string]any)
	if !ok {
		t.Fatal("transport headers should be a JSON object")
	}
	if headers["Host"] != "cdn.example.com" {
		t.Errorf("transport Host header: got %v want cdn.example.com", headers["Host"])
	}
}

func TestBuildOutbounds_GRPCTransport(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&type=grpc&serviceName=myGRPC#GRPC+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	if ob.Transport == nil {
		t.Fatal("Transport should not be nil for grpc type")
	}
	data, err := json.Marshal(ob.Transport)
	if err != nil {
		t.Fatalf("marshal transport: %v", err)
	}
	var m map[string]any
	_ = json.Unmarshal(data, &m)
	if m["type"] != "grpc" {
		t.Errorf("transport type: got %v want grpc", m["type"])
	}
	if m["service_name"] != "myGRPC" {
		t.Errorf("transport service_name: got %v want myGRPC", m["service_name"])
	}
}

func TestBuildOutbounds_XHTTPTransport(t *testing.T) {
	t.Run("defaults applied when mode and alpn absent", func(t *testing.T) {
		uris := []string{
			"vless://uuid@server.example.com:443?security=tls&sni=server.example.com&type=xhttp&path=%2Fpath#XHTTP+Node",
		}
		plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ob := vlessOB(t, plan, 0)

		// Transport defaults.
		if ob.Transport == nil {
			t.Fatal("Transport should not be nil for xhttp type")
		}
		data, err := json.Marshal(ob.Transport)
		if err != nil {
			t.Fatalf("marshal transport: %v", err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if m["type"] != "xhttp" {
			t.Errorf("transport type: got %v want xhttp", m["type"])
		}
		if m["mode"] != "auto" {
			t.Errorf("transport mode: got %v want auto", m["mode"])
		}

		// TLS ALPN default.
		if ob.TLS == nil {
			t.Fatal("TLS block should not be nil for security=tls")
		}
		if len(ob.TLS.ALPN) != 1 || ob.TLS.ALPN[0] != "h2" {
			t.Errorf("TLS.ALPN: got %v want [h2]", ob.TLS.ALPN)
		}
	})

	t.Run("explicit mode and alpn are preserved", func(t *testing.T) {
		uris := []string{
			"vless://uuid@server.example.com:443?security=tls&sni=server.example.com&type=xhttp&mode=stream-up&alpn=h3#XHTTP+Node",
		}
		plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		ob := vlessOB(t, plan, 0)

		data, err := json.Marshal(ob.Transport)
		if err != nil {
			t.Fatalf("marshal transport: %v", err)
		}
		var m map[string]any
		_ = json.Unmarshal(data, &m)
		if m["mode"] != "stream-up" {
			t.Errorf("transport mode: got %v want stream-up", m["mode"])
		}
		if len(ob.TLS.ALPN) != 1 || ob.TLS.ALPN[0] != "h3" {
			t.Errorf("TLS.ALPN: got %v want [h3]", ob.TLS.ALPN)
		}
	})
}

func TestBuildOutbounds_DeduplicatesIdenticalNodes(t *testing.T) {
	dup := "vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1"
	uris := []string{
		dup,
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
		dup,
		dup,
	}
	plan, err := subscription.BuildOutbounds("default", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.NodeOutbounds) != 2 {
		t.Fatalf("NodeOutbounds len: got %d want 2", len(plan.NodeOutbounds))
	}
	// Duplicates must be dropped, not suffixed.
	for _, tag := range plan.NodeTags {
		if strings.HasSuffix(tag, "-2") || strings.HasSuffix(tag, "-3") {
			t.Errorf("duplicate node kept with suffixed tag %q", tag)
		}
	}
	if len(plan.TagNames) != 4 { // 2 nodes + auto + manual
		t.Errorf("TagNames len: got %d want 4 (%v)", len(plan.TagNames), plan.TagNames)
	}
	if len(plan.URLTestGroup.Outbounds) != 2 {
		t.Errorf("URLTestGroup Outbounds: got %v want 2 entries", plan.URLTestGroup.Outbounds)
	}
	if len(plan.SelectorGroup.Outbounds) != 3 { // auto + 2 nodes
		t.Errorf("SelectorGroup Outbounds: got %v want 3 entries", plan.SelectorGroup.Outbounds)
	}
	// FinalTag must not move to <id>-single even when dedup shrinks the set.
	if plan.FinalTag != "default-manual" {
		t.Errorf("FinalTag: got %q want %q", plan.FinalTag, "default-manual")
	}
}

func TestBuildOutbounds_DeduplicationKeepsDistinctNodes(t *testing.T) {
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
		"vless://uuid3@host3.example.com:8443?security=reality&pbk=abc123#Node+3",
	}
	plan, err := subscription.BuildOutbounds("default", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.NodeOutbounds) != 3 {
		t.Fatalf("NodeOutbounds len: got %d want 3", len(plan.NodeOutbounds))
	}
}

func TestBuildOutbounds_DeduplicationIgnoresStableHash(t *testing.T) {
	// StableHash omits ALPN, xhttp mode and headerType, so nodes agreeing on the
	// hash can still render different outbounds. Those must both survive dedup,
	// with the tag collision resolved by the numeric suffix.
	cases := []struct {
		name string
		uris []string
	}{
		{
			name: "alpn",
			uris: []string{
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&alpn=h2#A",
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&alpn=http%2F1.1#B",
			},
		},
		{
			name: "xhttp mode",
			uris: []string{
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&type=xhttp&mode=packet-up#A",
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&type=xhttp&mode=stream-one#B",
			},
		},
		{
			name: "headerType",
			uris: []string{
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&type=tcp&headerType=http#A",
				"vless://u@h.example.com:443?security=tls&sni=h.example.com&type=tcp#B",
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			plan, err := subscription.BuildOutbounds("sub", tc.uris, buildOpts)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(plan.NodeOutbounds) != 2 {
				t.Fatalf("NodeOutbounds len: got %d want 2 — dedup dropped a distinct node", len(plan.NodeOutbounds))
			}
			first, second := plan.NodeTags[0], plan.NodeTags[1]
			if first == second {
				t.Fatalf("both nodes share tag %q", first)
			}
			if second != first+"-2" {
				t.Errorf("colliding tag: got %q want %q", second, first+"-2")
			}
			for _, tag := range []string{first, second} {
				if _, ok := plan.TagNames[tag]; !ok {
					t.Errorf("TagNames missing %q", tag)
				}
			}
		})
	}
}

// TestBuildOutbounds_DeduplicationDoesNotRenameSurvivors pins that dropping a
// duplicate never shifts the collision suffix of a later, genuinely distinct
// node. Without the counter advancing for skipped duplicates, the alpn=h2 node
// below would move from "-3" to "-2" — silently repointing a tag that saved
// selector choices and experimental.cache_file state still refer to.
func TestBuildOutbounds_DeduplicationDoesNotRenameSurvivors(t *testing.T) {
	// The first three URIs share a StableHash (it omits ALPN); the first two are
	// byte-identical and therefore dedup to one outbound.
	dup := "vless://u@h.example.com:443?security=tls&sni=h.example.com&alpn=http%2F1.1#A"
	uris := []string{
		dup,
		dup,
		"vless://u@h.example.com:443?security=tls&sni=h.example.com&alpn=h2#B",
	}

	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.NodeOutbounds) != 2 {
		t.Fatalf("NodeOutbounds len: got %d want 2 (%v)", len(plan.NodeOutbounds), plan.NodeTags)
	}

	base := plan.NodeTags[0]
	if got, want := plan.NodeTags[1], base+"-3"; got != want {
		t.Errorf("surviving distinct node tag: got %q want %q — dedup shifted the collision suffix", got, want)
	}
}

// TestBuildOutbounds_MixedProtocols pins that a subscription carrying more than
// one protocol produces one outbound per node, each in its own protocol's struct,
// under a single shared urltest/selector pair — groups reference members by tag,
// so nothing there is protocol-aware.
func TestBuildOutbounds_MixedProtocols(t *testing.T) {
	uris := []string{
		"vless://uuid1@vless.example.com:443?security=tls&sni=vless.example.com#VLESS+Node",
		"hysteria2://user:pass@hy2.example.com?sni=hy2.example.com&obfs=salamander&obfs-password=obfspw#HY2+Node",
		"hy2://token@short.example.com:8443#Short+Node",
	}
	opts := subscription.BuildOptions{Interval: "3m", Tolerance: 300, TestURL: testURL, ConnectTimeout: "3s"}
	plan, err := subscription.BuildOutbounds("mixed", uris, opts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(plan.NodeOutbounds) != 3 {
		t.Fatalf("NodeOutbounds len: got %d want 3", len(plan.NodeOutbounds))
	}
	if len(plan.NodeTags) != 3 {
		t.Fatalf("NodeTags len: got %d want 3 (%v)", len(plan.NodeTags), plan.NodeTags)
	}

	wantTypes := []string{"vless", "hysteria2", "hysteria2"}
	for i, want := range wantTypes {
		m := marshalToMap(t, plan.NodeOutbounds[i])
		if m["type"] != want {
			t.Errorf("node %d type: got %v want %v", i, m["type"], want)
		}
		if m["tag"] != plan.NodeTags[i] {
			t.Errorf("node %d tag: outbound has %v, NodeTags has %q", i, m["tag"], plan.NodeTags[i])
		}
		if !strings.HasPrefix(plan.NodeTags[i], "mixed-node-") {
			t.Errorf("node %d tag %q should start with 'mixed-node-'", i, plan.NodeTags[i])
		}
		if m["connect_timeout"] != "3s" {
			t.Errorf("node %d connect_timeout: got %v want 3s", i, m["connect_timeout"])
		}
	}

	// The concrete structs must be the protocol packages' own types.
	if _, ok := plan.NodeOutbounds[0].(*vless.Outbound); !ok {
		t.Errorf("node 0 is %T, want *vless.Outbound", plan.NodeOutbounds[0])
	}
	hy2, ok := plan.NodeOutbounds[1].(*hysteria2.Outbound)
	if !ok {
		t.Fatalf("node 1 is %T, want *hysteria2.Outbound", plan.NodeOutbounds[1])
	}
	if hy2.Password != "user:pass" {
		t.Errorf("hysteria2 password: got %q want %q", hy2.Password, "user:pass")
	}
	if hy2.Obfs == nil || hy2.Obfs.Type != "salamander" {
		t.Errorf("hysteria2 obfs: got %+v want salamander block", hy2.Obfs)
	}

	// One shared urltest/selector pair covering every node regardless of protocol.
	if plan.URLTestGroup == nil || plan.SelectorGroup == nil {
		t.Fatal("mixed-protocol subscription must still get one urltest and one selector group")
	}
	if len(plan.URLTestGroup.Outbounds) != 3 {
		t.Errorf("URLTestGroup Outbounds: got %v want all 3 node tags", plan.URLTestGroup.Outbounds)
	}
	for i, tag := range plan.NodeTags {
		if plan.URLTestGroup.Outbounds[i] != tag {
			t.Errorf("URLTestGroup Outbounds[%d]: got %q want %q", i, plan.URLTestGroup.Outbounds[i], tag)
		}
		if plan.SelectorGroup.Outbounds[i+1] != tag { // [0] is the auto group
			t.Errorf("SelectorGroup Outbounds[%d]: got %q want %q", i+1, plan.SelectorGroup.Outbounds[i+1], tag)
		}
	}
	if plan.FinalTag != "mixed-manual" {
		t.Errorf("FinalTag: got %q want %q", plan.FinalTag, "mixed-manual")
	}
	for _, tag := range plan.NodeTags {
		if _, ok := plan.TagNames[tag]; !ok {
			t.Errorf("TagNames missing node tag %q", tag)
		}
	}
	if plan.TagNames[plan.NodeTags[1]] != "HY2 Node" {
		t.Errorf("TagNames[%q]: got %q want %q", plan.NodeTags[1], plan.TagNames[plan.NodeTags[1]], "HY2 Node")
	}
}

// TestBuildOutbounds_SingleHysteria2Node pins that a non-VLESS node also takes
// the single-node path, tag and all.
func TestBuildOutbounds_SingleHysteria2Node(t *testing.T) {
	uris := []string{"hysteria2://pw@hy2.example.com#Personal+HY2"}
	plan, err := subscription.BuildOutbounds("personal", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.FinalTag != "personal-single" {
		t.Errorf("FinalTag: got %q want %q", plan.FinalTag, "personal-single")
	}
	if len(plan.NodeTags) != 1 || plan.NodeTags[0] != "personal-single" {
		t.Fatalf("NodeTags: got %v want [personal-single]", plan.NodeTags)
	}
	ob, ok := plan.NodeOutbounds[0].(*hysteria2.Outbound)
	if !ok {
		t.Fatalf("node 0 is %T, want *hysteria2.Outbound", plan.NodeOutbounds[0])
	}
	if ob.Tag != "personal-single" {
		t.Errorf("outbound Tag: got %q want %q", ob.Tag, "personal-single")
	}
	if ob.ServerPort != 443 {
		t.Errorf("ServerPort: got %d want 443 (spec default)", ob.ServerPort)
	}
	if plan.TagNames["personal-single"] != "Personal HY2" {
		t.Errorf("TagNames: got %q want %q", plan.TagNames["personal-single"], "Personal HY2")
	}
}

// TestBuildOutbounds_SkipsUnparseableKeepsRest pins that one bad URI costs one
// node, not the subscription, and that a node whose scheme has no parser is
// skipped the same way.
func TestBuildOutbounds_SkipsUnparseableKeepsRest(t *testing.T) {
	uris := []string{
		"trojan://pw@trojan.example.com:443#Unsupported",
		"hysteria2://@broken.example.com#Empty+Auth",
		"vless://uuid1@host1.example.com:443?security=tls#Good+1",
		"vless://uuid2@host2.example.com:443?security=tls#Good+2",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(plan.NodeOutbounds) != 2 {
		t.Fatalf("NodeOutbounds len: got %d want 2 (%v)", len(plan.NodeOutbounds), plan.NodeTags)
	}
}

func TestBuildOutbounds_NoValidNodesNamesSchemes(t *testing.T) {
	// The error is what an operator sees when a whole subscription fails to
	// parse, so it has to say which schemes would have worked.
	_, err := subscription.BuildOutbounds("sub", []string{"trojan://pw@host.example.com:443#X"}, buildOpts)
	if err == nil {
		t.Fatal("expected an error for a subscription with no parseable node")
	}
	for _, want := range []string{`"sub"`, "vless", "hysteria2", "hy2"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not mention %q", err, want)
		}
	}
	if strings.Contains(err.Error(), "VLESS") {
		t.Errorf("error %q still claims VLESS is the only protocol", err)
	}
}

func TestBuildOutbounds_NoURIs(t *testing.T) {
	_, err := subscription.BuildOutbounds("sub", nil, buildOpts)
	if err == nil {
		t.Error("expected error for empty URI list, got nil")
	}
}

func TestBuildOutbounds_InvalidURI(t *testing.T) {
	uris := []string{"not-a-vless-uri"}
	_, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err == nil {
		t.Error("expected error for invalid URI, got nil")
	}
}

func TestBuildOutbounds_PacketEncoding(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls#Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	if ob.PacketEncoding != "xudp" {
		t.Errorf("PacketEncoding: got %q want %q", ob.PacketEncoding, "xudp")
	}
}

func TestBuildOutbounds_JSONMarshal(t *testing.T) {
	// Verify that a single-node outbound marshals to valid JSON.
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&sni=server.example.com#Test+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := vlessOB(t, plan, 0)
	data, err := json.Marshal(ob)
	if err != nil {
		t.Fatalf("marshal outbound: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("unmarshal outbound: %v", err)
	}
	if m["type"] != "vless" {
		t.Errorf("type: got %v want vless", m["type"])
	}
	if m["tag"] != "sub-single" {
		t.Errorf("tag: got %v want sub-single", m["tag"])
	}
	if _, ok := m["tls"]; !ok {
		t.Error("tls block missing from JSON output")
	}
}

func TestBuildOutbounds_PlanCarriesID(t *testing.T) {
	uris := []string{"vless://uuid@host.example.com:443?encryption=none#Node"}
	plan, err := subscription.BuildOutbounds("personal", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if plan.ID != "personal" {
		t.Errorf("plan.ID: got %q want %q", plan.ID, "personal")
	}
}

func TestFallbackOutbound_JSONMarshal(t *testing.T) {
	cases := []struct {
		name             string
		blacklistTimeout string
		wantTimeout      any // nil means the field must be absent
	}{
		{name: "with blacklist_timeout", blacklistTimeout: "1m", wantTimeout: "1m"},
		{name: "without blacklist_timeout", blacklistTimeout: "", wantTimeout: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			group := &subscription.FallbackOutbound{
				Type:             "fallback",
				Tag:              "primary-fallback",
				Outbounds:        []string{"primary-single", "backup-manual"},
				BlacklistTimeout: tc.blacklistTimeout,
			}
			data, err := json.Marshal(group)
			if err != nil {
				t.Fatalf("marshal fallback group: %v", err)
			}
			var m map[string]any
			if err := json.Unmarshal(data, &m); err != nil {
				t.Fatalf("unmarshal fallback group: %v", err)
			}
			if m["type"] != "fallback" {
				t.Errorf("type: got %v want fallback", m["type"])
			}
			if m["tag"] != "primary-fallback" {
				t.Errorf("tag: got %v want primary-fallback", m["tag"])
			}
			outbounds, _ := m["outbounds"].([]any)
			if len(outbounds) != 2 || outbounds[0] != "primary-single" || outbounds[1] != "backup-manual" {
				t.Errorf("outbounds: got %v want [primary-single backup-manual]", m["outbounds"])
			}
			got, ok := m["blacklist_timeout"]
			switch {
			case tc.wantTimeout == nil && ok:
				t.Errorf("blacklist_timeout = %v, want absent", got)
			case tc.wantTimeout != nil && got != tc.wantTimeout:
				t.Errorf("blacklist_timeout = %v, want %v", got, tc.wantTimeout)
			}
			// sing-box decodes outbound options with unknown fields disallowed
			// and the extended build's FallbackOutboundOptions declares only
			// outbounds and blacklist_timeout, so any extra key here makes
			// `sing-box check` reject the config on a real device.
			want := map[string]bool{"type": true, "tag": true, "outbounds": true}
			if tc.wantTimeout != nil {
				want["blacklist_timeout"] = true
			}
			for k := range m {
				if !want[k] {
					t.Errorf("unexpected field %q in fallback group JSON; sing-box rejects unknown outbound fields", k)
				}
			}
		})
	}
}

// goldenConfigPath holds the sing-box config rendered from goldenSubscriptions.
//
// A DIFF IN THIS FILE IS NOT A FAILURE TO PAPER OVER. Node tags are
// "<id>-node-<StableHash>" and live outside this repository: they are written to
// subs-tags.json, referenced by the selector choice an operator saved in LuCI,
// and persisted in experimental.cache_file (/etc/sing-box/clash.db) on every
// deployed router. Move a tag and every saved choice silently repoints to a node
// nobody picked, while urltest/selector membership shifts underneath it. The
// rendered outbound bodies matter for the same reason in reverse: a changed TLS
// or transport shape is a behaviour change on the wire that no unit assertion
// about individual fields would catch.
//
// Regenerating this file is a deliberate, separately reviewed decision — never a
// way to make a diff go away.
const goldenConfigPath = "testdata/golden_vless_config.json"

// goldenTemplate is inlined rather than read from the shipped default template
// so that editing the template cannot force the golden to be regenerated. It
// still carries the pieces RenderConfig has to handle: placeholders in both
// arrays, a static outbound, a static route rule, and deprecated inbound fields.
const goldenTemplate = `{
  "log": { "level": "info", "timestamp": true },
  "inbounds": [
    {
      "type": "tun",
      "tag": "tun-in",
      "address": ["172.19.0.1/30"],
      "auto_route": true,
      "sniff": true,
      "domain_strategy": "prefer_ipv4"
    }
  ],
  "outbounds": [
    "__VLESS_OUTBOUNDS__",
    { "type": "direct", "tag": "direct" },
    { "type": "block", "tag": "block" }
  ],
  "route": {
    "rules": [
      "__ROUTE_RULES__",
      { "protocol": "dns", "outbound": "direct" }
    ],
    "final": "direct",
    "auto_detect_interface": true
  },
  "experimental": {
    "cache_file": { "enabled": true, "path": "/etc/sing-box/clash.db" }
  }
}`

// goldenSubscriptions is the fixed input behind goldenConfigPath. Between them
// the entries cover every VLESS rendering path the tool has: multi-node with
// reality, xhttp and ws transports; single-node with its "<id>-single" tag; a
// byte-identical duplicate that dedup must drop; two nodes colliding on
// StableHash so the "-2" suffix is exercised; connect_timeout both set and
// omitted; and per-subscription route rules.
var goldenSubscriptions = []struct {
	id    string
	uris  []string
	opts  subscription.BuildOptions
	route *config.SubscriptionRoute
}{
	{
		id: "default",
		uris: []string{
			"vless://uuid-reality@reality.example.com:8443?security=reality&pbk=publickey123&sid=ab12&sni=www.microsoft.com&fp=chrome&flow=xtls-rprx-vision#Reality+Node",
			"vless://uuid-xhttp@xhttp.example.com:443?security=tls&sni=xhttp.example.com&type=xhttp&mode=stream-up&path=%2Fdownload&host=cdn.example.com&fp=firefox#XHTTP+Node",
			"vless://uuid-ws@ws.example.com:2053?security=tls&sni=ws.example.com&type=ws&path=%2Fwebsocket&host=cdn.example.com#WS+Node",
		},
		opts: subscription.BuildOptions{Interval: "3m", Tolerance: 300, TestURL: testURL, ConnectTimeout: "3s"},
	},
	{
		id: "personal",
		uris: []string{
			"vless://uuid-single@personal.example.com:443?security=tls&sni=personal.example.com&fp=chrome#Personal",
		},
		opts:  subscription.BuildOptions{Interval: "5m", Tolerance: 100, TestURL: testURL, ConnectTimeout: "3s"},
		route: &config.SubscriptionRoute{Domains: []string{"example.org", "example.net"}, IPCIDRs: []string{"198.51.100.0/24"}},
	},
	{
		id: "collide",
		// The first two URIs are byte-identical and dedup to one outbound; the
		// third shares their StableHash (it omits ALPN) and therefore renders
		// under the "-3" collision suffix, pinning that the suffix counter keeps
		// advancing for the dropped duplicate.
		uris: []string{
			"vless://uuid-dup@dup.example.com:443?security=tls&sni=dup.example.com&alpn=http%2F1.1#Dup",
			"vless://uuid-dup@dup.example.com:443?security=tls&sni=dup.example.com&alpn=http%2F1.1#Dup",
			"vless://uuid-dup@dup.example.com:443?security=tls&sni=dup.example.com&alpn=h2#Dup+h2",
			"vless://uuid-plain@plain.example.com:80?type=tcp&headerType=http&host=plain.example.com&path=%2F#Plain+HTTP",
		},
		opts:  subscription.BuildOptions{TestURL: testURL},
		route: &config.SubscriptionRoute{Domains: []string{"collide.example"}},
	},
}

// renderGoldenConfig builds the goldenSubscriptions plans and renders them the
// way the subscriptions pipeline does: node outbounds, then urltest, then
// selector, per subscription in order, with route rules in the same order.
func renderGoldenConfig(t *testing.T) []byte {
	t.Helper()

	var outbounds, routeRules []any
	for _, sub := range goldenSubscriptions {
		plan, err := subscription.BuildOutbounds(sub.id, sub.uris, sub.opts)
		if err != nil {
			t.Fatalf("BuildOutbounds(%q): %v", sub.id, err)
		}
		outbounds = append(outbounds, plan.NodeOutbounds...)
		if plan.URLTestGroup != nil {
			outbounds = append(outbounds, plan.URLTestGroup)
		}
		if plan.SelectorGroup != nil {
			outbounds = append(outbounds, plan.SelectorGroup)
		}
		for _, r := range subscription.BuildRouteRules(sub.route, plan.FinalTag) {
			routeRules = append(routeRules, r)
		}
	}

	data, err := singbox.RenderConfig([]byte(goldenTemplate), outbounds, routeRules, "default-manual", "warn")
	if err != nil {
		t.Fatalf("RenderConfig: %v", err)
	}
	return data
}

// TestRenderedConfig_MatchesGolden is the regression gate for node tag
// stability and rendered outbound shape. See goldenConfigPath.
func TestRenderedConfig_MatchesGolden(t *testing.T) {
	got := renderGoldenConfig(t)

	want, err := os.ReadFile(goldenConfigPath)
	if err != nil {
		t.Fatalf("read golden: %v", err)
	}

	if bytes.Equal(got, want) {
		return
	}

	gotLines, wantLines := strings.Split(string(got), "\n"), strings.Split(string(want), "\n")
	for i := 0; i < len(gotLines) || i < len(wantLines); i++ {
		var gotLine, wantLine string
		if i < len(gotLines) {
			gotLine = gotLines[i]
		}
		if i < len(wantLines) {
			wantLine = wantLines[i]
		}
		if gotLine != wantLine {
			t.Fatalf("rendered config differs from %s at line %d:\n  golden: %s\n  got:    %s\n\n"+
				"If a node tag moved, every subs-tags.json entry, saved LuCI selector choice and "+
				"clash.db entry on every deployed router now points at the wrong node. Fix the code, "+
				"do not regenerate the golden.", goldenConfigPath, i+1, wantLine, gotLine)
		}
	}
	t.Fatalf("rendered config differs from %s in length only: got %d bytes, golden %d bytes",
		goldenConfigPath, len(got), len(want))
}

func TestBuildOutbounds_GroupsInterruptExistConnections(t *testing.T) {
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, buildOpts)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	groups := map[string]any{
		"urltest":  plan.URLTestGroup,
		"selector": plan.SelectorGroup,
	}
	for name, group := range groups {
		data, err := json.Marshal(group)
		if err != nil {
			t.Fatalf("marshal %s group: %v", name, err)
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			t.Fatalf("unmarshal %s group: %v", name, err)
		}
		v, ok := m["interrupt_exist_connections"]
		if !ok {
			t.Fatalf("%s group: interrupt_exist_connections missing from JSON output", name)
		}
		if v != true {
			t.Errorf("%s group: interrupt_exist_connections = %v, want true", name, v)
		}
	}
}
