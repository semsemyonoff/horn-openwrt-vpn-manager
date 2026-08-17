// Package vless implements VLESS URI parsing, stable node identity hashing and
// the sing-box outbound generated from a parsed node. It satisfies proto.Node.
package vless

import (
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/proto"
)

const (
	typeVLESS      = "vless"
	uriPrefix      = "vless://"
	packetEncoding = "xudp"

	transportWS    = "ws"
	transportTCP   = "tcp"
	transportHTTP  = "http"
	transportH2    = "h2"
	transportXHTTP = "xhttp"
	transportGRPC  = "grpc"
	transportQUIC  = "quic"

	securityTLS     = "tls"
	securityReality = "reality"
)

// Node is a parsed VLESS URI with all connection parameters as typed fields.
//
// The fields are unexported and read through the accessors below: proto.Node
// requires Server(), Port() and Name() methods, and a Go type cannot carry a
// field and a method under the same name.
type Node struct {
	uuid   string
	server string
	port   int
	name   string // display name from URI fragment (URL-decoded)

	// Connection
	flow     string
	security string // tls, reality, or empty

	// TLS
	sni         string
	alpn        []string
	fingerprint string // fp param (uTLS fingerprint)

	// Reality (when security == "reality")
	publicKey string // pbk param
	shortID   string // sid param

	// Transport
	transportType string // ws, grpc, http, h2, xhttp, quic, tcp, or empty
	path          string
	host          string
	serviceName   string // grpc service name
	mode          string // xhttp mode
	headerType    string // tcp with headerType=http triggers http transport
}

var _ proto.Node = (*Node)(nil)

// Type returns the sing-box outbound type.
func (n *Node) Type() string { return typeVLESS }

// Server returns the node hostname or IP.
func (n *Node) Server() string { return n.server }

// Port returns the node port.
func (n *Node) Port() int { return n.port }

// Name returns the display name taken from the URI fragment.
func (n *Node) Name() string { return n.name }

// UUID returns the VLESS user id.
func (n *Node) UUID() string { return n.uuid }

// Flow returns the flow parameter (e.g. xtls-rprx-vision).
func (n *Node) Flow() string { return n.flow }

// Security returns the security parameter: "tls", "reality" or empty.
func (n *Node) Security() string { return n.security }

// SNI returns the TLS server name.
func (n *Node) SNI() string { return n.sni }

// ALPN returns the negotiated protocol list, nil when the URI carried none.
func (n *Node) ALPN() []string { return n.alpn }

// Fingerprint returns the uTLS fingerprint (fp parameter).
func (n *Node) Fingerprint() string { return n.fingerprint }

// PublicKey returns the REALITY public key (pbk parameter).
func (n *Node) PublicKey() string { return n.publicKey }

// ShortID returns the REALITY short id (sid parameter).
func (n *Node) ShortID() string { return n.shortID }

// TransportType returns the raw type parameter.
func (n *Node) TransportType() string { return n.transportType }

// Path returns the transport path.
func (n *Node) Path() string { return n.path }

// Host returns the transport host header.
func (n *Node) Host() string { return n.host }

// ServiceName returns the gRPC service name.
func (n *Node) ServiceName() string { return n.serviceName }

// Mode returns the xhttp mode.
func (n *Node) Mode() string { return n.mode }

// HeaderType returns the headerType parameter.
func (n *Node) HeaderType() string { return n.headerType }

// parseReason strips the URI out of a url.Error. Its Error() renders as
// `parse "<the whole URI>": <reason>`, and a VLESS URI carries the UUID that
// authenticates to the server — the reason alone is what may be logged or shown
// in LuCI.
func parseReason(err error) error {
	var uerr *url.Error
	if errors.As(err, &uerr) && uerr.Err != nil {
		return uerr.Err
	}
	return err
}

// Parse parses a VLESS URI into a Node.
// The URI must start with "vless://".
func Parse(rawURI string) (*Node, error) {
	if !strings.HasPrefix(rawURI, uriPrefix) {
		return nil, errors.New("not a vless URI")
	}

	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", parseReason(err))
	}

	if u.User == nil {
		return nil, errors.New("missing UUID in VLESS URI")
	}
	uuid := u.User.Username()
	if uuid == "" {
		return nil, errors.New("empty UUID in VLESS URI")
	}

	server := u.Hostname()
	if server == "" {
		return nil, errors.New("missing server in VLESS URI")
	}

	portStr := u.Port()
	if portStr == "" {
		return nil, errors.New("missing port in VLESS URI")
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("invalid port %q in VLESS URI", portStr)
	}

	q := u.Query()

	alpnRaw := q.Get("alpn")
	var alpn []string
	if alpnRaw != "" {
		for a := range strings.SplitSeq(alpnRaw, ",") {
			if a = strings.TrimSpace(a); a != "" {
				alpn = append(alpn, a)
			}
		}
	}

	// Name from fragment: also replace '+' with space to match legacy behavior
	// where subscription generators encode spaces as '+' in fragments.
	name := strings.ReplaceAll(u.Fragment, "+", " ")

	n := &Node{
		uuid:          uuid,
		server:        server,
		port:          port,
		name:          name,
		flow:          q.Get("flow"),
		security:      q.Get("security"),
		sni:           q.Get("sni"),
		alpn:          alpn,
		fingerprint:   q.Get("fp"),
		publicKey:     q.Get("pbk"),
		shortID:       q.Get("sid"),
		transportType: q.Get("type"),
		path:          q.Get("path"),
		host:          q.Get("host"),
		serviceName:   q.Get("serviceName"),
		mode:          q.Get("mode"),
		headerType:    q.Get("headerType"),
	}
	return n, nil
}

