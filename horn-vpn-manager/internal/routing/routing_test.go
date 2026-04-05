package routing

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
)

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

func TestParseLines(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  []string
	}{
		{"empty", "", nil},
		{"comments and blanks", "# comment\n\n  \n# another\n", nil},
		{"mixed", "10.0.0.0/8\n# comment\n192.168.0.0/16\n\n172.16.0.0/12\n", []string{"10.0.0.0/8", "192.168.0.0/16", "172.16.0.0/12"}},
		{"trailing whitespace", "  10.0.0.0/8  \n  192.168.0.0/16\t\n", []string{"10.0.0.0/8", "192.168.0.0/16"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ParseLines([]byte(tt.input))
			if len(got) != len(tt.want) {
				t.Fatalf("len = %d, want %d: %v", len(got), len(tt.want), got)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Errorf("[%d] = %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

func TestDedup(t *testing.T) {
	input := []string{"b", "a", "c", "a", "b", "d"}
	got := Dedup(input)
	want := []string{"a", "b", "c", "d"}

	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

func TestDedup_empty(t *testing.T) {
	got := Dedup(nil)
	if got != nil {
		t.Errorf("expected nil, got %v", got)
	}
}

// fakeApplier records calls for testing.
type fakeApplier struct {
	domainsCalls []string
	ipsCalls     []string
}

func (f *fakeApplier) ApplyDomains(cacheFile, dnsmasqDir string) error {
	f.domainsCalls = append(f.domainsCalls, cacheFile)
	return nil
}

func (f *fakeApplier) ApplyIPs(ipListFile string) error {
	f.ipsCalls = append(f.ipsCalls, ipListFile)
	return nil
}

func TestRunner_Run(t *testing.T) {
	// Set up HTTP servers
	domainSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "ipset=/example.com/vpn\nipset=/test.org/vpn\n")
	}))
	defer domainSrv.Close()

	subnetSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprint(w, "10.0.0.0/8\n192.168.0.0/16\n")
	}))
	defer subnetSrv.Close()

	listsDir := t.TempDir()
	manualFile := filepath.Join(listsDir, "manual-ip.lst")
	writeFile(t, manualFile, []byte("172.16.0.0/12\n10.0.0.0/8\n"))

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 2, TimeoutSeconds: 5, Parallelism: 2},
		Routing: config.Routing{
			Domains: config.Domains{URL: domainSrv.URL},
			Subnets: config.Subnets{
				URLs:       []string{subnetSrv.URL},
				ManualFile: manualFile,
			},
		},
	}

	applier := &fakeApplier{}
	runner := &Runner{Cfg: cfg, Applier: applier, ListsDir: listsDir}

	if err := runner.Run(context.Background()); err != nil {
		t.Fatalf("Run failed: %v", err)
	}

	// Check domain cache was written
	domData, err := os.ReadFile(filepath.Join(listsDir, DomainsCacheFile))
	if err != nil {
		t.Fatalf("read domains cache: %v", err)
	}
	if !strings.Contains(string(domData), "example.com") {
		t.Error("domains cache missing expected content")
	}

	// Check subnet cache was written (deduplicated)
	subData, err := os.ReadFile(filepath.Join(listsDir, SubnetsCacheFile))
	if err != nil {
		t.Fatalf("read subnets cache: %v", err)
	}
	if !strings.Contains(string(subData), "10.0.0.0/8") {
		t.Error("subnets cache missing expected content")
	}

	// Check vpn-ip-list was written (merged + deduped)
	ipData, err := os.ReadFile(filepath.Join(listsDir, VPNIPListFile))
	if err != nil {
		t.Fatalf("read vpn-ip-list: %v", err)
	}
	ipLines := ParseLines(ipData)
	if len(ipLines) != 3 { // 10.0.0.0/8, 172.16.0.0/12, 192.168.0.0/16
		t.Errorf("vpn-ip-list lines = %d, want 3: %v", len(ipLines), ipLines)
	}

	// Applier should have been called
	if len(applier.domainsCalls) != 1 {
		t.Errorf("ApplyDomains calls = %d, want 1", len(applier.domainsCalls))
	}
	if len(applier.ipsCalls) != 1 {
		t.Errorf("ApplyIPs calls = %d, want 1", len(applier.ipsCalls))
	}
}

