package subscription

import (
	"context"
	"fmt"
	"net"
	"strings"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/routing"
)

// FetchRouteEntries resolves domain_urls and ip_urls for a subscription's route
// config, validates the entries, and merges them with manual config entries.
// Manual entries are placed first so they take precedence during deduplication.
// Returns a new SubscriptionRoute with URL lists replaced by merged Domains/IPCIDRs.
// Returns nil if route is nil.
// A URL that cannot be resolved at all is logged as an error and skipped; the
// function does not return an error for partial failures, since a subscription
// keeps working with the entries it did get.
//
// cache.Dir, if non-empty, enables list caching — see ListCacheOptions.
func FetchRouteEntries(ctx context.Context, subID string, route *config.SubscriptionRoute, opts fetch.Options, cache ListCacheOptions) *config.SubscriptionRoute {
	if route == nil {
		return nil
	}

	merged := &config.SubscriptionRoute{}

	// Domains: manual entries first, then resolved (manual wins in dedup).
	domains := make([]string, 0, len(route.Domains))
	domains = append(domains, route.Domains...)

	if len(route.DomainURLs) > 0 {
		logx.Info("Subscription %s: %d domain list URL(s)", subID, len(route.DomainURLs))
		resolved := resolveLists(ctx, route.DomainURLs, opts, IsValidDomain, "domain", "domains", cache)
		logx.Detail("  Subscription %s: %d valid domain(s) from URL(s)", subID, len(resolved))
		domains = append(domains, resolved...)
	}

	merged.Domains = routing.Dedup(domains)

	// IP/CIDRs: manual entries first, then resolved (manual wins in dedup).
	cidrs := make([]string, 0, len(route.IPCIDRs))
	cidrs = append(cidrs, route.IPCIDRs...)

	if len(route.IPURLs) > 0 {
		logx.Info("Subscription %s: %d IP list URL(s)", subID, len(route.IPURLs))
		resolved := resolveLists(ctx, route.IPURLs, opts, IsValidCIDR, "IP/CIDR", "ip", cache)
		logx.Detail("  Subscription %s: %d valid IP/CIDR(s) from URL(s)", subID, len(resolved))
		cidrs = append(cidrs, resolved...)
	}

	merged.IPCIDRs = routing.Dedup(cidrs)

	return merged
}

// listPlan is the per-URL decision taken before any request is made.
type listPlan struct {
	url string
	// serveFromCache is set when the cached copy is young enough to use as is.
	serveFromCache bool
	// cacheAge is how old that copy is; only meaningful with serveFromCache.
	cacheAge time.Duration
	req      fetch.Request
}

// planLists decides, per URL, whether the cached copy is served as is, is
// revalidated, or is bypassed entirely.
func planLists(urls []string, kind string, cache ListCacheOptions, now time.Time) []listPlan {
	plans := make([]listPlan, len(urls))
	for i, u := range urls {
		p := listPlan{url: u, req: fetch.Request{URL: u}}
		if cache.Dir == "" || cache.ForceDownload {
			plans[i] = p
			continue
		}
		if ReadCachedList(cache.Dir, u, kind) == nil {
			plans[i] = p
			continue
		}
		age, known := CachedListAge(cache.Dir, u, kind, now)
		p.cacheAge = age
		if known && cache.TTL > 0 && age >= 0 && age < cache.TTL {
			p.serveFromCache = true
			plans[i] = p
			continue
		}
		// Past the TTL (or of unknown age) the copy is revalidated rather than
		// re-downloaded: a 304 keeps the saved bytes and costs no body. The
		// validators are only sent when they provably describe the body on disk.
		etag, lastMod := ValidatorsFor(cache.Dir, u, kind, ReadCachedList(cache.Dir, u, kind))
		p.req.Validators = fetch.Validators{ETag: etag, LastModified: lastMod}
		plans[i] = p
	}
	return plans
}

// resolveLists returns the validated entries of every URL, taking each one from
// the network or from the cache according to cache.
//
// Every URL reports its entry count and its real source on one line, at info
// level or, when it contributed nothing, at warning level. A run that silently
// served a months-old list used to be indistinguishable in the log from one that
// downloaded it.
func resolveLists(ctx context.Context, urls []string, opts fetch.Options, validate func(string) bool, entryType, kind string, cache ListCacheOptions) []string {
	now := time.Now()
	plans := planLists(urls, kind, cache, now)

	// Claim before planning any request: a list another subscription is already
	// resolving is taken from that result, so one run never mixes two revisions
	// of the same URL — and never downloads it twice.
	entries := make([]*listRunEntry, len(plans))
	owned := make([]bool, len(plans))
	for i, p := range plans {
		entries[i], owned[i] = cache.Run.claim(kind, p.url)
	}

	// planIdx maps each request back to its plan by position, not by URL: the
	// same URL may legitimately appear twice in one list.
	var reqs []fetch.Request
	var planIdx []int
	for i, p := range plans {
		if p.serveFromCache || !owned[i] {
			continue
		}
		planIdx = append(planIdx, i)
		reqs = append(reqs, p.req)
	}

	var all []string
	results := make([]fetch.Result, len(plans))
	for i, res := range fetch.DownloadAllConditional(ctx, reqs, opts) {
		results[planIdx[i]] = res
	}

	// Owned lists are resolved and published first; only then does this call
	// block on anything another goroutine owns. The reverse order deadlocks two
	// subscriptions that share two URLs.
	resolved := make([][]string, len(plans))
	for i := range plans {
		if !owned[i] {
			continue
		}
		resolved[i] = resolveOne(i, &plans[i], results, validate, entryType, kind, cache)
		entries[i].publish(resolved[i])
	}
	for i := range plans {
		if owned[i] {
			all = append(all, resolved[i]...)
			continue
		}
		shared := entries[i].wait()
		logx.Detail("  %s list %s: %d entries (resolved once for this run)", entryType, plans[i].url, len(shared))
		all = append(all, shared...)
	}
	return all
}

