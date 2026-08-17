package hysteria2_test

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/hysteria2"
)

// mustParse fails the test when the URI does not parse. Nodes are built through
// Parse because hysteria2.Node carries unexported fields: the accessors are what
// satisfies proto.Node, and a field cannot share a name with a method.
func mustParse(t *testing.T, uri string) *hysteria2.Node {
	t.Helper()
	n, err := hysteria2.Parse(uri)
	if err != nil {
		t.Fatalf("Parse(%q): %v", uri, err)
	}
	return n
}

func TestParse(t *testing.T) {
	cases := []struct {
		name string
		uri  string

		auth     string
		server   string
		port     int
		nodeName string
		sni      string
		insecure bool
		alpn     []string
		obfsType string
		obfsPass string
		upMbps   int
		downMbps int
	}{
		{
			name:   "minimal URI, port defaults to 443",
			uri:    "hysteria2://secret@example.com",
			auth:   "secret",
			server: "example.com",
			port:   443,
		},
		{
			name:     "fully populated",
			uri:      "hysteria2://user:pw@vps.example.com:8443?sni=s.example.com&insecure=1&obfs=salamander&obfs-password=obfspw&alpn=h3%2Ch2&upmbps=100&downmbps=200#My+Node",
			auth:     "user:pw",
			server:   "vps.example.com",
			port:     8443,
			nodeName: "My Node",
			sni:      "s.example.com",
			insecure: true,
			alpn:     []string{"h3", "h2"},
			obfsType: "salamander",
			obfsPass: "obfspw",
			upMbps:   100,
			downMbps: 200,
		},
		{
			name:     "hy2 alias reaches the same parser",
			uri:      "hy2://secret@example.com:2096#Short",
			auth:     "secret",
			server:   "example.com",
			port:     2096,
			nodeName: "Short",
		},
		{
			// Auth is the whole userinfo component: taking the username alone
			// would truncate this to "user".
			name:   "colon in auth is preserved",
			uri:    "hy2://user:pa:ss@example.com",
			auth:   "user:pa:ss",
			server: "example.com",
			port:   443,
		},
		{
			// A percent-encoded colon does not split userinfo, so url.Parse
			// stores it all as the username; it must still decode to the colon.
			name:   "percent-encoded auth is decoded once",
			uri:    "hy2://p%40ss%3Aword@example.com",
			auth:   "p@ss:word",
			server: "example.com",
			port:   443,
		},
		{
			name:     "insecure=0 verifies certificates",
			uri:      "hy2://secret@example.com?insecure=0",
			auth:     "secret",
			server:   "example.com",
			port:     443,
			insecure: false,
		},
		{
			// Non-boolean insecure means "verify", the safe reading; unusable
			// bandwidth values leave congestion control on BBR.
			name:     "unusable extension values fall back to the safe default",
			uri:      "hy2://secret@example.com?insecure=maybe&upmbps=fast&downmbps=-5",
			auth:     "secret",
			server:   "example.com",
			port:     443,
			insecure: false,
		},
		{
			name:   "IPv6 literal host",
			uri:    "hy2://secret@[2001:db8::1]:443",
			auth:   "secret",
			server: "2001:db8::1",
			port:   443,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := mustParse(t, tc.uri)
			if got := n.Auth(); got != tc.auth {
				t.Errorf("Auth: got %q want %q", got, tc.auth)
			}
			if got := n.Server(); got != tc.server {
				t.Errorf("Server: got %q want %q", got, tc.server)
			}
			if got := n.Port(); got != tc.port {
				t.Errorf("Port: got %d want %d", got, tc.port)
			}
			if got := n.Name(); got != tc.nodeName {
				t.Errorf("Name: got %q want %q", got, tc.nodeName)
			}
			if got := n.SNI(); got != tc.sni {
				t.Errorf("SNI: got %q want %q", got, tc.sni)
			}
			if got := n.Insecure(); got != tc.insecure {
				t.Errorf("Insecure: got %v want %v", got, tc.insecure)
			}
			if got := n.ALPN(); !reflect.DeepEqual(got, tc.alpn) {
				t.Errorf("ALPN: got %v want %v", got, tc.alpn)
			}
			if got := n.ObfsType(); got != tc.obfsType {
				t.Errorf("ObfsType: got %q want %q", got, tc.obfsType)
			}
			if got := n.ObfsPassword(); got != tc.obfsPass {
				t.Errorf("ObfsPassword: got %q want %q", got, tc.obfsPass)
			}
			if got := n.UpMbps(); got != tc.upMbps {
				t.Errorf("UpMbps: got %d want %d", got, tc.upMbps)
			}
			if got := n.DownMbps(); got != tc.downMbps {
				t.Errorf("DownMbps: got %d want %d", got, tc.downMbps)
			}
		})
	}
}

