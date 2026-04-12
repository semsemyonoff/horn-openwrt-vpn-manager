package subscription_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
)

const testURL = "https://www.gstatic.com/generate_204"

func TestBuildOutbounds_SingleNode(t *testing.T) {
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
	}
	plan, err := subscription.BuildOutbounds("default", uris, "5m", 100, testURL)
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
	ob := plan.NodeOutbounds[0]
	if ob.Tag != "default-single" {
		t.Errorf("outbound Tag: got %q want %q", ob.Tag, "default-single")
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
	plan, err := subscription.BuildOutbounds("default", uris, "5m", 100, testURL)
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

	// Each node must be tagged <id>-node-<8char-hash>.
	for _, ob := range plan.NodeOutbounds {
		if !strings.HasPrefix(ob.Tag, "default-node-") {
			t.Errorf("node tag %q should start with 'default-node-'", ob.Tag)
		}
		hash := strings.TrimPrefix(ob.Tag, "default-node-")
		if len(hash) != 8 {
			t.Errorf("node hash should be 8 chars, got %d in tag %q", len(hash), ob.Tag)
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
	for _, ob := range plan.NodeOutbounds {
		if _, ok := plan.TagNames[ob.Tag]; !ok {
			t.Errorf("TagNames missing node tag %q", ob.Tag)
		}
	}
}

func TestBuildOutbounds_TagsAreStable(t *testing.T) {
	// Running BuildOutbounds twice with the same URIs must produce the same tags.
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls&sni=host1.example.com#Node+1",
		"vless://uuid2@host2.example.com:443?security=tls&sni=host2.example.com#Node+2",
	}
	p1, err := subscription.BuildOutbounds("sub", uris, "", 0, testURL)
	if err != nil {
		t.Fatalf("first call error: %v", err)
	}
	p2, err := subscription.BuildOutbounds("sub", uris, "", 0, testURL)
	if err != nil {
		t.Fatalf("second call error: %v", err)
	}
	for i, ob := range p1.NodeOutbounds {
		if ob.Tag != p2.NodeOutbounds[i].Tag {
			t.Errorf("node %d tag mismatch: %q vs %q", i, ob.Tag, p2.NodeOutbounds[i].Tag)
		}
	}
}

func TestBuildOutbounds_Defaults(t *testing.T) {
	// Empty interval and zero tolerance must fall back to defaults.
	uris := []string{
		"vless://uuid1@host1.example.com:443?security=tls#A",
		"vless://uuid2@host2.example.com:443?security=tls#B",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, "", 0, testURL)
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

func TestBuildOutbounds_TLSBlock(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&sni=server.example.com&fp=chrome#TLS+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
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
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
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
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
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
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
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

func TestBuildOutbounds_NoURIs(t *testing.T) {
	_, err := subscription.BuildOutbounds("sub", nil, "5m", 100, testURL)
	if err == nil {
		t.Error("expected error for empty URI list, got nil")
	}
}

func TestBuildOutbounds_InvalidURI(t *testing.T) {
	uris := []string{"not-a-vless-uri"}
	_, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err == nil {
		t.Error("expected error for invalid URI, got nil")
	}
}

func TestBuildOutbounds_PacketEncoding(t *testing.T) {
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls#Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
	if ob.PacketEncoding != "xudp" {
		t.Errorf("PacketEncoding: got %q want %q", ob.PacketEncoding, "xudp")
	}
}

func TestBuildOutbounds_JSONMarshal(t *testing.T) {
	// Verify that a single-node outbound marshals to valid JSON.
	uris := []string{
		"vless://uuid@server.example.com:443?security=tls&sni=server.example.com#Test+Node",
	}
	plan, err := subscription.BuildOutbounds("sub", uris, "5m", 100, testURL)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	ob := plan.NodeOutbounds[0]
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