func TestRunner_Restore(t *testing.T) {
	listsDir := t.TempDir()

	// Pre-populate caches
	writeFile(t, filepath.Join(listsDir, DomainsCacheFile), []byte("ipset=/cached.com/vpn\n"))
	writeFile(t, filepath.Join(listsDir, SubnetsCacheFile), []byte("10.0.0.0/8\n"))

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Routing: config.Routing{
			Domains: config.Domains{URL: "https://unused.example.com"},
			Subnets: config.Subnets{
				URLs:       []string{"https://unused.example.com"},
				ManualFile: "/nonexistent/manual.lst",
			},
		},
	}

	applier := &fakeApplier{}
	runner := &Runner{Cfg: cfg, Applier: applier, ListsDir: listsDir}

	if err := runner.Restore(); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Applier should have been called for both
	if len(applier.domainsCalls) != 1 {
		t.Errorf("ApplyDomains calls = %d, want 1", len(applier.domainsCalls))
	}
	if len(applier.ipsCalls) != 1 {
		t.Errorf("ApplyIPs calls = %d, want 1", len(applier.ipsCalls))
	}

	// vpn-ip-list should exist
	ipData, err := os.ReadFile(filepath.Join(listsDir, VPNIPListFile))
	if err != nil {
		t.Fatalf("read vpn-ip-list: %v", err)
	}
	if !strings.Contains(string(ipData), "10.0.0.0/8") {
		t.Error("vpn-ip-list missing cached subnet")
	}
}

func TestRunner_Restore_no_cache(t *testing.T) {
	listsDir := t.TempDir()

	cfg := &config.Config{
		Fetch: config.Fetch{Retries: 1, TimeoutSeconds: 5, Parallelism: 1},
		Routing: config.Routing{
			Domains: config.Domains{URL: "https://unused.example.com"},
			Subnets: config.Subnets{ManualFile: "/nonexistent/manual.lst"},
		},
	}

	applier := &fakeApplier{}
	runner := &Runner{Cfg: cfg, Applier: applier, ListsDir: listsDir}

	if err := runner.Restore(); err != nil {
		t.Fatalf("Restore failed: %v", err)
	}

	// Nothing to restore, so applier should not be called
	if len(applier.domainsCalls) != 0 {
		t.Errorf("ApplyDomains calls = %d, want 0", len(applier.domainsCalls))
	}
	if len(applier.ipsCalls) != 0 {
		t.Errorf("ApplyIPs calls = %d, want 0", len(applier.ipsCalls))
	}
}

func TestBuildIPList_merges_and_deduplicates(t *testing.T) {
	listsDir := t.TempDir()

	writeFile(t, filepath.Join(listsDir, SubnetsCacheFile), []byte("10.0.0.0/8\n192.168.0.0/16\n"))
	manualFile := filepath.Join(listsDir, "manual.lst")
	writeFile(t, manualFile, []byte("# comment\n192.168.0.0/16\n172.16.0.0/12\n\n"))

	cfg := &config.Config{
		Routing: config.Routing{
			Subnets: config.Subnets{ManualFile: manualFile},
		},
	}

	runner := &Runner{Cfg: cfg, ListsDir: listsDir}
	got, err := runner.BuildIPList()
	if err != nil {
		t.Fatalf("BuildIPList: %v", err)
	}

	want := []string{"10.0.0.0/8", "172.16.0.0/12", "192.168.0.0/16"}
	if len(got) != len(want) {
		t.Fatalf("len = %d, want %d: %v", len(got), len(want), got)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
