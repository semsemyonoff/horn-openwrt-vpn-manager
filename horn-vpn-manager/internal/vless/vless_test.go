package vless_test

import (
	"encoding/json"
	"reflect"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

func TestParse_TLS(t *testing.T) {
	uri := "vless://550e8400-e29b-41d4-a716-446655440000@example.com:443?security=tls&sni=example.com&fp=chrome&flow=xtls-rprx-vision#My+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.UUID() != "550e8400-e29b-41d4-a716-446655440000" {
		t.Errorf("UUID: got %q want %q", n.UUID(), "550e8400-e29b-41d4-a716-446655440000")
	}
	if n.Server() != "example.com" {
		t.Errorf("Server: got %q want %q", n.Server(), "example.com")
	}
	if n.Port() != 443 {
		t.Errorf("Port: got %d want 443", n.Port())
	}
	if n.Security() != "tls" {
		t.Errorf("Security: got %q want %q", n.Security(), "tls")
	}
	if n.SNI() != "example.com" {
		t.Errorf("SNI: got %q want %q", n.SNI(), "example.com")
	}
	if n.Fingerprint() != "chrome" {
		t.Errorf("Fingerprint: got %q want %q", n.Fingerprint(), "chrome")
	}
	if n.Flow() != "xtls-rprx-vision" {
		t.Errorf("Flow: got %q want %q", n.Flow(), "xtls-rprx-vision")
	}
	if n.Name() != "My Node" {
		t.Errorf("Name: got %q want %q", n.Name(), "My Node")
	}
}

func TestParse_Reality(t *testing.T) {
	uri := "vless://uuid-abc@10.0.0.1:8443?security=reality&pbk=pubkey123&sid=shortid1&sni=www.example.com&fp=firefox#Reality+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.Security() != "reality" {
		t.Errorf("Security: got %q want %q", n.Security(), "reality")
	}
	if n.PublicKey() != "pubkey123" {
		t.Errorf("PublicKey: got %q want %q", n.PublicKey(), "pubkey123")
	}
	if n.ShortID() != "shortid1" {
		t.Errorf("ShortID: got %q want %q", n.ShortID(), "shortid1")
	}
	if n.Name() != "Reality Node" {
		t.Errorf("Name: got %q want %q", n.Name(), "Reality Node")
	}
}

func TestParse_WSTransport(t *testing.T) {
	uri := "vless://uuid@server.example.com:80?security=tls&sni=server.example.com&type=ws&path=%2Fwspath&host=cdn.example.com#WS+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.TransportType() != "ws" {
		t.Errorf("TransportType: got %q want %q", n.TransportType(), "ws")
	}
	if n.Path() != "/wspath" {
		t.Errorf("Path: got %q want %q", n.Path(), "/wspath")
	}
	if n.Host() != "cdn.example.com" {
		t.Errorf("Host: got %q want %q", n.Host(), "cdn.example.com")
	}
}

func TestParse_GRPCTransport(t *testing.T) {
	uri := "vless://uuid@server.example.com:443?security=tls&sni=server.example.com&type=grpc&serviceName=myService#GRPC+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if n.TransportType() != "grpc" {
		t.Errorf("TransportType: got %q want %q", n.TransportType(), "grpc")
	}
	if n.ServiceName() != "myService" {
		t.Errorf("ServiceName: got %q want %q", n.ServiceName(), "myService")
	}
}

