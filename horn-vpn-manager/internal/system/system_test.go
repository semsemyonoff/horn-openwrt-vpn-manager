package system

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

func TestApplySingbox_success(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"log":{"level":"warn"}}`))

	cmd := &fakeRunner{
		runFunc: func(name string, _ ...string) ([]byte, error) {
			return nil, nil // sing-box check and sing-box restart both succeed
		},
	}

	o := &OpenWrt{Cmd: cmd}
	if err := o.ApplySingbox(stagingPath, finalPath); err != nil {
		t.Fatalf("ApplySingbox: %v", err)
	}

	// Should have called sing-box check then sing-box restart
	if len(cmd.calls) != 2 {
		t.Fatalf("expected 2 commands (check + restart), got %d: %v", len(cmd.calls), cmd.calls)
	}
	if cmd.calls[0][0] != "sing-box" || cmd.calls[0][1] != "check" {
		t.Errorf("first call = %v, want sing-box check", cmd.calls[0])
	}
	if cmd.calls[1][0] != "/etc/init.d/sing-box" || cmd.calls[1][1] != "restart" {
		t.Errorf("second call = %v, want /etc/init.d/sing-box restart", cmd.calls[1])
	}

	// Staging must be promoted to final path.
	if _, err := os.Stat(finalPath); err != nil {
		t.Errorf("final config not found after apply: %v", err)
	}
	if _, err := os.Stat(stagingPath); err == nil {
		t.Errorf("staging file should be gone after successful apply")
	}
}

func TestApplySingbox_check_failure(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{}`))

	cmd := &fakeRunner{
		runFunc: func(name string, _ ...string) ([]byte, error) {
			if name == "sing-box" {
				return []byte("invalid config"), fmt.Errorf("exit 1")
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd}
	err := o.ApplySingbox(stagingPath, finalPath)
	if err == nil {
		t.Fatal("expected error when sing-box check fails")
	}
	// Should not have called restart after check failure
	for _, call := range cmd.calls {
		if call[0] == "/etc/init.d/sing-box" {
			t.Errorf("sing-box restart should not be called after check failure")
		}
	}
	// Staging must be cleaned up; final must be untouched.
	if _, err := os.Stat(stagingPath); err == nil {
		t.Errorf("staging file should be removed after check failure")
	}
	if _, err := os.Stat(finalPath); err == nil {
		t.Errorf("final config should not exist after check failure with no prior config")
	}
}

// checkFailureError runs ApplySingbox against a sing-box check that fails with
// the given output and returns the resulting error.
func checkFailureError(t *testing.T, checkOutput string) error {
	t.Helper()
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{}`))

	cmd := &fakeRunner{
		runFunc: func(name string, _ ...string) ([]byte, error) {
			if name == "sing-box" {
				return []byte(checkOutput), fmt.Errorf("exit 1")
			}
			return nil, nil
		},
	}

	err := (&OpenWrt{Cmd: cmd}).ApplySingbox(stagingPath, finalPath)
	if err == nil {
		t.Fatal("expected error when sing-box check fails")
	}
	// The failure must stay hard: no promotion, no restart.
	if _, statErr := os.Stat(finalPath); statErr == nil {
		t.Errorf("final config should not exist after check failure")
	}
	for _, call := range cmd.calls {
		if call[0] == "/etc/init.d/sing-box" {
			t.Errorf("sing-box restart should not be called after check failure")
		}
	}
	return err
}

func TestApplySingbox_check_failure_fallback_hint(t *testing.T) {
	cases := []string{
		"FATAL[0000] decode config at config.json: outbound[2]: unknown outbound type: fallback",
		"unknown type: fallback",
		"UNKNOWN OUTBOUND TYPE: FALLBACK",
	}
	for _, out := range cases {
		t.Run(out, func(t *testing.T) {
			err := checkFailureError(t, out)
			if !strings.Contains(err.Error(), "extended build") {
				t.Errorf("error = %q, want extended-build hint", err)
			}
			// The original sing-box output must survive alongside the hint.
			if !strings.Contains(err.Error(), strings.TrimSpace(out)) {
				t.Errorf("error = %q, want original check output preserved", err)
			}
		})
	}
}

func TestApplySingbox_check_failure_unrelated_not_hinted(t *testing.T) {
	cases := map[string]string{
		"no fallback mentioned": "decode config: outbound[0]: missing server address",
		// A fallback group rejected for some other reason is a real config bug,
		// not a missing extended build.
		"fallback but known type": `decode config: outbound[2]: fallback: empty outbounds`,
		"unknown other type":      "decode config: outbound[1]: unknown outbound type: hysteria2",
	}
	for name, out := range cases {
		t.Run(name, func(t *testing.T) {
			err := checkFailureError(t, out)
			if strings.Contains(err.Error(), "extended build") {
				t.Errorf("error = %q, want no extended-build hint", err)
			}
		})
	}
}

func TestApplySingbox_staging_path_passed_to_check(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{}`))

	var checkArgs []string
	cmd := &fakeRunner{
		runFunc: func(name string, args ...string) ([]byte, error) {
			if name == "sing-box" {
				checkArgs = args
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd}
	_ = o.ApplySingbox(stagingPath, finalPath)

	if len(checkArgs) < 3 || checkArgs[0] != "check" || checkArgs[1] != "-c" || checkArgs[2] != stagingPath {
		t.Errorf("sing-box check args = %v, want [check -c %s]", checkArgs, stagingPath)
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

// TestApplySingbox_unchanged_skips_restart pins that a rendered config identical
// to the live one, and already applied, does not tear down every established
// connection.
func TestApplySingbox_unchanged_skips_restart(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"log":{"level":"warn"}}`))
	writeFile(t, finalPath, []byte(`{"log":{"level":"warn"}}`))

	cmd := &fakeRunner{
		runFunc: func(_ string, _ ...string) ([]byte, error) {
			return nil, nil // "running" succeeds → service is up
		},
	}

	o := &OpenWrt{Cmd: cmd, StateDir: dir}
	o.markApplied("sing-box", []byte(`{"log":{"level":"warn"}}`))
	if err := o.ApplySingbox(stagingPath, finalPath); err != nil {
		t.Fatalf("ApplySingbox: %v", err)
	}

	// The config is still validated against the installed binary — only the
	// restart is skipped.
	if len(cmd.calls) != 2 {
		t.Fatalf("calls = %v, want sing-box check + the running check", cmd.calls)
	}
	if cmd.calls[0][0] != "sing-box" || cmd.calls[0][1] != "check" {
		t.Errorf("first call = %v, want sing-box check", cmd.calls[0])
	}
	if cmd.calls[1][1] != "running" {
		t.Errorf("second call = %v, want the running check", cmd.calls[1])
	}
	if _, err := os.Stat(stagingPath); !os.IsNotExist(err) {
		t.Errorf("staging file survived the skipped apply: %v", err)
	}
}

// TestApplySingbox_unchanged_but_stopped_restarts pins the other half: skipping
// the restart must not leave a stopped sing-box down.
func TestApplySingbox_unchanged_but_stopped_restarts(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"log":{"level":"warn"}}`))
	writeFile(t, finalPath, []byte(`{"log":{"level":"warn"}}`))

	cmd := &fakeRunner{
		runFunc: func(name string, args ...string) ([]byte, error) {
			if name == "/etc/init.d/sing-box" && len(args) > 0 && args[0] == "running" {
				return nil, fmt.Errorf("exit 1")
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd, StateDir: dir}
	o.markApplied("sing-box", []byte(`{"log":{"level":"warn"}}`))
	if err := o.ApplySingbox(stagingPath, finalPath); err != nil {
		t.Fatalf("ApplySingbox: %v", err)
	}

	var restarted bool
	for _, c := range cmd.calls {
		if c[0] == "/etc/init.d/sing-box" && c[1] == "restart" {
			restarted = true
		}
	}
	if !restarted {
		t.Errorf("calls = %v, want a restart when the service is down", cmd.calls)
	}
}

// TestApplyDomains_unchanged_skips_restart pins that an unchanged domain list
// does not flush the DNS cache on every routing run.
func TestApplyDomains_unchanged_skips_restart(t *testing.T) {
	dir := t.TempDir()
	cacheFile := filepath.Join(dir, "domains.lst")
	dnsmasqDir := filepath.Join(dir, "dnsmasq.d")
	if err := os.MkdirAll(dnsmasqDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeFile(t, cacheFile, []byte("ipset=/example.com/vpn\n"))
	writeFile(t, filepath.Join(dnsmasqDir, "domains.lst"), []byte("ipset=/example.com/vpn\n"))

	cmd := &fakeRunner{} // every command succeeds, so "running" reports up
	o := &OpenWrt{Cmd: cmd, StateDir: dir}
	o.markApplied("dnsmasq", []byte("ipset=/example.com/vpn\n"))
	if err := o.ApplyDomains(cacheFile, dnsmasqDir); err != nil {
		t.Fatalf("ApplyDomains: %v", err)
	}

	// Only the liveness probe may run: no syntax check, no restart.
	if len(cmd.calls) != 1 || cmd.calls[0][1] != "running" {
		t.Errorf("calls = %v, want only the dnsmasq running check", cmd.calls)
	}
}

// TestApplySingbox_promoted_but_never_restarted pins the crash window: a run
// killed between promoting the config and restarting sing-box leaves the new
// file live and the old config in the running process. Comparing files alone
// would then read as "already applied" on every later run and skip the restart
// forever.
func TestApplySingbox_promoted_but_never_restarted(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"log":{"level":"warn"}}`))
	writeFile(t, finalPath, []byte(`{"log":{"level":"warn"}}`))

	// No marker: the file was promoted but the restart never happened.
	cmd := &fakeRunner{}
	o := &OpenWrt{Cmd: cmd, StateDir: dir}
	if err := o.ApplySingbox(stagingPath, finalPath); err != nil {
		t.Fatalf("ApplySingbox: %v", err)
	}

	var restarted bool
	for _, c := range cmd.calls {
		if c[0] == "/etc/init.d/sing-box" && c[1] == "restart" {
			restarted = true
		}
	}
	if !restarted {
		t.Fatalf("calls = %v, want a restart for a config that was never applied", cmd.calls)
	}

	// The marker now vouches for it, so the next identical run may skip.
	if !o.isApplied("sing-box", []byte(`{"log":{"level":"warn"}}`)) {
		t.Error("applied marker was not recorded after a successful restart")
	}
}

// TestApplySingbox_failed_restart_clears_marker pins that a failed restart does
// not leave a marker claiming the staged revision is live: whatever sing-box
// runs after the rollback, it is not that config.
func TestApplySingbox_failed_restart_clears_marker(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"log":{"level":"debug"}}`))
	writeFile(t, finalPath, []byte(`{"log":{"level":"warn"}}`))

	cmd := &fakeRunner{
		runFunc: func(name string, args ...string) ([]byte, error) {
			if name == "/etc/init.d/sing-box" && len(args) > 0 && args[0] == "restart" {
				return []byte("failed to start"), fmt.Errorf("exit 1")
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd, StateDir: dir}
	o.markApplied("sing-box", []byte(`{"log":{"level":"warn"}}`))
	if err := o.ApplySingbox(stagingPath, finalPath); err == nil {
		t.Fatal("expected an error when the restart fails")
	}

	if o.isApplied("sing-box", []byte(`{"log":{"level":"debug"}}`)) {
		t.Error("marker claims the staged config is live after a failed restart")
	}
	if o.isApplied("sing-box", []byte(`{"log":{"level":"warn"}}`)) {
		t.Error("marker still claims the rolled-back config is live; the next run would skip its restart")
	}
}

// TestApplySingbox_unchanged_still_validates pins that the skip sits after the
// check: this is the only place a config meets the sing-box binary actually
// installed, so swapping the extended build for the stock one must keep failing
// the run even when the rendered config did not change.
func TestApplySingbox_unchanged_still_validates(t *testing.T) {
	dir := t.TempDir()
	stagingPath := filepath.Join(dir, "config.json.new")
	finalPath := filepath.Join(dir, "config.json")
	writeFile(t, stagingPath, []byte(`{"outbounds":[{"type":"fallback"}]}`))
	writeFile(t, finalPath, []byte(`{"outbounds":[{"type":"fallback"}]}`))

	cmd := &fakeRunner{
		runFunc: func(name string, _ ...string) ([]byte, error) {
			if name == "sing-box" {
				return []byte("unknown outbound type: fallback"), fmt.Errorf("exit 1")
			}
			return nil, nil
		},
	}

	o := &OpenWrt{Cmd: cmd}
	err := o.ApplySingbox(stagingPath, finalPath)
	if err == nil {
		t.Fatal("expected an error when the installed sing-box rejects the config")
	}
	if !strings.Contains(err.Error(), singboxFallbackHint) {
		t.Errorf("error = %v, want the extended-build hint", err)
	}
	if _, statErr := os.Stat(finalPath); statErr != nil {
		t.Errorf("live config was removed on a validation failure: %v", statErr)
	}
}
