# Route list cache reference

Detail behind the one-line rules in `AGENTS.md` → *Config Model → Route list cache*.

Applies to `route.domain_urls` / `route.ip_urls` on a subscription. Cache lives in
`/etc/horn-vpn-manager/lists/subscriptions/`, one `<kind>-<hash>.lst` plus a `<kind>-<hash>.meta.json`
sidecar per URL.

## Freshness

- **A cached list is never served without a bounded staleness.** `--cached-lists` used to mean "if the file is there, use it", and the only writer that ever refreshed it was `routing run --with-subscriptions`. A list narrowed on the server therefore kept routing domains to the wrong outbound on every subscriptions run until the next routing run — and because subscription rules are emitted **before** the template's static rules (`mergeRoute`), a stale broad `domain_suffix` claims domains a later static rule was written to route elsewhere. The cache now carries an age (`ListMeta.FetchedAt`): younger than `fetch.list_cache_ttl` it is served as is, older it is revalidated with `If-None-Match` / `If-Modified-Since`, so a 304 costs no body and a 200 lands on the same run that noticed the change. GitHub issue #2.
- **Every list URL logs its entry count and its real source on one line** (`network`, `cache, age 2h13m`, `cache, revalidated (304)`, `cache, age … — refresh failed`), at info level, or at warning level when it contributed no entries. The old code printed `downloading N domain list URL(s)` before it even looked at the cache, so a run that downloaded nothing was indistinguishable in the log from one that did — which is exactly why the stale config in issue #2 could not be diagnosed from the log at all. Never reintroduce a source-agnostic "downloading" line.
- All fetches send `Cache-Control: no-cache`: an intermediary answering from its own cache produces the same stale-list symptom with nothing to see in either the log or the config. This is also the only part of the fix that reaches the flagless "live refresh" mode, which touches no cache at all.

## Cache entry integrity

- `ReadCachedList` treats a **zero-byte file as a miss**, not as a list of zero entries: served as a list it silently drops every route rule built from that URL, and a subscription's domains then fall through to `route.final`.
- Every cached list has a `<kind>-<hash>.meta.json` sidecar (URL, kind, validators, `fetched_at`). `index.json` is written only by the prefetch command, so without the sidecar a file written by `subscriptions run` is indistinguishable from an orphan. A cache entry with no sidecar is legacy and falls back to the list file's mtime for its age.
- **`PruneListCache` is keyed on the configured URLs, never on what a run managed to download.** A URL that failed today must keep its copy — that copy is the fallback keeping its route rules alive. Without pruning, a URL that is removed and later re-added is served from an arbitrarily old orphan without a single request.
- **Validators are only sent when the sidecar provably describes the body on disk.** The body and the sidecar are two files and cannot be renamed as one unit, so a crash between them pairs a new list with old validators; sending those lets the server answer 304 for a body it never served and pins the wrong routing data indefinitely. `ListMeta.Digest` makes the pair checkable and `ValidatorsFor` drops them on a mismatch, forcing a full refresh. A legacy sidecar with no digest is trusted.
- **A `304` is only accepted as the answer to validators the request actually carried.** `fetch.Download` hands its body straight to a cache writer, so a server or intermediary answering 304 to an unconditional request would otherwise produce a successful *empty* body: `routing.Run` writes it over the domain cache and reloads dnsmasq, emptying the routed set. An unconditional 304 is treated as any other unexpected status. Pinned by `TestDownload_unconditional_304_is_an_error`.

## Concurrency

- **A route list is resolved once per run** (`ListRunCache`, claimed before any request is planned). Phase 2 resolves subscriptions concurrently, so a URL two of them share was fetched twice; if the list changed mid-run one fetch could answer 304 with the old revision while the other returned the new one, and a single generated config would carry both. Owners publish every list they claimed *before* waiting on any list somebody else owns — the reverse order deadlocks two subscriptions that share two URLs.
- **Writes to one cache entry are serialised on the entry's filename** (`lockCacheEntry`). Phase 2 processes subscriptions concurrently and the filename derives from the URL alone, so two subscriptions listing the same route list URL write the same two files: without the lock they collide on the shared `.tmp` path (the loser's rename fails with `ENOENT`) and can leave one response's body next to the other's validators — the self-inconsistent entry revalidation assumes cannot exist. Pinned by `TestSaveCachedList_ConcurrentSameURL`.
