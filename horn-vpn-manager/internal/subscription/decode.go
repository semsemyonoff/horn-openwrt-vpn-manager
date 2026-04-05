// Package subscription implements the subscription download and processing pipeline.
package subscription

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

// Format identifies the detected payload encoding.
type Format int

const (
	FormatUnknown Format = iota
	FormatRaw            // plain vless:// lines
)

func (f Format) String() string {
	switch f {
	case FormatRaw:
		return "raw"
	default:
		return "unknown"
	}
}

// DecodePayload detects and decodes a subscription payload, returning VLESS URIs.
// Returns an error if the payload cannot be decoded into any known format.
func DecodePayload(data []byte) ([]string, error) {
	uris, format := tryRaw(data)
	if format == FormatRaw {
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
