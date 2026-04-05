package system

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func writeFile(t *testing.T, path string, content []byte) {
	t.Helper()
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write test file: %v", err)
	}
}

// fakeRunner records commands and returns preset results.
type fakeRunner struct {
	calls    [][]string
	runFunc  func(name string, args ...string) ([]byte, error)
	lookFunc func(name string) (string, error)
}

func (f *fakeRunner) Run(name string, args ...string) ([]byte, error) {
	f.calls = append(f.calls, append([]string{name}, args...))
	if f.runFunc != nil {
		return f.runFunc(name, args...)
	}
	return nil, nil
}

func (f *fakeRunner) LookPath(name string) (string, error) {
	if f.lookFunc != nil {
		return f.lookFunc(name)
	}
	return "", fmt.Errorf("not found")
}

func TestApplyDomains_success(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "domains.lst")
	dnsmasqDir := filepath.Join(dir, "dnsmasq.d")

	writeFile(t, cacheFile, []byte("ipset=/example.com/vpn\n"))

	cmd := &fakeRunner{
		runFunc: func(name string, _ ...string) ([]byte, error) {
			if name == "dnsmasq" {
				return []byte("dnsmasq: syntax check OK."), nil
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd}
	if err := o.ApplyDomains(cacheFile, dnsmasqDir); err != nil {
		t.Fatalf("ApplyDomains: %v", err)
	}

	// Check file was copied
	dest := filepath.Join(dnsmasqDir, "domains.lst")
	data, err := os.ReadFile(dest)
	if err != nil {
		t.Fatalf("read dest: %v", err)
	}
	if string(data) != "ipset=/example.com/vpn\n" {
		t.Errorf("dest content = %q", string(data))
	}

	// Check dnsmasq was restarted
	if len(cmd.calls) != 2 {
		t.Fatalf("calls = %d, want 2", len(cmd.calls))
	}
	if cmd.calls[1][0] != "/etc/init.d/dnsmasq" {
		t.Errorf("second call = %v, want dnsmasq restart", cmd.calls[1])
	}
}

func TestApplyDomains_syntax_fail(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "domains.lst")
	writeFile(t, cacheFile, []byte("bad config\n"))

	cmd := &fakeRunner{
		runFunc: func(_ string, _ ...string) ([]byte, error) {
			return []byte("dnsmasq: error at line 1"), fmt.Errorf("exit 1")
		},
	}

	o := &OpenWrt{Cmd: cmd}
	err := o.ApplyDomains(cacheFile, filepath.Join(dir, "dnsmasq.d"))
	if err == nil {
		t.Fatal("expected error on syntax check failure")
	}
}

func TestApplyIPs_fw4(t *testing.T) {
	cmd := &fakeRunner{
		lookFunc: func(name string) (string, error) {
			if name == "fw4" {
				return "/usr/sbin/fw4", nil
			}
			return "", fmt.Errorf("not found")
		},
	}

	o := &OpenWrt{Cmd: cmd}
	if err := o.ApplyIPs("/tmp/vpn-ip-list.lst"); err != nil {
		t.Fatalf("ApplyIPs: %v", err)
	}

	if len(cmd.calls) != 1 || cmd.calls[0][0] != "fw4" {
		t.Errorf("calls = %v, want fw4 reload", cmd.calls)
	}
}

func TestApplyIPs_fallback_init(t *testing.T) {
	cmd := &fakeRunner{
		lookFunc: func(_ string) (string, error) {
			return "", fmt.Errorf("not found")
		},
	}

	o := &OpenWrt{Cmd: cmd}
	if err := o.ApplyIPs("/tmp/vpn-ip-list.lst"); err != nil {
		t.Fatalf("ApplyIPs: %v", err)
	}

	if len(cmd.calls) != 1 || cmd.calls[0][0] != "/etc/init.d/firewall" {
		t.Errorf("calls = %v, want firewall init reload", cmd.calls)
	}
}
