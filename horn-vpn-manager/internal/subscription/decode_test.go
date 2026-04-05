package subscription

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestDecodePayload_raw(t *testing.T) {
	data := []byte("vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
	if uris[0] != "vless://uuid1@host1.example.com:443?encryption=none#Node+1" {
		t.Errorf("uri[0] = %q", uris[0])
	}
	if uris[1] != "vless://uuid2@host2.example.com:443?encryption=none#Node+2" {
		t.Errorf("uri[1] = %q", uris[1])
	}
}

func TestDecodePayload_raw_single_line(t *testing.T) {
	data := []byte("vless://onlynode@host.example.com:443?encryption=none#Single")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
}

func TestDecodePayload_raw_with_comments_and_blanks(t *testing.T) {
	data := []byte(`
# This is a comment
vless://uuid1@host1.example.com:443?encryption=none#Node+1

some random text
vless://uuid2@host2.example.com:443?encryption=none#Node+2
`)

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2 (non-vless lines should be ignored)", len(uris))
	}
}

func TestDecodePayload_raw_windows_line_endings(t *testing.T) {
	data := []byte("vless://uuid1@host1.example.com:443?encryption=none#Node+1\r\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\r\n")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
	// URIs should not contain \r
	for _, uri := range uris {
		for _, ch := range uri {
			if ch == '\r' {
				t.Errorf("URI contains carriage return: %q", uri)
			}
		}
	}
}

func TestDecodePayload_no_vless_lines(t *testing.T) {
	data := []byte("this is not a vless subscription\njust some text\n")

	_, err := DecodePayload(data)
	if err == nil {
		t.Fatal("expected error for payload with no vless:// lines")
	}
}

func TestDecodePayload_fixture_raw(t *testing.T) {
	_, testFile, _, _ := runtime.Caller(0)
	dir := filepath.Dir(testFile)
	data, err := os.ReadFile(filepath.Join(dir, "testdata", "raw_subscription.txt"))
	if err != nil {
		t.Fatalf("read fixture: %v", err)
	}

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 3 {
		t.Fatalf("got %d URIs from fixture, want 3", len(uris))
	}
}

func TestDecodePayload_base64(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
	if uris[0] != "vless://uuid1@host1.example.com:443?encryption=none#Node+1" {
		t.Errorf("uri[0] = %q", uris[0])
	}
}

func TestDecodePayload_base64_no_padding(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\n"
	encoded := base64.RawStdEncoding.EncodeToString([]byte(raw))

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
}

func TestDecodePayload_base64url(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n"
	encoded := base64.URLEncoding.EncodeToString([]byte(raw))
	// Verify it's actually URL-safe (no + or /)
	if strings.ContainsAny(encoded, "+/") {
		t.Logf("encoded contains + or / — still testing URL encoding path")
	}

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
}

func TestDecodePayload_base64url_no_padding(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\n"
	encoded := base64.RawURLEncoding.EncodeToString([]byte(raw))

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
}

func TestDecodePayload_base64_with_windows_line_endings(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\r\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\r\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
	for _, uri := range uris {
		if strings.ContainsRune(uri, '\r') {
			t.Errorf("URI contains carriage return: %q", uri)
		}
	}
}

func TestDecodePayload_base64_empty_after_decode(t *testing.T) {
	// Valid base64 that decodes to non-vless content — should be treated as unrecognized
	encoded := base64.StdEncoding.EncodeToString([]byte("just some text, no vless lines"))

	_, err := DecodePayload([]byte(encoded))
	if err == nil {
		t.Fatal("expected error for base64 payload with no vless:// lines")
	}
}

func TestDecodePayload_malformed_base64(t *testing.T) {
	// Not valid base64 and not raw vless — should error
	data := []byte("this is !@#$ not base64 nor vless content !!!!")

	_, err := DecodePayload(data)
	if err == nil {
		t.Fatal("expected error for malformed payload")
	}
}

func TestDecodePayload_empty(t *testing.T) {
	_, err := DecodePayload([]byte{})
	if err == nil {
		t.Fatal("expected error for empty payload")
	}
}

func TestFormatString(t *testing.T) {
	cases := []struct {
		format Format
		want   string
	}{
		{FormatRaw, "raw"},
		{FormatBase64, "base64"},
		{FormatBase64URL, "base64url"},
		{FormatUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.format.String(); got != tc.want {
			t.Errorf("Format(%d).String() = %q, want %q", tc.format, got, tc.want)
		}
	}
}
