package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/routing"
)

// DefaultSubsListsDir is where subscription list files are cached on-device.
const DefaultSubsListsDir = "/etc/horn-vpn-manager/lists/subscriptions"

// SubsListsSubdir is the directory name appended to a lists dir for subscription caches.
const SubsListsSubdir = "subscriptions"

// ListCacheEntry records source metadata for a cached list file.
type ListCacheEntry struct {
	URL  string `json:"url"`
	Sub  string `json:"sub"`
	Kind string `json:"kind"`
}

// ListIndex maps cache filename to source metadata.
type ListIndex map[string]ListCacheEntry

// urlCacheFilename returns a stable, collision-resistant filename for a URL.
// Uses the first 12 hex chars of SHA-256(url).
func urlCacheFilename(url, kind string) string {
	h := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%s-%x.lst", kind, h[:6])
}

// WriteListIndex writes index.json to dir, creating it if necessary.
func WriteListIndex(dir string, index ListIndex) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, "index.json"), append(data, '\n'))
}

// ReadCachedList reads raw bytes from the cache for a URL. Returns nil if not found.
func ReadCachedList(dir, url, kind string) []byte {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, urlCacheFilename(url, kind)))
	if err != nil {
		return nil
	}
	return data
}

// WriteCachedList saves raw bytes to the cache for a URL, creating dir if needed.
func WriteCachedList(dir, url, kind string, data []byte) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, urlCacheFilename(url, kind)), data)
}

// DownloadSubscriptionLists downloads all route URL lists for enabled subscriptions
// and saves them to subsListsDir. index.json is written mapping filenames to
// source URLs and subscription IDs. Called by "routing run --with-subscriptions"
// to pre-populate the subscription list cache.
// Individual download failures are logged as warnings; the function continues and
// does not return an error for partial failures.
func DownloadSubscriptionLists(ctx context.Context, cfg *config.Config, subsListsDir string, opts fetch.Options) error {
	if err := os.MkdirAll(subsListsDir, 0o755); err != nil {
		return fmt.Errorf("create subs lists dir: %w", err)
	}

	subIDs := make([]string, 0, len(cfg.Subscriptions))
	for id := range cfg.Subscriptions {
		subIDs = append(subIDs, id)
	}
	sort.Strings(subIDs)

	index := make(ListIndex)

	for _, id := range subIDs {
		sub := cfg.Subscriptions[id]
		if !sub.IsEnabled() || sub.Route == nil {
			continue
		}
		route := sub.Route

		if len(route.DomainURLs) > 0 {
			logx.Info("Subscription %s: downloading %d domain list URL(s) for cache...", id, len(route.DomainURLs))
			results := fetch.DownloadAll(ctx, route.DomainURLs, opts)
			for _, res := range results {
				if res.Err != nil {
					logx.Warn("  Failed to download domain list from %s: %v", res.URL, res.Err)
					continue
				}
				if err := WriteCachedList(subsListsDir, res.URL, "domains", res.Data); err != nil {
					logx.Warn("  Failed to cache domain list from %s: %v", res.URL, err)
					continue
				}
				fname := urlCacheFilename(res.URL, "domains")
				index[fname] = ListCacheEntry{URL: res.URL, Sub: id, Kind: "domains"}
				lines := routing.ParseLines(res.Data)
				logx.Detail("  %s: %d lines -> %s", res.URL, len(lines), fname)
			}
		}

		if len(route.IPURLs) > 0 {
			logx.Info("Subscription %s: downloading %d IP list URL(s) for cache...", id, len(route.IPURLs))
			results := fetch.DownloadAll(ctx, route.IPURLs, opts)
			for _, res := range results {
				if res.Err != nil {
					logx.Warn("  Failed to download IP list from %s: %v", res.URL, res.Err)
					continue
				}
				if err := WriteCachedList(subsListsDir, res.URL, "ip", res.Data); err != nil {
					logx.Warn("  Failed to cache IP list from %s: %v", res.URL, err)
					continue
				}
				fname := urlCacheFilename(res.URL, "ip")
				index[fname] = ListCacheEntry{URL: res.URL, Sub: id, Kind: "ip"}
				lines := routing.ParseLines(res.Data)
				logx.Detail("  %s: %d lines -> %s", res.URL, len(lines), fname)
			}
		}
	}

	if err := WriteListIndex(subsListsDir, index); err != nil {
		logx.Warn("Failed to write list index: %v", err)
	} else {
		logx.Detail("List index written: %s", filepath.Join(subsListsDir, "index.json"))
	}

	return nil
}
