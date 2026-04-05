package subscription

import (
	"encoding/json"
	"fmt"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

const (
	defaultInterval  = "5m"
	defaultTolerance = 100
	packetEncoding   = "xudp"
)

// OutboundPlan holds the sing-box outbound configuration generated for a single
// subscription. It covers both node outbounds and group outbounds.
type OutboundPlan struct {
	// NodeOutbounds holds individual VLESS node outbounds.
	// Single-node: one entry tagged "<id>-single".
	// Multi-node: entries tagged "<id>-node-<hash>".
	NodeOutbounds []*VLESSOutbound

	// URLTestGroup is the auto-select group for multi-node subscriptions.
	// Nil for single-node subscriptions.
	URLTestGroup *URLTestOutbound

	// SelectorGroup is the manual-select group for multi-node subscriptions.
	// Nil for single-node subscriptions.
	SelectorGroup *SelectorOutbound

	// FinalTag is the routing outbound tag to use in route rules:
	// "<id>-single" for single-node, "<id>-manual" for multi-node.
	FinalTag string

	// TagNames maps each generated tag to its display name. Useful for
	// UI integration (e.g., future LuCI phase).
	TagNames map[string]string

	// RouteRules holds the sing-box route rules for this subscription's manual
	// routing entries (domains and IP CIDRs). Nil for default subscriptions
	// and for subscriptions with no route config. Domains and IP CIDRs are
	// stored as separate rules to preserve OR match semantics in sing-box.
	RouteRules []*RouteRule
}

// VLESSOutbound is a typed sing-box VLESS outbound configuration.
type VLESSOutbound struct {
	Type           string             `json:"type"`
	Tag            string             `json:"tag"`
	Server         string             `json:"server"`
	ServerPort     int                `json:"server_port"`
	UUID           string             `json:"uuid"`
	Flow           string             `json:"flow,omitempty"`
	PacketEncoding string             `json:"packet_encoding,omitempty"`
	TLS            *OutboundTLS       `json:"tls,omitempty"`
	Transport      *OutboundTransport `json:"transport,omitempty"`
}

// OutboundTLS is the TLS block for a sing-box VLESS outbound.
type OutboundTLS struct {
	Enabled    bool        `json:"enabled"`
	Insecure   bool        `json:"insecure"`
	ServerName string      `json:"server_name,omitempty"`
	ALPN       []string    `json:"alpn,omitempty"`
	UTLS       *UTLSConfig `json:"utls,omitempty"`
	Reality    *RealityTLS `json:"reality,omitempty"`
}

// UTLSConfig configures the uTLS fingerprint for TLS.
type UTLSConfig struct {
	Enabled     bool   `json:"enabled"`
	Fingerprint string `json:"fingerprint"`
}

// RealityTLS configures REALITY TLS extension parameters.
type RealityTLS struct {
	Enabled   bool   `json:"enabled"`
	PublicKey string `json:"public_key"`
	ShortID   string `json:"short_id,omitempty"`
}

// OutboundTransport is the transport-layer config for a sing-box outbound.
// Different transport types use different subsets of fields. MarshalJSON
// produces the correct per-type JSON shape.
type OutboundTransport struct {
	Type string

	// ws
	WSPath    string
	WSHeaders map[string]string

	// http / h2
	HTTPHosts []string
	HTTPPath  string

	// grpc
	ServiceName string

	// xhttp
	XHTTPHost     string
	XHTTPPath     string
	XHTTPMode     string
	XPaddingBytes string
}

// MarshalJSON emits transport JSON in the shape sing-box expects per transport type.
func (t *OutboundTransport) MarshalJSON() ([]byte, error) {
	m := map[string]interface{}{"type": t.Type}
	switch t.Type {
	case "ws":
		if t.WSPath != "" {
			m["path"] = t.WSPath
		}
		if len(t.WSHeaders) > 0 {
			m["headers"] = t.WSHeaders
		}
	case "http":
		if len(t.HTTPHosts) > 0 {
			m["host"] = t.HTTPHosts
		}
		if t.HTTPPath != "" {
			m["path"] = t.HTTPPath
		}
	case "grpc":
		if t.ServiceName != "" {
			m["service_name"] = t.ServiceName
		}
	case "xhttp":
		if t.XHTTPMode != "" {
			m["mode"] = t.XHTTPMode
		}
		if t.XHTTPHost != "" {
			m["host"] = t.XHTTPHost
		}
		if t.XHTTPPath != "" {
			m["path"] = t.XHTTPPath
		}
		if t.XPaddingBytes != "" {
			m["x_padding_bytes"] = t.XPaddingBytes
		}
	}
	return json.Marshal(m)
}

// URLTestOutbound is a sing-box urltest outbound group.
type URLTestOutbound struct {
	Type      string   `json:"type"`
	Tag       string   `json:"tag"`
	Outbounds []string `json:"outbounds"`
	URL       string   `json:"url"`
	Interval  string   `json:"interval"`
	Tolerance int      `json:"tolerance"`
}

// SelectorOutbound is a sing-box selector outbound group.
type SelectorOutbound struct {
	Type      string   `json:"type"`
	Tag       string   `json:"tag"`
	Outbounds []string `json:"outbounds"`
	Default   string   `json:"default"`
}

// BuildOutbounds generates the sing-box outbound configuration for a subscription
// from its VLESS URIs. The id parameter is the stable subscription key used to
// derive outbound tags.
//
// For a single node, one VLESSOutbound is produced with tag "<id>-single".
// For multiple nodes, per-node outbounds tagged "<id>-node-<hash>" are produced
// alongside a urltest group "<id>-auto" and a selector group "<id>-manual".
func BuildOutbounds(id string, uris []string, interval string, tolerance int, testURL string) (*OutboundPlan, error) {
	if len(uris) == 0 {
		return nil, fmt.Errorf("no URIs for subscription %q", id)
	}

	// Apply defaults matching legacy shell behavior.
	if interval == "" {
		interval = defaultInterval
	}
	if tolerance == 0 {
		tolerance = defaultTolerance
	}

	plan := &OutboundPlan{
		TagNames: make(map[string]string),
	}

	nodes := make([]*vless.Node, 0, len(uris))
	for _, u := range uris {
		n, err := vless.Parse(u)
		if err != nil {
			logx.Warn("skipping unparseable VLESS URI: %v", err)
			continue
		}
		nodes = append(nodes, n)
	}
	if len(nodes) == 0 {
		return nil, fmt.Errorf("no valid VLESS nodes found in subscription %q", id)
	}

	if len(nodes) == 1 {
		// Single-node mode: use <id>-single tag directly.
		tag := id + "-single"
		ob := nodeToOutbound(nodes[0], tag)
		plan.NodeOutbounds = append(plan.NodeOutbounds, ob)
		plan.FinalTag = tag
		plan.TagNames[tag] = nodes[0].Name
	} else {
		// Multi-node mode: hash-tagged nodes + urltest + selector.
		nodeTags := make([]string, 0, len(nodes))
		for _, n := range nodes {
			hash := vless.StableHash(n)
			tag := fmt.Sprintf("%s-node-%s", id, hash)
			ob := nodeToOutbound(n, tag)
			plan.NodeOutbounds = append(plan.NodeOutbounds, ob)
			plan.TagNames[tag] = n.Name
			nodeTags = append(nodeTags, tag)
		}

		autoTag := id + "-auto"
		manualTag := id + "-manual"

		plan.URLTestGroup = &URLTestOutbound{
			Type:      "urltest",
			Tag:       autoTag,
			Outbounds: nodeTags,
			URL:       testURL,
			Interval:  interval,
			Tolerance: tolerance,
		}
		plan.TagNames[autoTag] = "Auto"

		manualOutbounds := make([]string, 0, len(nodeTags)+1)
		manualOutbounds = append(manualOutbounds, autoTag)
		manualOutbounds = append(manualOutbounds, nodeTags...)
		plan.SelectorGroup = &SelectorOutbound{
			Type:      "selector",
			Tag:       manualTag,
			Outbounds: manualOutbounds,
			Default:   autoTag,
		}
		plan.TagNames[manualTag] = id

		plan.FinalTag = manualTag
	}

	return plan, nil
}

// nodeToOutbound converts a parsed VLESS node into a typed sing-box VLESSOutbound.
func nodeToOutbound(n *vless.Node, tag string) *VLESSOutbound {
	ob := &VLESSOutbound{
		Type:           "vless",
		Tag:            tag,
		Server:         n.Server,
		ServerPort:     n.Port,
		UUID:           n.UUID,
		Flow:           n.Flow,
		PacketEncoding: packetEncoding,
	}

	// TLS block: generate only when security is explicitly "tls" or "reality".
	// An empty security field means plaintext — do not inject TLS.
	if n.Security == "tls" || n.Security == "reality" {
		tls := &OutboundTLS{
			Enabled:    true,
			Insecure:   false,
			ServerName: n.SNI,
			ALPN:       n.ALPN,
		}
		if n.Fingerprint != "" {
			tls.UTLS = &UTLSConfig{
				Enabled:     true,
				Fingerprint: n.Fingerprint,
			}
		}
		if n.Security == "reality" && n.PublicKey != "" {
			tls.Reality = &RealityTLS{
				Enabled:   true,
				PublicKey: n.PublicKey,
				ShortID:   n.ShortID,
			}
		}
		ob.TLS = tls
	}

	// Transport block.
	ob.Transport = buildTransport(n)

	return ob
}

// buildTransport constructs the transport block from a parsed VLESS node.
// Returns nil when no explicit transport is needed (plain TCP).
func buildTransport(n *vless.Node) *OutboundTransport {
	// Determine effective transport type, matching legacy shell logic.
	effType := n.TransportType
	if n.TransportType == "tcp" && n.HeaderType == "http" {
		effType = "http"
	}
	// h2 is an alias for http transport.
	if n.TransportType == "h2" {
		effType = "http"
	}

	switch effType {
	case "ws":
		t := &OutboundTransport{Type: "ws", WSPath: n.Path}
		if n.Host != "" {
			t.WSHeaders = map[string]string{"Host": n.Host}
		}
		return t
	case "http":
		t := &OutboundTransport{Type: "http", HTTPPath: n.Path}
		if n.Host != "" {
			t.HTTPHosts = []string{n.Host}
		}
		return t
	case "grpc":
		return &OutboundTransport{Type: "grpc", ServiceName: n.ServiceName}
	case "xhttp":
		return &OutboundTransport{
			Type:          "xhttp",
			XHTTPMode:     n.Mode,
			XHTTPHost:     n.Host,
			XHTTPPath:     n.Path,
			XPaddingBytes: "100-1000",
		}
	case "quic":
		return &OutboundTransport{Type: "quic"}
	default:
		// plain tcp or no transport; no transport block needed
		return nil
	}
}
