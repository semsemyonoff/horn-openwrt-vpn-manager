package vless_test

import (
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

func TestParse_TLS(t *testing.T) {
	uri := "vless://550e8400-e29b-41d4-a716-446655440000@example.com:443?security=tls&sni=example.com&fp=chrome&flow=xtls-rprx-vision#My+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.UUID != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("UUID: got %q want %q", n.UUID, "550e8400-e29b-41d4-a716-446655440000")
	}
	if n.Server != "example.com" {
		t.Errorf("Server: got %q want %q", n.Server, "example.com")
	}
	if n.Port != 443 {
		t.Errorf("Port: got %d want 443", n.Port)
	}
	if n.Security != "tls" {
		t.Errorf("Security: got %q want %q", n.Security, "tls")
	}
	if n.SNI != "example.com" {
		t.Errorf("SNI: got %q want %q", n.SNI, "example.com")
	}
	if n.Fingerprint != "chrome" {
		t.Errorf("Fingerprint: got %q want %q", n.Fingerprint, "chrome")
	}
	if n.Flow != "xtls-rprx-vision" {
		t.Errorf("Flow: got %q want %q", n.Flow, "xtls-rprx-vision")
	}
	if n.Name != "My Node" {
		t.Errorf("Name: got %q want %q", n.Name, "My Node")
	}
}

func TestParse_Reality(t *testing.T) {
	uri := "vless://uuid-abc@10.0.0.1:8443?security=reality&pbk=pubkey123&sid=shortid1&sni=www.example.com&fp=firefox#Reality+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Security != "reality" {
		t.Errorf("Security: got %q want %q", n.Security, "reality")
	}
	if n.PublicKey != "pubkey123" {
		t.Errorf("PublicKey: got %q want %q", n.PublicKey, "pubkey123")
	}
	if n.ShortID != "shortid1" {
		t.Errorf("ShortID: got %q want %q", n.ShortID, "shortid1")
	}
	if n.Name != "Reality Node" {
		t.Errorf("Name: got %q want %q", n.Name, "Reality Node")
	}
}

func TestParse_WSTransport(t *testing.T) {
	uri := "vless://uuid@server.example.com:80?security=tls&sni=server.example.com&type=ws&path=%2Fwspath&host=cdn.example.com#WS+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.TransportType != "ws" {
		t.Errorf("TransportType: got %q want %q", n.TransportType, "ws")
	}
	if n.Path != "/wspath" {
		t.Errorf("Path: got %q want %q", n.Path, "/wspath")
	}
	if n.Host != "cdn.example.com" {
		t.Errorf("Host: got %q want %q", n.Host, "cdn.example.com")
	}
}

func TestParse_GRPCTransport(t *testing.T) {
	uri := "vless://uuid@server.example.com:443?security=tls&sni=server.example.com&type=grpc&serviceName=myService#GRPC+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.TransportType != "grpc" {
		t.Errorf("TransportType: got %q want %q", n.TransportType, "grpc")
	}
	if n.ServiceName != "myService" {
		t.Errorf("ServiceName: got %q want %q", n.ServiceName, "myService")
	}
}

func TestParse_ALPN(t *testing.T) {
	uri := "vless://uuid@server.example.com:443?security=tls&alpn=h2%2Chttp%2F1.1#ALPN+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(n.ALPN) != 2 {
		t.Fatalf("ALPN len: got %d want 2", len(n.ALPN))
	}
	if n.ALPN[0] != "h2" || n.ALPN[1] != "http/1.1" {
		t.Errorf("ALPN: got %v want [h2 http/1.1]", n.ALPN)
	}
}

func TestParse_NotVLESS(t *testing.T) {
	_, err := vless.Parse("vmess://something")
	if err == nil {
		t.Error("expected error for non-vless URI, got nil")
	}
}

func TestParse_MissingPort(t *testing.T) {
	_, err := vless.Parse("vless://uuid@server.example.com?security=tls")
	if err == nil {
		t.Error("expected error for missing port, got nil")
	}
}

func TestParse_InvalidPort(t *testing.T) {
	_, err := vless.Parse("vless://uuid@server.example.com:99999?security=tls")
	if err == nil {
		t.Error("expected error for invalid port, got nil")
	}
}

