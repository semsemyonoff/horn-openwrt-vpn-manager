// Package hysteria2 implements hysteria2 URI parsing, stable node identity
// hashing and the sing-box outbound generated from a parsed node. It satisfies
// proto.Node.
//
// The URI scheme follows https://v2.hysteria.network/docs/developers/URI-Scheme/.
// Parameters that are not part of that spec are marked as client extensions
// where they appear below.
package hysteria2

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/proto"
)

const (
	typeHysteria2 = "hysteria2"

	// Both schemes are official; hy2 is the short form.
	uriPrefix      = "hysteria2://"
	uriPrefixShort = "hy2://"

	// The spec makes the port optional and defaults it to 443.
	defaultPort = 443

	// sing-box implements only salamander obfuscation. The URI spec also allows
	// "gecko", which has to be rejected rather than silently downgraded.
	obfsSalamander = "salamander"
)

// Node is a parsed hysteria2 URI with all connection parameters as typed fields.
//
// The fields are unexported and read through the accessors below: proto.Node
// requires Server(), Port() and Name() methods, and a Go type cannot carry a
// field and a method under the same name.
type Node struct {
	auth   string // the whole userinfo component, percent-decoded
	server string
	port   int
	name   string // display name from URI fragment

	// TLS
	sni      string
	insecure bool
	alpn     []string // client extension, not in the URI spec

	// Obfuscation
	obfsType     string
	obfsPassword string

	// Bandwidth. Client extensions, not in the URI spec. Leaving both unset
	// selects BBR congestion control instead of Brutal.
	upMbps   int
	downMbps int
}

var _ proto.Node = (*Node)(nil)

// Type returns the sing-box outbound type.
func (n *Node) Type() string { return typeHysteria2 }

// Server returns the node hostname or IP.
func (n *Node) Server() string { return n.server }

// Port returns the node port.
func (n *Node) Port() int { return n.port }

// Name returns the display name taken from the URI fragment.
func (n *Node) Name() string { return n.name }

// Auth returns the authentication string: the whole userinfo component, which
// may itself contain a colon for userpass auth.
func (n *Node) Auth() string { return n.auth }

// SNI returns the TLS server name.
func (n *Node) SNI() string { return n.sni }

// Insecure reports whether certificate verification is disabled.
func (n *Node) Insecure() bool { return n.insecure }

// ALPN returns the negotiated protocol list, nil when the URI carried none.
func (n *Node) ALPN() []string { return n.alpn }

// ObfsType returns the obfuscation type, empty when the URI carried none.
func (n *Node) ObfsType() string { return n.obfsType }

// ObfsPassword returns the obfuscation password.
func (n *Node) ObfsPassword() string { return n.obfsPassword }

// UpMbps returns the uplink bandwidth hint, 0 when the URI carried none.
func (n *Node) UpMbps() int { return n.upMbps }

// DownMbps returns the downlink bandwidth hint, 0 when the URI carried none.
func (n *Node) DownMbps() int { return n.downMbps }

// Parse parses a hysteria2 URI into a Node.
// The URI must start with "hysteria2://" or "hy2://".
func Parse(rawURI string) (*Node, error) {
	if !strings.HasPrefix(rawURI, uriPrefix) && !strings.HasPrefix(rawURI, uriPrefixShort) {
		return nil, errors.New("not a hysteria2 URI")
	}

	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", err)
	}

	if u.User == nil {
		return nil, errors.New("missing auth in hysteria2 URI")
	}
	// Auth is the entire userinfo component and may contain a colon
	// (username:password auth), so Username() alone would truncate it.
	// Userinfo.String() re-escapes what url.Parse decoded, hence the unescape.
	auth, err := url.PathUnescape(u.User.String())
	if err != nil {
		return nil, fmt.Errorf("decode auth in hysteria2 URI: %w", err)
	}
	if auth == "" {
		return nil, errors.New("empty auth in hysteria2 URI")
	}

	server := u.Hostname()
	if server == "" {
		return nil, errors.New("missing server in hysteria2 URI")
	}

	port := defaultPort
	if portStr := u.Port(); portStr != "" {
		port, err = strconv.Atoi(portStr)
		if err != nil || port <= 0 || port > 65535 {
			return nil, fmt.Errorf("invalid port %q in hysteria2 URI", portStr)
		}
	}

	q := u.Query()

	obfsType := q.Get("obfs")
	obfsPassword := q.Get("obfs-password")
	if obfsType != "" {
		if obfsType != obfsSalamander {
			return nil, fmt.Errorf("unsupported obfs %q in hysteria2 URI: sing-box implements only %q", obfsType, obfsSalamander)
		}
		// sing-box rejects a salamander block without a password, and that
		// rejection would fail the whole generated config rather than this one
		// node, so catch it here instead.
		if obfsPassword == "" {
			return nil, errors.New("missing obfs-password in hysteria2 URI")
		}
	}

	var alpn []string
	if alpnRaw := q.Get("alpn"); alpnRaw != "" {
		for a := range strings.SplitSeq(alpnRaw, ",") {
			if a = strings.TrimSpace(a); a != "" {
				alpn = append(alpn, a)
			}
		}
	}

	// Name from fragment: also replace '+' with space to match the VLESS
	// behavior, where subscription generators encode spaces as '+'.
	name := strings.ReplaceAll(u.Fragment, "+", " ")

	n := &Node{
		auth:         auth,
		server:       server,
		port:         port,
		name:         name,
		sni:          q.Get("sni"),
		insecure:     parseBool(q.Get("insecure")),
		alpn:         alpn,
		obfsType:     obfsType,
		obfsPassword: obfsPassword,
		upMbps:       parseMbps(q.Get("upmbps")),
		downMbps:     parseMbps(q.Get("downmbps")),
	}
	return n, nil
}

