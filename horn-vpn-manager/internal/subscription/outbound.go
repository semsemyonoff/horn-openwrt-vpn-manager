package subscription

import (
	"encoding/json"
	"fmt"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/proto"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

const (
	defaultInterval  = "5m"
	defaultTolerance = 100
	packetEncoding   = "xudp"

	protoVLESS = "vless"

	transportTCP   = "tcp"
	transportHTTP  = "http"
	transportXHTTP = "xhttp"
	transportGRPC  = "grpc"

	securityTLS     = "tls"
	securityReality = "reality"
)

// OutboundPlan holds the sing-box outbound configuration generated for a single
// subscription. It covers both node outbounds and group outbounds.
type OutboundPlan struct {
	// ID is the subscription key this plan was generated from. Fallback chains
	// reference subscriptions by id, so plans must stay resolvable by it.
	ID string

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

	// FallbackGroup is the chain group generated when the subscription declares
	// a fallback. Nil otherwise. When set it supersedes FinalTag as the tag
	// route.final (default subscription) or the route rules point at.
	FallbackGroup *FallbackOutbound

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
	TLS            *proto.OutboundTLS `json:"tls,omitempty"`
	Transport      *OutboundTransport `json:"transport,omitempty"`
	// ConnectTimeout is a sing-box dial field. A failing dial otherwise hangs
	// for the OS default, delaying any fallback switch by the same amount.
	ConnectTimeout string `json:"connect_timeout,omitempty"`
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
	m := map[string]any{"type": t.Type}
	switch t.Type {
	case "ws":
		if t.WSPath != "" {
			m["path"] = t.WSPath
		}
		if len(t.WSHeaders) > 0 {
			m["headers"] = t.WSHeaders
		}
	case transportHTTP:
		if len(t.HTTPHosts) > 0 {
			m["host"] = t.HTTPHosts
		}
		if t.HTTPPath != "" {
			m["path"] = t.HTTPPath
		}
	case transportGRPC:
		if t.ServiceName != "" {
			m["service_name"] = t.ServiceName
		}
	case transportXHTTP:
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
	Type                      string   `json:"type"`
	Tag                       string   `json:"tag"`
	Outbounds                 []string `json:"outbounds"`
	URL                       string   `json:"url"`
	Interval                  string   `json:"interval"`
	Tolerance                 int      `json:"tolerance"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections"`
}

// SelectorOutbound is a sing-box selector outbound group.
type SelectorOutbound struct {
	Type                      string   `json:"type"`
	Tag                       string   `json:"tag"`
	Outbounds                 []string `json:"outbounds"`
	Default                   string   `json:"default"`
	InterruptExistConnections bool     `json:"interrupt_exist_connections"`
}

// FallbackOutbound is a sing-box fallback outbound group: connections try each
// outbound in order and an outbound that fails to dial is skipped for
// BlacklistTimeout. Unlike urltest it reacts to dial failure rather than probe
// latency, which is the only way to leave a node that still answers probes but
// stalls under load.
//
// fallback is not an upstream sing-box outbound type — it exists only in the
// extended build. A stock build rejects it at `sing-box check` time with
// "unknown outbound type", which system.ApplySingbox surfaces before the live
// config is replaced.
//
// The field set is exactly FallbackOutboundOptions in the extended build
// (option/group.go): outbounds and blacklist_timeout, nothing else. sing-box
// decodes outbound options with unknown fields disallowed, so an extra key —
// interrupt_exist_connections, which selector and urltest do accept — makes
// `sing-box check` reject the whole config.
type FallbackOutbound struct {
	Type             string   `json:"type"`
	Tag              string   `json:"tag"`
	Outbounds        []string `json:"outbounds"`
	BlacklistTimeout string   `json:"blacklist_timeout,omitempty"`
}

// fallbackTag returns the generated fallback group tag for a subscription id.
func fallbackTag(id string) string { return id + "-fallback" }

// BuildOptions carries the non-identifying inputs of BuildOutbounds. They are
// grouped rather than passed positionally because Interval and ConnectTimeout
// are adjacent duration strings and would be trivial to transpose.
type BuildOptions struct {
	// Interval is the urltest probe interval; empty means defaultInterval.
	Interval string
	// Tolerance is the urltest switch tolerance in ms; zero means defaultTolerance.
	Tolerance int
	// TestURL is the urltest probe URL.
	TestURL string
	// ConnectTimeout is set on every node outbound; empty omits the field.
	ConnectTimeout string
}

// BuildOutbounds generates the sing-box outbound configuration for a subscription
// from its VLESS URIs. The id parameter is the stable subscription key used to
// derive outbound tags.
//
// For a single node, one VLESSOutbound is produced with tag "<id>-single".
// For multiple nodes, per-node outbounds tagged "<id>-node-<hash>" are produced
// alongside a urltest group "<id>-auto" and a selector group "<id>-manual".
func BuildOutbounds(id string, uris []string, opts BuildOptions) (*OutboundPlan, error) {
	if len(uris) == 0 {
		return nil, fmt.Errorf("no URIs for subscription %q", id)
	}

	// Apply defaults matching legacy shell behavior.
	interval := opts.Interval
	if interval == "" {
		interval = defaultInterval
	}
	tolerance := opts.Tolerance
	if tolerance == 0 {
		tolerance = defaultTolerance
	}

	plan := &OutboundPlan{
		ID:       id,
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
		ob := nodeToOutbound(nodes[0], tag, opts.ConnectTimeout)
		plan.NodeOutbounds = append(plan.NodeOutbounds, ob)
		plan.FinalTag = tag
		plan.TagNames[tag] = nodes[0].Name
	} else {
		// Multi-node mode: hash-tagged nodes + urltest + selector.
		//
		// Providers routinely repeat the same endpoint across a subscription;
		// duplicates cost an extra urltest probe each and add nothing. Dedup
		// runs here rather than before the single/multi split so that an
		// all-duplicate subscription keeps its "<id>-manual" FinalTag instead of
		// silently collapsing to "<id>-single" and moving route.final.
		//
		// The dedup key is the marshalled outbound, not StableHash: the hash
		// omits ALPN, Mode and HeaderType, each of which changes the rendered
		// outbound. Tags still come from StableHash so subs-tags.json, saved
		// selector choices and experimental.cache_file state stay valid — which
		// is also why the collision suffix below has to stay: two distinct nodes
		// can share a hash.
		nodeTags := make([]string, 0, len(nodes))
		seenTags := make(map[string]int, len(nodes))
		seenOutbounds := make(map[string]struct{}, len(nodes))
		duplicates := 0
		for _, n := range nodes {
			ob := nodeToOutbound(n, "", opts.ConnectTimeout)
			key, err := json.Marshal(ob)
			if err != nil {
				return nil, fmt.Errorf("marshalling outbound for subscription %q: %w", id, err)
			}
			hash := vless.StableHash(n)
			base := fmt.Sprintf("%s-node-%s", id, hash)
			// The suffix counter advances for skipped duplicates too, so dedup
			// never renames a surviving node: without this, dropping a duplicate
			// would shift the next colliding node from "-3" to "-2", silently
			// repointing a tag that saved selector choices and
			// experimental.cache_file state still refer to.
			count := seenTags[base]
			seenTags[base]++
			if _, seen := seenOutbounds[string(key)]; seen {
				duplicates++
				continue
			}
			seenOutbounds[string(key)] = struct{}{}

			tag := base
			if count > 0 {
				tag = fmt.Sprintf("%s-%d", base, count+1)
			}
			ob.Tag = tag
			plan.NodeOutbounds = append(plan.NodeOutbounds, ob)
			plan.TagNames[tag] = n.Name
			nodeTags = append(nodeTags, tag)
		}
		if duplicates > 0 {
			logx.Detail("  Subscription %s: skipped %d duplicate nodes", id, duplicates)
		}

		autoTag := id + "-auto"
		manualTag := id + "-manual"

		// interrupt_exist_connections tears down connections to the previously
		// selected node when a group re-selects; without it they hang on a node
		// that has already stopped answering. On urltest it also fires on benign
		// latency-driven re-selection, so the per-subscription tolerance is what
		// keeps re-selection tied to genuine degradation.
		plan.URLTestGroup = &URLTestOutbound{
			Type:                      "urltest",
			Tag:                       autoTag,
			Outbounds:                 nodeTags,
			URL:                       opts.TestURL,
			Interval:                  interval,
			Tolerance:                 tolerance,
			InterruptExistConnections: true,
		}
		plan.TagNames[autoTag] = "Auto"

		manualOutbounds := make([]string, 0, len(nodeTags)+1)
		manualOutbounds = append(manualOutbounds, autoTag)
		manualOutbounds = append(manualOutbounds, nodeTags...)
		plan.SelectorGroup = &SelectorOutbound{
			Type:                      "selector",
			Tag:                       manualTag,
			Outbounds:                 manualOutbounds,
			Default:                   autoTag,
			InterruptExistConnections: true,
		}
		plan.TagNames[manualTag] = id

		plan.FinalTag = manualTag
	}

	return plan, nil
}

// nodeToOutbound converts a parsed VLESS node into a typed sing-box VLESSOutbound.
// An empty connectTimeout omits the connect_timeout field entirely.
func nodeToOutbound(n *vless.Node, tag, connectTimeout string) *VLESSOutbound {
	ob := &VLESSOutbound{
		Type:           protoVLESS,
		Tag:            tag,
		Server:         n.Server,
		ServerPort:     n.Port,
		UUID:           n.UUID,
		Flow:           n.Flow,
		ConnectTimeout: connectTimeout,
	}
	// packet_encoding is incompatible with XTLS flow (e.g. xtls-rprx-vision).
	// Only set it when flow is absent.
	if n.Flow == "" {
		ob.PacketEncoding = packetEncoding
	}

	// TLS block: generate only when security is explicitly "tls" or "reality".
	// An empty security field means plaintext — do not inject TLS.
	if n.Security == securityTLS || n.Security == securityReality {
		alpn := n.ALPN
		if len(alpn) == 0 && n.TransportType == transportXHTTP {
			alpn = []string{"h2"}
		}
		tls := &proto.OutboundTLS{
			Enabled:    true,
			Insecure:   false,
			ServerName: n.SNI,
			ALPN:       alpn,
		}
		if n.Fingerprint != "" {
			tls.UTLS = &proto.UTLSConfig{
				Enabled:     true,
				Fingerprint: n.Fingerprint,
			}
		}
		if n.Security == securityReality && n.PublicKey != "" {
			tls.Reality = &proto.RealityTLS{
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
	if n.TransportType == transportTCP && n.HeaderType == transportHTTP {
		effType = transportHTTP
	}
	// h2 is an alias for http transport.
	if n.TransportType == "h2" {
		effType = transportHTTP
	}

	switch effType {
	case "ws":
		t := &OutboundTransport{Type: "ws", WSPath: n.Path}
		if n.Host != "" {
			t.WSHeaders = map[string]string{"Host": n.Host}
		}
		return t
	case transportHTTP:
		t := &OutboundTransport{Type: transportHTTP, HTTPPath: n.Path}
		if n.Host != "" {
			t.HTTPHosts = []string{n.Host}
		}
		return t
	case transportGRPC:
		return &OutboundTransport{Type: transportGRPC, ServiceName: n.ServiceName}
	case transportXHTTP:
		mode := n.Mode
		if mode == "" {
			mode = "auto"
		}
		return &OutboundTransport{
			Type:          transportXHTTP,
			XHTTPMode:     mode,
			XHTTPHost:     n.Host,
			XHTTPPath:     n.Path,
			XPaddingBytes: "100-1000",
		}
	case "quic":
		// QUIC transport is not a standalone transport block in sing-box VLESS;
		// treat as plain TCP so sing-box check does not fail.
		return nil
	default:
		// plain tcp or no transport; no transport block needed
		return nil
	}
}
