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
	"slices"
	"sort"
	"strconv"
	"strings"
	"sync"
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

// buildOptsForSub combines the per-subscription group settings with the global
// sing-box settings into the inputs BuildOutbounds needs.
func (r *Runner) buildOptsForSub(sub *config.Subscription, testURL string) BuildOptions {
	return BuildOptions{
		Interval:       sub.Interval,
		Tolerance:      sub.Tolerance,
		TestURL:        testURL,
		ConnectTimeout: r.Cfg.Singbox.ConnectTimeout,
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

// extractNodeName returns the display name from a node URI fragment, for any
// scheme: every supported protocol carries its name the same way, and this
// drives include/exclude filtering before the URI is handed to a parser.
// Converts '+' to space to match the parsers' behavior, since subscription
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
		urlCache        = make(map[string][]string) // url → decoded URIs from the default sub, shared read-only with goroutines
	)

	// Find the default subscription.
	var defaultID string
	for id, sub := range r.Cfg.Subscriptions {
		if sub.Default {
			defaultID = id
			break
		}
	}

	// ── Phase 1: process the default subscription (must succeed before the rest) ─
	if defaultID != "" {
		sub := r.Cfg.Subscriptions[defaultID]
		enabledCount++

		var uris []string
		fromPayload := len(sub.Nodes) == 0
		if len(sub.Nodes) > 0 {
			logx.Info("Subscription %s: %s inline node(s)", logx.Bold(defaultID), strconv.Itoa(len(sub.Nodes)))
			uris = slices.Clone(sub.Nodes)
		} else {
			opts := r.fetchOptsForSub(sub)

			logx.Info("Downloading subscription %s...", logx.Bold(defaultID))
			logx.Detail("  URL: %s", urlHost(sub.URL))
			data, dlErr := fetch.Download(ctx, sub.URL, opts)
			if dlErr != nil {
				if ctx.Err() != nil {
					return fmt.Errorf("interrupted: %w", ctx.Err())
				}
				return fmt.Errorf("default subscription %q failed to download, aborting", defaultID)
			}

			decoded, decErr := DecodePayload(data)
			if decErr != nil {
				return fmt.Errorf("default subscription %q failed to decode, aborting", defaultID)
			}
			// Only URL-backed results belong in the cache: keying an inline
			// subscription under "" would hand its nodes to every other
			// inline subscription on lookup.
			if sub.URL != "" {
				urlCache[sub.URL] = decoded
			}
			uris = decoded
		}

		if len(sub.Include) > 0 {
			before := len(uris)
			uris = filterInclude(uris, sub.Include)
			logx.Info("Subscription %s: include filter matched %d/%d node(s)", defaultID, len(uris), before)
			for _, uri := range uris {
				logx.Debug("  included: %s", extractNodeName(uri))
			}
		}

		if len(sub.Exclude) > 0 {
			var excludedURIs []string
			uris, excludedURIs = filterExclude(uris, sub.Exclude)
			if len(excludedURIs) > 0 {
				logx.Info("Subscription %s: excluded %d node(s) matching exclude patterns", defaultID, len(excludedURIs))
				for _, uri := range excludedURIs {
					logx.Debug("  excluded: %s", extractNodeName(uri))
				}
			}
		}

		logx.OK("Subscription %s: %s node(s)", defaultID, logx.Bold(strconv.Itoa(len(uris))))
		for _, uri := range uris {
			logx.Debug("  %s", uri)
		}

		if fromPayload {
			warnTopologyShift(defaultID, uris)
		}

		if r.DryRun {
			if writeErr := r.writeDryRunNodes(defaultID, uris); writeErr != nil {
				logx.Err("Failed to write dry-run output for %s: %v", defaultID, writeErr)
			}
		}

		plan, buildErr := BuildOutbounds(defaultID, uris, r.buildOptsForSub(sub, testURL))
		if buildErr != nil {
			return fmt.Errorf("default subscription %q failed to build outbounds, aborting", defaultID)
		}

		logx.Detail("  Subscription %s: final outbound tag: %s", defaultID, logx.Bold(plan.FinalTag))
		for _, tag := range plan.NodeTags {
			logx.Debug("  node: %s (%s)", tag, plan.TagNames[tag])
		}
		if plan.URLTestGroup != nil {
			logx.Debug("  group(urltest): %s", plan.URLTestGroup.Tag)
		}
		if plan.SelectorGroup != nil {
			logx.Debug("  group(selector): %s", plan.SelectorGroup.Tag)
		}

		defaultFinalTag = plan.FinalTag
		maps.Copy(tagNames, plan.TagNames)
		plans = append(plans, plan)
		processed++
	}

	// ── Phase 2: process non-default subscriptions in parallel ───────────────
	rest := make([]string, 0, len(r.Cfg.Subscriptions)-1)
	for id := range r.Cfg.Subscriptions {
		if id != defaultID {
			rest = append(rest, id)
		}
	}
	sort.Strings(rest)

	// Pre-filter: skip disabled/empty before spawning goroutines.
	type subJob struct {
		id  string
		sub *config.Subscription
	}
	var jobs []subJob
	for _, id := range rest {
		sub := r.Cfg.Subscriptions[id]
		if !sub.IsEnabled() {
			logx.Info("Skipping disabled subscription: %s", logx.Bold(id))
			continue
		}
		enabledCount++
		// No source check here: ValidateSubscriptions has already rejected an
		// enabled subscription carrying neither url nor nodes.
		jobs = append(jobs, subJob{id: id, sub: sub})
	}

	if len(jobs) > 0 {
		// urlCache is read-only from here: goroutines may reuse the default
		// subscription's decoded URIs when the URL matches.
		results := make([]subResult, len(jobs))
		var wg sync.WaitGroup
		for i, job := range jobs {
			wg.Add(1)
			go func(i int, id string, sub *config.Subscription) {
				defer wg.Done()
				results[i] = r.processSub(ctx, id, sub, testURL, urlCache)
			}(i, job.id, job.sub)
		}
		wg.Wait()

		// Merge results in the original sorted order.
		for _, res := range results {
			if res.err != nil {
				failedSubs = append(failedSubs, res.id)
				continue
			}
			maps.Copy(tagNames, res.plan.TagNames)
			plans = append(plans, res.plan)
			processed++
		}
	}

	if processed == 0 && enabledCount > 0 {
		return errors.New("no subscriptions were processed successfully")
	}

	if defaultFinalTag == "" {
		return errors.New("default subscription produced no outbound tag; check that the default subscription has either a url or inline nodes configured")
	}

	if tag := r.applyFallbackChains(plans, defaultID, tagNames); tag != "" {
		defaultFinalTag = tag
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

// applyFallbackChains generates a fallback group for every enabled subscription
// that declares a chain and produced a plan, storing it on that plan and
// registering its tag name. A non-default subscription's route rules are
// retargeted at its group; for the default subscription the group tag is
// returned so it can become route.final. Returns "" when the default
// subscription got no group.
//
// A backup that produced no plan (download or build failure) is dropped from the
// chain with a warning rather than aborting the run, matching the skip-and-continue
// policy applied to failed subscriptions. When every backup is gone the group is
// not emitted at all and the subscription keeps its own final tag.
func (r *Runner) applyFallbackChains(plans []*OutboundPlan, defaultID string, tagNames map[string]string) string {
	byID := make(map[string]*OutboundPlan, len(plans))
	for _, plan := range plans {
		byID[plan.ID] = plan
	}

	// Which subscriptions end up with a group has to be settled before any
	// reference is resolved: a backup declaring a chain of its own contributes
	// its fallback tag, not its bare final tag. Config validation rejects
	// cycles, so a single resolution pass is enough.
	ids := slices.Sorted(maps.Keys(r.Cfg.Subscriptions))
	chains := make(map[string][]string, len(ids))
	for _, id := range ids {
		sub := r.Cfg.Subscriptions[id]
		if sub == nil || sub.Fallback == nil || !sub.IsEnabled() || byID[id] == nil {
			continue
		}
		backups := make([]string, 0, len(sub.Fallback.Subscriptions))
		for _, ref := range sub.Fallback.Subscriptions {
			if byID[ref] == nil {
				logx.Warn("Subscription %s: fallback backup %q produced no outbounds, dropping it from the chain", id, ref)
				continue
			}
			backups = append(backups, ref)
		}
		if len(backups) == 0 {
			logx.Warn("Subscription %s: every fallback backup failed, keeping %s as its outbound", id, byID[id].FinalTag)
			continue
		}
		chains[id] = backups
	}

	// A subscription listed as somebody else's backup is reachable through that
	// chain even with no route rules of its own.
	isBackup := make(map[string]bool, len(chains))
	for _, backups := range chains {
		for _, ref := range backups {
			isBackup[ref] = true
		}
	}

	var defaultTag string
	for _, id := range ids {
		backups, ok := chains[id]
		if !ok {
			continue
		}
		plan := byID[id]
		tag := fallbackTag(id)

		outbounds := make([]string, 0, len(backups)+1)
		outbounds = append(outbounds, plan.FinalTag)
		for _, ref := range backups {
			if _, nested := chains[ref]; nested {
				outbounds = append(outbounds, fallbackTag(ref))
				continue
			}
			outbounds = append(outbounds, byID[ref].FinalTag)
		}

		plan.FallbackGroup = &FallbackOutbound{
			Type:             "fallback",
			Tag:              tag,
			Outbounds:        outbounds,
			BlacklistTimeout: r.Cfg.Subscriptions[id].Fallback.BlacklistTimeout,
		}
		tagNames[tag] = id + " (fallback)"

		logx.Detail("  Subscription %s: fallback chain: %s -> [%s]", id, tag, strings.Join(outbounds, ", "))

		if id == defaultID {
			defaultTag = tag
			continue
		}
		// A non-default subscription is reached either through its own route
		// rules or as another chain's backup — the outer group lists this
		// subscription's own <id>-fallback tag. With neither, the group is
		// emitted but nothing routes to it and the chain silently does nothing.
		if len(plan.RouteRules) == 0 && !isBackup[id] {
			logx.Warn("Subscription %s: fallback chain has no effect — the subscription is not the default one, defines no route and is not a backup of another chain, so nothing is routed through %s", id, tag)
		}
		RetargetRouteRules(plan.RouteRules, tag)
	}
	return defaultTag
}

// collectSingboxParts flattens outbound plans into the two slices expected by
// singbox.RenderConfig: all outbounds (nodes, urltest, selector) and all route rules.
func collectSingboxParts(plans []*OutboundPlan) (outbounds, routeRules []any) {
	for _, plan := range plans {
		outbounds = append(outbounds, plan.NodeOutbounds...)
		if plan.URLTestGroup != nil {
			outbounds = append(outbounds, plan.URLTestGroup)
		}
		if plan.SelectorGroup != nil {
			outbounds = append(outbounds, plan.SelectorGroup)
		}
		if plan.FallbackGroup != nil {
			outbounds = append(outbounds, plan.FallbackGroup)
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

// subResult holds the outcome of processing a single non-default subscription.
type subResult struct {
	id   string
	plan *OutboundPlan
	err  error
}

// processSub resolves, filters, and builds outbounds for a single non-default
// subscription. Nodes come either from the inline list or from a download. It is
// safe to call concurrently from multiple goroutines. urlCache is read-only: if
// the subscription URL matches a cached entry (e.g. from the default
// subscription), the cached URIs are reused.
func (r *Runner) processSub(ctx context.Context, id string, sub *config.Subscription, testURL string, urlCache map[string][]string) subResult {
	opts := r.fetchOptsForSub(sub)

	var uris []string
	fromPayload := len(sub.Nodes) == 0
	cached, cacheHit := urlCache[sub.URL]
	switch {
	case len(sub.Nodes) > 0:
		logx.Info("Subscription %s: %s inline node(s)", logx.Bold(id), strconv.Itoa(len(sub.Nodes)))
		uris = slices.Clone(sub.Nodes)
	case sub.URL != "" && cacheHit:
		logx.Info("Subscription %s: reusing cached nodes from %s", logx.Bold(id), urlHost(sub.URL))
		uris = slices.Clone(cached)
	default:
		logx.Info("Downloading subscription %s...", logx.Bold(id))
		logx.Detail("  URL: %s", urlHost(sub.URL))
		data, dlErr := fetch.Download(ctx, sub.URL, opts)
		if dlErr != nil {
			if ctx.Err() != nil {
				logx.Err("Subscription %s interrupted: %v", id, ctx.Err())
			} else {
				logx.Err("Failed to download subscription %s: %v", id, dlErr)
			}
			return subResult{id: id, err: dlErr}
		}

		decoded, decErr := DecodePayload(data)
		if decErr != nil {
			logx.Err("Failed to decode subscription %s: %v", id, decErr)
			return subResult{id: id, err: decErr}
		}
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

	if fromPayload {
		warnTopologyShift(id, uris)
	}

	if r.DryRun {
		if writeErr := r.writeDryRunNodes(id, uris); writeErr != nil {
			logx.Err("Failed to write dry-run output for %s: %v", id, writeErr)
		}
	}

	plan, buildErr := BuildOutbounds(id, uris, r.buildOptsForSub(sub, testURL))
	if buildErr != nil {
		logx.Err("Failed to build outbounds for %s: %v", id, buildErr)
		return subResult{id: id, err: buildErr}
	}

	logx.Detail("  Subscription %s: final outbound tag: %s", id, logx.Bold(plan.FinalTag))
	for _, tag := range plan.NodeTags {
		logx.Debug("  node: %s (%s)", tag, plan.TagNames[tag])
	}
	if plan.URLTestGroup != nil {
		logx.Debug("  group(urltest): %s", plan.URLTestGroup.Tag)
	}
	if plan.SelectorGroup != nil {
		logx.Debug("  group(selector): %s", plan.SelectorGroup.Tag)
	}

	if sub.Route != nil {
		mergedRoute := FetchRouteEntries(ctx, id, sub.Route, opts, r.SubsListsDir, r.DownloadLists)
		rules := BuildRouteRules(mergedRoute, plan.FinalTag)
		plan.RouteRules = rules
		if len(rules) > 0 {
			var nDomains, nCIDRs int
			for _, rule := range rules {
				nDomains += len(rule.DomainSuffix)
				nCIDRs += len(rule.IPCIDR)
			}
			logx.Detail("  Subscription %s: route rules -> %s (%d domain(s), %d CIDR(s))",
				id, plan.FinalTag, nDomains, nCIDRs)
		}
	}

	return subResult{id: id, plan: plan}
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
