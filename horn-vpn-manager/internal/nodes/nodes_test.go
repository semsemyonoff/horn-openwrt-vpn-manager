package nodes_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/hysteria2"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/nodes"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

const (
	vlessURI     = "vless://550e8400-e29b-41d4-a716-446655440000@example.com:443?security=tls&sni=example.com&fp=chrome#My+Node"
	hysteria2URI = "hysteria2://user:pass@hy.example.com:8443?sni=hy.example.com&obfs=salamander&obfs-password=secret#HY+Node"
	hy2URI       = "hy2://user:pass@hy.example.com:8443?sni=hy.example.com&obfs=salamander&obfs-password=secret#HY+Node"
)

func TestParse_DispatchesByScheme(t *testing.T) {
	tests := []struct {
		name       string
		uri        string
		wantType   string
		wantServer string
		wantPort   int
		wantName   string
		wantGoType any
	}{
		{
			name:       "vless",
			uri:        vlessURI,
			wantType:   "vless",
			wantServer: "example.com",
			wantPort:   443,
			wantName:   "My Node",
			wantGoType: (*vless.Node)(nil),
		},
		{
			name:       "hysteria2",
			uri:        hysteria2URI,
			wantType:   "hysteria2",
			wantServer: "hy.example.com",
			wantPort:   8443,
			wantName:   "HY Node",
			wantGoType: (*hysteria2.Node)(nil),
		},
		{
			name:       "hy2 alias",
			uri:        hy2URI,
			wantType:   "hysteria2",
			wantServer: "hy.example.com",
			wantPort:   8443,
			wantName:   "HY Node",
			wantGoType: (*hysteria2.Node)(nil),
		},
		{
			name:       "hysteria2 default port",
			uri:        "hysteria2://auth@hy.example.com#Default",
			wantType:   "hysteria2",
			wantServer: "hy.example.com",
			wantPort:   443,
			wantName:   "Default",
			wantGoType: (*hysteria2.Node)(nil),
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := nodes.Parse(tt.uri)
			if err != nil {
				t.Fatalf("Parse() error = %v", err)
			}
			if got := n.Type(); got != tt.wantType {
				t.Errorf("Type() = %q, want %q", got, tt.wantType)
			}
			if got := n.Server(); got != tt.wantServer {
				t.Errorf("Server() = %q, want %q", got, tt.wantServer)
			}
			if got := n.Port(); got != tt.wantPort {
				t.Errorf("Port() = %d, want %d", got, tt.wantPort)
			}
			if got := n.Name(); got != tt.wantName {
				t.Errorf("Name() = %q, want %q", got, tt.wantName)
			}
			switch tt.wantGoType.(type) {
			case *vless.Node:
				if _, ok := n.(*vless.Node); !ok {
					t.Errorf("Parse() returned %T, want *vless.Node", n)
				}
			case *hysteria2.Node:
				if _, ok := n.(*hysteria2.Node); !ok {
					t.Errorf("Parse() returned %T, want *hysteria2.Node", n)
				}
			}
		})
	}
}

// The hy2 alias must reach the same parser, not a lookalike: an identical URI
// under either scheme has to produce the same tag, or switching the spelling in
// a subscription would repoint the saved selector choice.
func TestParse_HY2AliasMatchesHysteria2(t *testing.T) {
	a, err := nodes.Parse(hysteria2URI)
	if err != nil {
		t.Fatalf("Parse(hysteria2) error = %v", err)
	}
	b, err := nodes.Parse(hy2URI)
	if err != nil {
		t.Fatalf("Parse(hy2) error = %v", err)
	}
	if a.StableHash() != b.StableHash() {
		t.Errorf("StableHash mismatch: hysteria2 = %q, hy2 = %q", a.StableHash(), b.StableHash())
	}
}

func TestParse_UnknownScheme(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantInError string
	}{
		{name: "unregistered scheme", uri: "trojan://pass@example.com:443#T", wantInError: `"trojan"`},
		{name: "no scheme at all", uri: "just some text", wantInError: "no scheme"},
		{name: "empty", uri: "", wantInError: "no scheme"},
		{name: "scheme separator only", uri: "://example.com", wantInError: "no scheme"},
		{name: "case sensitive", uri: "VLESS://uuid@example.com:443", wantInError: `"VLESS"`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := nodes.Parse(tt.uri)
			if err == nil {
				t.Fatalf("Parse() error = nil, want ErrUnknownScheme")
			}
			if n != nil {
				t.Errorf("Parse() node = %v, want nil on error", n)
			}
			if !errors.Is(err, nodes.ErrUnknownScheme) {
				t.Errorf("errors.Is(err, ErrUnknownScheme) = false, err = %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantInError) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantInError)
			}
			// The message has to tell the operator what is accepted.
			for _, scheme := range nodes.Schemes() {
				if !strings.Contains(err.Error(), scheme) {
					t.Errorf("error %q does not list supported scheme %q", err.Error(), scheme)
				}
			}
		})
	}
}

