package subscription

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/config"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/fetch"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/logx"
	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/routing"
)

// DefaultSubsListsDir is where subscription list files are cached on-device.
const DefaultSubsListsDir = "/etc/horn-vpn-manager/lists/subscriptions"

// SubsListsSubdir is the directory name appended to a lists dir for subscription caches.
const SubsListsSubdir = "subscriptions"

// ListCacheOptions controls how route lists are cached between runs.
//
// Dir empty disables caching entirely: every list is downloaded and nothing is
// written. ForceDownload (--download-lists) skips both the cache read and the
// conditional request. TTL bounds how long a cached copy is used without
// revalidation; zero means "revalidate on every run".
type ListCacheOptions struct {
	Dir           string
	ForceDownload bool
	TTL           time.Duration

	// Run, when set, resolves each (kind, url) exactly once for the whole
	// pipeline run. Nil disables that and every occurrence is fetched again.
	Run *ListRunCache
}

// ListRunCache memoises the resolved entries of a route list for one pipeline
// run.
//
// Phase 2 resolves subscriptions concurrently, so the same URL listed by two
// subscriptions is fetched twice. That is not merely wasteful: if the list
// changes mid-run one request can answer 304 (yielding the old revision) while
// the other answers 200 with the new one, and the generated config ends up
// carrying two revisions of the same list at once. A URL repeated inside a
// single route has the same problem.
type ListRunCache struct {
	mu      sync.Mutex
	entries map[string]*listRunEntry
}

type listRunEntry struct {
	done  chan struct{}
	valid []string
}

// NewListRunCache returns a memo table scoped to one pipeline run.
func NewListRunCache() *ListRunCache {
	return &ListRunCache{entries: make(map[string]*listRunEntry)}
}

// claim returns the entry for a list and whether the caller owns resolving it.
// The owner must call publish exactly once; every other caller blocks in wait
// until it does. Owners publish all of their own entries before waiting on any
// foreign one, so two runs claiming the same pair of URLs cannot deadlock.
func (c *ListRunCache) claim(kind, url string) (*listRunEntry, bool) {
	if c == nil {
		return nil, true
	}
	key := kind + "|" + url
	c.mu.Lock()
	defer c.mu.Unlock()
	if e, ok := c.entries[key]; ok {
		return e, false
	}
	e := &listRunEntry{done: make(chan struct{})}
	c.entries[key] = e
	return e, true
}

func (e *listRunEntry) publish(valid []string) {
	if e == nil {
		return
	}
	e.valid = valid
	close(e.done)
}

func (e *listRunEntry) wait() []string {
	<-e.done
	return e.valid
}

// ListCacheEntry records source metadata for a cached list file.
type ListCacheEntry struct {
	URL  string `json:"url"`
	Sub  string `json:"sub"`
	Kind string `json:"kind"`
}

// ListIndex maps cache filename to source metadata.
type ListIndex map[string]ListCacheEntry

// ListMeta is the sidecar stored next to a cached list. It makes every cached
// file self-describing — index.json is rewritten only by the prefetch command,
// so a file written by "subscriptions run" would otherwise be indistinguishable
// from an orphan — and carries what a conditional refresh needs.
//
// FetchedAt is when the copy was last confirmed current, which a 304 refreshes
// without rewriting the list itself.
// Digest is the SHA-256 of the list the validators describe. The body and the
// sidecar are two files and cannot be renamed as one unit, so a crash or a
// failed second write can leave a new body beside the previous validators.
// Sending those validators would then let a server answer 304 for a body it
// never served, pinning the wrong routing data. Digest makes that pair
// detectable; a mismatch simply drops the validators and forces a full refresh.
type ListMeta struct {
	URL          string    `json:"url"`
	Kind         string    `json:"kind"`
	ETag         string    `json:"etag,omitempty"`
	LastModified string    `json:"last_modified,omitempty"`
	Digest       string    `json:"digest,omitempty"`
	FetchedAt    time.Time `json:"fetched_at"`
}

// ListDigest is the digest format stored in ListMeta.Digest.
func ListDigest(data []byte) string {
	return fmt.Sprintf("%x", sha256.Sum256(data))
}

// ValidatorsFor returns the stored validators of a cached list, but only when
// the sidecar provably describes the body on disk. A legacy sidecar carries no
// digest and is trusted, since it predates the field and the pair was written
// by the same code path.
func ValidatorsFor(dir, url, kind string, body []byte) (etag, lastModified string) {
	meta, ok := ReadListMeta(dir, url, kind)
	if !ok {
		return "", ""
	}
	if meta.Digest != "" && meta.Digest != ListDigest(body) {
		logx.Warn("  Cached list %s: body does not match its stored validators, refreshing in full", url)
		return "", ""
	}
	return meta.ETag, meta.LastModified
}

