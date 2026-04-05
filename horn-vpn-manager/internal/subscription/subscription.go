package subscription

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/singbox"
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

// extractNodeName returns the display name from a VLESS URI fragment.
// Converts '+' to space to match vless.Parse behavior, since subscription
// generators commonly encode spaces as '+' in URI fragments.
// Returns an empty string if no fragment is present.
func extractNodeName(uri string) string {
	if u, err := url.Parse(uri); err == nil && u.Fragment != "" {
		return strings.ReplaceAll(u.Fragment, "+", " ")
	}
	if idx := strings.LastIndex(uri, "#"); idx >= 0 {
		name := uri[idx+1:]
		if unescaped, err := url.PathUnescape(name); err == nil {
			return strings.ReplaceAll(unescaped, "+", " ")
		}
		return strings.ReplaceAll(name, "+", " ")
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

// Run downloads and processes all enabled subscriptions, renders the sing-box
// config, writes it to OutDir/config.json, and calls the applier unless DryRun.
//
// Validates subscription config constraints before starting. Aborts if the
// default subscription fails. Logs and skips non-default failures.
func (r *Runner) Run(ctx context.Context) error {
	if err := r.Cfg.ValidateSubscriptions(); err != nil {
		return fmt.Errorf("subscription config invalid: %w", err)
	}

	start := time.Now()

	if r.DryRun {
		logx.Header("subscriptions dry-run")
		logx.Dim("dry-run: config will be rendered but not applied")
	} else {
		logx.Header("subscriptions run")
	}

	testURL := r.Cfg.Singbox.TestURL
	if testURL == "" {
		testURL = "https://www.gstatic.com/generate_204"
	}

	var (
		plans           []*OutboundPlan
		defaultFinalTag string
		tagNames        = make(map[string]string)
		processed       int
		failedSubs      []string
	)

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
			failedSubs = append(failedSubs, id)
			continue
		}

		uris, err := DecodePayload(data)
		if err != nil {
			logx.Err("Failed to decode subscription %s: %v", id, err)
			if sub.Default {
				return fmt.Errorf("default subscription %q failed to decode, aborting", id)
			}
			failedSubs = append(failedSubs, id)
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

		plan, err := BuildOutbounds(id, uris, sub.Interval, sub.Tolerance, testURL)
		if err != nil {
			logx.Err("Failed to build outbounds for %s: %v", id, err)
			if sub.Default {
				return fmt.Errorf("default subscription %q failed to build outbounds, aborting", id)
			}
			failedSubs = append(failedSubs, id)
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

		// Generate per-subscription route rules for non-default subscriptions only.
		if !sub.Default && sub.Route != nil {
			mergedRoute := FetchRouteEntries(ctx, id, sub.Route, opts)
			rule := BuildRouteRules(mergedRoute, plan.FinalTag)
			plan.RouteRule = rule
			if rule != nil {
				logx.Detail("  Subscription %s: route rule -> %s (%d domain(s), %d CIDR(s))",
					id, plan.FinalTag, len(rule.DomainSuffix), len(rule.IPCIDR))
			}
		}

		if sub.Default {
			defaultFinalTag = plan.FinalTag
		}
		for k, v := range plan.TagNames {
			tagNames[k] = v
		}
		plans = append(plans, plan)
		processed++
	}

	if processed == 0 && len(r.Cfg.Subscriptions) > 0 {
		return fmt.Errorf("no subscriptions were processed successfully")
	}

	if defaultFinalTag == "" {
		return fmt.Errorf("default subscription produced no outbound tag; check that the default subscription has a URL configured")
	}

	// Render the final sing-box config from the template and all outbound plans.
	templateData, err := singbox.LoadTemplate(r.Cfg.Singbox.Template)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}

	outbounds, routeRules := collectSingboxParts(plans)

	configData, err := singbox.RenderConfig(templateData, outbounds, routeRules, defaultFinalTag, r.Cfg.Singbox.LogLevel)
	if err != nil {
		return fmt.Errorf("render sing-box config: %w", err)
	}

	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	configPath := filepath.Join(r.OutDir, "config.json")
	if err := atomicWrite(configPath, configData); err != nil {
		return fmt.Errorf("write sing-box config: %w", err)
	}
	logx.OK("sing-box config written: %s", configPath)

	// Write subs-tags.json for future LuCI UI integration.
	if len(tagNames) > 0 {
		if tagsData, err := json.MarshalIndent(tagNames, "", "  "); err == nil {
			tagsPath := filepath.Join(r.OutDir, singbox.SubsTagsFilename)
			if err := atomicWrite(tagsPath, append(tagsData, '\n')); err != nil {
				logx.Warn("Failed to write %s: %v", singbox.SubsTagsFilename, err)
			} else {
				logx.Detail("Tag names written: %s", tagsPath)
			}
		}
	}

	if r.DryRun {
		logx.Dim("dry-run: skipping sing-box apply and restart")
	} else {
		if err := r.Apply.ApplySingbox(configPath); err != nil {
			return fmt.Errorf("apply sing-box: %w", err)
		}
	}

	elapsed := time.Since(start).Round(time.Millisecond)
	if len(failedSubs) > 0 {
		logx.Warn("subscriptions: %d processed, %d failed (%s) — elapsed: %s",
			processed, len(failedSubs), strings.Join(failedSubs, ", "), elapsed)
	} else {
		logx.OK("subscriptions: %d processed — elapsed: %s", processed, elapsed)
	}
	logx.Header("done")
	return nil
}

// collectSingboxParts flattens outbound plans into the two slices expected by
// singbox.RenderConfig: all outbounds (nodes, urltest, selector) and all route rules.
func collectSingboxParts(plans []*OutboundPlan) (outbounds []any, routeRules []any) {
	for _, plan := range plans {
		for _, ob := range plan.NodeOutbounds {
			outbounds = append(outbounds, ob)
		}
		if plan.URLTestGroup != nil {
			outbounds = append(outbounds, plan.URLTestGroup)
		}
		if plan.SelectorGroup != nil {
			outbounds = append(outbounds, plan.SelectorGroup)
		}
		if plan.RouteRule != nil {
			routeRules = append(routeRules, plan.RouteRule)
		}
	}
	return outbounds, routeRules
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

// atomicWrite writes data to path via a temp file and rename to prevent partial writes.
func atomicWrite(path string, data []byte) error {
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
