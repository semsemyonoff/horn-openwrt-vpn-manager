package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
)

// fakeApplier records calls without executing system commands.
// It performs the staging→final rename to match real applier behavior so tests
// that check for the final config.json continue to work.
type fakeApplier struct {
	applySingboxCalls []string
}

func (f *fakeApplier) ApplySingbox(stagingPath, finalPath string) error {
	if err := os.Rename(stagingPath, finalPath); err != nil {
		return err
	}
	f.applySingboxCalls = append(f.applySingboxCalls, finalPath)
	return nil
}

// rawPayload is a minimal multi-node raw subscription payload.
const rawPayload = "vless://uuid1@h1.example.com:443?encryption=none#Node+1\nvless://uuid2@h2.example.com:443?encryption=none#Node+2\n"

func newTestServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
}

func boolPtr(b bool) *bool { return &b }

func TestRunner_Run_raw_debug(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	outDir := t.TempDir()
	enabled := true
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"default": {
				Name:    "Default",
				URL:     srv.URL,
				Default: true,
				Enabled: &enabled,
			},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// In dry-run mode, nodes file should be written
	nodesFile := filepath.Join(outDir, "default-nodes.txt")
	data, err := os.ReadFile(nodesFile)
	if err != nil {
		t.Fatalf("nodes file not written: %v", err)
	}
	content := string(data)
	if len(content) == 0 {
		t.Error("nodes file is empty")
	}
	// Should contain both node URIs
	for _, expected := range []string{"uuid1", "uuid2"} {
		if !strings.Contains(content, expected) {
			t.Errorf("nodes file missing %q", expected)
		}
	}
}

func TestRunner_Run_disabled_subscription_skipped(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			// default enabled subscription (required for validation)
			"main": {Name: "Main", URL: srv.URL, Default: true},
			// non-default disabled subscription
			"disabled": {Name: "Disabled", URL: srv.URL, Enabled: boolPtr(false)},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Disabled subscription should produce no nodes file
	if _, err := os.Stat(filepath.Join(runner.OutDir, "disabled-nodes.txt")); err == nil {
		t.Error("expected no output file for disabled subscription")
	}
	// Main subscription should have been processed
	if _, err := os.Stat(filepath.Join(runner.OutDir, "main-nodes.txt")); err != nil {
		t.Errorf("expected nodes file for enabled subscription: %v", err)
	}
}

func TestRunner_Run_download_failure_continues(t *testing.T) {
	// Non-default subscription pointing to a server that returns 500
	badSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer badSrv.Close()

	goodSrv := newTestServer(t, rawPayload, http.StatusOK)
	defer goodSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"bad":  {Name: "Bad", URL: badSrv.URL},
			"good": {Name: "Good", URL: goodSrv.URL, Default: true},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	// Should not return an error even though one (non-default) subscription fails
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Good subscription should produce a nodes file
	nodesFile := filepath.Join(runner.OutDir, "good-nodes.txt")
	if _, err := os.Stat(nodesFile); err != nil {
		t.Errorf("good subscription nodes file not created: %v", err)
	}
}

func TestRunner_Run_no_url_returns_error(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"nourl": {Name: "No URL", Default: true},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()

	// A subscription with no URL cannot produce any output; Run must return an error.
	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("Run() succeeded unexpectedly; want error when no subscriptions can be processed")
	}
}

func TestDebugApplier_ApplySingbox(t *testing.T) {
	a := NewDebugApplier()
	if err := a.ApplySingbox("/some/path/config.json.new", "/some/path/config.json"); err != nil {
		t.Errorf("DebugApplier.ApplySingbox() error: %v", err)
	}
}

func TestRunner_Run_default_failure_aborts(t *testing.T) {
	badSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer badSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: badSrv.URL, Default: true},
		},
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()

	err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when default subscription fails to download")
	}
}

func TestRunner_Run_exclude_filtering(t *testing.T) {
	payload := "vless://uuid1@h1.example.com:443?encryption=none#Russia-Moscow\n" +
		"vless://uuid2@h2.example.com:443?encryption=none#Germany\n" +
		"vless://uuid3@h3.example.com:443?encryption=none#traffic-relay\n"
	srv := newTestServer(t, payload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     srv.URL,
				Default: true,
				Exclude: []string{"Russia", "traffic"},
			},
		},
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(runner.OutDir, "main-nodes.txt"))
	if err != nil {
		t.Fatalf("nodes file not written: %v", err)
	}
	content := string(data)

	if !strings.Contains(content, "Germany") {
		t.Error("expected Germany node to be present after filtering")
	}
	if strings.Contains(content, "Russia") {
		t.Error("expected Russia node to be excluded")
	}
	if strings.Contains(content, "traffic") {
		t.Error("expected traffic node to be excluded")
	}
}

