// Package system handles OpenWrt side-effects: dnsmasq, firewall, file operations.
//
// All external commands are run through a CommandRunner interface to allow
// testing without an actual OpenWrt environment.
package system

import (
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

// OpenWrt implements routing.Applier using real system commands.
type OpenWrt struct {
	Cmd CommandRunner
}

func NewOpenWrt() *OpenWrt {
	return &OpenWrt{Cmd: ExecRunner{}}
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
func (o *OpenWrt) ApplyDomains(cacheFile, dnsmasqDir string) error {
	// Validate syntax
	out, err := o.Cmd.Run("dnsmasq", "--conf-file="+cacheFile, "--test")
	if err != nil {
		return fmt.Errorf("dnsmasq syntax check failed: %s", string(out))
	}
	logx.OK("dnsmasq syntax check passed")

	// Copy to dnsmasq drop-in directory
	if err := os.MkdirAll(dnsmasqDir, 0o755); err != nil {
		return fmt.Errorf("create dnsmasq dir: %w", err)
	}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return fmt.Errorf("read domain cache: %w", err)
	}
	dest := filepath.Join(dnsmasqDir, "domains.lst")
	if err := atomicWrite(dest, data); err != nil {
		return fmt.Errorf("write dnsmasq config: %w", err)
	}
	logx.Info("Domain list applied to %s, restarting dnsmasq...", dest)

	// Restart dnsmasq
	if _, err := o.Cmd.Run("/etc/init.d/dnsmasq", "restart"); err != nil {
		return fmt.Errorf("restart dnsmasq: %w", err)
	}
	logx.OK("dnsmasq restarted")
	return nil
}

// ApplySingbox validates the config at stagingPath with sing-box check, then
// atomically renames it to finalPath and restarts sing-box. On validation
// failure the staging file is removed and finalPath is left untouched.
// On restart failure, the previous config is restored from a backup so the
// router is not left in an inconsistent state.
func (o *OpenWrt) ApplySingbox(stagingPath, finalPath string) error {
	logx.Info("Validating sing-box config...")
	out, err := o.Cmd.Run("sing-box", "check", "-c", stagingPath)
	if err != nil {
		_ = os.Remove(stagingPath)
		return fmt.Errorf("sing-box check failed: %s: %w", strings.TrimSpace(string(out)), err)
	}
	logx.OK("sing-box config validation passed")

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
	logx.OK("sing-box restarted")
	return nil
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