func TestParse_Rejections(t *testing.T) {
	cases := []struct {
		name    string
		uri     string
		wantErr string
	}{
		{
			name:    "wrong scheme",
			uri:     "vless://uuid@example.com:443",
			wantErr: "not a hysteria2 URI",
		},
		{
			name:    "no scheme at all",
			uri:     "example.com:443",
			wantErr: "not a hysteria2 URI",
		},
		{
			name:    "missing auth",
			uri:     "hysteria2://example.com:443",
			wantErr: "missing auth",
		},
		{
			name:    "empty auth",
			uri:     "hy2://@example.com:443",
			wantErr: "empty auth",
		},
		{
			name:    "missing server",
			uri:     "hy2://secret@:443",
			wantErr: "missing server",
		},
		{
			name:    "port out of range",
			uri:     "hy2://secret@example.com:99999",
			wantErr: "invalid port",
		},
		{
			// The spec allows gecko, sing-box does not implement it, and
			// silently downgrading to salamander would produce a node that
			// cannot connect.
			name:    "obfs=gecko names salamander in the error",
			uri:     "hy2://secret@example.com?obfs=gecko&obfs-password=x",
			wantErr: `sing-box implements only "salamander"`,
		},
		{
			name:    "unknown obfs type",
			uri:     "hy2://secret@example.com?obfs=rot13&obfs-password=x",
			wantErr: `sing-box implements only "salamander"`,
		},
		{
			// sing-box rejects a salamander block without a password, which
			// would fail the whole generated config instead of this one node.
			name:    "obfs without a password",
			uri:     "hy2://secret@example.com?obfs=salamander",
			wantErr: "missing obfs-password",
		},
		{
			// The mirror case: the outbound emits the obfs block only when
			// obfs is set, so accepting this would render an unobfuscated
			// outbound for a URI whose author asked for obfuscation.
			name:    "obfs-password without obfs",
			uri:     "hy2://secret@example.com?obfs-password=x",
			wantErr: "obfs-password without obfs",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := hysteria2.Parse(tc.uri)
			if err == nil {
				t.Fatalf("Parse(%q): expected error, got nil", tc.uri)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err, tc.wantErr)
			}
		})
	}
}

