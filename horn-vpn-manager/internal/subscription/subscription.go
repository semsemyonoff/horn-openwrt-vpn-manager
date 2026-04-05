package subscription

import (
	"context"
	"fmt"
	"net/url"
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

// fetchOptsForSub returns fetch options for a subscription, using the per-subscription
// retry count if set, otherwise falling back to the global config value.
func (r *Runner) fetchOptsForSub(sub *config.Subscription) fetch.Options {
	retries := r.Cfg.Fetch.Retries
	if sub.Retries != nil {
		retries = *sub.Retries
	}
	return fetch.Options{
		Retries:     retries,
		Timeout:     time.Duration(r.Cfg.Fetch.TimeoutSeconds) * time.Second,
		Parallelism: r.Cfg.Fetch.Parallelism,
	}
}

// extractNodeName returns the URL-decoded fragment (display name/label) from a VLESS URI.
// Returns an empty string if no fragment is present.
func extractNodeName(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Fragment != "" {
		return u.Fragment
	}
	if idx := strings.LastIndex(uri, "#"); idx >= 0 {
		name := uri[idx+1:]
		if unescaped, err := url.PathUnescape(name); err == nil {
			return unescaped
		}
		return name
	}
	return ""
}

// filterExclude returns uris with any entry whose node name contains
// one of the exclude patterns removed (case-insensitive substring match).
func filterExclude(uris []string, patterns []string) []string {
	if len(patterns) == 0 {
		return uris
	}
	lower := make([]string, len(patterns))
	for i, p := range patterns {
		lower[i] = strings.ToLower(p)
	}
	out := uris[:0:0]
	for _, uri := range uris {
		name := strings.ToLower(extractNodeName(uri))
		excluded := false
		for _, pat := range lower {
			if strings.Contains(name, pat) {
				excluded = true
				break
			}
		}
		if !excluded {
			out = append(out, uri)
		}
	}
	return out
}

// Run downloads and processes all enabled subscriptions.
// It validates subscription config constraints, aborts if the default subscription
// fails, and logs and skips non-default failures.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.Cfg.ValidateSubscriptions(); err != nil {
		return fmt.Errorf("subscription config invalid: %w", err)
	}

	if r.DryRun {
		logx.Header("subscriptions dry-run")
		logx.Dim("dry-run: no system actions will be taken")
	} else {
		logx.Header("subscriptions run")
	}

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

		opts := r.fetchOptsForSub(sub)
		data, err := fetch.Download(ctx, sub.URL, opts)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("interrupted: %w", ctx.Err())
			}
			logx.Err("Failed to download subscription %s: %v", id, err)
			if sub.Default {
				return fmt.Errorf("default subscription %q failed to download, aborting", id)
			}
			continue
		}

		uris, err := DecodePayload(data)
		if err != nil {
			logx.Err("Failed to decode subscription %s: %v", id, err)
			if sub.Default {
				return fmt.Errorf("default subscription %q failed to decode, aborting", id)
			}
			continue
		}

		if len(sub.Exclude) > 0 {
			before := len(uris)
			uris = filterExclude(uris, sub.Exclude)
			if skipped := before - len(uris); skipped > 0 {
				logx.Info("Subscription %s: excluded %d node(s) matching exclude patterns", id, skipped)
			}
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

		testURL := r.Cfg.Singbox.TestURL
		if testURL == "" {
			testURL = "https://www.gstatic.com/generate_204"
		}
		plan, err := BuildOutbounds(id, uris, sub.Interval, sub.Tolerance, testURL)
		if err != nil {
			logx.Err("Failed to build outbounds for %s: %v", id, err)
			if sub.Default {
				return fmt.Errorf("default subscription %q failed to build outbounds, aborting", id)
			}
			continue
		}

		logx.Detail("  Subscription %s: final outbound tag: %s", id, logx.Bold(plan.FinalTag))
		for _, ob := range plan.NodeOutbounds {
			logx.Debug("  node: %s (%s)", ob.Tag, plan.TagNames[ob.Tag])
		}
		if plan.URLTestGroup != nil {
			logx.Debug("  group(urltest): %s", plan.URLTestGroup.Tag)
		}
		if plan.SelectorGroup != nil {
			logx.Debug("  group(selector): %s", plan.SelectorGroup.Tag)
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
