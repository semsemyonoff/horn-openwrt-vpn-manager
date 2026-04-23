package subscription

import (
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

// v2rayTCPRealityArrayFixture mirrors the shape of a V2Ray/Xray-style JSON
// subscription response with a single VLESS outbound per entry using
// TCP transport and Reality security. Values are synthetic test data.
const v2rayTCPRealityArrayFixture = `[
  {
    "remarks": "Hungary",
    "outbounds": [
      {
        "protocol": "vless",
        "settings": {
          "vnext": [
            {
              "address": "h1.example.com",
              "port": 8443,
              "users": [
                {"id": "11111111-1111-1111-1111-111111111111", "encryption": "none", "flow": "xtls-rprx-vision", "level": 8}
              ]
            }
          ]
        },
        "streamSettings": {
          "network": "tcp",
          "security": "reality",
          "realitySettings": {
            "publicKey": "pubkey-hu",
            "shortId": "sidhu1",
            "serverName": "sni.example.com",
            "fingerprint": "chrome"
          },
          "tcpSettings": {"header": {"type": "none"}}
        },
        "tag": "proxy"
      },
      {"protocol": "freedom", "tag": "direct"}
    ]
  },
  {
    "remarks": "Canada",
    "outbounds": [
      {
        "protocol": "vless",
        "settings": {
          "vnext": [
            {
              "address": "h2.example.com",
              "port": 443,
              "users": [
                {"id": "22222222-2222-2222-2222-222222222222", "encryption": "none", "flow": "xtls-rprx-vision", "level": 8}
              ]
            }
          ]
        },
        "streamSettings": {
          "network": "tcp",
          "security": "reality",
          "realitySettings": {
            "publicKey": "pubkey-ca",
            "shortId": "sidca1",
            "serverName": "sni.example.com",
            "fingerprint": "chrome"
          }
        },
        "tag": "proxy"
      }
    ]
  }
]`

func TestDecodePayload_json_v2ray_array(t *testing.T) {
	uris, err := DecodePayload([]byte(v2rayTCPRealityArrayFixture))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}

	// Round-trip each URI through vless.Parse and verify fields.
	n0, err := vless.Parse(uris[0])
	if err != nil {
		t.Fatalf("parse uri[0]=%q: %v", uris[0], err)
	}
	if n0.UUID != "11111111-1111-1111-1111-111111111111" ||
		n0.Server != "h1.example.com" ||
		n0.Port != 8443 ||
		n0.Name != "Hungary" ||
		n0.Security != "reality" ||
		n0.SNI != "sni.example.com" ||
		n0.PublicKey != "pubkey-hu" ||
		n0.ShortID != "sidhu1" ||
		n0.Fingerprint != "chrome" ||
		n0.Flow != "xtls-rprx-vision" ||
		n0.TransportType != "tcp" {
		t.Errorf("uri[0] parsed fields mismatch: %+v", n0)
	}

	n1, err := vless.Parse(uris[1])
	if err != nil {
		t.Fatalf("parse uri[1]=%q: %v", uris[1], err)
	}
	if n1.Server != "h2.example.com" || n1.Port != 443 || n1.Name != "Canada" {
		t.Errorf("uri[1] parsed fields mismatch: %+v", n1)
	}
}

func TestDecodePayload_json_single_object(t *testing.T) {
	single := `{
      "remarks": "Solo",
      "outbounds": [
        {
          "protocol": "vless",
          "settings": {"vnext": [{"address": "solo.example.com", "port": 443, "users": [{"id": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa", "encryption": "none"}]}]},
          "streamSettings": {"network": "tcp", "security": "none"}
        }
      ]
    }`
	uris, err := DecodePayload([]byte(single))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
	n, err := vless.Parse(uris[0])
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	if n.Name != "Solo" || n.Security != "" || n.TransportType != "tcp" {
		t.Errorf("unexpected fields: %+v", n)
	}
}

func TestDecodePayload_json_non_vless_only(t *testing.T) {
	// A JSON config with zero vless outbounds should not be treated as a
	// successful JSON decode. DecodePayload should return an error.
	data := `[{"remarks": "nope", "outbounds": [{"protocol": "freedom", "tag": "direct"}]}]`
	_, err := DecodePayload([]byte(data))
	if err == nil {
		t.Fatal("expected error when JSON contains no vless outbounds")
	}
}

