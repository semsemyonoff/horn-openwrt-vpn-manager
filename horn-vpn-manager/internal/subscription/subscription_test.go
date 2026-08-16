package subscription

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"slices"
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
	if content == "" {
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
			"disabled": {Name: "Disabled", URL: srv.URL, Enabled: new(bool)},
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

func TestRunner_Run_include_filtering(t *testing.T) {
	payload := "vless://uuid1@h1.example.com:443?encryption=none#Germany-Frankfurt\n" +
		"vless://uuid2@h2.example.com:443?encryption=none#Russia-Moscow\n" +
		"vless://uuid3@h3.example.com:443?encryption=none#Germany-Berlin\n"
	srv := newTestServer(t, payload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     srv.URL,
				Default: true,
				Include: []string{"Germany"},
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

	if !strings.Contains(content, "Germany-Frankfurt") {
		t.Error("expected Germany-Frankfurt node to be present after include filtering")
	}
	if !strings.Contains(content, "Germany-Berlin") {
		t.Error("expected Germany-Berlin node to be present after include filtering")
	}
	if strings.Contains(content, "Russia") {
		t.Error("expected Russia node to be excluded by include filter")
	}
}

func TestRunner_Run_include_then_exclude_filtering(t *testing.T) {
	payload := "vless://uuid1@h1.example.com:443?encryption=none#Germany-Frankfurt\n" +
		"vless://uuid2@h2.example.com:443?encryption=none#Russia-Moscow\n" +
		"vless://uuid3@h3.example.com:443?encryption=none#Germany-relay\n"
	srv := newTestServer(t, payload, http.StatusOK)
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {
				Name:    "Main",
				URL:     srv.URL,
				Default: true,
				Include: []string{"Germany"},
				Exclude: []string{"relay"},
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

	if !strings.Contains(content, "Germany-Frankfurt") {
		t.Error("expected Germany-Frankfurt to survive include+exclude")
	}
	if strings.Contains(content, "relay") {
		t.Error("expected relay node to be excluded after include then exclude")
	}
	if strings.Contains(content, "Russia") {
		t.Error("expected Russia to be dropped by include filter")
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
	var cfg2 map[string]any
	if err := json.Unmarshal(data, &cfg2); err != nil {
		t.Fatalf("config.json is not valid JSON: %v", err)
	}

	// Should contain the outbound tag
	outbounds, _ := cfg2["outbounds"].([]any)
	found := false
	for _, ob := range outbounds {
		if m, ok := ob.(map[string]any); ok {
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

// TestRunner_Run_connect_timeout_applied covers both BuildOutbounds call sites:
// the default subscription (phase 1) and a non-default one (processSub).
func TestRunner_Run_connect_timeout_applied(t *testing.T) {
	cases := []struct {
		name           string
		connectTimeout string
		want           any // nil means the field must be absent
	}{
		{name: "configured", connectTimeout: "7s", want: "7s"},
		{name: "unset", connectTimeout: "", want: nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mainSrv := newTestServer(t, rawPayload, http.StatusOK)
			defer mainSrv.Close()
			extraSrv := newTestServer(t, "vless://uuid3@h3.example.com:443?encryption=none#Node+3\n", http.StatusOK)
			defer extraSrv.Close()

			cfg := &config.Config{
				Fetch:   config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
				Singbox: config.Singbox{ConnectTimeout: tc.connectTimeout},
				Subscriptions: map[string]*config.Subscription{
					"main":  {Name: "Main", URL: mainSrv.URL, Default: true},
					"extra": {Name: "Extra", URL: extraSrv.URL},
				},
			}

			outDir := t.TempDir()
			runner := NewRunner(cfg, &fakeApplier{})
			runner.OutDir = outDir
			runner.DryRun = true

			if err := runner.Run(context.Background()); err != nil {
				t.Fatalf("Run() error: %v", err)
			}

			data, err := os.ReadFile(filepath.Join(outDir, "config.json"))
			if err != nil {
				t.Fatalf("config.json not written: %v", err)
			}
			var generated struct {
				Outbounds []map[string]any `json:"outbounds"`
			}
			if err := json.Unmarshal(data, &generated); err != nil {
				t.Fatalf("config.json is not valid JSON: %v", err)
			}

			var vlessCount int
			for _, ob := range generated.Outbounds {
				tag, _ := ob["tag"].(string)
				if ob["type"] != "vless" {
					if _, ok := ob["connect_timeout"]; ok {
						t.Errorf("non-vless outbound %q carries connect_timeout", tag)
					}
					continue
				}
				vlessCount++
				got, ok := ob["connect_timeout"]
				if tc.want == nil {
					if ok {
						t.Errorf("outbound %q: connect_timeout = %v, want absent", tag, got)
					}
					continue
				}
				if !ok {
					t.Errorf("outbound %q: connect_timeout missing", tag)
				} else if got != tc.want {
					t.Errorf("outbound %q: connect_timeout = %v, want %v", tag, got, tc.want)
				}
			}
			// 2 nodes from main + 1 from extra: both call sites must be covered.
			if vlessCount != 3 {
				t.Errorf("vless outbound count = %d, want 3", vlessCount)
			}
		})
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

func TestRunner_Run_shared_url_downloaded_once(t *testing.T) {
	var mu sync.Mutex
	var requests int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		requests++
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(rawPayload))
	}))
	defer srv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main":  {Name: "Main", URL: srv.URL, Default: true},
			"extra": {Name: "Extra", URL: srv.URL}, // same URL
		},
	}

	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = t.TempDir()
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	mu.Lock()
	got := requests
	mu.Unlock()
	if got != 1 {
		t.Errorf("expected 1 HTTP request for shared URL, got %d", got)
	}
}

// inline node URIs used by the inline-subscription tests.
const (
	inlineNodeA = "vless://uuid-a@a.example.com:443?encryption=none#Alpha"
	inlineNodeB = "vless://uuid-b@b.example.com:443?encryption=none#Beta"
	inlineNodeC = "vless://uuid-c@c.example.com:443?encryption=none#Gamma"
)

// TestRunner_Run_inline_nodes_distinct_per_subscription guards the urlCache[""]
// collision: an inline subscription must never inherit another one's nodes just
// because both have an empty url.
func TestRunner_Run_inline_nodes_distinct_per_subscription(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"personal": {Name: "Personal", Default: true, Nodes: []string{inlineNodeA}},
			"backup":   {Name: "Backup", Nodes: []string{inlineNodeB}},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	for _, tc := range []struct{ id, want, notWant string }{
		{id: "personal", want: "uuid-a", notWant: "uuid-b"},
		{id: "backup", want: "uuid-b", notWant: "uuid-a"},
	} {
		data, err := os.ReadFile(filepath.Join(outDir, tc.id+"-nodes.txt"))
		if err != nil {
			t.Fatalf("nodes file for %s not written: %v", tc.id, err)
		}
		content := string(data)
		if !strings.Contains(content, tc.want) {
			t.Errorf("%s nodes file missing %q, got %q", tc.id, tc.want, content)
		}
		if strings.Contains(content, tc.notWant) {
			t.Errorf("%s nodes file leaked %q from another subscription, got %q", tc.id, tc.notWant, content)
		}
	}

	// Both subscriptions are single-node, so each yields its own -single outbound.
	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	tags := collectOutboundTags(generated)
	for _, tag := range []string{"personal-single", "backup-single"} {
		if !tags[tag] {
			t.Errorf("expected outbound %q, got tags: %v", tag, tags)
		}
	}
}

// TestRunner_Run_inline_nodes_filtering verifies include/exclude apply to inline
// nodes exactly as they do to downloaded ones, at both call sites.
func TestRunner_Run_inline_nodes_filtering(t *testing.T) {
	all := []string{inlineNodeA, inlineNodeB, inlineNodeC}
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main":  {Name: "Main", Default: true, Nodes: all, Include: []string{"alpha", "beta"}},
			"extra": {Name: "Extra", Nodes: all, Exclude: []string{"gamma"}},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	for _, id := range []string{"main", "extra"} {
		data, err := os.ReadFile(filepath.Join(outDir, id+"-nodes.txt"))
		if err != nil {
			t.Fatalf("nodes file for %s not written: %v", id, err)
		}
		content := string(data)
		for _, want := range []string{"uuid-a", "uuid-b"} {
			if !strings.Contains(content, want) {
				t.Errorf("%s: filtered nodes missing %q, got %q", id, want, content)
			}
		}
		if strings.Contains(content, "uuid-c") {
			t.Errorf("%s: filter did not drop Gamma, got %q", id, content)
		}
	}
}

// TestRunner_Run_inline_nodes_skipped_when_disabled_sibling_has_no_source verifies a
// disabled sourceless subscription is skipped while inline ones still render.
func TestRunner_Run_inline_nodes_skipped_when_disabled_sibling_has_no_source(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main":       {Name: "Main", Default: true, Nodes: []string{inlineNodeA}},
			"sourceless": {Name: "Sourceless", Enabled: new(bool)},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	tags := collectOutboundTags(readConfig(t, filepath.Join(outDir, "config.json")))
	if !tags["main-single"] {
		t.Errorf("expected main-single outbound, got tags: %v", tags)
	}
}

// outboundByTag returns the generated outbound with the given tag, or nil.
func outboundByTag(cfg map[string]any, tag string) map[string]any {
	outbounds, _ := cfg["outbounds"].([]any)
	for _, ob := range outbounds {
		m, ok := ob.(map[string]any)
		if !ok {
			continue
		}
		if t, _ := m["tag"].(string); t == tag {
			return m
		}
	}
	return nil
}

// routeFinal returns route.final from a parsed sing-box config.
func routeFinal(t *testing.T, cfg map[string]any) string {
	t.Helper()
	route, ok := cfg["route"].(map[string]any)
	if !ok {
		t.Fatalf("config has no route object: %v", cfg["route"])
	}
	final, _ := route["final"].(string)
	return final
}

// outboundList returns the "outbounds" member list of a group outbound.
func outboundList(t *testing.T, group map[string]any) []string {
	t.Helper()
	raw, _ := group["outbounds"].([]any)
	list := make([]string, 0, len(raw))
	for _, v := range raw {
		s, _ := v.(string)
		list = append(list, s)
	}
	return list
}

// TestRunner_Run_fallback_on_default covers the headline case: a single-node
// default subscription chained to a multi-node backup. route.final must point at
// the generated group, whose members resolve to each subscription's own final
// tag in declared order, and the group tag must reach subs-tags.json so LuCI can
// name it.
func TestRunner_Run_fallback_on_default(t *testing.T) {
	backupSrv := newTestServer(t, rawPayload, http.StatusOK)
	defer backupSrv.Close()
	spareSrv := newTestServer(t, inlineNodeC+"\n", http.StatusOK)
	defer spareSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"primary": {
				Name:    "Primary",
				Default: true,
				Nodes:   []string{inlineNodeA},
				Fallback: &config.Fallback{
					Subscriptions:    []string{"backup", "spare"},
					BlacklistTimeout: "1m",
				},
			},
			"backup": {Name: "Backup", URL: backupSrv.URL},
			"spare":  {Name: "Spare", URL: spareSrv.URL},
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

	generated := readConfig(t, filepath.Join(outDir, "config.json"))

	group := outboundByTag(generated, "primary-fallback")
	if group == nil {
		t.Fatalf("primary-fallback outbound missing, tags: %v", collectOutboundTags(generated))
	}
	if group["type"] != "fallback" {
		t.Errorf("group type = %v, want fallback", group["type"])
	}
	if group["blacklist_timeout"] != "1m" {
		t.Errorf("blacklist_timeout = %v, want 1m", group["blacklist_timeout"])
	}
	if group["interrupt_exist_connections"] != true {
		t.Errorf("interrupt_exist_connections = %v, want true", group["interrupt_exist_connections"])
	}
	// Single-node primary → -single, multi-node backup → -manual, order as declared.
	want := []string{"primary-single", "backup-manual", "spare-single"}
	if got := outboundList(t, group); !slices.Equal(got, want) {
		t.Errorf("group outbounds = %v, want %v", got, want)
	}

	if final := routeFinal(t, generated); final != "primary-fallback" {
		t.Errorf("route.final = %q, want primary-fallback", final)
	}

	var tags map[string]string
	data, err := os.ReadFile(filepath.Join(configDir, "subs-tags.json"))
	if err != nil {
		t.Fatalf("subs-tags.json not written: %v", err)
	}
	if err := json.Unmarshal(data, &tags); err != nil {
		t.Fatalf("subs-tags.json is not valid JSON: %v", err)
	}
	if tags["primary-fallback"] == "" {
		t.Errorf("no name registered for primary-fallback, got: %v", tags)
	}
}

// TestRunner_Run_fallback_on_non_default verifies a chain on a non-default
// subscription retargets that subscription's own route rules and leaves
// route.final alone.
func TestRunner_Run_fallback_on_non_default(t *testing.T) {
	mainSrv := newTestServer(t, rawPayload, http.StatusOK)
	defer mainSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: mainSrv.URL, Default: true},
			"corp": {
				Name:  "Corp",
				Nodes: []string{inlineNodeA},
				Route: &config.SubscriptionRoute{
					Domains: []string{"corp.example.com"},
					IPCIDRs: []string{"10.0.0.0/8"},
				},
				Fallback: &config.Fallback{Subscriptions: []string{"spare"}},
			},
			"spare": {Name: "Spare", Nodes: []string{inlineNodeB}},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))

	group := outboundByTag(generated, "corp-fallback")
	if group == nil {
		t.Fatalf("corp-fallback outbound missing, tags: %v", collectOutboundTags(generated))
	}
	if want := []string{"corp-single", "spare-single"}; !slices.Equal(outboundList(t, group), want) {
		t.Errorf("group outbounds = %v, want %v", outboundList(t, group), want)
	}
	if group["blacklist_timeout"] != nil {
		t.Errorf("blacklist_timeout = %v, want absent when unconfigured", group["blacklist_timeout"])
	}

	// The default subscription is untouched by another subscription's chain.
	if final := routeFinal(t, generated); final != "main-manual" {
		t.Errorf("route.final = %q, want main-manual", final)
	}

	// Both of corp's rules must now reach the group, not corp-single.
	route, _ := generated["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	var retargeted int
	for _, r := range rules {
		m, ok := r.(map[string]any)
		if !ok {
			continue
		}
		outbound, _ := m["outbound"].(string)
		if outbound == "corp-single" {
			t.Errorf("rule still points at corp-single: %v", m)
		}
		if outbound == "corp-fallback" {
			retargeted++
		}
	}
	if retargeted != 2 {
		t.Errorf("rules pointing at corp-fallback = %d, want 2 (domain + ip_cidr)", retargeted)
	}
}

