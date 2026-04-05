// Package subscription implements the subscription download and processing pipeline.
package subscription

import (
	"bufio"
	"bytes"
	"encoding/base64"
	"fmt"
	"strings"
)

// Format identifies the detected payload encoding.
type Format int

const (
	FormatUnknown  Format = iota
	FormatRaw             // plain vless:// lines
	FormatBase64          // standard base64-encoded payload
	FormatBase64URL       // URL-safe base64-encoded payload
)

func (f Format) String() string {
	switch f {
	case FormatRaw:
		return "raw"
	case FormatBase64:
		return "base64"
	case FormatBase64URL:
		return "base64url"
	default:
		return "unknown"
	}
}

// DecodePayload detects and decodes a subscription payload, returning VLESS URIs.
// Returns an error if the payload cannot be decoded into any known format.
func DecodePayload(data []byte) ([]string, error) {
	if len(data) == 0 {
		return nil, fmt.Errorf("empty subscription payload")
	}

	if uris, format := tryRaw(data); format == FormatRaw {
		return uris, nil
	}

	if uris, format := tryBase64(data); format == FormatBase64 {
		return uris, nil
	}

	if uris, format := tryBase64URL(data); format == FormatBase64URL {
		return uris, nil
	}

	return nil, fmt.Errorf("unrecognized subscription payload: no vless:// lines found and no supported encoding detected")
}

// tryRaw attempts to extract vless:// URIs from raw (unencoded) payload data.
// Returns the URIs and FormatRaw if at least one URI is found.
func tryRaw(data []byte) ([]string, Format) {
	uris := extractVLESSLines(data)
	if len(uris) > 0 {
		return uris, FormatRaw
	}
	return nil, FormatUnknown
}

// tryBase64 attempts to decode a standard base64 payload and extract vless:// URIs.
// Tries both padded and unpadded variants.
func tryBase64(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.StdEncoding, base64.RawStdEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	uris := extractVLESSLines(decoded)
	if len(uris) > 0 {
		return uris, FormatBase64
	}
	return nil, FormatUnknown
}

// tryBase64URL attempts to decode a URL-safe base64 payload and extract vless:// URIs.
// Tries both padded and unpadded variants.
func tryBase64URL(data []byte) ([]string, Format) {
	decoded, err := decodeBase64(strings.TrimSpace(string(data)), base64.URLEncoding, base64.RawURLEncoding)
	if err != nil {
		return nil, FormatUnknown
	}
	uris := extractVLESSLines(decoded)
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

// extractVLESSLines scans data line by line and returns all vless:// lines.
// Windows line endings are handled transparently by TrimSpace.
func extractVLESSLines(data []byte) []string {
	var uris []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if strings.HasPrefix(line, "vless://") {
			uris = append(uris, line)
		}
	}
	return uris
}
