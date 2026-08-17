package subscription_test

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/semsemyonoff/horn-openwrt-vpn-manager/internal/subscription"
)

// TestReadCachedList_EmptyFileIsMiss pins that a zero-byte cache entry is a
// miss, not a list of zero entries: served as a list it silently drops every
// route rule built from that URL.
func TestReadCachedList_EmptyFileIsMiss(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/domains.lst"

	if err := subscription.WriteCachedList(dir, url, "domains", []byte{}); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}
	if got := subscription.ReadCachedList(dir, url, "domains"); got != nil {
		t.Errorf("ReadCachedList = %q, want nil for an empty file", got)
	}
}

func TestReadListMeta_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/domains.lst"
	at := time.Now().Add(-2 * time.Hour).Truncate(time.Second)

	meta := subscription.ListMeta{URL: url, Kind: "domains", ETag: `"v1"`, FetchedAt: at}
	if err := subscription.SaveCachedList(dir, []byte("a.example.com\n"), &meta); err != nil {
		t.Fatalf("SaveCachedList: %v", err)
	}

	got, ok := subscription.ReadListMeta(dir, url, "domains")
	if !ok {
		t.Fatal("ReadListMeta: not found")
	}
	if got.ETag != `"v1"` || !got.FetchedAt.Equal(at) {
		t.Errorf("meta = %+v, want etag %q at %s", got, `"v1"`, at)
	}

	age, known := subscription.CachedListAge(dir, url, "domains", at.Add(90*time.Minute))
	if !known || age != 90*time.Minute {
		t.Errorf("CachedListAge = %s (known=%v), want 1h30m", age, known)
	}
}

// TestCachedListAge_FallsBackToMtime covers a cache entry written before the
// sidecar existed: it still has to report an age, or it would never be
// revalidated.
func TestCachedListAge_FallsBackToMtime(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/legacy.lst"

	if err := subscription.WriteCachedList(dir, url, "domains", []byte("a.example.com\n")); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}
	// Drop the sidecar to model a legacy cache directory.
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}
	for _, e := range entries {
		if filepath.Ext(e.Name()) == ".json" {
			if err := os.Remove(filepath.Join(dir, e.Name())); err != nil {
				t.Fatalf("remove sidecar: %v", err)
			}
		}
	}

	if _, known := subscription.CachedListAge(dir, url, "domains", time.Now()); !known {
		t.Error("CachedListAge: age unknown without a sidecar, want the file mtime")
	}
}

// TestPruneListCache_RemovesOrphansOnly pins that a cache file whose URL is
// still configured survives while one for a URL that is gone is removed. An
// orphan that stays is served, without a single request, if the URL is ever
// added back.
func TestPruneListCache_RemovesOrphansOnly(t *testing.T) {
	dir := t.TempDir()
	keptURL := "http://lists.example.com/kept.lst"
	goneURL := "http://lists.example.com/gone.lst"

	for _, u := range []string{keptURL, goneURL} {
		if err := subscription.WriteCachedList(dir, u, "domains", []byte("a.example.com\n")); err != nil {
			t.Fatalf("WriteCachedList(%s): %v", u, err)
		}
	}
	if err := subscription.WriteListIndex(dir, subscription.ListIndex{}); err != nil {
		t.Fatalf("WriteListIndex: %v", err)
	}

	keep := map[string]bool{subscription.ListCacheFilename(keptURL, "domains"): true}
	if n := subscription.PruneListCache(dir, keep); n != 2 {
		t.Errorf("PruneListCache removed %d file(s), want 2 (list + sidecar)", n)
	}

	if subscription.ReadCachedList(dir, keptURL, "domains") == nil {
		t.Error("configured URL lost its cached list")
	}
	if _, ok := subscription.ReadListMeta(dir, keptURL, "domains"); !ok {
		t.Error("configured URL lost its sidecar")
	}
	if subscription.ReadCachedList(dir, goneURL, "domains") != nil {
		t.Error("orphaned list survived the prune")
	}
	if _, err := os.Stat(filepath.Join(dir, "index.json")); err != nil {
		t.Errorf("index.json was pruned: %v", err)
	}
}

