// Package vless implements VLESS URI parsing and stable node identity hashing.
package vless

import (
	"crypto/md5"
	"encoding/hex"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

// Node is a parsed VLESS URI with all connection parameters as typed fields.
type Node struct {
	UUID   string
	Server string
	Port   int
	Name   string // display name from URI fragment (URL-decoded)

	// Connection
	Flow     string
	Security string // tls, reality, or empty

	// TLS
	SNI         string
	ALPN        []string
	Fingerprint string // fp param (uTLS fingerprint)

	// Reality (when Security == "reality")
	PublicKey string // pbk param
	ShortID   string // sid param

	// Transport
	TransportType string // ws, grpc, http, h2, xhttp, quic, tcp, or empty
	Path          string
	Host          string
	ServiceName   string // grpc service name
	Mode          string // xhttp mode
	HeaderType    string // tcp with headerType=http triggers http transport
}

// Parse parses a VLESS URI into a Node.
// The URI must start with "vless://".
func Parse(rawURI string) (*Node, error) {
	if !strings.HasPrefix(rawURI, "vless://") {
		return nil, errors.New("not a vless URI")
	}

	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("parse URI: %w", err)
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
		UUID:          uuid,
		Server:        server,
		Port:          port,
		Name:          name,
		Flow:          q.Get("flow"),
		Security:      q.Get("security"),
		SNI:           q.Get("sni"),
		ALPN:          alpn,
		Fingerprint:   q.Get("fp"),
		PublicKey:     q.Get("pbk"),
		ShortID:       q.Get("sid"),
		TransportType: q.Get("type"),
		Path:          q.Get("path"),
		Host:          q.Get("host"),
		ServiceName:   q.Get("serviceName"),
		Mode:          q.Get("mode"),
		HeaderType:    q.Get("headerType"),
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
func StableHash(n *Node) string {
	input := fmt.Sprintf(
		"vless|%s|%d|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s|%s",
		n.Server, n.Port, n.UUID,
		n.Security, n.SNI, n.PublicKey, n.ShortID,
		n.Flow, n.Fingerprint,
		n.TransportType, n.Path, n.Host, n.ServiceName,
	)
	sum := md5.Sum([]byte(input))
	return hex.EncodeToString(sum[:])[:8]
}
