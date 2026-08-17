// Package subscription implements the subscription download and processing pipeline.
package subscription

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/nodes"
)

// Format identifies the detected payload encoding.
type Format int

const (
	FormatUnknown       Format = iota
	FormatRaw                  // plain node URI lines, one per line
	FormatGzip                 // gzip-compressed raw payload
	FormatBase64               // standard base64-encoded payload
	FormatBase64URL            // URL-safe base64-encoded payload
	FormatGzipBase64           // gzip-compressed payload wrapped in standard base64
	FormatGzipBase64URL        // gzip-compressed payload wrapped in URL-safe base64
	FormatJSON                 // V2Ray/Xray-style JSON config (array or object)
)

func (f Format) String() string {
	switch f {
	case FormatRaw:
		return "raw"
	case FormatGzip:
		return "gzip"
	case FormatBase64:
		return "base64"
	case FormatBase64URL:
		return "base64url"
	case FormatGzipBase64:
		return "gzip+base64"
	case FormatGzipBase64URL:
		return "gzip+base64url"
	case FormatJSON:
		return "json"
	default:
		return "unknown"
	}
}

// DecodePayload detects and decodes a subscription payload, returning node URIs
// of every scheme the nodes dispatcher supports.
// Detection order: raw → gzip → base64 (with gzip probe) → base64url (with gzip probe).
// Returns an error if the payload cannot be decoded into any known format.
func DecodePayload(data []byte) ([]string, error) {
	return decodePayload(data)
}

// decodePayload runs the format probes in order and returns the extracted URIs.
func decodePayload(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, errors.New("empty subscription payload")
	}

	if uris, format := tryRaw(data); format == FormatRaw {
		return uris, nil
	}

	if uris, format := tryGzip(data); format == FormatGzip {
		return uris, nil
	}

	if uris, format := tryJSON(data); format == FormatJSON {
		return uris, nil
	}

	if uris, format := tryBase64(data); format != FormatUnknown {
		return uris, nil
	}

	if uris, format := tryBase64URL(data); format != FormatUnknown {
		return uris, nil
	}

	return nil, fmt.Errorf("unrecognized subscription payload: no node lines found (supported schemes: %s) and no supported encoding detected",
		strings.Join(nodes.Schemes(), ", "))
}

// tryRaw attempts to extract node URIs from raw (unencoded) payload data.
// Returns the URIs and FormatRaw if at least one URI is found.
func tryRaw(data []byte) ([]string, Format) {
	uris := extractNodeLines(normalizeLineEndings(data))
	if len(uris) > 0 {
		return uris, FormatRaw
	}
	return nil, FormatUnknown
}

// tryGzip attempts to decompress a gzip payload and extract node URIs.
// Returns FormatGzip on success.
func tryGzip(data []byte) ([]string, Format) {
	if !isGzip(data) {
		return nil, FormatUnknown
	}
	decompressed, err := decompressGzip(data)
	if err != nil {
		return nil, FormatUnknown
	}
	uris := extractNodeLines(normalizeLineEndings(decompressed))
	if len(uris) > 0 {
		return uris, FormatGzip
	}
	return nil, FormatUnknown
}

// tryBase64 attempts to decode a standard base64 payload and extract node URIs.
// Tries both padded and unpadded variants. If the decoded bytes are gzip-compressed,
// decompression is attempted first, returning FormatGzipBase64 on success.
func tryBase64(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.StdEncoding, base64.RawStdEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	if isGzip(decoded) {
		if decompressed, err := decompressGzip(decoded); err == nil {
			if uris := extractNodeLines(normalizeLineEndings(decompressed)); len(uris) > 0 {
				return uris, FormatGzipBase64
			}
		}
	}
	uris := extractNodeLines(normalizeLineEndings(decoded))
	if len(uris) > 0 {
		return uris, FormatBase64
	}
	return nil, FormatUnknown
}