// TestRunner_Run_fallback_nested verifies a backup that declares a chain of its
// own contributes its fallback group tag, not its bare final tag.
func TestRunner_Run_fallback_nested(t *testing.T) {
	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"a": {
				Name:     "A",
				Default:  true,
				Nodes:    []string{inlineNodeA},
				Fallback: &config.Fallback{Subscriptions: []string{"b"}},
			},
			"b": {
				Name:     "B",
				Nodes:    []string{inlineNodeB},
				Fallback: &config.Fallback{Subscriptions: []string{"c"}},
			},
			"c": {Name: "C", Nodes: []string{inlineNodeC}},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))

	outer := outboundByTag(generated, "a-fallback")
	inner := outboundByTag(generated, "b-fallback")
	if outer == nil || inner == nil {
		t.Fatalf("expected a-fallback and b-fallback, tags: %v", collectOutboundTags(generated))
	}
	if want := []string{"a-single", "b-fallback"}; !slices.Equal(outboundList(t, outer), want) {
		t.Errorf("a-fallback outbounds = %v, want %v", outboundList(t, outer), want)
	}
	if want := []string{"b-single", "c-single"}; !slices.Equal(outboundList(t, inner), want) {
		t.Errorf("b-fallback outbounds = %v, want %v", outboundList(t, inner), want)
	}
	if final := routeFinal(t, generated); final != "a-fallback" {
		t.Errorf("route.final = %q, want a-fallback", final)
	}
}