// resolveOne turns a single planned list into its validated entries, reporting
// where the data actually came from.
func resolveOne(i int, p *listPlan, results []fetch.Result, validate func(string) bool, entryType, kind string, cache ListCacheOptions) []string {
	now := time.Now()

	var (
		data   []byte
		source string
	)
	switch {
	case p.serveFromCache:
		data = ReadCachedList(cache.Dir, p.url, kind)
		source = fmt.Sprintf("cache, age %s", roundAge(p.cacheAge))
	case results[i].Err != nil:
		// Cache fallback keeps the subscription's route rules alive; without one
		// they vanish and its domains fall through to route.final. The cache is
		// read here rather than taken from the plan: --download-lists bypasses
		// the cache on the way out but still falls back to it.
		data = ReadCachedList(cache.Dir, p.url, kind)
		if data == nil {
			logx.Err("  %s list %s: unavailable (%v) and not cached — its entries are missing from the generated route rules",
				entryType, p.url, results[i].Err)
			return nil
		}
		age, _ := CachedListAge(cache.Dir, p.url, kind, now)
		source = fmt.Sprintf("cache, age %s — refresh failed: %v", roundAge(age), results[i].Err)
	case results[i].NotModified:
		data = ReadCachedList(cache.Dir, p.url, kind)
		if data == nil {
			// The copy the validators were taken from is gone; nothing to serve.
			logx.Err("  %s list %s: server reported unchanged but the cached copy is gone — its entries are missing from the generated route rules", entryType, p.url)
			return nil
		}
		source = "cache, revalidated (304)"
		refreshCacheAge(cache.Dir, p.url, kind, now)
	default:
		data = results[i].Data
		source = "network"
		storeList(cache, p.url, kind, &results[i], now, entryType)
	}

	lines := routing.ParseLines(data)
	valid, invalid := filterValidate(lines, validate)
	if invalid > 0 {
		logx.Warn("  %s list %s: skipped %d invalid entry(s)", entryType, p.url, invalid)
	}
	if len(valid) == 0 {
		logx.Warn("  %s list %s: 0 entries (%s) — it contributes nothing to the route rules", entryType, p.url, source)
	} else {
		logx.Info("  %s list %s: %d entries (%s)", entryType, p.url, len(valid), source)
	}
	return valid
}

// storeList saves a freshly downloaded list with its validators.
func storeList(cache ListCacheOptions, url, kind string, res *fetch.Result, now time.Time, entryType string) {
	if cache.Dir == "" {
		return
	}
	meta := ListMeta{URL: url, Kind: kind, ETag: res.ETag, LastModified: res.LastModified, FetchedAt: now}
	if err := SaveCachedList(cache.Dir, res.Data, &meta); err != nil {
		logx.Warn("  Failed to cache %s list from %s: %v", entryType, url, err)
	}
}

// refreshCacheAge records that the stored copy was confirmed current, so the
// next run inside the TTL does not revalidate it again.
func refreshCacheAge(dir, url, kind string, now time.Time) {
	if err := RefreshCachedListAge(dir, url, kind, now); err != nil {
		logx.Warn("  Failed to refresh cache metadata for %s: %v", url, err)
	}
}

// roundAge renders a cache age at a resolution an operator can act on.
func roundAge(d time.Duration) time.Duration {
	if d < time.Minute {
		return d.Round(time.Second)
	}
	return d.Round(time.Minute)
}

// filterValidate partitions lines into valid and invalid using the provided
// predicate, returning the valid slice and the count of invalid entries.
func filterValidate(lines []string, validate func(string) bool) (valid []string, invalidCount int) {
	for _, line := range lines {
		if validate(line) {
			valid = append(valid, line)
		} else {
			invalidCount++
		}
	}
	return valid, invalidCount
}

// IsValidDomain returns true if s looks like a valid DNS domain name.
// It accepts names like "example.com", "sub.example.com", and single-label
// names like "localhost". Empty strings, strings with spaces, and names with
// invalid characters or labels are rejected.
func IsValidDomain(s string) bool {
	if s == "" {
		return false
	}
	for label := range strings.SplitSeq(s, ".") {
		if !isValidDomainLabel(label) {
			return false
		}
	}
	return true
}

// isValidDomainLabel returns true if a single DNS label is valid:
// 1-63 characters, alphanumeric plus interior hyphens only.
func isValidDomainLabel(label string) bool {
	n := len(label)
	if n == 0 || n > 63 {
		return false
	}
	if label[0] == '-' || label[n-1] == '-' {
		return false
	}
	for _, r := range label {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '-' {
			return false
		}
	}
	return true
}

// IsValidCIDR returns true if s is a valid IPv4 or IPv6 CIDR block or a plain
// IP address (interpreted as a host route by sing-box).
func IsValidCIDR(s string) bool {
	if s == "" {
		return false
	}
	if strings.Contains(s, "/") {
		_, _, err := net.ParseCIDR(s)
		return err == nil
	}
	return net.ParseIP(s) != nil
}