func TestParse_MissingServer(t *testing.T) {
	_, err := vless.Parse("vless://uuid@:443?security=tls")
	if err == nil {
		t.Error("expected error for missing server, got nil")
	}
}

func TestStableHash_Deterministic(t *testing.T) {
	n := &vless.Node{
		UUID:          "550e8400-e29b-41d4-a716-446655440000",
		Server:        "example.com",
		Port:          443,
		Security:      "tls",
		SNI:           "example.com",
		Fingerprint:   "chrome",
		TransportType: "",
	}
	h1 := vless.StableHash(n)
	h2 := vless.StableHash(n)
	if h1 != h2 {
		t.Errorf("hash is not deterministic: %q vs %q", h1, h2)
	}
	if len(h1) != 8 {
		t.Errorf("hash length: got %d want 8", len(h1))
	}
}

func TestStableHash_DifferentServers(t *testing.T) {
	base := &vless.Node{
		UUID:     "uuid-abc",
		Server:   "server1.example.com",
		Port:     443,
		Security: "tls",
		SNI:      "server1.example.com",
	}
	other := &vless.Node{
		UUID:     "uuid-abc",
		Server:   "server2.example.com",
		Port:     443,
		Security: "tls",
		SNI:      "server2.example.com",
	}
	if vless.StableHash(base) == vless.StableHash(other) {
		t.Error("different servers produced the same hash")
	}
}

func TestStableHash_NameDoesNotAffectHash(t *testing.T) {
	n1 := &vless.Node{
		UUID:     "uuid-abc",
		Server:   "server.example.com",
		Port:     443,
		Security: "tls",
		Name:     "Name A",
	}
	n2 := *n1
	n2.Name = "Name B"
	if vless.StableHash(n1) != vless.StableHash(&n2) {
		t.Error("display name should not affect stable hash")
	}
}

func TestStableHash_KnownValue(t *testing.T) {
	// Verify hash matches expected value computed from legacy shell formula:
	// printf 'vless|example.com|443|uuid-test|tls|sni.example.com||||||||' | md5sum | cut -c1-8
	// Result: 62ba582c
	n := &vless.Node{
		UUID:     "uuid-test",
		Server:   "example.com",
		Port:     443,
		Security: "tls",
		SNI:      "sni.example.com",
	}
	h := vless.StableHash(n)
	const want = "62ba582c"
	if h != want {
		t.Errorf("StableHash = %q, want %q (legacy shell formula mismatch)", h, want)
	}
}

func TestParse_RoundtripFromFixture(t *testing.T) {
	// URIs taken from testdata/raw_subscription.txt
	cases := []struct {
		uri      string
		server   string
		port     int
		security string
		sni      string
	}{
		{
			uri:      "vless://uuid1@host1.example.com:443?encryption=none&security=tls&sni=host1.example.com#Node+1",
			server:   "host1.example.com",
			port:     443,
			security: "tls",
			sni:      "host1.example.com",
		},
		{
			uri:      "vless://uuid2@host2.example.com:443?encryption=none&security=tls&sni=host2.example.com#Node+2",
			server:   "host2.example.com",
			port:     443,
			security: "tls",
			sni:      "host2.example.com",
		},
		{
			uri:      "vless://uuid3@host3.example.com:8443?encryption=none&security=reality&pbk=abc123#Node+3",
			server:   "host3.example.com",
			port:     8443,
			security: "reality",
			sni:      "",
		},
	}
	for _, tc := range cases {
		n, err := vless.Parse(tc.uri)
		if err != nil {
			t.Errorf("Parse(%q): unexpected error: %v", tc.uri, err)
			continue
		}
		if n.Server != tc.server {
			t.Errorf("Server: got %q want %q", n.Server, tc.server)
		}
		if n.Port != tc.port {
			t.Errorf("Port: got %d want %d", n.Port, tc.port)
		}
		if n.Security != tc.security {
			t.Errorf("Security: got %q want %q", n.Security, tc.security)
		}
		if n.SNI != tc.sni {
			t.Errorf("SNI: got %q want %q", n.SNI, tc.sni)
		}
	}
}