// tryBase64URL attempts to decode a URL-safe base64 payload and extract node URIs.
// Tries both padded and unpadded variants. If the decoded bytes are gzip-compressed,
// decompression is attempted first, returning FormatGzipBase64URL on success.
func tryBase64URL(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.URLEncoding, base64.RawURLEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	if isGzip(decoded) {
		if decompressed, err := decompressGzip(decoded); err == nil {
			if uris := extractNodeLines(normalizeLineEndings(decompressed)); len(uris) > 0 {
				return uris, FormatGzipBase64URL
			}
		}
	}
	uris := extractNodeLines(normalizeLineEndings(decoded))
	if len(uris) > 0 {
		return uris, FormatBase64URL
	}
	return nil, FormatUnknown
}

// decodeBase64 tries padded then unpadded decoding using the provided encodings.
func decodeBase64(s string, padded, raw *base64.Encoding) ([]byte, error) {
	if b, err := padded.DecodeString(s); err == nil {
		return b, nil
	}
	return raw.DecodeString(s)
}

// isGzip reports whether data begins with the gzip magic bytes (0x1f 0x8b).
func isGzip(data []byte) bool {
	return len(data) >= 2 && data[0] == 0x1f && data[1] == 0x8b
}

// decompressGzip decompresses gzip-compressed data and returns the raw bytes.
func decompressGzip(data []byte) ([]byte, error) {
	r, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	out, err := io.ReadAll(io.LimitReader(r, 10<<20))
	if closeErr := r.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return out, err
}

// normalizeLineEndings replaces Windows-style \r\n with Unix \n before scanning.
func normalizeLineEndings(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// extractNodeLines scans data line by line and returns every line whose scheme
// the nodes dispatcher can parse. A line of an unregistered scheme is skipped
// silently, exactly like any other non-node line in a subscription payload.
func extractNodeLines(data []byte) []string {
	var uris []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if nodes.IsKnownScheme(line) {
			uris = append(uris, line)
		}
	}
	if err := scanner.Err(); err != nil {
		logx.Warn("node line scan truncated (line too long): %v", err)
		return uris
	}
	return uris
}

// legacyScheme is the only scheme this tool accepted before multi-protocol
// support. It is the baseline for warnTopologyShift and nothing else.
const legacyScheme = "vless://"

// warnTopologyShift warns when accepting schemes beyond vless:// turns a payload
// that used to yield exactly one node into a multi-node one.
//
// The consequence is not cosmetic: a single-node subscription's final outbound
// tag is "<id>-single", while a multi-node one gets "<id>-node-<hash>" outbounds
// behind an "<id>-auto" urltest and an "<id>-manual" selector that becomes the
// final tag. The subscription's saved selector choice and its
// experimental.cache_file (clash.db) entry therefore stop resolving, and a node
// has to be re-picked once in LuCI. This is a one-time event per subscription.
//
// The zero-to-many case is deliberately not warned about: a payload carrying no
// vless:// line failed to decode at all before, so there is no saved state to
// invalidate.
//
// It takes the URI list the subscription actually builds outbounds from, not the
// freshly decoded payload: include/exclude run after decoding, so a subscription
// that filters the new-scheme nodes back out keeps its "<id>-single" tag and must
// not be told otherwise on every run. For the same reason it is never called for
// inline nodes, which have no pre-multi-protocol history at all.
func warnTopologyShift(id string, uris []string) {
	if len(uris) < 2 {
		return
	}
	legacy := 0
	var gained []string
	for _, uri := range uris {
		if strings.HasPrefix(uri, legacyScheme) {
			legacy++
			continue
		}
		if scheme, _, ok := strings.Cut(uri, "://"); ok && !slices.Contains(gained, scheme) {
			gained = append(gained, scheme)
		}
	}
	if legacy != 1 {
		return
	}
	logx.Warn("subscription %s yields %d node(s): 1 vless:// node plus %d node(s) of newly supported scheme(s) (%s). "+
		"This subscription was single-node before and is multi-node now, so its final outbound tag moves from %s-single to %s-manual; "+
		"its saved selector choice and clash.db entry no longer resolve and a node has to be re-picked once in LuCI",
		id, len(uris), len(uris)-1, strings.Join(gained, ", "), id, id)
}