// ListCacheFilename returns a stable, collision-resistant filename for a URL.
// Uses the first 12 hex chars of SHA-256(url).
func ListCacheFilename(url, kind string) string {
	h := sha256.Sum256([]byte(url))
	return fmt.Sprintf("%s-%x.lst", kind, h[:6])
}

// metaFilename returns the sidecar filename for a cached list.
func metaFilename(url, kind string) string {
	return strings.TrimSuffix(ListCacheFilename(url, kind), ".lst") + ".meta.json"
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

// ReadCachedList reads raw bytes from the cache for a URL. Returns nil if not
// found or empty: an empty file is a broken cache entry, and treating it as a
// zero-entry list would silently drop every route rule built from that URL.
func ReadCachedList(dir, url, kind string) []byte {
	if dir == "" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(dir, ListCacheFilename(url, kind)))
	if err != nil || len(data) == 0 {
		return nil
	}
	return data
}

// ReadListMeta returns the sidecar for a cached list. ok is false when there is
// none — a legacy cache entry, for which the caller falls back to the list
// file's mtime.
func ReadListMeta(dir, url, kind string) (meta ListMeta, ok bool) {
	if dir == "" {
		return ListMeta{}, false
	}
	data, err := os.ReadFile(filepath.Join(dir, metaFilename(url, kind)))
	if err != nil {
		return ListMeta{}, false
	}
	if err := json.Unmarshal(data, &meta); err != nil {
		return ListMeta{}, false
	}
	return meta, true
}

// cacheWriteMu serialises writes to one cache entry.
//
// Phase 2 of the pipeline processes subscriptions concurrently and nothing stops
// two of them from listing the same route list URL — the cache filename is
// derived from the URL alone, so both goroutines write the same two files. That
// costs one of them its write (both `atomicWrite` calls stage through the same
// `.tmp` path, so the loser's rename fails with ENOENT) and, worse, can pair one
// response's list body with the other's validators, which is exactly the
// self-inconsistent entry the revalidation logic assumes cannot exist.
var cacheWriteMu sync.Map // cache filename → *sync.Mutex

func lockCacheEntry(url, kind string) func() {
	v, _ := cacheWriteMu.LoadOrStore(ListCacheFilename(url, kind), &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

// WriteListMeta stores the sidecar for a cached list.
func WriteListMeta(dir string, meta *ListMeta) error {
	defer lockCacheEntry(meta.URL, meta.Kind)()
	return writeListMeta(dir, meta)
}

// writeListMeta is the unlocked half, for callers already holding the entry lock.
func writeListMeta(dir string, meta *ListMeta) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	data, err := json.MarshalIndent(meta, "", "  ")
	if err != nil {
		return err
	}
	return atomicWrite(filepath.Join(dir, metaFilename(meta.URL, meta.Kind)), append(data, '\n'))
}

// RefreshCachedListAge records that the stored copy of url was confirmed
// current, leaving its body and validators alone. The read-modify-write runs
// under the entry lock: taken separately, a refresh racing a save of the same
// URL can put the old validators back on top of the new body.
func RefreshCachedListAge(dir, url, kind string, at time.Time) error {
	defer lockCacheEntry(url, kind)()
	meta, _ := ReadListMeta(dir, url, kind)
	meta.URL, meta.Kind, meta.FetchedAt = url, kind, at
	return writeListMeta(dir, &meta)
}

// CachedListAge returns how long ago the cached copy of url was last confirmed
// current, and whether that could be determined at all.
func CachedListAge(dir, url, kind string, now time.Time) (time.Duration, bool) {
	if meta, ok := ReadListMeta(dir, url, kind); ok && !meta.FetchedAt.IsZero() {
		return now.Sub(meta.FetchedAt), true
	}
	info, err := os.Stat(filepath.Join(dir, ListCacheFilename(url, kind)))
	if err != nil {
		return 0, false
	}
	return now.Sub(info.ModTime()), true
}

// WriteCachedList saves raw bytes to the cache for a URL, creating dir if needed,
// and records a sidecar so the copy carries its own provenance.
func WriteCachedList(dir, url, kind string, data []byte) error {
	return SaveCachedList(dir, data, &ListMeta{URL: url, Kind: kind, FetchedAt: time.Now()})
}

// SaveCachedList writes the list and its sidecar as one unit. The list lands
// first: a sidecar pointing at a list that is not there yet would claim a fresh
// copy the next run cannot read.
func SaveCachedList(dir string, data []byte, meta *ListMeta) error {
	defer lockCacheEntry(meta.URL, meta.Kind)()
	meta.Digest = ListDigest(data)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	if err := atomicWrite(filepath.Join(dir, ListCacheFilename(meta.URL, meta.Kind)), data); err != nil {
		return err
	}
	return writeListMeta(dir, meta)
}

// PruneListCache removes cached lists and sidecars whose URL is no longer
// configured. keep holds the list filenames that are still referenced.
//
// Pruning is keyed on the configured URLs, never on what a run managed to
// download: a URL that failed today must keep its cached copy, which is the
// fallback that keeps its route rules alive. Without pruning a URL that is
// removed and later re-added is served from an arbitrarily old file without a
// single request.
func PruneListCache(dir string, keep map[string]bool) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		listName := name
		switch {
		case strings.HasSuffix(name, ".meta.json"):
			listName = strings.TrimSuffix(name, ".meta.json") + ".lst"
		case strings.HasSuffix(name, ".lst"):
		default:
			continue // index.json and anything else stays
		}
		if keep[listName] {
			continue
		}
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			logx.Warn("  Failed to remove orphaned cache file %s: %v", name, err)
			continue
		}
		logx.Detail("  Removed orphaned cache file: %s", name)
		removed++
	}
	return removed
}

