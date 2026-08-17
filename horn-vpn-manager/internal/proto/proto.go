// Package proto defines the contract every node protocol implements and owns the
// structs shared between protocol packages.
//
// The TLS structs live here rather than in internal/subscription because that is
// what keeps the import graph acyclic: protocol packages (vless, hysteria2, …)
// import proto, while subscription imports the protocol packages.
package proto

// Node is a parsed node URI of any supported protocol. Each protocol package
// implements it for its own URI scheme; internal/nodes dispatches to them by
// scheme.
type Node interface {
	// Type returns the sing-box outbound type ("vless", "hysteria2", …).
	Type() string

	// Server returns the node hostname or IP.
	Server() string

	// Port returns the node port.
	Port() int

	// Name returns the display name taken from the URI fragment.
	Name() string

	// StableHash returns the 8 hex characters node tags are derived from.
	//
	// The hash input is frozen per protocol and prefixed with the protocol
	// name, so tags never collide across protocols and an existing protocol's
	// tags never move. Changing a protocol's hash input silently repoints
	// subs-tags.json, saved selector choices and experimental.cache_file state
	// on every deployed router.
	StableHash() string

	// Outbound returns the typed sing-box outbound struct for this node. An
	// empty tag yields the tagless form used as the deduplication key; an empty
	// connectTimeout omits the connect_timeout field entirely.
	Outbound(tag, connectTimeout string) any
}

// OutboundTLS is the TLS block of a sing-box outbound.
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
