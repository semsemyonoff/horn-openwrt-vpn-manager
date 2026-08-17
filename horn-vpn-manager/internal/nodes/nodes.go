// Package nodes dispatches a node URI to the protocol package that owns its
// scheme.
//
// Adding a protocol is a new package implementing proto.Node plus one entry in
// the parsers map below. Registration is an explicit map rather than init()
// side-effects on purpose: this package holds the only non-test imports of the
// protocol packages, so an import-side-effect registry would be empty at runtime
// the moment a blank import went missing — the build would still succeed and
// every subscription would silently fail to parse.
package nodes

import (
	"errors"
	"fmt"
	"slices"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/hysteria2"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/proto"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/vless"
)

// parsers maps a URI scheme to the parser of the protocol package owning it.
var parsers = map[string]func(string) (proto.Node, error){
	"vless":     parseVLESS,
	"hysteria2": parseHysteria2,
	"hy2":       parseHysteria2, // official short form of hysteria2
}

// ErrUnknownScheme is returned by Parse for a URI whose scheme has no parser.
// Its message lists the supported schemes so a config error tells the operator
// what is accepted.
var ErrUnknownScheme = errors.New("unsupported node scheme, supported: " + strings.Join(Schemes(), ", "))

// parseVLESS and parseHysteria2 adapt the protocol parsers to the dispatcher
// signature. The explicit nil on the error path matters: returning the typed nil
// pointer straight through would yield a non-nil proto.Node interface.

func parseVLESS(uri string) (proto.Node, error) {
	n, err := vless.Parse(uri)
	if err != nil {
		return nil, err
	}
	return n, nil
}

func parseHysteria2(uri string) (proto.Node, error) {
	n, err := hysteria2.Parse(uri)
	if err != nil {
		return nil, err
	}
	return n, nil
}

// Parse parses a node URI of any supported protocol, dispatching on its scheme.
// A URI with an unregistered scheme yields an error wrapping ErrUnknownScheme.
//
// Errors never quote the URI itself: node URIs carry credentials.
func Parse(uri string) (proto.Node, error) {
	scheme := schemeOf(uri)
	parse, ok := parsers[scheme]
	if !ok {
		if scheme == "" {
			return nil, fmt.Errorf("node URI has no scheme: %w", ErrUnknownScheme)
		}
		return nil, fmt.Errorf("node scheme %q: %w", scheme, ErrUnknownScheme)
	}
	return parse(uri)
}

// Schemes returns the supported URI schemes, sorted, for error messages and
// line extraction.
func Schemes() []string {
	out := make([]string, 0, len(parsers))
	for scheme := range parsers {
		out = append(out, scheme)
	}
	slices.Sort(out)
	return out
}

// IsKnownScheme reports whether uri starts with a scheme this package can parse.
// It only looks at the scheme; the URI may still fail to parse.
func IsKnownScheme(uri string) bool {
	_, ok := parsers[schemeOf(uri)]
	return ok
}

// schemeOf returns the scheme of uri, or empty when there is none. Matching is
// case-sensitive, like the protocol parsers themselves.
func schemeOf(uri string) string {
	i := strings.Index(uri, "://")
	if i <= 0 {
		return ""
	}
	return uri[:i]
}
