package subscription

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
)

// fakeApplier records calls without executing system commands.
type fakeApplier struct {
	applySingboxCalls []string
}

func (f *fakeApplier) ApplySingbox(configPath string) error {
	f.applySingboxCalls = append(f.applySingboxCalls, configPath)
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
			"disabled": {
				Name:    "Disabled",
				URL:     srv.URL,
				Enabled: boolPtr(false),
			},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	// Disabled subscription should produce no nodes file
	files, _ := filepath.Glob(filepath.Join(runner.OutDir, "*.txt"))
	if len(files) != 0 {
		t.Errorf("expected no output files for disabled subscription, got: %v", files)
	}
}

func TestRunner_Run_download_failure_continues(t *testing.T) {
	// Subscription pointing to a server that returns 500
	badSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer badSrv.Close()

	goodSrv := newTestServer(t, rawPayload, http.StatusOK)
	defer goodSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"bad":  {Name: "Bad", URL: badSrv.URL},
			"good": {Name: "Good", URL: goodSrv.URL},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	// Should not return an error even though one subscription fails
	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() returned error: %v", err)
	}

	// Good subscription should produce a nodes file
	nodesFile := filepath.Join(runner.OutDir, "good-nodes.txt")
	if _, err := os.Stat(nodesFile); err != nil {
		t.Errorf("good subscription nodes file not created: %v", err)
	}
}

func TestRunner_Run_no_url_skipped(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"nourl": {Name: "No URL"},
		},
	}

	applier := &fakeApplier{}
	runner := NewRunner(cfg, applier)
	runner.OutDir = t.TempDir()

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}
}

func TestDebugApplier_ApplySingbox(t *testing.T) {
	a := NewDebugApplier()
	if err := a.ApplySingbox("/some/path/config.json"); err != nil {
		t.Errorf("DebugApplier.ApplySingbox() error: %v", err)
	}
}
