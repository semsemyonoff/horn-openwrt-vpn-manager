package subscription

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
)

// DefaultOutDir is where generated sing-box config is written on-device.
const DefaultOutDir = "/etc/sing-box"

// Applier abstracts system side-effects for the subscription pipeline.
type Applier interface {
	ApplySingbox(configPath string) error
}

// DebugApplier logs system actions without executing them.
type DebugApplier struct{}

func NewDebugApplier() *DebugApplier { return &DebugApplier{} }

func (d *DebugApplier) ApplySingbox(configPath string) error {
	logx.Dim("skipping sing-box apply in debug mode (config=%s)", configPath)
	return nil
}

// Runner executes the subscription pipeline.
type Runner struct {
	Cfg    *config.Config
	Apply  Applier
	OutDir string
	DryRun bool
}

// NewRunner returns a Runner using the provided config and applier.
func NewRunner(cfg *config.Config, applier Applier) *Runner {
	return &Runner{
		Cfg:    cfg,
		Apply:  applier,
		OutDir: DefaultOutDir,
	}
}

func (r *Runner) fetchOpts() fetch.Options {
	return fetch.Options{
		Retries:     r.Cfg.Fetch.Retries,
		Timeout:     time.Duration(r.Cfg.Fetch.TimeoutSeconds) * time.Second,
		Parallelism: r.Cfg.Fetch.Parallelism,
	}
}

// Run downloads and processes all enabled subscriptions.
// It returns an error only on fatal failures (e.g. context cancelled).
// Per-subscription errors are logged and skipped.
func (r *Runner) Run(ctx context.Context) error {
	if r.DryRun {
		logx.Header("subscriptions dry-run")
		logx.Dim("dry-run: no system actions will be taken")
	} else {
		logx.Header("subscriptions run")
	}

	opts := r.fetchOpts()
	var processed int

	for id, sub := range r.Cfg.Subscriptions {
		if !sub.IsEnabled() {
			logx.Info("Skipping disabled subscription: %s", logx.Bold(id))
			continue
		}
		if sub.URL == "" {
			logx.Warn("Subscription %s has no URL configured, skipping", id)
			continue
		}

		logx.Info("Downloading subscription %s...", logx.Bold(id))
		logx.Detail("  URL: %s", sub.URL)

		data, err := fetch.Download(ctx, sub.URL, opts)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("interrupted: %w", ctx.Err())
			}
			logx.Err("Failed to download subscription %s: %v", id, err)
			continue
		}

		uris, err := DecodePayload(data)
		if err != nil {
			logx.Err("Failed to decode subscription %s: %v", id, err)
			continue
		}

		logx.OK("Subscription %s: %s node(s)", id, logx.Bold(fmt.Sprintf("%d", len(uris))))
		for _, uri := range uris {
			logx.Debug("  %s", uri)
		}

		if r.DryRun {
			if err := r.writeDryRunNodes(id, uris); err != nil {
				logx.Err("Failed to write dry-run output for %s: %v", id, err)
			}
		}

		processed++
	}

	if processed == 0 && len(r.Cfg.Subscriptions) > 0 {
		logx.Warn("No subscriptions were processed successfully")
	}

	logx.Header("done")
	return nil
}

// writeDryRunNodes writes extracted URIs to OutDir/<id>-nodes.txt for inspection.
func (r *Runner) writeDryRunNodes(id string, uris []string) error {
	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}
	path := filepath.Join(r.OutDir, id+"-nodes.txt")
	data := []byte(strings.Join(uris, "\n") + "\n")
	return os.WriteFile(path, data, 0o644)
}