// TestRunner_Run_fallback_degraded verifies a backup that produced no plan is
// dropped from the chain rather than aborting the run or leaving a dangling tag.
func TestRunner_Run_fallback_degraded(t *testing.T) {
	deadSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer deadSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"primary": {
				Name:     "Primary",
				Default:  true,
				Nodes:    []string{inlineNodeA},
				Fallback: &config.Fallback{Subscriptions: []string{"dead", "alive"}},
			},
			"dead":  {Name: "Dead", URL: deadSrv.URL},
			"alive": {Name: "Alive", Nodes: []string{inlineNodeB}},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	group := outboundByTag(generated, "primary-fallback")
	if group == nil {
		t.Fatalf("primary-fallback outbound missing, tags: %v", collectOutboundTags(generated))
	}
	if want := []string{"primary-single", "alive-single"}; !slices.Equal(outboundList(t, group), want) {
		t.Errorf("group outbounds = %v, want %v", outboundList(t, group), want)
	}
	if final := routeFinal(t, generated); final != "primary-fallback" {
		t.Errorf("route.final = %q, want primary-fallback", final)
	}
}

// TestRunner_Run_fallback_all_backups_failed verifies the degenerate case: no
// group is emitted and the subscription keeps the tag it already had.
func TestRunner_Run_fallback_all_backups_failed(t *testing.T) {
	deadSrv := newTestServer(t, "", http.StatusInternalServerError)
	defer deadSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"primary": {
				Name:     "Primary",
				Default:  true,
				Nodes:    []string{inlineNodeA},
				Fallback: &config.Fallback{Subscriptions: []string{"dead"}},
			},
			"dead": {Name: "Dead", URL: deadSrv.URL},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	if group := outboundByTag(generated, "primary-fallback"); group != nil {
		t.Errorf("expected no fallback group when every backup failed, got %v", group)
	}
	if final := routeFinal(t, generated); final != "primary-single" {
		t.Errorf("route.final = %q, want primary-single", final)
	}
}