// TestStableHash_Golden pins the hash of a fixed URI set. The expected values
// were computed outside Go, from the frozen input layout:
//
//	printf 'hysteria2|server|port|auth|obfsType|obfsPassword|sni|insecure' | md5sum | cut -c1-8
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
			name:  "minimal, default port",
			uri:   "hysteria2://pass@example.com#Name",
			input: "hysteria2|example.com|443|pass||||false",
			want:  "5db3d471",
		},
		{
			name:  "fully populated",
			uri:   "hysteria2://user:pw@vps.example.com:8443?sni=s.example.com&insecure=1&obfs=salamander&obfs-password=obfspw&alpn=h3&upmbps=100&downmbps=200#N",
			input: "hysteria2|vps.example.com|8443|user:pw|salamander|obfspw|s.example.com|true",
			want:  "e5150c45",
		},
		{
			name:  "insecure flips the hash",
			uri:   "hy2://pass@example.com?insecure=1",
			input: "hysteria2|example.com|443|pass||||true",
			want:  "dd153801",
		},
		{
			name:  "explicit port differs from the default",
			uri:   "hy2://pass@example.com:8443",
			input: "hysteria2|example.com|8443|pass||||false",
			want:  "f9390d43",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			n := mustParse(t, tc.uri)
			if got := hysteria2.StableHash(n); got != tc.want {
				t.Errorf("StableHash = %q, want %q (hash input: %q)", got, tc.want, tc.input)
			}
			// The method must not diverge from the function.
			if got := n.StableHash(); got != tc.want {
				t.Errorf("(*Node).StableHash = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestStableHash_NameAndBandwidthDoNotAffectHash(t *testing.T) {
	base := mustParse(t, "hy2://pass@example.com#A")
	other := mustParse(t, "hy2://pass@example.com?upmbps=100&downmbps=200#B")
	if hysteria2.StableHash(base) != hysteria2.StableHash(other) {
		t.Error("display name and bandwidth hints must not affect the stable hash")
	}
}

func TestStableHash_DefaultPortMatchesExplicit443(t *testing.T) {
	// The port is part of the hash input as a number, so the spec default has
	// to hash the same as spelling 443 out.
	implicit := mustParse(t, "hy2://pass@example.com")
	explicit := mustParse(t, "hy2://pass@example.com:443")
	if hysteria2.StableHash(implicit) != hysteria2.StableHash(explicit) {
		t.Error("implicit and explicit port 443 produced different hashes")
	}
}

func TestNode_ContractMethods(t *testing.T) {
	n := mustParse(t, "hysteria2://secret@node.example.com:2096?sni=node.example.com#My+Node")

	if got := n.Type(); got != "hysteria2" {
		t.Errorf("Type: got %q want %q", got, "hysteria2")
	}
	if got := n.Server(); got != "node.example.com" {
		t.Errorf("Server: got %q want %q", got, "node.example.com")
	}
	if got := n.Port(); got != 2096 {
		t.Errorf("Port: got %d want 2096", got)
	}
	if got := n.Name(); got != "My Node" {
		t.Errorf("Name: got %q want %q", got, "My Node")
	}

	// Outbound must produce the same value as the exported constructor, and its
	// dynamic type must stay the typed struct the pipeline marshals.
	got, ok := n.Outbound("tag-1", "3s").(*hysteria2.Outbound)
	if !ok {
		t.Fatalf("Outbound returned %T, want *hysteria2.Outbound", n.Outbound("tag-1", "3s"))
	}
	want := hysteria2.NewOutbound(n, "tag-1", "3s")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("Outbound diverged from NewOutbound:\n got %+v\nwant %+v", got, want)
	}
}

// TestNewOutbound_MarshalJSON pins the exact bytes of every rendered outbound
// shape. These bytes end up in /etc/sing-box/config.json, so a diff is a change
// to what sing-box is asked to dial. Note that "tls" is always present:
// hysteria2 runs over QUIC and sing-box requires the block.
func TestNewOutbound_MarshalJSON(t *testing.T) {
	cases := []struct {
		name           string
		uri            string
		tag            string
		connectTimeout string
		want           string
	}{
		{
			name: "minimal node",
			uri:  "hysteria2://secret@example.com#Node",
			tag:  "t1",
			want: `{"type":"hysteria2","tag":"t1","server":"example.com","server_port":443,"password":"secret","tls":{"enabled":true,"insecure":false}}`,
		},
		{
			name: "obfs and sni",
			uri:  "hysteria2://pw@a.example.com:8443?obfs=salamander&obfs-password=op&sni=a.sni.example.com",
			tag:  "t2",
			want: `{"type":"hysteria2","tag":"t2","server":"a.example.com","server_port":8443,"password":"pw","obfs":{"type":"salamander","password":"op"},"tls":{"enabled":true,"insecure":false,"server_name":"a.sni.example.com"}}`,
		},
		{
			// Bandwidth hints switch sing-box from BBR to Brutal, so they are
			// emitted only when the URI carried them.
			name: "bandwidth, insecure and alpn",
			uri:  "hy2://user:pw@b.example.com?upmbps=50&downmbps=100&insecure=1&alpn=h3",
			tag:  "t3",
			want: `{"type":"hysteria2","tag":"t3","server":"b.example.com","server_port":443,"password":"user:pw","up_mbps":50,"down_mbps":100,"tls":{"enabled":true,"insecure":true,"alpn":["h3"]}}`,
		},
		{
			name:           "connect_timeout is emitted when set",
			uri:            "hy2://secret@c.example.com",
			tag:            "t4",
			connectTimeout: "3s",
			want:           `{"type":"hysteria2","tag":"t4","server":"c.example.com","server_port":443,"password":"secret","tls":{"enabled":true,"insecure":false},"connect_timeout":"3s"}`,
		},
		{
			// BuildOutbounds marshals the tagless form as its dedup key, so the
			// empty tag has to keep rendering rather than being dropped.
			name: "empty tag still renders, giving a stable dedup key",
			uri:  "hysteria2://secret@example.com#Node",
			want: `{"type":"hysteria2","tag":"","server":"example.com","server_port":443,"password":"secret","tls":{"enabled":true,"insecure":false}}`,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ob := hysteria2.NewOutbound(mustParse(t, tc.uri), tc.tag, tc.connectTimeout)
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
