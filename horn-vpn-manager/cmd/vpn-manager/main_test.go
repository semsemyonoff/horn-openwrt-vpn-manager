package main

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// buildBinary compiles the CLI once per test and returns its path. The contract
// under test lives in main(), which os.Exit()s, so it can only be observed by
// running the real binary.
func buildBinary(t *testing.T) string {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "vpn-manager")
	cmd := exec.Command("go", "build", "-o", bin, ".")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	return bin
}

// extractCoreError reimplements the awk one-liner the rpcd backend uses to pull
// the message out of the core's stderr:
//
//	awk 'index($0, "error: ") == 1 { m = substr($0, 8) } END { print m }'
//
// Keeping the two in step is the point of this test: rpcd falls back to a
// generic "config rejected by vpn-manager check" whenever this yields nothing,
// so a change to main()'s stderr format silently downgrades every core
// rejection LuCI shows the operator.
func extractCoreError(stderr string) string {
	var msg string
	for line := range strings.SplitSeq(stderr, "\n") {
		if rest, ok := strings.CutPrefix(line, "error: "); ok {
			msg = rest
		}
	}
	return msg
}

func TestCheck_RejectionIsRelayableToLuCI(t *testing.T) {
	bin := buildBinary(t)

	// A config the core rejects for a reason no rpcd jq check can express: the
	// chain names a subscription that does not exist.
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	const cfg = `{
	  "subscriptions": {
	    "main": {
	      "name": "Main",
	      "default": true,
	      "nodes": ["vless://11111111-2222-3333-4444-555555555555@example.com:443?encryption=none#Main"],
	      "fallback": {"subscriptions": ["ghost"]}
	    }
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "check", "-c", cfgPath, "--no-color")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err == nil {
		t.Fatalf("check exited 0 on an invalid config, stderr:\n%s", stderr.String())
	}
	var exitErr *exec.ExitError
	if !errors.As(err, &exitErr) {
		t.Fatalf("running the binary failed: %v", err)
	}

	msg := extractCoreError(stderr.String())
	if msg == "" {
		t.Fatalf("no line starting with %q on stderr — rpcd would show a generic message instead:\n%s",
			"error: ", stderr.String())
	}
	// The message has to carry the concrete reason, ids and all, since that is
	// the whole text the operator sees in LuCI.
	if !strings.Contains(msg, `"ghost"`) {
		t.Errorf("relayed message does not name the unknown subscription: %q", msg)
	}
}

// A run that succeeds must not print an "error: " line at all, or rpcd's awk
// would relay a stale message on the next rejection-free save.
func TestCheck_ValidConfigPrintsNoErrorLine(t *testing.T) {
	bin := buildBinary(t)

	cfgPath := filepath.Join(t.TempDir(), "config.json")
	const cfg = `{
	  "subscriptions": {
	    "main": {
	      "name": "Main",
	      "default": true,
	      "nodes": ["vless://11111111-2222-3333-4444-555555555555@example.com:443?encryption=none#Main"]
	    }
	  }
	}`
	if err := os.WriteFile(cfgPath, []byte(cfg), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cmd := exec.Command(bin, "check", "-c", cfgPath, "--no-color")
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("check rejected a valid config: %v\n%s", err, stderr.String())
	}
	if msg := extractCoreError(stderr.String()); msg != "" {
		t.Errorf("valid config still produced an error line: %q", msg)
	}
}

func TestRun_UnknownCommand(t *testing.T) {
	err := run([]string{"nonsense"})
	if err == nil {
		t.Fatal("run() error = nil, want an unknown-command error")
	}
	if !strings.Contains(err.Error(), "nonsense") {
		t.Errorf("error does not name the command: %v", err)
	}
}
