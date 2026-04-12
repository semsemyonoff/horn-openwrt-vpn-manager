package subscription

import (
	"context"
	"net"
	"strings"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/routing"
)

// FetchRouteEntries downloads domain_urls and ip_urls for a subscription's route
// config, validates downloaded entries, and merges them with manual config entries.
// Manual entries are placed first so they take precedence during deduplication.
// Returns a new SubscriptionRoute with URL lists replaced by merged Domains/IPCIDRs.
// Returns nil if route is nil.
// Individual URL download failures are logged as warnings and skipped; the function
// does not return an error for partial failures.
//
// cacheDir, if non-empty, enables list caching:
//   - if forceDownload is false, cached files are read instead of downloading;
//   - successful downloads are always saved to cacheDir;
//   - if a download fails but a cached copy exists, a warning is logged and the
//     cache is used as a fallback.
func FetchRouteEntries(ctx context.Context, subID string, route *config.SubscriptionRoute, opts fetch.Options, cacheDir string, forceDownload bool) *config.SubscriptionRoute {
	if route == nil {
		return nil
	}

	merged := &config.SubscriptionRoute{}

	// Domains: manual entries first, then downloaded (manual wins in dedup).
	domains := make([]string, len(route.Domains))
	copy(domains, route.Domains)

	if len(route.DomainURLs) > 0 {
		logx.Info("Subscription %s: downloading %d domain list URL(s)...", subID, len(route.DomainURLs))
		downloaded := fetchValidatedURLsWithCache(ctx, route.DomainURLs, opts, IsValidDomain, "domain", cacheDir, "domains", forceDownload)
		logx.Detail("  Subscription %s: %d valid domain(s) from URL(s)", subID, len(downloaded))
		domains = append(domains, downloaded...)
	}

	merged.Domains = routing.Dedup(domains)

	// IP/CIDRs: manual entries first, then downloaded (manual wins in dedup).
	cidrs := make([]string, len(route.IPCIDRs))
	copy(cidrs, route.IPCIDRs)

	if len(route.IPURLs) > 0 {
		logx.Info("Subscription %s: downloading %d IP list URL(s)...", subID, len(route.IPURLs))
		downloaded := fetchValidatedURLsWithCache(ctx, route.IPURLs, opts, IsValidCIDR, "IP/CIDR", cacheDir, "ip", forceDownload)
		logx.Detail("  Subscription %s: %d valid IP/CIDR(s) from URL(s)", subID, len(downloaded))
		cidrs = append(cidrs, downloaded...)
	}

	merged.IPCIDRs = routing.Dedup(cidrs)

	return merged
}

// fetchValidatedURLsWithCache downloads from urls with optional cache support.
// When cacheDir is non-empty and forceDownload is false, each URL is read from
// cache before attempting a download. Successful downloads are written to cacheDir.
// If a download fails and a cached copy exists, the cache is used with a warning.
func fetchValidatedURLsWithCache(ctx context.Context, urls []string, opts fetch.Options, validate func(string) bool, entryType, cacheDir, kind string, forceDownload bool) []string {
	var all []string
	var toDownload []string

	// Serve from cache first when not forcing re-download.
	if cacheDir != "" && !forceDownload {
		for _, u := range urls {
			if cached := ReadCachedList(cacheDir, u, kind); cached != nil {
				lines := routing.ParseLines(cached)
				valid, invalid := filterValidate(lines, validate)
				if invalid > 0 {
					logx.Warn("  %s list %s (cached): skipped %d invalid entry(s)", entryType, u, invalid)
				}
				logx.Detail("  %s list %s: %d valid entries (from cache)", entryType, u, len(valid))
				all = append(all, valid...)
			} else {
				toDownload = append(toDownload, u)
			}
		}
	} else {
		toDownload = urls
	}

	if len(toDownload) == 0 {
		return all
	}

	results := fetch.DownloadAll(ctx, toDownload, opts)
	for _, res := range results {
		if res.Err != nil {
			// Try cache fallback on download failure.
			if cacheDir != "" {
				if cached := ReadCachedList(cacheDir, res.URL, kind); cached != nil {
					logx.Warn("  Failed to download %s list from %s: %v — using cached version", entryType, res.URL, res.Err)
					lines := routing.ParseLines(cached)
					valid, invalid := filterValidate(lines, validate)
					if invalid > 0 {
						logx.Warn("  %s list %s (cached fallback): skipped %d invalid entry(s)", entryType, res.URL, invalid)
					}
					all = append(all, valid...)
					continue
				}
			}
			logx.Warn("  Failed to download %s list from %s: %v", entryType, res.URL, res.Err)
			continue
		}

		// Save successful download to cache.
		if cacheDir != "" {
			if err := WriteCachedList(cacheDir, res.URL, kind, res.Data); err != nil {
				logx.Warn("  Failed to cache %s list from %s: %v", entryType, res.URL, err)
			}
		}

		lines := routing.ParseLines(res.Data)
		valid, invalid := filterValidate(lines, validate)
		if invalid > 0 {
			logx.Warn("  %s list %s: skipped %d invalid %s entry(s)", entryType, res.URL, invalid, entryType)
		}
		logx.Detail("  %s list %s: %d valid entries", entryType, res.URL, len(valid))
		all = append(all, valid...)
	}
	return all
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
	labels := strings.Split(s, ".")
	for _, label := range labels {
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
