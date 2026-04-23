package subscription

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// utf8BOM is the UTF-8 byte-order mark some servers prepend to JSON bodies.
var utf8BOM = []byte{0xEF, 0xBB, 0xBF}

// v2rayEntry is one top-level object in a V2Ray/Xray-style subscription response.
// Only the fields relevant for extracting a VLESS node are modeled; unknown fields
// are ignored by encoding/json.
type v2rayEntry struct {
	Remarks   string          `json:"remarks"`
	Outbounds []v2rayOutbound `json:"outbounds"`
}

type v2rayOutbound struct {
	Protocol       string              `json:"protocol"`
	Settings       *v2rayOutSettings   `json:"settings"`
	StreamSettings *v2rayStreamSetting `json:"streamSettings"`
	Tag            string              `json:"tag"`
}

type v2rayOutSettings struct {
	Vnext []v2rayVnext `json:"vnext"`
}

type v2rayVnext struct {
	Address string      `json:"address"`
	Port    int         `json:"port"`
	Users   []v2rayUser `json:"users"`
}

type v2rayUser struct {
	ID         string `json:"id"`
	Flow       string `json:"flow"`
	Encryption string `json:"encryption"`
}

type v2rayStreamSetting struct {
	Network         string          `json:"network"`
	Security        string          `json:"security"`
	RealitySettings *v2rayReality   `json:"realitySettings"`
	TLSSettings     *v2rayTLS       `json:"tlsSettings"`
	TCPSettings     *v2rayTCP       `json:"tcpSettings"`
	WSSettings      *v2rayWS        `json:"wsSettings"`
	GRPCSettings    *v2rayGRPC      `json:"grpcSettings"`
	HTTPSettings    *v2rayHTTPAlike `json:"httpSettings"`
}

type v2rayReality struct {
	PublicKey   string `json:"publicKey"`
	ShortID     string `json:"shortId"`
	ServerName  string `json:"serverName"`
	Fingerprint string `json:"fingerprint"`
}

type v2rayTLS struct {
	ServerName  string   `json:"serverName"`
	ALPN        []string `json:"alpn"`
	Fingerprint string   `json:"fingerprint"`
}

type v2rayTCP struct {
	Header *v2rayTCPHeader `json:"header"`
}

type v2rayTCPHeader struct {
	Type string `json:"type"`
}

type v2rayWS struct {
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers"`
}

type v2rayGRPC struct {
	ServiceName string `json:"serviceName"`
}

type v2rayHTTPAlike struct {
	Host []string `json:"host"`
	Path string   `json:"path"`
}

// trimJSONPrefix strips UTF-8 BOM and leading whitespace from data.
func trimJSONPrefix(data []byte) []byte {
	data = bytes.TrimPrefix(data, utf8BOM)
	return bytes.TrimSpace(data)
}

// looksLikeJSON reports whether the payload (after BOM/whitespace stripping)
// starts with '[' or '{'.
func looksLikeJSON(data []byte) bool {
	trimmed := trimJSONPrefix(data)
	if len(trimmed) == 0 {
		return false
	}
	return trimmed[0] == '[' || trimmed[0] == '{'
}

// tryJSON attempts to decode a V2Ray/Xray-style JSON subscription response into
// vless:// URIs. Accepts either a top-level JSON array of config objects or a
// single config object. Returns FormatJSON when at least one VLESS node is found.
// When the payload looks like JSON but cannot be decoded into VLESS URIs, a
// diagnostic is emitted at warn level so operators can tell that JSON detection
// fired but the shape was not understood.
func tryJSON(data []byte) ([]string, Format) {
	if !looksLikeJSON(data) {
		return nil, FormatUnknown
	}
	uris, err := parseV2RayJSON(data)
	if err != nil {
		logx.Warn("subscription payload looks like JSON but failed to parse as V2Ray/Xray config: %v", err)
		return nil, FormatUnknown
	}
	if len(uris) == 0 {
		logx.Warn("subscription payload parsed as JSON but contained no vless outbounds")
		return nil, FormatUnknown
	}
	return uris, FormatJSON
}