// TestSaveCachedList_ConcurrentSameURL pins that two subscriptions listing the
// same route list URL do not fight over its cache files. Phase 2 processes
// subscriptions concurrently and the cache filename comes from the URL alone, so
// without serialisation both writers stage through the same ".tmp" path and one
// rename fails with ENOENT — and the surviving list body can end up paired with
// the other response's validators.
func TestSaveCachedList_ConcurrentSameURL(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/shared.lst"

	const writers = 40
	errs := make(chan error, writers)
	var wg sync.WaitGroup
	for i := range writers {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			body := fmt.Sprintf("host%d.example\n", i)
			meta := ListMetaFor(url, i)
			errs <- subscription.SaveCachedList(dir, []byte(body), &meta)
		}(i)
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("SaveCachedList: %v", err)
		}
	}

	// Whatever writer won, the body and its sidecar must come from the same one.
	body := string(subscription.ReadCachedList(dir, url, "domains"))
	meta, ok := subscription.ReadListMeta(dir, url, "domains")
	if !ok {
		t.Fatal("sidecar missing after concurrent writes")
	}
	var winner int
	if _, err := fmt.Sscanf(meta.ETag, `"v%d"`, &winner); err != nil {
		t.Fatalf("unparseable sidecar etag %q: %v", meta.ETag, err)
	}
	if want := fmt.Sprintf("host%d.example\n", winner); body != want {
		t.Errorf("body = %q but the sidecar claims etag %q (body of %q)", body, meta.ETag, want)
	}
}

// ListMetaFor builds a sidecar whose ETag identifies the writer, so a mismatched
// body/sidecar pair is detectable.
func ListMetaFor(url string, i int) subscription.ListMeta {
	return subscription.ListMeta{
		URL:       url,
		Kind:      "domains",
		ETag:      fmt.Sprintf(`"v%d"`, i),
		FetchedAt: time.Now(),
	}
}

// TestValidatorsFor_MismatchedBodyDropsValidators pins that a sidecar left over
// from a different body is not used to revalidate. The body and the sidecar are
// two files and cannot be renamed as one unit, so a crash between them pairs a
// new list with old validators; sending those lets the server answer 304 for a
// body it never served, pinning the wrong routing data indefinitely.
func TestValidatorsFor_MismatchedBodyDropsValidators(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/domains.lst"

	meta := subscription.ListMeta{URL: url, Kind: "domains", ETag: `"v1"`, FetchedAt: time.Now()}
	if err := subscription.SaveCachedList(dir, []byte("v1.example\n"), &meta); err != nil {
		t.Fatalf("SaveCachedList: %v", err)
	}

	// Intact pair: the validators are usable.
	if etag, _ := subscription.ValidatorsFor(dir, url, "domains", subscription.ReadCachedList(dir, url, "domains")); etag != `"v1"` {
		t.Fatalf("etag = %q, want %q for an intact pair", etag, `"v1"`)
	}

	// Body replaced behind the sidecar's back, as an interrupted save would.
	if err := os.WriteFile(filepath.Join(dir, subscription.ListCacheFilename(url, "domains")), []byte("v2.example\n"), 0o644); err != nil {
		t.Fatalf("write body: %v", err)
	}
	etag, lastMod := subscription.ValidatorsFor(dir, url, "domains", subscription.ReadCachedList(dir, url, "domains"))
	if etag != "" || lastMod != "" {
		t.Errorf("validators = %q / %q, want both empty for a mismatched pair", etag, lastMod)
	}
}

// TestSaveCachedList_RecordsDigest pins that every save stores the digest the
// mismatch check depends on.
func TestSaveCachedList_RecordsDigest(t *testing.T) {
	dir := t.TempDir()
	url := "http://lists.example.com/domains.lst"
	body := []byte("a.example\n")

	if err := subscription.WriteCachedList(dir, url, "domains", body); err != nil {
		t.Fatalf("WriteCachedList: %v", err)
	}
	meta, ok := subscription.ReadListMeta(dir, url, "domains")
	if !ok {
		t.Fatal("sidecar missing")
	}
	if meta.Digest != subscription.ListDigest(body) {
		t.Errorf("digest = %q, want %q", meta.Digest, subscription.ListDigest(body))
	}
}