// TestRunner_Run_no_fallback_unchanged pins the backward-compatible shape: a
// config declaring no chain generates no fallback group and keeps route.final on
// the default subscription's own tag.
func TestRunner_Run_no_fallback_unchanged(t *testing.T) {
	mainSrv := newTestServer(t, rawPayload, http.StatusOK)
	defer mainSrv.Close()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Subscriptions: map[string]*config.Subscription{
			"main": {Name: "Main", URL: mainSrv.URL, Default: true},
			"corp": {
				Name:  "Corp",
				Nodes: []string{inlineNodeA},
				Route: &config.SubscriptionRoute{Domains: []string{"corp.example.com"}},
			},
		},
	}

	outDir := t.TempDir()
	runner := NewRunner(cfg, &fakeApplier{})
	runner.OutDir = outDir
	runner.DryRun = true

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run() error: %v", err)
	}

	generated := readConfig(t, filepath.Join(outDir, "config.json"))
	for tag := range collectOutboundTags(generated) {
		if strings.HasSuffix(tag, "-fallback") {
			t.Errorf("unexpected fallback outbound %q", tag)
		}
	}
	if final := routeFinal(t, generated); final != "main-manual" {
		t.Errorf("route.final = %q, want main-manual", final)
	}

	route, _ := generated["route"].(map[string]any)
	rules, _ := route["rules"].([]any)
	var corpRules int
	for _, r := range rules {
		if m, ok := r.(map[string]any); ok {
			if outbound, _ := m["outbound"].(string); outbound == "corp-single" {
				corpRules++
			}
		}
	}
	if corpRules != 1 {
		t.Errorf("rules pointing at corp-single = %d, want 1", corpRules)
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

func TestFilterInclude(t *testing.T) {
	uris := []string{
		"vless://id1@h1.example.com:443#Germany-Frankfurt",
		"vless://id2@h2.example.com:443#Russia-Moscow",
		"vless://id3@h3.example.com:443#germany-berlin",
		"vless://id4@h4.example.com:443#Japan",
	}

	got := filterInclude(uris, []string{"germany"})
	if len(got) != 2 {
		t.Fatalf("expected 2 uris after include filter, got %d: %v", len(got), got)
	}
	for _, uri := range got {
		name := strings.ToLower(extractNodeName(uri))
		if !strings.Contains(name, "germany") {
			t.Errorf("included URI does not match pattern: %s", uri)
		}
	}
}

func TestFilterInclude_empty_patterns(t *testing.T) {
	uris := []string{
		"vless://id1@h1.example.com:443#Node1",
		"vless://id2@h2.example.com:443#Node2",
	}
	got := filterInclude(uris, nil)
	if len(got) != 2 {
		t.Fatalf("expected all uris when no patterns, got %d", len(got))
	}
}

func TestFilterInclude_multiple_patterns(t *testing.T) {
	uris := []string{
		"vless://id1@h1.example.com:443#Germany",
		"vless://id2@h2.example.com:443#Japan",
		"vless://id3@h3.example.com:443#Russia",
	}
	got := filterInclude(uris, []string{"germany", "japan"})
	if len(got) != 2 {
		t.Fatalf("expected 2 uris for two patterns, got %d: %v", len(got), got)
	}
}

func TestFilterExclude(t *testing.T) {
	uris := []string{
		"vless://id1@h1.example.com:443#Russia",
		"vless://id2@h2.example.com:443#germany",
		"vless://id3@h3.example.com:443#Traffic-Relay",
		"vless://id4@h4.example.com:443#Japan",
	}

	got, _ := filterExclude(uris, []string{"russia", "traffic"})
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
