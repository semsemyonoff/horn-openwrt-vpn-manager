// Package system handles OpenWrt side-effects: dnsmasq, firewall, file operations.
//
// All external commands are run through a CommandRunner interface to allow
// testing without an actual OpenWrt environment.
package system

import (
	"bytes"
	"crypto/sha256"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// CommandRunner abstracts shell command execution for testability.
type CommandRunner interface {
	Run(name string, args ...string) ([]byte, error)
	LookPath(name string) (string, error)
}

// ExecRunner runs real OS commands.
type ExecRunner struct{}

func (ExecRunner) Run(name string, args ...string) ([]byte, error) {
	return exec.Command(name, args...).CombinedOutput()
}

func (ExecRunner) LookPath(name string) (string, error) {
	return exec.LookPath(name)
}

// DefaultStateDir is where the applied-revision markers live on-device.
const DefaultStateDir = "/etc/horn-vpn-manager"

// OpenWrt implements routing.Applier using real system commands.
type OpenWrt struct {
	Cmd CommandRunner

	// StateDir holds the applied-revision markers that let a run tell "this
	// file is already live" from "this file was written but the service was
	// never restarted with it". Empty disables the optimisation entirely: with
	// no marker every apply restarts, which is what the code did before.
	StateDir string
}

func NewOpenWrt() *OpenWrt {
	return &OpenWrt{Cmd: ExecRunner{}, StateDir: DefaultStateDir}
}

// appliedMarkerPath is where the digest of the last revision service was
// restarted with is stored.
func (o *OpenWrt) appliedMarkerPath(service string) string {
	if o.StateDir == "" {
		return ""
	}
	return filepath.Join(o.StateDir, ".applied-"+service)
}

// isApplied reports whether service was last restarted with exactly this
// content. Comparing the destination file instead is not enough: a run killed
// between promoting the file and restarting the service leaves the new file
// live and the old config in the running process, and every later run would
// then see a match and skip the restart forever.
func (o *OpenWrt) isApplied(service string, data []byte) bool {
	path := o.appliedMarkerPath(service)
	if path == "" {
		return false
	}
	stored, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return strings.TrimSpace(string(stored)) == contentDigest(data)
}

// markApplied records that service is now running this content. A failure to
// write only costs the next run a redundant restart, so it is a warning.
func (o *OpenWrt) markApplied(service string, data []byte) {
	path := o.appliedMarkerPath(service)
	if path == "" {
		return
	}
	if err := atomicWrite(path, []byte(contentDigest(data)+"\n")); err != nil {
		logx.Warn("Failed to record applied %s revision: %v", service, err)
	}
}

// clearApplied drops the marker, so the next apply restarts the service rather
// than trusting a revision it can no longer vouch for.
func (o *OpenWrt) clearApplied(service string) {
	if path := o.appliedMarkerPath(service); path != "" {
		_ = os.Remove(path)
	}
}

// serviceRunning reports whether the init script says the service is up.
// procd answers "running" with exit 0/1; an init script that does not implement
// the action fails too, which reads as "not running" and keeps the previous
// unconditional-restart behaviour.
func (o *OpenWrt) serviceRunning(service string) bool {
	_, err := o.Cmd.Run("/etc/init.d/"+service, "running")
	return err == nil
}

func contentDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// DebugApplier logs system actions without executing them.
type DebugApplier struct{}

func NewDebugApplier() *DebugApplier { return &DebugApplier{} }

func (d *DebugApplier) ApplyDomains(cacheFile, dnsmasqDir string) error {
	logx.Dim("skipping dnsmasq apply in debug mode (cache=%s)", cacheFile)
	return nil
}

func (d *DebugApplier) ApplyIPs(ipListFile string) error {
	logx.Dim("skipping firewall reload in debug mode (ip_list=%s)", ipListFile)
	return nil
}

func (d *DebugApplier) ApplySingbox(stagingPath, finalPath string) error {
	logx.Dim("skipping sing-box apply in debug mode (staging=%s final=%s)", stagingPath, finalPath)
	return nil
}

// ApplyDomains validates the domain list with dnsmasq --test, copies it
// to the dnsmasq drop-in directory, and restarts dnsmasq.
//
// A drop-in that is already live is left alone: restarting dnsmasq drops the
// DNS cache and every in-flight query, and the routing pipeline runs on a
// schedule where the list usually has not changed at all. "Already live" means
// the same bytes on disk *and* a marker saying dnsmasq was restarted with them
// *and* dnsmasq actually running.
func (o *OpenWrt) ApplyDomains(cacheFile, dnsmasqDir string) error {
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return fmt.Errorf("read domain cache: %w", err)
	}

	dest := filepath.Join(dnsmasqDir, "domains.lst")
	if same, sameErr := sameFileContents(cacheFile, dest); sameErr == nil && same {
		switch {
		case !o.isApplied("dnsmasq", data):
			logx.Info("Domain list unchanged but never applied, restarting dnsmasq...")
		case !o.serviceRunning("dnsmasq"):
			logx.Info("Domain list unchanged but dnsmasq is not running")
		default:
			logx.Info("Domain list unchanged, skipping dnsmasq restart")
			return nil
		}
	}

	// Validate syntax
	out, err := o.Cmd.Run("dnsmasq", "--conf-file="+cacheFile, "--test")
	if err != nil {
		return fmt.Errorf("dnsmasq syntax check failed: %s", string(out))
	}
	logx.OK("dnsmasq syntax check passed")

	// Copy to dnsmasq drop-in directory
	if mkdirErr := os.MkdirAll(dnsmasqDir, 0o755); mkdirErr != nil {
		return fmt.Errorf("create dnsmasq dir: %w", mkdirErr)
	}
	if err := atomicWrite(dest, data); err != nil {
		return fmt.Errorf("write dnsmasq config: %w", err)
	}
	logx.Info("Domain list applied to %s, restarting dnsmasq...", dest)

	// Restart dnsmasq
	if _, err := o.Cmd.Run("/etc/init.d/dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart dnsmasq: %w", err)
	}
	o.markApplied("dnsmasq", data)
	logx.OK("dnsmasq restarted")
	return nil
}