func TestParse_ALPN(t *testing.T) {
	uri := "vless://uuid@server.example.com:443?security=tls&alpn=h2%2Chttp%2F1.1#ALPN+Node"
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(n.ALPN()) != 2 {
		t.Fatalf("ALPN len: got %d want 2", len(n.ALPN()))
	}
	if n.ALPN()[0] != "h2" || n.ALPN()[1] != "http/1.1" {
		t.Errorf("ALPN: got %v want [h2 http/1.1]", n.ALPN())
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

// mustParse fails the test when the URI does not parse. Nodes are built through
// Parse because vless.Node carries unexported fields: the accessors are what
// satisfies proto.Node, and a field cannot share a name with a method.
func mustParse(t *testing.T, uri string) *vless.Node {
	t.Helper()
	n, err := vless.Parse(uri)
	if err != nil {
		t.Fatalf("Parse(%q): %v", uri, err)
	}
	return n
}

func TestStableHash_Deterministic(t *testing.T) {
	n := mustParse(t, "vless://550e8400-e29b-41d4-a716-446655440000@example.com:443?security=tls&sni=example.com&fp=chrome")
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
	base := mustParse(t, "vless://uuid-abc@server1.example.com:443?security=tls&sni=server1.example.com")
	other := mustParse(t, "vless://uuid-abc@server2.example.com:443?security=tls&sni=server2.example.com")
	if vless.StableHash(base) == vless.StableHash(other) {
		t.Error("different servers produced the same hash")
	}
}

func TestStableHash_NameDoesNotAffectHash(t *testing.T) {
	n1 := mustParse(t, "vless://uuid-abc@server.example.com:443?security=tls#Name+A")
	n2 := mustParse(t, "vless://uuid-abc@server.example.com:443?security=tls#Name+B")
	if vless.StableHash(n1) != vless.StableHash(n2) {
		t.Error("display name should not affect stable hash")
	}
}

// TestStableHash_Golden pins the hash of a fixed URI set. The expected values
// were computed outside Go, from the frozen input layout:
//
//	printf 'vless|server|port|uuid|security|sni|pbk|sid|flow|fp|type|path|host|serviceName' | md5sum | cut -c1-8
//
// A diff here means node tags moved. Tags are "<id>-node-<hash>" and they are
// written to subs-tags.json, referenced by saved selector choices and persisted
// in experimental.cache_file (/etc/sing-box/clash.db) — moving them silently
// repoints every saved choice on every deployed router. Fix the code, never the
// expectations.
func TestStableHash_Golden(t *testing.T) {
	cases := []struct {
		name string
		uri  string
		// input is the exact string fed to md5, recorded so a failure can be
		// re-derived by hand without reading the implementation.
		input string
		want  string
	}{
		{
			name:  "tls, no transport",
			uri:   "vless://uuid-test@example.com:443?security=tls&sni=sni.example.com#A",
			input: "vless|example.com|443|uuid-test|tls|sni.example.com||||||||",
			want:  "62ba582c",
		},
		{
			name:  "reality over xhttp",
			uri:   "vless://uuid-r@r.example.com:8443?security=reality&pbk=pk1&sid=sid1&sni=r.sni.example.com&fp=chrome&type=xhttp&path=%2Fx&host=h.example.com#R",
			input: "vless|r.example.com|8443|uuid-r|reality|r.sni.example.com|pk1|sid1||chrome|xhttp|/x|h.example.com|",
			want:  "c8a15975",
		},
		{
			name:  "plaintext ws",
			uri:   "vless://uuid-w@w.example.com:80?type=ws&path=%2Fws&host=cdn.example.com#W",
			input: "vless|w.example.com|80|uuid-w|||||||ws|/ws|cdn.example.com|",
			want:  "56ee630e",
		},
		{
			name:  "grpc with flow",
			uri:   "vless://uuid-g@g.example.com:443?security=tls&type=grpc&serviceName=svc&flow=xtls-rprx-vision#G",
			input: "vless|g.example.com|443|uuid-g|tls||||xtls-rprx-vision||grpc|||svc",
			want:  "961de8ca",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := mustParse(t, tc.uri)
			if got := vless.StableHash(n); got != tc.want {
				t.Errorf("StableHash = %q, want %q (hash input: %q)", got, tc.want, tc.input)
			}
			// The method must not diverge from the function.
			if got := n.StableHash(); got != tc.want {
				t.Errorf("(*Node).StableHash = %q, want %q", got, tc.want)
			}
		})
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
		if n.Server() != tc.server {
			t.Errorf("Server: got %q want %q", n.Server(), tc.server)
		}
		if n.Port() != tc.port {
			t.Errorf("Port: got %d want %d", n.Port(), tc.port)
		}
		if n.Security() != tc.security {
			t.Errorf("Security: got %q want %q", n.Security(), tc.security)
		}
		if n.SNI() != tc.sni {
			t.Errorf("SNI: got %q want %q", n.SNI(), tc.sni)
		}
	}
}

func TestNode_ContractMethods(t *testing.T) {
	n := mustParse(t, "vless://uuid-c@node.example.com:2053?security=tls&sni=node.example.com#My+Node")

	if got := n.Type(); got != "vless" {
		t.Errorf("Type: got %q want %q", got, "vless")
	}
	if got := n.Server(); got != "node.example.com" {
		t.Errorf("Server: got %q want %q", got, "node.example.com")
	}
	if got := n.Port(); got != 2053 {
		t.Errorf("Port: got %d want 2053", got)
	}
	if got := n.Name(); got != "My Node" {
		t.Errorf("Name: got %q want %q", got, "My Node")
	}

	// Outbound must produce the same value as the exported constructor, and its
	// dynamic type must stay the typed struct the pipeline marshals.
	got, ok := n.Outbound("tag-1", "3s").(*vless.Outbound)
	if !ok {
		t.Fatalf("Outbound returned %T, want *vless.Outbound", n.Outbound("tag-1", "3s"))
	}
	want := vless.NewOutbound(n, "tag-1", "3s")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Outbound diverged from NewOutbound:\n got %+v\nwant %+v", got, want)
	}
}