// Node URIs carry credentials, so a dispatch failure must not echo the URI.
func TestParse_UnknownSchemeDoesNotLeakURI(t *testing.T) {
	_, err := nodes.Parse("trojan://sup3rsecret@example.com:443#T")
	if err == nil {
		t.Fatal("Parse() error = nil, want error")
	}
	if strings.Contains(err.Error(), "sup3rsecret") {
		t.Errorf("error %q leaks the URI credentials", err.Error())
	}
}

// The no-leak rule covers every rejection path, not just an unknown scheme. It
// is easiest to break through url.Parse, whose *url.Error renders as
// `parse "<the whole URI>": <reason>` — wrapping it verbatim puts the VLESS UUID
// or the hysteria2 auth into the subscriptions log and into the LuCI
// notification that rpcd check_with_core relays.
func TestParse_ErrorsDoNotLeakCredentials(t *testing.T) {
	const secret = "sup3rsecret"
	uris := map[string]string{
		"vless control byte in host":     "vless://" + secret + "@exa\x7fmple.com:443",
		"vless invalid port":             "vless://" + secret + "@example.com:notaport",
		"hysteria2 control byte in host": "hysteria2://" + secret + "@exa\x7fmple.com:443",
		"hysteria2 invalid port":         "hysteria2://" + secret + "@example.com:notaport",
		"hy2 invalid port":               "hy2://" + secret + "@example.com:notaport",
		"unknown scheme":                 "trojan://" + secret + "@example.com:443",
	}
	for name, uri := range uris {
		t.Run(name, func(t *testing.T) {
			_, err := nodes.Parse(uri)
			if err == nil {
				t.Fatalf("Parse(%q) error = nil, want error", name)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q leaks the URI credentials", err.Error())
			}
		})
	}
}

// A URI with a known scheme but a broken body must surface the protocol
// package's own error, not ErrUnknownScheme.
func TestParse_PropagatesProtocolError(t *testing.T) {
	tests := []struct {
		name        string
		uri         string
		wantInError string
	}{
		{name: "vless missing uuid", uri: "vless://@example.com:443", wantInError: "UUID"},
		{name: "hysteria2 missing auth", uri: "hysteria2://hy.example.com:8443", wantInError: "auth"},
		{name: "hysteria2 gecko obfs", uri: "hysteria2://auth@hy.example.com?obfs=gecko", wantInError: "salamander"},
		{name: "hy2 invalid port", uri: "hy2://auth@hy.example.com:99999", wantInError: "invalid port"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			n, err := nodes.Parse(tt.uri)
			if err == nil {
				t.Fatalf("Parse() error = nil, want error")
			}
			// A typed nil pointer returned through the interface would make this
			// non-nil while every callers' err check passed.
			if n != nil {
				t.Errorf("Parse() node = %v (%T), want nil on error", n, n)
			}
			if errors.Is(err, nodes.ErrUnknownScheme) {
				t.Errorf("error wraps ErrUnknownScheme, want the protocol error: %v", err)
			}
			if !strings.Contains(err.Error(), tt.wantInError) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantInError)
			}
		})
	}
}

func TestSchemes(t *testing.T) {
	got := nodes.Schemes()
	want := []string{"hy2", "hysteria2", "vless"}
	if len(got) != len(want) {
		t.Fatalf("Schemes() = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("Schemes() = %v, want %v (sorted)", got, want)
		}
	}

	// The returned slice must not be a view the caller can reorder.
	got[0] = "zzz"
	if again := nodes.Schemes()[0]; again != want[0] {
		t.Errorf("Schemes() is not defensive: got %q after caller mutation, want %q", again, want[0])
	}
}

func TestIsKnownScheme(t *testing.T) {
	tests := []struct {
		name string
		uri  string
		want bool
	}{
		{name: "vless", uri: vlessURI, want: true},
		{name: "hysteria2", uri: hysteria2URI, want: true},
		{name: "hy2", uri: hy2URI, want: true},
		{name: "scheme only", uri: "hy2://", want: true},
		{name: "unregistered scheme", uri: "trojan://pass@example.com:443", want: false},
		{name: "no scheme at all", uri: "# a comment line", want: false},
		{name: "empty line", uri: "", want: false},
		{name: "scheme separator only", uri: "://example.com", want: false},
		{name: "scheme without separator", uri: "vless:uuid@example.com", want: false},
		{name: "scheme not at line start", uri: "see vless://uuid@example.com", want: false},
		{name: "uppercase scheme", uri: "VLESS://uuid@example.com:443", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := nodes.IsKnownScheme(tt.uri); got != tt.want {
				t.Errorf("IsKnownScheme(%q) = %v, want %v", tt.uri, got, tt.want)
			}
		})
	}
}
