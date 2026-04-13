package subscription

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/singbox"
)

// DefaultOutDir is where generated sing-box config is written on-device.
const DefaultOutDir = "/etc/sing-box"

// DefaultConfigDir is the horn-vpn-manager config directory on-device.
const DefaultConfigDir = "/etc/horn-vpn-manager"

// Applier abstracts system side-effects for the subscription pipeline.
type Applier interface {
	// ApplySingbox validates the config at stagingPath, atomically moves it to
	// finalPath, and restarts sing-box. On validation failure stagingPath is
	// removed and finalPath is left untouched.
	ApplySingbox(stagingPath, finalPath string) error
}

// DebugApplier logs system actions without executing them.
type DebugApplier struct{}

func NewDebugApplier() *DebugApplier { return &DebugApplier{} }

func (d *DebugApplier) ApplySingbox(stagingPath, finalPath string) error {
	logx.Dim("skipping sing-box apply in debug mode (staging=%s final=%s)", stagingPath, finalPath)
	return nil
}

// Runner executes the subscription pipeline.
type Runner struct {
	Cfg          *config.Config
	Apply        Applier
	OutDir       string
	ConfigDir    string
	TemplatePath string // overrides cfg.Singbox.Template when non-empty
	DryRun       bool

	// SubsListsDir, if non-empty, enables subscription list caching.
	// Lists downloaded from domain_urls/ip_urls are read from and written to
	// this directory. When empty, lists are always downloaded.
	SubsListsDir string

	// DownloadLists forces re-download of all route lists even when cached
	// copies exist in SubsListsDir. Downloaded data is still saved to cache.
	DownloadLists bool
}

// NewRunner returns a Runner using the provided config and applier.
func NewRunner(cfg *config.Config, applier Applier) *Runner {
	return &Runner{
		Cfg:       cfg,
		Apply:     applier,
		OutDir:    DefaultOutDir,
		ConfigDir: DefaultConfigDir,
	}
}

// fetchOptsForSub returns fetch options for a subscription, using the per-subscription
// retry count if set, otherwise falling back to the global config value.
func (r *Runner) fetchOptsForSub(sub *config.Subscription) fetch.Options {
	retries := r.Cfg.Fetch.Retries
	if sub.Retries != nil && *sub.Retries > 0 {
		retries = *sub.Retries
	}
	return fetch.Options{
		Retries:     retries,
		Timeout:     time.Duration(r.Cfg.Fetch.TimeoutSeconds) * time.Second,
		Parallelism: r.Cfg.Fetch.Parallelism,
	}
}

// urlHost returns only the scheme and host of a URL for safe logging.
// Subscription URLs commonly embed auth tokens in the path or query string;
// logging only the host avoids credential exposure in verbose output.
func urlHost(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "[configured]"
	}
	return u.Scheme + "://" + u.Host + "/..."
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

// filterInclude returns only the uris whose node name contains at least one of
// the patterns (case-insensitive substring match). When patterns is empty, all
// uris are returned unchanged.
func filterInclude(uris, patterns []string) []string {
	if len(patterns) == 0 {
		return uris
	}
	lower := make([]string, len(patterns))
	for i, p := range patterns {
		lower[i] = strings.ToLower(p)
	}
	kept := make([]string, 0, len(uris))
	for _, uri := range uris {
		name := strings.ToLower(extractNodeName(uri))
		for _, pat := range lower {
			if strings.Contains(name, pat) {
				kept = append(kept, uri)
				break
			}
		}
	}
	return kept
}

// filterExclude returns uris split into kept and excluded slices.
// An entry is excluded if its node name contains one of the patterns
// (case-insensitive substring match).
func filterExclude(uris, patterns []string) (kept, excluded []string) {
	if len(patterns) == 0 {
		return uris, nil
	}
	lower := make([]string, len(patterns))
	for i, p := range patterns {
		lower[i] = strings.ToLower(p)
	}
	for _, uri := range uris {
		name := strings.ToLower(extractNodeName(uri))
		ex := false
		for _, pat := range lower {
			if strings.Contains(name, pat) {
				ex = true
				break
			}
		}
		if ex {
			excluded = append(excluded, uri)
		} else {
			kept = append(kept, uri)
		}
	}
	return kept, excluded
}

