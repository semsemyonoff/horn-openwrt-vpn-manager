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
func FetchRouteEntries(ctx context.Context, subID string, route *config.SubscriptionRoute, opts fetch.Options) *config.SubscriptionRoute {
	if route == nil {
		return nil
	}

	merged := &config.SubscriptionRoute{}

	// Domains: manual entries first, then downloaded (manual wins in dedup).
	domains := make([]string, len(route.Domains))
	copy(domains, route.Domains)

	if len(route.DomainURLs) > 0 {
		logx.Info("Subscription %s: downloading %d domain list URL(s)...", subID, len(route.DomainURLs))
		downloaded := fetchValidatedURLs(ctx, route.DomainURLs, opts, IsValidDomain, "domain")
		logx.Detail("  Subscription %s: %d valid domain(s) from URL(s)", subID, len(downloaded))
		domains = append(domains, downloaded...)
	}

	merged.Domains = routing.Dedup(domains)

	// IP/CIDRs: manual entries first, then downloaded (manual wins in dedup).
	cidrs := make([]string, len(route.IPCIDRs))
	copy(cidrs, route.IPCIDRs)

	if len(route.IPURLs) > 0 {
		logx.Info("Subscription %s: downloading %d IP list URL(s)...", subID, len(route.IPURLs))
		downloaded := fetchValidatedURLs(ctx, route.IPURLs, opts, IsValidCIDR, "IP/CIDR")
		logx.Detail("  Subscription %s: %d valid IP/CIDR(s) from URL(s)", subID, len(downloaded))
		cidrs = append(cidrs, downloaded...)
	}

	merged.IPCIDRs = routing.Dedup(cidrs)

	return merged
}

// fetchValidatedURLs downloads from all urls in parallel using bounded concurrency,
// parses each response into lines, validates each line with the provided validator,
// and returns all valid entries. Failed downloads and invalid entries are logged
// and skipped.
func fetchValidatedURLs(ctx context.Context, urls []string, opts fetch.Options, validate func(string) bool, entryType string) []string {
	results := fetch.DownloadAll(ctx, urls, opts)
	var all []string
	for _, res := range results {
		if res.Err != nil {
			logx.Warn("  Failed to download %s list from %s: %v", entryType, res.URL, res.Err)
			continue
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
