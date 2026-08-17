package proto

import (
	"encoding/json"
	"testing"
)

// The expected strings below are the JSON these structs produced while they
// lived in internal/subscription. JSON key order follows struct field order, so
// reordering a field here changes every generated sing-box config even though
// no value changed. Keep them verbatim.
func TestOutboundTLSMarshalJSON(t *testing.T) {
	tests := []struct {
		name string
		tls  *OutboundTLS
		want string
	}{
		{
			name: "empty",
			tls:  &OutboundTLS{},
			want: `{"enabled":false,"insecure":false}`,
		},
		{
			name: "plain tls",
			tls: &OutboundTLS{
				Enabled:    true,
				ServerName: "example.com",
			},
			want: `{"enabled":true,"insecure":false,"server_name":"example.com"}`,
		},
		{
			name: "insecure with alpn",
			tls: &OutboundTLS{
				Enabled:    true,
				Insecure:   true,
				ServerName: "example.com",
				ALPN:       []string{"h3", "h2"},
			},
			want: `{"enabled":true,"insecure":true,"server_name":"example.com","alpn":["h3","h2"]}`,
		},
		{
			name: "utls",
			tls: &OutboundTLS{
				Enabled:    true,
				ServerName: "example.com",
				UTLS:       &UTLSConfig{Enabled: true, Fingerprint: "chrome"},
			},
			want: `{"enabled":true,"insecure":false,"server_name":"example.com","utls":{"enabled":true,"fingerprint":"chrome"}}`,
		},
		{
			name: "reality with short id",
			tls: &OutboundTLS{
				Enabled:    true,
				ServerName: "example.com",
				UTLS:       &UTLSConfig{Enabled: true, Fingerprint: "chrome"},
				Reality:    &RealityTLS{Enabled: true, PublicKey: "pbk-value", ShortID: "aabb"},
			},
			want: `{"enabled":true,"insecure":false,"server_name":"example.com","utls":{"enabled":true,"fingerprint":"chrome"},"reality":{"enabled":true,"public_key":"pbk-value","short_id":"aabb"}}`,
		},
		{
			name: "reality without short id",
			tls: &OutboundTLS{
				Enabled: true,
				Reality: &RealityTLS{Enabled: true, PublicKey: "pbk-value"},
			},
			want: `{"enabled":true,"insecure":false,"reality":{"enabled":true,"public_key":"pbk-value"}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := json.Marshal(tt.tls)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}
			if string(got) != tt.want {
				t.Errorf("marshal mismatch\ngot:  %s\nwant: %s", got, tt.want)
			}
		})
	}
}

func TestUTLSConfigMarshalJSON(t *testing.T) {
	got, err := json.Marshal(&UTLSConfig{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// Neither field is omitempty: a zero UTLS block still renders both keys.
	if want := `{"enabled":false,"fingerprint":""}`; string(got) != want {
		t.Errorf("marshal mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

func TestRealityTLSMarshalJSON(t *testing.T) {
	got, err := json.Marshal(&RealityTLS{})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	// short_id is omitempty; enabled and public_key are not.
	if want := `{"enabled":false,"public_key":""}`; string(got) != want {
		t.Errorf("marshal mismatch\ngot:  %s\nwant: %s", got, want)
	}
}

// nodeStub is a minimal Node implementation: it exists only so the interface
// keeps compiling with the shape the protocol packages implement in later tasks.
type nodeStub struct{}

func (nodeStub) Type() string             { return "stub" }
func (nodeStub) Server() string           { return "example.com" }
func (nodeStub) Port() int                { return 443 }
func (nodeStub) Name() string             { return "stub node" }
func (nodeStub) StableHash() string       { return "0badcafe" }
func (nodeStub) Outbound(_, _ string) any { return struct{}{} }

func TestNodeInterfaceSatisfied(t *testing.T) {
	var n Node = nodeStub{}
	if n.Type() != "stub" || n.Server() != "example.com" || n.Port() != 443 {
		t.Errorf("unexpected stub values: %s %s %d", n.Type(), n.Server(), n.Port())
	}
	if n.Name() != "stub node" || n.StableHash() != "0badcafe" {
		t.Errorf("unexpected stub values: %s %s", n.Name(), n.StableHash())
	}
	if n.Outbound("tag", "3s") == nil {
		t.Error("Outbound returned nil")
	}
}