// parseBool reads a spec "insecure" flag. Anything that is not a recognized
// boolean literal means "verify certificates", which is the safe reading.
func parseBool(s string) bool {
	v, err := strconv.ParseBool(s)
	return err == nil && v
}

// parseMbps reads a bandwidth extension parameter. A missing or unusable value
// yields 0, which omits the field and leaves congestion control on BBR.
func parseMbps(s string) int {
	v, err := strconv.Atoi(s)
	if err != nil || v <= 0 {
		return 0
	}
	return v
}

// StableHash computes the 8-character stable node identity hash from connection
// parameters that determine the server endpoint.
//
// Hash input format:
//
//	hysteria2|server|port|auth|obfsType|obfsPassword|sni|insecure
//
// The layout is frozen, and the protocol prefix makes collisions with another
// protocol's tags structurally impossible. Node tags are "<id>-node-<hash>" and
// those tags are written to subs-tags.json, referenced by saved selector choices
// and persisted in experimental.cache_file, so any change to the input silently
// repoints every saved choice on every deployed router.
func StableHash(n *Node) string {
	input := fmt.Sprintf(
		"hysteria2|%s|%d|%s|%s|%s|%s|%t",
		n.server, n.port, n.auth,
		n.obfsType, n.obfsPassword,
		n.sni, n.insecure,
	)
	sum := md5.Sum([]byte(input))
	return hex.EncodeToString(sum[:])[:8]
}

// StableHash implements proto.Node. It delegates to the package-level function
// so there is exactly one definition of the frozen hash input.
func (n *Node) StableHash() string { return StableHash(n) }

// Outbound is a typed sing-box hysteria2 outbound configuration.
//
// ignore_client_bandwidth and masquerade are deliberately absent: sing-box
// accepts them on the hysteria2 *inbound* only. server_ports/hop_interval (port
// hopping) are out of scope.
type Outbound struct {
	Type       string `json:"type"`
	Tag        string `json:"tag"`
	Server     string `json:"server"`
	ServerPort int    `json:"server_port"`
	Password   string `json:"password"`
	Obfs       *Obfs  `json:"obfs,omitempty"`
	// UpMbps/DownMbps select Brutal congestion control. Omitting both leaves
	// sing-box on BBR, which is the right default on a lossless path.
	UpMbps   int `json:"up_mbps,omitempty"`
	DownMbps int `json:"down_mbps,omitempty"`
	// TLS is required on a hysteria2 outbound: the transport is QUIC.
	TLS *proto.OutboundTLS `json:"tls"`
	// ConnectTimeout is a sing-box dial field. It is emitted for consistency
	// with the other protocols; hysteria2 dials a UDP packet conn and then runs
	// a QUIC handshake with its own idle timeout, so it likely does not shorten
	// a fallback switch the way it does for VLESS.
	ConnectTimeout string `json:"connect_timeout,omitempty"`
}

// Obfs is the salamander obfuscation block of a hysteria2 outbound.
type Obfs struct {
	Type     string `json:"type"`
	Password string `json:"password"`
}

// Outbound implements proto.Node. It delegates to NewOutbound; an empty tag
// yields the tagless form used as the deduplication key.
func (n *Node) Outbound(tag, connectTimeout string) any {
	return NewOutbound(n, tag, connectTimeout)
}

// NewOutbound converts a parsed hysteria2 node into a typed sing-box outbound.
// An empty connectTimeout omits the connect_timeout field entirely.
func NewOutbound(n *Node, tag, connectTimeout string) *Outbound {
	ob := &Outbound{
		Type:       typeHysteria2,
		Tag:        tag,
		Server:     n.server,
		ServerPort: n.port,
		Password:   n.auth,
		UpMbps:     n.upMbps,
		DownMbps:   n.downMbps,
		TLS: &proto.OutboundTLS{
			Enabled:    true,
			Insecure:   n.insecure,
			ServerName: n.sni,
			ALPN:       n.alpn,
		},
		ConnectTimeout: connectTimeout,
	}
	if n.obfsType != "" {
		ob.Obfs = &Obfs{Type: n.obfsType, Password: n.obfsPassword}
	}
	return ob
}