// StableHash computes the 8-character stable node identity hash from connection
// parameters that determine the server endpoint. The hash is stable across
// subscription refreshes as long as those parameters do not change, enabling
// consistent tag generation for sing-box outbounds.
//
// Hash input format mirrors the legacy shell implementation:
//
//	vless|server|port|uuid|security|sni|pbk|sid|flow|fp|type|path|host|serviceName
//
// The layout is frozen. Node tags are "<id>-node-<hash>" and those tags are
// written to subs-tags.json, referenced by saved selector choices and persisted
// in experimental.cache_file, so any change to the input silently repoints every
// saved choice on every deployed router.
func StableHash(n *Node) string {
	input := fmt.Sprintf(
		"vless|%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		n.server, n.port, n.uuid,
		n.security, n.sni, n.publicKey, n.shortID,
		n.flow, n.fingerprint,
		n.transportType, n.path, n.host, n.serviceName,
	)
	sum := md5.Sum([]byte(input))
	return hex.EncodeToString(sum[:])[:8]
}

// StableHash implements proto.Node. It delegates to the package-level function
// so there is exactly one definition of the frozen hash input.
func (n *Node) StableHash() string { return StableHash(n) }

// Outbound is a typed sing-box VLESS outbound configuration.
type Outbound struct {
	Type           string             `json:"type"`
	Tag            string             `json:"tag"`
	Server         string             `json:"server"`
	ServerPort     int                `json:"server_port"`
	UUID           string             `json:"uuid"`
	Flow           string             `json:"flow,omitempty"`
	PacketEncoding string             `json:"packet_encoding,omitempty"`
	TLS            *proto.OutboundTLS `json:"tls,omitempty"`
	Transport      *Transport         `json:"transport,omitempty"`
	// ConnectTimeout is a sing-box dial field. A failing dial otherwise hangs
	// for the OS default, delaying any fallback switch by the same amount.
	ConnectTimeout string `json:"connect_timeout,omitempty"`
}

// Transport is the transport-layer config for a sing-box VLESS outbound.
// Different transport types use different subsets of fields. MarshalJSON
// produces the correct per-type JSON shape.
type Transport struct {
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
func (t *Transport) MarshalJSON() ([]byte, error) {
	m := map[string]any{"type": t.Type}
	switch t.Type {
	case transportWS:
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

// Outbound implements proto.Node. It delegates to NewOutbound; an empty tag
// yields the tagless form used as the deduplication key.
func (n *Node) Outbound(tag, connectTimeout string) any {
	return NewOutbound(n, tag, connectTimeout)
}

// NewOutbound converts a parsed VLESS node into a typed sing-box outbound.
// An empty connectTimeout omits the connect_timeout field entirely.
func NewOutbound(n *Node, tag, connectTimeout string) *Outbound {
	ob := &Outbound{
		Type:           typeVLESS,
		Tag:            tag,
		Server:         n.server,
		ServerPort:     n.port,
		UUID:           n.uuid,
		Flow:           n.flow,
		ConnectTimeout: connectTimeout,
	}
	// packet_encoding is incompatible with XTLS flow (e.g. xtls-rprx-vision).
	// Only set it when flow is absent.
	if n.flow == "" {
		ob.PacketEncoding = packetEncoding
	}

	// TLS block: generate only when security is explicitly "tls" or "reality".
	// An empty security field means plaintext — do not inject TLS.
	if n.security == securityTLS || n.security == securityReality {
		alpn := n.alpn
		if len(alpn) == 0 && n.transportType == transportXHTTP {
			alpn = []string{"h2"}
		}
		tls := &proto.OutboundTLS{
			Enabled:    true,
			Insecure:   false,
			ServerName: n.sni,
			ALPN:       alpn,
		}
		if n.fingerprint != "" {
			tls.UTLS = &proto.UTLSConfig{
				Enabled:     true,
				Fingerprint: n.fingerprint,
			}
		}
		if n.security == securityReality && n.publicKey != "" {
			tls.Reality = &proto.RealityTLS{
				Enabled:   true,
				PublicKey: n.publicKey,
				ShortID:   n.shortID,
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
func buildTransport(n *Node) *Transport {
	// Determine effective transport type, matching legacy shell logic.
	effType := n.transportType
	if n.transportType == transportTCP && n.headerType == transportHTTP {
		effType = transportHTTP
	}
	// h2 is an alias for http transport.
	if n.transportType == transportH2 {
		effType = transportHTTP
	}

	switch effType {
	case transportWS:
		t := &Transport{Type: transportWS, WSPath: n.path}
		if n.host != "" {
			t.WSHeaders = map[string]string{"Host": n.host}
		}
		return t
	case transportHTTP:
		t := &Transport{Type: transportHTTP, HTTPPath: n.path}
		if n.host != "" {
			t.HTTPHosts = []string{n.host}
		}
		return t
	case transportGRPC:
		return &Transport{Type: transportGRPC, ServiceName: n.serviceName}
	case transportXHTTP:
		mode := n.mode
		if mode == "" {
			mode = "auto"
		}
		return &Transport{
			Type:          transportXHTTP,
			XHTTPMode:     mode,
			XHTTPHost:     n.host,
			XHTTPPath:     n.path,
			XPaddingBytes: "100-1000",
		}
	case transportQUIC:
		// QUIC transport is not a standalone transport block in sing-box VLESS;
		// treat as plain TCP so sing-box check does not fail.
		return nil
	default:
		// plain tcp or no transport; no transport block needed
		return nil
	}
}
