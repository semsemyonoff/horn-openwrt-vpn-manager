// Package subscription implements the subscription download and processing pipeline.
package subscription

import (
	"bufio"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// Format identifies the detected payload encoding.
type Format int

const (
	FormatUnknown       Format = iota
	FormatRaw                  // plain vless:// lines
	FormatGzip                 // gzip-compressed raw payload
	FormatBase64               // standard base64-encoded payload
	FormatBase64URL            // URL-safe base64-encoded payload
	FormatGzipBase64           // gzip-compressed payload wrapped in standard base64
	FormatGzipBase64URL        // gzip-compressed payload wrapped in URL-safe base64
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
	default:
		return "unknown"
	}
}

// DecodePayload detects and decodes a subscription payload, returning VLESS URIs.
// Detection order: raw → gzip → base64 (with gzip probe) → base64url (with gzip probe).
// Returns an error if the payload cannot be decoded into any known format.
func DecodePayload(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty subscription payload")
	}

	if uris, format := tryRaw(data); format == FormatRaw {
		return uris, nil
	}

	if uris, format := tryGzip(data); format == FormatGzip {
		return uris, nil
	}

	if uris, format := tryBase64(data); format != FormatUnknown {
		return uris, nil
	}

	if uris, format := tryBase64URL(data); format != FormatUnknown {
		return uris, nil
	}

	return nil, fmt.Errorf("unrecognized subscription payload: no vless:// lines found and no supported encoding detected")
}

// tryRaw attempts to extract vless:// URIs from raw (unencoded) payload data.
// Returns the URIs and FormatRaw if at least one URI is found.
func tryRaw(data []byte) ([]string, Format) {
	uris := extractVLESSLines(normalizeLineEndings(data))
	if len(uris) > 0 {
		return uris, FormatRaw
	}
	return nil, FormatUnknown
}

// tryGzip attempts to decompress a gzip payload and extract vless:// URIs.
// Returns FormatGzip on success.
func tryGzip(data []byte) ([]string, Format) {
	if !isGzip(data) {
		return nil, FormatUnknown
	}
	decompressed, err := decompressGzip(data)
	if err != nil {
		return nil, FormatUnknown
	}
	uris := extractVLESSLines(normalizeLineEndings(decompressed))
	if len(uris) > 0 {
		return uris, FormatGzip
	}
	return nil, FormatUnknown
}

// tryBase64 attempts to decode a standard base64 payload and extract vless:// URIs.
// Tries both padded and unpadded variants. If the decoded bytes are gzip-compressed,
// decompression is attempted first, returning FormatGzipBase64 on success.
func tryBase64(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.StdEncoding, base64.RawStdEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	if isGzip(decoded) {
		if decompressed, err := decompressGzip(decoded); err == nil {
			if uris := extractVLESSLines(normalizeLineEndings(decompressed)); len(uris) > 0 {
				return uris, FormatGzipBase64
			}
		}
	}
	uris := extractVLESSLines(normalizeLineEndings(decoded))
	if len(uris) > 0 {
		return uris, FormatBase64
	}
	return nil, FormatUnknown
}

// tryBase64URL attempts to decode a URL-safe base64 payload and extract vless:// URIs.
// Tries both padded and unpadded variants. If the decoded bytes are gzip-compressed,
// decompression is attempted first, returning FormatGzipBase64URL on success.
func tryBase64URL(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.URLEncoding, base64.RawURLEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	if isGzip(decoded) {
		if decompressed, err := decompressGzip(decoded); err == nil {
			if uris := extractVLESSLines(normalizeLineEndings(decompressed)); len(uris) > 0 {
				return uris, FormatGzipBase64URL
			}
		}
	}
	uris := extractVLESSLines(normalizeLineEndings(decoded))
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
	out, err := io.ReadAll(io.LimitReader(r, 50<<20))
	if closeErr := r.Close(); closeErr != nil && err == nil {
		err = closeErr
	}
	return out, err
}

// normalizeLineEndings replaces Windows-style \r\n with Unix \n before scanning.
func normalizeLineEndings(data []byte) []byte {
	return bytes.ReplaceAll(data, []byte("\r\n"), []byte("\n"))
}

// extractVLESSLines scans data line by line and returns all vless:// lines.
func extractVLESSLines(data []byte) []string {
	var uris []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "vless://") {
			uris = append(uris, line)
		}
	}
	if err := scanner.Err(); err != nil {
		logx.Warn("vless line scan truncated (line too long): %v", err)
		return uris
	}
	return uris
}