// TestNewOutbound_MarshalJSON pins the exact bytes of every rendered outbound
// shape. These bytes end up in /etc/sing-box/config.json, so a diff is a change
// to what sing-box is asked to dial.
func TestNewOutbound_MarshalJSON(t *testing.T) {
	cases := []struct {
		name           string
		uri            string
		tag            string
		connectTimeout string
		want           string
	}{
		{
			name: "plaintext tcp, no tls block",
			uri:  "vless://uuid-1@a.example.com:443#Node",
			tag:  "t1",
			want: `{"type":"vless","tag":"t1","server":"a.example.com","server_port":443,"uuid":"uuid-1","packet_encoding":"xudp"}`,
		},
		{
			name: "tls with utls and flow, packet_encoding omitted",
			uri:  "vless://uuid-2@b.example.com:443?security=tls&sni=b.example.com&fp=chrome&flow=xtls-rprx-vision",
			tag:  "t2",
			want: `{"type":"vless","tag":"t2","server":"b.example.com","server_port":443,"uuid":"uuid-2","flow":"xtls-rprx-vision","tls":{"enabled":true,"insecure":false,"server_name":"b.example.com","utls":{"enabled":true,"fingerprint":"chrome"}}}`,
		},
		{
			name:           "reality over xhttp with connect_timeout",
			uri:            "vless://uuid-3@c.example.com:8443?security=reality&pbk=pk&sid=sid&sni=c.example.com&type=xhttp&host=h.example.com&path=%2Fp",
			tag:            "t3",
			connectTimeout: "3s",
			want:           `{"type":"vless","tag":"t3","server":"c.example.com","server_port":8443,"uuid":"uuid-3","packet_encoding":"xudp","tls":{"enabled":true,"insecure":false,"server_name":"c.example.com","alpn":["h2"],"reality":{"enabled":true,"public_key":"pk","short_id":"sid"}},"transport":{"host":"h.example.com","mode":"auto","path":"/p","type":"xhttp","x_padding_bytes":"100-1000"},"connect_timeout":"3s"}`,
		},
		{
			name: "ws transport carries the host header",
			uri:  "vless://uuid-4@d.example.com:80?type=ws&path=%2Fws&host=cdn.example.com",
			tag:  "t4",
			want: `{"type":"vless","tag":"t4","server":"d.example.com","server_port":80,"uuid":"uuid-4","packet_encoding":"xudp","transport":{"headers":{"Host":"cdn.example.com"},"path":"/ws","type":"ws"}}`,
		},
		{
			name: "grpc transport",
			uri:  "vless://uuid-5@e.example.com:443?security=tls&type=grpc&serviceName=svc",
			tag:  "t5",
			want: `{"type":"vless","tag":"t5","server":"e.example.com","server_port":443,"uuid":"uuid-5","packet_encoding":"xudp","tls":{"enabled":true,"insecure":false},"transport":{"service_name":"svc","type":"grpc"}}`,
		},
		{
			name: "tcp with headerType=http renders an http transport",
			uri:  "vless://uuid-6@f.example.com:80?type=tcp&headerType=http&host=x.example.com&path=%2Fh",
			tag:  "t6",
			want: `{"type":"vless","tag":"t6","server":"f.example.com","server_port":80,"uuid":"uuid-6","packet_encoding":"xudp","transport":{"host":["x.example.com"],"path":"/h","type":"http"}}`,
		},
		{
			// type=h2 renders as an http transport: h2 is the URI-side alias,
			// sing-box only knows "http".
			name: "h2 is an alias for http",
			uri:  "vless://uuid-7@g.example.com:443?security=tls&type=h2&host=y.example.com&path=%2Fh2",
			tag:  "t7",
			want: `{"type":"vless","tag":"t7","server":"g.example.com","server_port":443,"uuid":"uuid-7","packet_encoding":"xudp","tls":{"enabled":true,"insecure":false},"transport":{"host":["y.example.com"],"path":"/h2","type":"http"}}`,
		},
		{
			name: "quic emits no transport block",
			uri:  "vless://uuid-8@h.example.com:443?security=tls&type=quic",
			tag:  "t8",
			want: `{"type":"vless","tag":"t8","server":"h.example.com","server_port":443,"uuid":"uuid-8","packet_encoding":"xudp","tls":{"enabled":true,"insecure":false}}`,
		},
		{
			name: "explicit alpn wins over the xhttp default",
			uri:  "vless://uuid-9@i.example.com:443?security=tls&type=xhttp&mode=stream-up&alpn=h3",
			tag:  "t9",
			want: `{"type":"vless","tag":"t9","server":"i.example.com","server_port":443,"uuid":"uuid-9","packet_encoding":"xudp","tls":{"enabled":true,"insecure":false,"alpn":["h3"]},"transport":{"mode":"stream-up","type":"xhttp","x_padding_bytes":"100-1000"}}`,
		},
		{
			// BuildOutbounds marshals the tagless form as its dedup key, so the
			// empty tag has to keep rendering rather than being dropped.
			name: "empty tag still renders, giving a stable dedup key",
			uri:  "vless://uuid-1@a.example.com:443#Node",
			want: `{"type":"vless","tag":"","server":"a.example.com","server_port":443,"uuid":"uuid-1","packet_encoding":"xudp"}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ob := vless.NewOutbound(mustParse(t, tc.uri), tc.tag, tc.connectTimeout)
			data, err := json.Marshal(ob)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(data) != tc.want {
				t.Errorf("outbound JSON mismatch:\n got %s\nwant %s", data, tc.want)
			}
		})
	}
}