// singboxFallbackHint explains a sing-box check failure caused by the generated
// "fallback" outbound group: it is not an upstream outbound type and exists only
// in the extended build.
const singboxFallbackHint = `"fallback" outbounds require the sing-box extended build (sing-box-extended); ` +
	`install it, or remove the "fallback" chains from config.json`

// isUnknownFallbackType reports whether msg is sing-box rejecting the "fallback"
// outbound type itself, as opposed to any other complaint about a fallback group.
func isUnknownFallbackType(msg string) bool {
	low := strings.ToLower(msg)
	if !strings.Contains(low, "fallback") {
		return false
	}
	return strings.Contains(low, "unknown outbound type") || strings.Contains(low, "unknown type")
}

// ApplySingbox validates the config at stagingPath with sing-box check, then
// atomically renames it to finalPath and restarts sing-box. On validation
// failure the staging file is removed and finalPath is left untouched.
// On restart failure, the previous config is restored from a backup so the
// router is not left in an inconsistent state.
//
// A config byte-identical to the live one skips the rename and the restart, but
// not the check — see below.
//
// This check only runs on a real apply: --dry-run and --debug never reach here,
// so it does not replace verifying a fallback-using config on the device.
func (o *OpenWrt) ApplySingbox(stagingPath, finalPath string) error {
	logx.Info("Validating sing-box config...")
	out, err := o.Cmd.Run("sing-box", "check", "-c", stagingPath)
	if err != nil {
		_ = os.Remove(stagingPath)
		msg := strings.TrimSpace(string(out))
		logx.Err("sing-box config validation failed: %s", msg)
		if isUnknownFallbackType(msg) {
			logx.Err("%s", singboxFallbackHint)
			return fmt.Errorf("sing-box check failed: %s: %s: %w", msg, singboxFallbackHint, err)
		}
		return fmt.Errorf("sing-box check failed: %s: %w", msg, err)
	}
	logx.OK("sing-box config validation passed")

	// Restarting sing-box tears down every established connection. The
	// subscription pipeline runs on a schedule and usually renders exactly what
	// is already live, so an unconditional restart costs the user a connection
	// drop for no config change at all.
	//
	// The skip deliberately sits *after* the check: this is the only place a
	// config is validated against the sing-box binary actually installed, so
	// swapping the extended build for the stock one has to keep surfacing
	// "unknown outbound type" on the next run even when nothing else changed.
	//
	// Identical bytes on disk are not enough on their own. A run killed between
	// the rename below and the restart leaves the new file live and the old
	// config in the running process; without the applied marker every later run
	// would see a match and skip the restart forever.
	staged, readErr := os.ReadFile(stagingPath)
	if readErr != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("read staged sing-box config: %w", readErr)
	}
	if same, sameErr := sameFileContents(stagingPath, finalPath); sameErr == nil && same {
		switch {
		case !o.isApplied("sing-box", staged):
			logx.Info("sing-box config unchanged but never applied, restarting...")
		case !o.serviceRunning("sing-box"):
			logx.Info("sing-box config unchanged but the service is not running")
		default:
			logx.OK("sing-box config unchanged, skipping restart")
			_ = os.Remove(stagingPath)
			return nil
		}
	}

	// Back up the existing config so we can restore it if restart fails.
	backupPath := finalPath + ".bak"
	hasBackup := false
	if _, statErr := os.Stat(finalPath); statErr == nil {
		if backupErr := copyFile(finalPath, backupPath); backupErr != nil {
			_ = os.Remove(stagingPath)
			return fmt.Errorf("back up existing config: %w", backupErr)
		}
		hasBackup = true
	}

	if err := os.Rename(stagingPath, finalPath); err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("promote sing-box config: %w", err)
	}

	logx.Info("Restarting sing-box...")
	if out, err := o.Cmd.Run("/etc/init.d/sing-box", "restart"); err != nil {
		// Whatever sing-box ends up running now, it is not the staged config —
		// drop the marker so the next run applies rather than skips.
		o.clearApplied("sing-box")
		if hasBackup {
			logx.Info("sing-box restart failed; restoring previous config...")
			if restoreErr := os.Rename(backupPath, finalPath); restoreErr != nil {
				logx.Dim("Warning: could not restore backup: %v", restoreErr)
			} else {
				logx.OK("Previous config restored")
				// Attempt to bring sing-box back up with the restored config so
				// the router is not left without a running proxy.
				if _, startErr := o.Cmd.Run("/etc/init.d/sing-box", "start"); startErr != nil {
					logx.Dim("Warning: could not start sing-box with restored config: %v", startErr)
				} else {
					logx.OK("sing-box started with previous config")
				}
			}
		}
		return fmt.Errorf("restart sing-box: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if hasBackup {
		_ = os.Remove(backupPath)
	}
	o.markApplied("sing-box", staged)
	logx.OK("sing-box restarted")
	return nil
}