// configuredListFilenames returns the cache filenames of every route list URL
// referenced by an enabled subscription.
func configuredListFilenames(cfg *config.Config) map[string]bool {
	keep := make(map[string]bool)
	for _, sub := range cfg.Subscriptions {
		if sub == nil || !sub.IsEnabled() || sub.Route == nil {
			continue
		}
		for _, u := range sub.Route.DomainURLs {
			keep[ListCacheFilename(u, "domains")] = true
		}
		for _, u := range sub.Route.IPURLs {
			keep[ListCacheFilename(u, "ip")] = true
		}
	}
	return keep
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
			cacheDownloads(ctx, id, route.DomainURLs, "domains", opts, subsListsDir, index)
		}

		if len(route.IPURLs) > 0 {
			logx.Info("Subscription %s: downloading %d IP list URL(s) for cache...", id, len(route.IPURLs))
			cacheDownloads(ctx, id, route.IPURLs, "ip", opts, subsListsDir, index)
		}
	}

	if err := WriteListIndex(subsListsDir, index); err != nil {
		logx.Warn("Failed to write list index: %v", err)
	} else {
		logx.Detail("List index written: %s", filepath.Join(subsListsDir, "index.json"))
	}

	if n := PruneListCache(subsListsDir, configuredListFilenames(cfg)); n > 0 {
		logx.Info("Removed %d orphaned cache file(s)", n)
	}

	return nil
}

// cacheDownloads fetches urls of one kind for one subscription and stores each
// successful body with its validators, recording it in index.
func cacheDownloads(ctx context.Context, id string, urls []string, kind string, opts fetch.Options, dir string, index ListIndex) {
	label := "domain"
	if kind == "ip" {
		label = "IP"
	}
	reqs := make([]fetch.Request, len(urls))
	for i, u := range urls {
		etag, lastMod := ValidatorsFor(dir, u, kind, ReadCachedList(dir, u, kind))
		reqs[i] = fetch.Request{URL: u, Validators: fetch.Validators{ETag: etag, LastModified: lastMod}}
	}

	now := time.Now()
	for _, res := range fetch.DownloadAllConditional(ctx, reqs, opts) {
		fname := ListCacheFilename(res.URL, kind)
		switch {
		case res.Err != nil:
			logx.Warn("  Failed to download %s list from %s: %v", label, res.URL, res.Err)
			continue
		case res.NotModified:
			// The stored copy is still current; only its age is refreshed, so
			// the next subscriptions run does not revalidate it again.
			if err := RefreshCachedListAge(dir, res.URL, kind, now); err != nil {
				logx.Warn("  Failed to refresh cache metadata for %s: %v", res.URL, err)
				continue
			}
			logx.Info("  %s list %s: unchanged (304), cache kept -> %s", label, res.URL, fname)
		default:
			meta := ListMeta{URL: res.URL, Kind: kind, ETag: res.ETag, LastModified: res.LastModified, FetchedAt: now}
			if err := SaveCachedList(dir, res.Data, &meta); err != nil {
				logx.Warn("  Failed to cache %s list from %s: %v", label, res.URL, err)
				continue
			}
			logx.Info("  %s list %s: %d entries -> %s", label, res.URL, len(routing.ParseLines(res.Data)), fname)
		}
		index[fname] = ListCacheEntry{URL: res.URL, Sub: id, Kind: kind}
	}
}