func TestRunner_Run_per_subscription_retries(t *testing.T) {
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	// Per-subscription override: 1 attempt; global would allow 2 (with a sleep between them)
	one := 1
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 2, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true, Retries: &one},
		},
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()

	err := runner.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when default subscription fails")
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	// Per-subscription Retries=1 means exactly 1 HTTP attempt; global would have been 2
	if got != 1 {
		t.Errorf("expected 1 request with per-sub Retries=1, got %d (global would have been 2)", got)
	}
}

func TestRunner_Run_apply_called_when_not_dryrun(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = false

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(applier.applySingboxCalls) != 1 {
		t.Errorf("expected ApplySingbox called once, got %d calls", len(applier.applySingboxCalls))
	}
}

func TestRunner_Run_apply_not_called_when_dryrun(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	if len(applier.applySingboxCalls) != 0 {
		t.Errorf("expected ApplySingbox not called in dry-run, got %d calls", len(applier.applySingboxCalls))
	}
}

func TestRunner_Run_config_written_to_outdir(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Config file should be written
	configPath := filepath.Join(outDir, "config.json")
	data, err := os.ReadFile(configPath)
	if err != nil {
		t.Fatalf("config.json not written: %v", err)
	}

	// Should be valid JSON
	var cfg2 map[string]interface{}
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}

	// Should contain the outbound tag
	outbounds, _ := cfg2["outbounds"].([]interface{})
	found := false
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]interface{}); ok {
			if tag, _ := m["tag"].(string); strings.HasPrefix(tag, "main-") {
				found = true
				break
			}
		}
	}
	if !found {
		t.Errorf("expected a main-* outbound in config.json, outbounds: %v", outbounds)
	}
}

func TestRunner_Run_subs_tags_written(t *testing.T) {
	srv := newTestServer(t, rawPayload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: srv.URL, Default: true},
		},
	}

	outDir := t.TempDir()
	configDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.ConfigDir = configDir

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	tagsPath := filepath.Join(configDir, "subs-tags.json")
	data, err := os.ReadFile(tagsPath)
	if err != nil {
		t.Fatalf("subs-tags.json not written: %v", err)
	}

	var tags map[string]string
	if err := json.Unmarshal(data, &tags); err != nil {
		t.Fatalf("subs-tags.json is not valid JSON: %v", err)
	}

	if len(tags) == 0 {
		t.Error("subs-tags.json should have at least one tag entry")
	}
}

func TestRunner_Run_invalid_config_returns_error(t *testing.T) {
	// No subscriptions → ValidateSubscriptions should fail
	cfg := &config.Config{
		Fetch:         config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{},
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()

	if err := runner.Run(context.Background()); err == nil {
		t.Fatal("expected error for invalid subscription config")
	}
}

func TestFilterExclude(t *testing.T) {
	uris := []string{
		"vless://id1@h1.example.com:443#Russia",
		"vless://id2@h2.example.com:443#germany",
		"vless://id3@h3.example.com:443#Traffic-Relay",
		"vless://id4@h4.example.com:443#Japan",
	}

	got := filterExclude(uris, []string{"russia", "traffic"})
	if len(got) != 2 {
		t.Fatalf("expected 2 uris after filtering, got %d: %v", len(got), got)
	}
	for _, uri := range got {
		name := strings.ToLower(extractNodeName(uri))
		if strings.Contains(name, "russia") || strings.Contains(name, "traffic") {
			t.Errorf("filtered URIs still contain excluded node: %s", uri)
		}
	}
}

func TestExtractNodeName(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"vless://id@host:443?foo=bar#Hello+World", "Hello World"},
		{"vless://id@host:443?foo=bar#Hello%20World", "Hello World"},
		{"vless://id@host:443?foo=bar", ""},
		{"vless://id@host:443?foo=bar#", ""},
	}
	for _, tt := range tests {
		got := extractNodeName(tt.uri)
		if got != tt.want {
			t.Errorf("extractNodeName(%q) = %q, want %q", tt.uri, got, tt.want)
		}
	}
}