// sameFileContents reports whether both paths exist and hold identical bytes.
// A missing file is not an error the caller has to distinguish: it simply means
// "not the same", so the caller proceeds with the write.
func sameFileContents(a, b string) (bool, error) {
	da, err := os.ReadFile(a)
	if err != nil {
		return false, err
	}
	db, err := os.ReadFile(b)
	if err != nil {
		return false, err
	}
	return bytes.Equal(da, db), nil
}

// copyFile copies src to dst, creating or truncating dst.
func copyFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

// atomicWrite writes data to path via a temp file and rename so readers never
// see a partial write.
func atomicWrite(path string, data []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return nil
}

// ApplyIPs reloads the firewall so it picks up the updated IP list.
func (o *OpenWrt) ApplyIPs(ipListFile string) error {
	logx.Info("Reloading firewall...")

	if _, err := o.Cmd.LookPath("fw4"); err == nil {
		if out, err := o.Cmd.Run("fw4", "reload"); err != nil {
			return fmt.Errorf("fw4 reload: %s: %w", string(out), err)
		}
	} else {
		if out, err := o.Cmd.Run("/etc/init.d/firewall", "reload"); err != nil {
			return fmt.Errorf("firewall reload: %s: %w", string(out), err)
		}
	}

	logx.OK("Firewall reloaded")
	return nil
}
