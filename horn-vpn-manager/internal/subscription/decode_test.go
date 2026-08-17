package subscription

import (
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/nodes"
)

// gzipBytes compresses data with gzip for use in tests.
func gzipBytes(t *testing.T, data []byte) []byte {
	t.Helper()
	var buf bytes.Buffer
	w := gzip.NewWriter(&buf)
	if _, err := w.Write(data); err != nil {
		t.Fatalf("gzip write: %v", err)
	}
	if err := w.Close(); err != nil {
		t.Fatalf("gzip close: %v", err)
	}
	return buf.Bytes()
}

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

func TestDecodePayload_gzip(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n"
	compressed := gzipBytes(t, []byte(raw))

	uris, err := DecodePayload(compressed)
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

func TestDecodePayload_gzip_base64(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n"
	compressed := gzipBytes(t, []byte(raw))
	encoded := base64.StdEncoding.EncodeToString(compressed)

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
}

func TestDecodePayload_gzip_base64url(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\n"
	compressed := gzipBytes(t, []byte(raw))
	encoded := base64.URLEncoding.EncodeToString(compressed)

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2", len(uris))
	}
}

func TestDecodePayload_gzip_windows_line_endings(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\r\nvless://uuid2@host2.example.com:443?encryption=none#Node+2\r\n"
	compressed := gzipBytes(t, []byte(raw))

	uris, err := DecodePayload(compressed)
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

func TestDecodePayload_gzip_base64_no_padding(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\n"
	compressed := gzipBytes(t, []byte(raw))
	encoded := base64.RawStdEncoding.EncodeToString(compressed)

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1", len(uris))
	}
}

const (
	hy2Line      = "hysteria2://pass@hy.example.com:8443?sni=hy.example.com#HY+Node"
	hy2ShortLine = "hy2://pass2@hy2.example.com#HY2+Short"
)

func TestDecodePayload_raw_mixed_schemes(t *testing.T) {
	data := []byte("vless://uuid1@host1.example.com:443?encryption=none#Node+1\n" +
		hy2Line + "\n" +
		hy2ShortLine + "\n")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"vless://uuid1@host1.example.com:443?encryption=none#Node+1", hy2Line, hy2ShortLine}
	if len(uris) != len(want) {
		t.Fatalf("got %d URIs, want %d: %v", len(uris), len(want), uris)
	}
	for i := range want {
		if uris[i] != want[i] {
			t.Errorf("uri[%d] = %q, want %q", i, uris[i], want[i])
		}
	}
}

func TestDecodePayload_raw_hysteria2_only(t *testing.T) {
	data := []byte(hy2Line + "\n")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 || uris[0] != hy2Line {
		t.Fatalf("got %v, want [%s]", uris, hy2Line)
	}
}

func TestDecodePayload_base64_hysteria2(t *testing.T) {
	raw := hy2Line + "\n" + hy2ShortLine + "\n"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))

	uris, err := DecodePayload([]byte(encoded))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2: %v", len(uris), uris)
	}
	if uris[0] != hy2Line || uris[1] != hy2ShortLine {
		t.Errorf("got %v, want [%s %s]", uris, hy2Line, hy2ShortLine)
	}
}

func TestDecodePayload_gzip_mixed_schemes(t *testing.T) {
	raw := "vless://uuid1@host1.example.com:443?encryption=none#Node+1\n" + hy2Line + "\n"
	compressed := gzipBytes(t, []byte(raw))

	uris, err := DecodePayload(compressed)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 2 {
		t.Fatalf("got %d URIs, want 2: %v", len(uris), uris)
	}
}