func TestDecodePayload_json_leading_whitespace(t *testing.T) {
	// Leading whitespace/newlines must not prevent JSON detection.
	payload := "\n\t  " + v2rayTCPRealityArrayFixture
	uris, err := DecodePayload([]byte(payload))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
}

func TestDecodePayload_json_ws_tls(t *testing.T) {
	data := `[{
      "remarks": "WS",
      "outbounds": [
        {
          "protocol": "vless",
          "settings": {"vnext": [{"address": "ws.example.com", "port": 443, "users": [{"id": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "encryption": "none"}]}]},
          "streamSettings": {
            "network": "ws",
            "security": "tls",
            "tlsSettings": {"serverName": "ws.example.com", "alpn": ["h2", "http/1.1"], "fingerprint": "firefox"},
            "wsSettings": {"path": "/vl", "headers": {"Host": "ws.example.com"}}
          }
        }
      ]
    }]`
	uris, err := DecodePayload([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
	n, err := vless.Parse(uris[0])
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	if n.TransportType != "ws" || n.Path != "/vl" || n.Host != "ws.example.com" {
		t.Errorf("ws transport mismatch: %+v", n)
	}
	if n.Security != "tls" || n.SNI != "ws.example.com" || n.Fingerprint != "firefox" {
		t.Errorf("tls fields mismatch: %+v", n)
	}
	if len(n.ALPN) != 2 || n.ALPN[0] != "h2" || n.ALPN[1] != "http/1.1" {
		t.Errorf("alpn mismatch: %+v", n.ALPN)
	}
}

func TestDecodePayload_json_grpc(t *testing.T) {
	data := `[{
      "remarks": "gRPC",
      "outbounds": [
        {
          "protocol": "vless",
          "settings": {"vnext": [{"address": "g.example.com", "port": 443, "users": [{"id": "cccccccc-cccc-cccc-cccc-cccccccccccc"}]}]},
          "streamSettings": {
            "network": "grpc",
            "security": "tls",
            "tlsSettings": {"serverName": "g.example.com"},
            "grpcSettings": {"serviceName": "gvs"}
          }
        }
      ]
    }]`
	uris, err := DecodePayload([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n, err := vless.Parse(uris[0])
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	if n.TransportType != "grpc" || n.ServiceName != "gvs" {
		t.Errorf("grpc transport mismatch: %+v", n)
	}
}

func TestDecodePayload_json_missing_fields_skipped(t *testing.T) {
	// One valid entry + one invalid (no vnext) — the valid one survives.
	data := `[
      {"remarks": "bad", "outbounds": [{"protocol": "vless", "settings": {}, "streamSettings": {"network": "tcp"}}]},
      {"remarks": "good", "outbounds": [{"protocol": "vless", "settings": {"vnext": [{"address": "good.example.com", "port": 443, "users": [{"id": "dddddddd-dddd-dddd-dddd-dddddddddddd"}]}]}, "streamSettings": {"network": "tcp"}}]}
    ]`
	uris, err := DecodePayload([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1 (invalid entry should be skipped)", len(uris))
	}
	if !strings.Contains(uris[0], "good.example.com") {
		t.Errorf("expected good URI, got %q", uris[0])
	}
}

func TestDecodePayload_json_remarks_with_non_ascii(t *testing.T) {
	// Remarks with multibyte UTF-8 (e.g. emoji + Cyrillic) must round-trip
	// through the URI fragment and back via vless.Parse.
	data := `[{
      "remarks": "🇭🇺Венгрия",
      "outbounds": [
        {
          "protocol": "vless",
          "settings": {"vnext": [{"address": "x.example.com", "port": 443, "users": [{"id": "eeeeeeee-eeee-eeee-eeee-eeeeeeeeeeee"}]}]},
          "streamSettings": {"network": "tcp"}
        }
      ]
    }]`
	uris, err := DecodePayload([]byte(data))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	n, err := vless.Parse(uris[0])
	if err != nil {
		t.Fatalf("parse uri: %v", err)
	}
	if n.Name != "🇭🇺Венгрия" {
		t.Errorf("fragment round-trip mismatch: got %q", n.Name)
	}
}