// parseV2RayJSON unmarshals data as either []v2rayEntry or v2rayEntry and returns
// the extracted vless:// URIs.
func parseV2RayJSON(data []byte) ([]string, error) {
	trimmed := trimJSONPrefix(data)
	if len(trimmed) == 0 {
		return nil, errors.New("empty JSON payload")
	}

	var entries []v2rayEntry
	if trimmed[0] == '[' {
		if err := json.Unmarshal(trimmed, &entries); err != nil {
			return nil, fmt.Errorf("unmarshal JSON array: %w", err)
		}
	} else {
		var single v2rayEntry
		if err := json.Unmarshal(trimmed, &single); err != nil {
			return nil, fmt.Errorf("unmarshal JSON object: %w", err)
		}
		entries = []v2rayEntry{single}
	}

	var uris []string
	for _, e := range entries {
		for _, ob := range e.Outbounds {
			if ob.Protocol != "vless" {
				continue
			}
			uri, ok := v2rayOutboundToVLESSURI(ob, e.Remarks)
			if !ok {
				continue
			}
			uris = append(uris, uri)
		}
	}
	return uris, nil
}

// v2rayOutboundToVLESSURI converts a V2Ray/Xray VLESS outbound (plus the parent
// entry's remarks for display) into a vless:// URI string. Returns false when
// required fields (uuid/address/port) are missing.
func v2rayOutboundToVLESSURI(ob v2rayOutbound, remarks string) (string, bool) {
	if ob.Settings == nil || len(ob.Settings.Vnext) == 0 {
		return "", false
	}
	vn := ob.Settings.Vnext[0]
	if vn.Address == "" || vn.Port <= 0 || vn.Port > 65535 || len(vn.Users) == 0 {
		return "", false
	}
	user := vn.Users[0]
	if user.ID == "" {
		return "", false
	}

	q := url.Values{}
	if user.Flow != "" {
		q.Set("flow", user.Flow)
	}
	applyStreamSettings(q, ob.StreamSettings)

	name := remarks
	if ob.Tag != "" && name == "" {
		name = ob.Tag
	}

	u := &url.URL{
		Scheme:   "vless",
		User:     url.User(user.ID),
		Host:     net.JoinHostPort(vn.Address, strconv.Itoa(vn.Port)),
		RawQuery: q.Encode(),
		Fragment: name,
	}
	return u.String(), true
}

// applyStreamSettings writes security/transport-related query parameters onto q
// based on ss. A nil ss is treated as plain tcp with no security.
func applyStreamSettings(q url.Values, ss *v2rayStreamSetting) {
	if ss == nil {
		q.Set("type", "tcp")
		return
	}
	network := ss.Network
	if network == "" {
		network = "tcp"
	}
	q.Set("type", network)
	if ss.Security != "" && ss.Security != "none" {
		q.Set("security", ss.Security)
	}
	applySecuritySettings(q, ss)
	applyTransportSettings(q, network, ss)
}

func applySecuritySettings(q url.Values, ss *v2rayStreamSetting) {
	switch ss.Security {
	case "reality":
		rs := ss.RealitySettings
		if rs == nil {
			return
		}
		setIfNonEmpty(q, "sni", rs.ServerName)
		setIfNonEmpty(q, "fp", rs.Fingerprint)
		setIfNonEmpty(q, "pbk", rs.PublicKey)
		setIfNonEmpty(q, "sid", rs.ShortID)
	case "tls":
		ts := ss.TLSSettings
		if ts == nil {
			return
		}
		setIfNonEmpty(q, "sni", ts.ServerName)
		setIfNonEmpty(q, "fp", ts.Fingerprint)
		if len(ts.ALPN) > 0 {
			q.Set("alpn", strings.Join(ts.ALPN, ","))
		}
	}
}

func applyTransportSettings(q url.Values, network string, ss *v2rayStreamSetting) {
	switch network {
	case "tcp":
		if ss.TCPSettings != nil && ss.TCPSettings.Header != nil {
			if t := ss.TCPSettings.Header.Type; t != "" && t != "none" {
				q.Set("headerType", t)
			}
		}
	case "ws":
		if ws := ss.WSSettings; ws != nil {
			setIfNonEmpty(q, "path", ws.Path)
			setIfNonEmpty(q, "host", ws.Headers["Host"])
		}
	case "grpc":
		if gs := ss.GRPCSettings; gs != nil {
			setIfNonEmpty(q, "serviceName", gs.ServiceName)
		}
	case "http", "h2":
		if hs := ss.HTTPSettings; hs != nil {
			setIfNonEmpty(q, "path", hs.Path)
			if len(hs.Host) > 0 {
				setIfNonEmpty(q, "host", hs.Host[0])
			}
		}
	}
}

func setIfNonEmpty(q url.Values, key, value string) {
	if value != "" {
		q.Set(key, value)
	}
}