// Run downloads and processes all enabled subscriptions, renders the sing-box
// config, writes it to OutDir/config.json, and calls the applier unless DryRun.
//
// Validates subscription config constraints before starting. Aborts if the
// default subscription fails. Logs and skips non-default failures.
func (r *Runner) Run(ctx context.Context) error { //nolint:gocognit,gocyclo // orchestration function, splitting would hurt readability
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
		enabledCount    int
		failedSubs      []string
		urlCache        = make(map[string][]string) // url → decoded URIs, avoids re-downloading shared URLs
	)

	// Build processing order: default subscription first, then the rest sorted.
	// Non-default subscriptions are only processed after the default succeeds.
	var defaultID string
	for id, sub := range r.Cfg.Subscriptions {
		if sub.Default {
			defaultID = id
			break
		}
	}
	subIDs := make([]string, 0, len(r.Cfg.Subscriptions))
	if defaultID != "" {
		subIDs = append(subIDs, defaultID)
	}
	rest := make([]string, 0, len(r.Cfg.Subscriptions)-1)
	for id := range r.Cfg.Subscriptions {
		if id != defaultID {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)
	subIDs = append(subIDs, rest...)

	defaultProcessed := false

	for _, id := range subIDs {
		sub := r.Cfg.Subscriptions[id]

		// Skip non-default subscriptions until the default has been processed successfully.
		if defaultID != "" && !sub.Default && !defaultProcessed {
			logx.Warn("Skipping subscription %s: default subscription has not completed successfully", logx.Bold(id))
			continue
		}

		if !sub.IsEnabled() {
			logx.Info("Skipping disabled subscription: %s", logx.Bold(id))
			continue
		}
		enabledCount++
		if sub.URL == "" {
			logx.Warn("Subscription %s has no URL configured, skipping", id)
			continue
		}

		opts := r.fetchOptsForSub(sub)

		var uris []string
		if cached, ok := urlCache[sub.URL]; ok {
			logx.Info("Subscription %s: reusing cached nodes from %s", logx.Bold(id), urlHost(sub.URL))
			uris = cached
		} else {
			logx.Info("Downloading subscription %s...", logx.Bold(id))
			logx.Detail("  URL: %s", urlHost(sub.URL))
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

			decoded, err := DecodePayload(data)
			if err != nil {
				logx.Err("Failed to decode subscription %s: %v", id, err)
				if sub.Default {
					return fmt.Errorf("default subscription %q failed to decode, aborting", id)
				}
				failedSubs = append(failedSubs, id)
				continue
			}

			urlCache[sub.URL] = decoded
			uris = decoded
		}

		if len(sub.Include) > 0 {
			before := len(uris)
			uris = filterInclude(uris, sub.Include)
			logx.Info("Subscription %s: include filter matched %d/%d node(s)", id, len(uris), before)
			for _, uri := range uris {
				logx.Debug("  included: %s", extractNodeName(uri))
			}
		}

		if len(sub.Exclude) > 0 {
			var excludedURIs []string
			uris, excludedURIs = filterExclude(uris, sub.Exclude)
			if len(excludedURIs) > 0 {
				logx.Info("Subscription %s: excluded %d node(s) matching exclude patterns", id, len(excludedURIs))
				for _, uri := range excludedURIs {
					logx.Debug("  excluded: %s", extractNodeName(uri))
				}
			}
		}

		logx.OK("Subscription %s: %s node(s)", id, logx.Bold(strconv.Itoa(len(uris))))
		for _, uri := range uris {
			logx.Debug("  %s", uri)
		}

		if r.DryRun {
			if writeErr := r.writeDryRunNodes(id, uris); writeErr != nil {
				logx.Err("Failed to write dry-run output for %s: %v", id, writeErr)
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
			mergedRoute := FetchRouteEntries(ctx, id, sub.Route, opts, r.SubsListsDir, r.DownloadLists)
			rules := BuildRouteRules(mergedRoute, plan.FinalTag)
			plan.RouteRules = rules
			if len(rules) > 0 {
				var nDomains, nCIDRs int
				for _, r := range rules {
					nDomains += len(r.DomainSuffix)
					nCIDRs += len(r.IPCIDR)
				}
				logx.Detail("  Subscription %s: route rules -> %s (%d domain(s), %d CIDR(s))",
					id, plan.FinalTag, nDomains, nCIDRs)
			}
		}

		if sub.Default {
			defaultFinalTag = plan.FinalTag
			defaultProcessed = true
		}
		maps.Copy(tagNames, plan.TagNames)
		plans = append(plans, plan)
		processed++
	}

	if processed == 0 && enabledCount > 0 {
		return errors.New("no subscriptions were processed successfully")
	}

	if defaultFinalTag == "" {
		return errors.New("default subscription produced no outbound tag; check that the default subscription has a URL configured")
	}

	// Render the final sing-box config from the template and all outbound plans.
	templatePath := r.TemplatePath
	if templatePath == "" {
		templatePath = r.Cfg.Singbox.Template
	}
	templateData, err := singbox.LoadTemplate(templatePath)
	if err != nil {
		return fmt.Errorf("load template: %w", err)
	}

	outbounds, routeRules := collectSingboxParts(plans)

	configData, err := singbox.RenderConfig(templateData, outbounds, routeRules, defaultFinalTag, r.Cfg.Singbox.LogLevel)
	if err != nil {
		return fmt.Errorf("render sing-box config: %w", err)
	}

	configPath := filepath.Join(r.OutDir, "config.json")

	if err := os.MkdirAll(r.OutDir, 0o755); err != nil {
		return fmt.Errorf("create output dir: %w", err)
	}

	if r.DryRun {
		// In dry-run, write directly to configPath for inspection; skip validation and restart.
		if err := atomicWrite(configPath, configData); err != nil {
			return fmt.Errorf("write sing-box config: %w", err)
		}
		logx.OK("sing-box config written (dry-run): %s", configPath)
		logx.Dim("dry-run: skipping sing-box apply and restart")
	} else {
		// Write subs-tags.json for future LuCI UI integration under the config dir,
		// not the sing-box dir, so LuCI can find it at /etc/horn-vpn-manager/subs-tags.json.
		if len(tagNames) > 0 {
			if tagsData, err := json.MarshalIndent(tagNames, "", "  "); err == nil {
				tagsPath := filepath.Join(r.ConfigDir, singbox.SubsTagsFilename)
				if err := atomicWrite(tagsPath, append(tagsData, '\n')); err != nil {
					logx.Warn("Failed to write %s: %v", singbox.SubsTagsFilename, err)
				} else {
					logx.Detail("Tag names written: %s", tagsPath)
				}
			}
		}
		// Write to staging first; ApplySingbox validates against staging, then atomically
		// promotes it to configPath and restarts sing-box. This ensures the live config is
		// never replaced by an invalid one.
		stagingPath := configPath + ".new"
		if err := os.WriteFile(stagingPath, configData, 0o644); err != nil {
			return fmt.Errorf("write sing-box config staging: %w", err)
		}
		if err := r.Apply.ApplySingbox(stagingPath, configPath); err != nil {
			return fmt.Errorf("apply sing-box: %w", err)
		}
		logx.OK("sing-box config applied: %s", configPath)
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
func collectSingboxParts(plans []*OutboundPlan) (outbounds, routeRules []any) {
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
		for _, r := range plan.RouteRules {
			routeRules = append(routeRules, r)
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
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp) // best-effort cleanup
		return err
	}
	return nil
}