// A scheme with no parser registered is not an error: subscription payloads
// routinely carry lines for protocols this tool does not implement, and failing
// the whole subscription over one of them would be worse than skipping it.
func TestDecodePayload_unregistered_scheme_skipped(t *testing.T) {
	data := []byte("trojan://secret@t.example.com:443#Trojan\n" +
		"vless://uuid1@host1.example.com:443?encryption=none#Node+1\n" +
		"ss://YWVzOnBhc3M@s.example.com:8388#SS\n")

	uris, err := DecodePayload(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(uris) != 1 {
		t.Fatalf("got %d URIs, want 1 (unregistered schemes are skipped): %v", len(uris), uris)
	}
	if uris[0] != "vless://uuid1@host1.example.com:443?encryption=none#Node+1" {
		t.Errorf("uri[0] = %q", uris[0])
	}
}

func TestDecodePayload_unregistered_scheme_only(t *testing.T) {
	data := []byte("trojan://secret@t.example.com:443#Trojan\n")

	_, err := DecodePayload(data)
	if err == nil {
		t.Fatal("expected an error for a payload with no supported node lines")
	}
	// The failure has to name what would have worked instead of asserting VLESS.
	for _, scheme := range nodes.Schemes() {
		if !strings.Contains(err.Error(), scheme) {
			t.Errorf("error %q does not list supported scheme %q", err, scheme)
		}
	}
}

// Decoding on its own must stay silent: whether a subscription ends up
// single- or multi-node is only known after include/exclude have run, so the
// warning belongs to the pipeline and not to DecodePayload.
func TestDecodePayload_does_not_warn_about_topology(t *testing.T) {
	buf := captureLog(t)
	data := []byte("vless://uuid1@host1.example.com:443?encryption=none#Node+1\n" + hy2Line + "\n")
	if _, err := DecodePayload(data); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.Contains(buf.String(), topologyWarning) {
		t.Errorf("DecodePayload emitted a topology-shift warning, log:\n%s", buf.String())
	}
}

const topologyWarning = "single-node before and is multi-node now"

// A subscription that used to yield exactly one node and now yields several
// silently moves its final tag from <id>-single to <id>-manual, which
// invalidates the saved selector choice and the clash.db entry.
func TestWarnTopologyShift(t *testing.T) {
	const vless1 = "vless://uuid1@host1.example.com:443?encryption=none#Node+1"
	const vless2 = "vless://uuid2@host2.example.com:443?encryption=none#Node+2"
	// Known scheme, rejected by the parser: sing-box implements only salamander.
	const hy2Broken = "hysteria2://pass@bad.example.com:8443?obfs=gecko#Broken"

	// warn drives the real BuildOutbounds so the plan the warning is checked
	// against is the one the run would actually generate.
	warn := func(t *testing.T, uris []string) {
		t.Helper()
		plan, err := BuildOutbounds("api", uris, BuildOptions{TestURL: "http://example.com"})
		if err != nil {
			t.Fatalf("BuildOutbounds: %v", err)
		}
		warnTopologyShift("api", uris, plan)
	}

	t.Run("warns on single vless plus a new scheme", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{vless1, hy2Line})
		log := buf.String()
		if !strings.Contains(log, topologyWarning) {
			t.Fatalf("expected a topology-shift warning, log:\n%s", log)
		}
		if !strings.Contains(log, "hysteria2") {
			t.Errorf("warning does not name the newly accepted scheme, log:\n%s", log)
		}
		// Phase 2 decodes concurrently, so an unattributed warning cannot be
		// traced back to a subscription.
		if !strings.Contains(log, "subscription api") {
			t.Errorf("warning does not name the subscription, log:\n%s", log)
		}
		if !strings.Contains(log, "api-single") || !strings.Contains(log, "api-manual") {
			t.Errorf("warning does not name the concrete tags, log:\n%s", log)
		}
	})

	t.Run("silent when already multi-node", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{vless1, vless2, hy2Line})
		if strings.Contains(buf.String(), topologyWarning) {
			t.Errorf("unexpected warning for an already multi-node subscription, log:\n%s", buf.String())
		}
	})

	t.Run("silent when still single-node", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{hy2Line})
		if strings.Contains(buf.String(), topologyWarning) {
			t.Errorf("unexpected warning for a single-node subscription, log:\n%s", buf.String())
		}
	})

	// No vless line means the payload failed to decode entirely before, so there
	// is no saved selector choice to invalidate.
	t.Run("silent when no vless node is present", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{hy2Line, hy2ShortLine})
		if strings.Contains(buf.String(), topologyWarning) {
			t.Errorf("unexpected warning for a vless-free subscription, log:\n%s", buf.String())
		}
	})

	// A new-scheme line the parser rejects is dropped by BuildOutbounds, so the
	// subscription keeps its <id>-single tag: telling the operator to re-pick a
	// node would be wrong, and it would repeat on every cron run.
	t.Run("silent when the new-scheme node does not parse", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{vless1, hy2Broken})
		if strings.Contains(buf.String(), topologyWarning) {
			t.Errorf("unexpected warning for a subscription that stayed single-node, log:\n%s", buf.String())
		}
	})

	// The node count comes from the plan, not the URI list: duplicates are
	// deduplicated away before the tags exist.
	t.Run("counts nodes from the plan", func(t *testing.T) {
		buf := captureLog(t)
		warn(t, []string{vless1, hy2Line, hy2Line})
		log := buf.String()
		if !strings.Contains(log, topologyWarning) {
			t.Fatalf("expected a topology-shift warning, log:\n%s", log)
		}
		if !strings.Contains(log, "yields 2 node(s)") {
			t.Errorf("warning does not count the deduplicated nodes, log:\n%s", log)
		}
	})
}

func TestNormalizeLineEndings(t *testing.T) {
	input := []byte("line1\r\nline2\r\nline3\n")
	want := []byte("line1\nline2\nline3\n")
	got := normalizeLineEndings(input)
	if !bytes.Equal(got, want) {
		t.Errorf("normalizeLineEndings(%q) = %q, want %q", input, got, want)
	}
}

func TestNormalizeLineEndings_no_crlf(t *testing.T) {
	input := []byte("line1\nline2\nline3\n")
	got := normalizeLineEndings(input)
	if !bytes.Equal(got, input) {
		t.Errorf("normalizeLineEndings modified input without \\r\\n: %q", got)
	}
}

func TestFormatString(t *testing.T) {
	cases := []struct {
		format Format
		want   string
	}{
		{FormatRaw, "raw"},
		{FormatGzip, "gzip"},
		{FormatBase64, "base64"},
		{FormatBase64URL, "base64url"},
		{FormatGzipBase64, "gzip+base64"},
		{FormatGzipBase64URL, "gzip+base64url"},
		{FormatJSON, "json"},
		{FormatUnknown, "unknown"},
	}
	for _, tc := range cases {
		if got := tc.format.String(); got != tc.want {
			t.Errorf("Format(%d).String() = %q, want %q", tc.format, got, tc.want)
		}
	}
}
