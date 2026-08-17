# Repository Guidelines

## Status

`horn-vpn-manager` has been rewritten in Go. The shell-based implementation is gone.

Current state:

- `horn-vpn-manager` is a Go application; the binary is `vpn-manager`.
- Shell is used only for OpenWrt package lifecycle glue (init script).
- A single `config.json` replaces the old `subs.json` + `domains.json` split.
- The CLI supports running subscriptions and routing updates independently.
- Both pipelines can be placed on separate cron schedules.
- Runtime dependencies on `jq`, `curl`, `awk`, `sed`, `grep`, `base64`, `md5sum`, and `gzip` have been removed from the core path.
- `horn-vpn-manager-luci` has been rewritten to work with the new Go core.

## Project Structure & Module Organization

This repository contains two OpenWrt packages plus local Docker-based build tooling used to assemble them.

### Root tooling

- `Makefile` — main entry point for local development: builds Docker images, packages, shells, and lint checks
- `Dockerfile` — OpenWrt SDK builder image
- `docker/entrypoint.sh` — syncs package sources into the SDK and builds `horn-vpn-manager` / `horn-vpn-manager-luci`
- `scripts/` — packaging helpers (`package-apk.sh`, `package-ipk.sh`, `package-luci-apk.sh`, `package-luci-ipk.sh`) invoked from inside the SDK container
- `docs/plans/` — design notes and implementation plans
- `bin/` — local build output (`.apk` and `.ipk` artifacts); treat as generated output, not source of truth
- `AGENTS.md` is the canonical guidelines file; `CLAUDE.md` is a symlink to it — edit `AGENTS.md`

### `horn-vpn-manager` (core package)

The core package is a Go binary named `vpn-manager`.

Package layout:

- `horn-vpn-manager/Makefile` — OpenWrt package definition for the Go-based core
- `horn-vpn-manager/files/horn-vpn-manager.init` — boot-time init script (thin POSIX sh wrapper)
- `horn-vpn-manager/files/sing-box.template.default.json` — default sing-box template shipped with the package
- `horn-vpn-manager/files/config.example.json` — annotated config example shipped with the package
- `horn-vpn-manager/cmd/vpn-manager` — CLI bootstrap
- `horn-vpn-manager/internal/` — application internals
- `horn-vpn-manager/internal/<pkg>/testdata/` — per-package fixtures and golden files (e.g. `internal/subscription/testdata`)

Internal package split:

- `internal/config` — single config schema, loading, validation
- `internal/fetch` — HTTP fetch, retries, gzip/base64 detection, bounded parallelism
- `internal/subscription` — subscription orchestration and tag planning
- `internal/proto` — the `Node` contract every protocol implements, plus the TLS structs shared between protocol packages
- `internal/nodes` — scheme → parser dispatcher; the only non-test importer of the protocol packages
- `internal/vless` — VLESS parsing, stable node identity and the VLESS outbound
- `internal/hysteria2` — hysteria2 parsing, stable node identity and the hysteria2 outbound
- `internal/routing` — domain/IP/subnet aggregation and route rule assembly
- `internal/singbox` — typed `sing-box` config generation
- `internal/system` — atomic writes, service reloads, firewall and dnsmasq integration
- `internal/logx` — structured, colored CLI logging

### `horn-vpn-manager-luci` (LuCI addon)

`horn-vpn-manager-luci` has been rewritten for the Go core. The rpcd backend and frontend now speak the new `config.json` format.

Package contents:

- `horn-vpn-manager-luci/Makefile` — LuCI package definition
- `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager` — rpcd backend (reads/writes `config.json`, calls `vpn-manager` binary)
- `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js` — main LuCI view
- `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/style.css` — frontend styles
- `horn-vpn-manager-luci/root/usr/share/rpcd/acl.d/horn-vpn-manager.json` — ubus ACL
- `horn-vpn-manager-luci/root/usr/share/luci/menu.d/horn-vpn-manager.json` — menu entry
- `horn-vpn-manager-luci/po/{en,ru}/horn-vpn-manager.po` — translations
- `horn-vpn-manager-luci/tools/po2lmo.py` — PO→LMO compiler for translations; changing a msgid means a device needs the regenerated `.lmo`, which both the LuCI `Makefile` and `scripts/package-luci-{apk,ipk}.sh` produce at package time — no `.lmo` is checked in, so `make build-luci` is the whole step
- `horn-vpn-manager-luci/tests/` — Node/`dash` harness for the view and the rpcd backend; not shipped in the package (`package-luci-apk.sh` copies only `root/`)

Tab order: Subscriptions → Routing → Run → Sing-box template config → Additional domains → Sing-box logs → Test

UI features:
- Import/export config buttons available on all tabs
- Subscription cards include `include` field (same shape as `exclude`)
- Run tab replaces old Update tab; has independent Subscriptions and Routing sections with per-command flag options and live log polling
- Subscription cards toggle between a remote `url` and inline `nodes`; a node URI is accepted when its scheme is in `NODE_URI_SCHEMES`
- Subscription cards carry a fallback-chain editor (ordered backup pickers plus `blacklist_timeout`) with client-side unknown/self/duplicate/cycle checks
- Subscriptions tab carries the global `singbox.connect_timeout` input

rpcd methods (current):
- `get_config` / `set_config` — reads/writes `config.json` (subscriptions + singbox settings)
- `set_full_config` — atomic write of both config and template in one call
- `get_template` / `set_template` / `reset_template` — manage sing-box template
- `get_domains_config` / `set_domains_config` — read/write `config.json → routing` section
- `get_manual_ips` / `set_manual_ips` — manual IP/CIDR list
- `get_manual_domains` / `set_manual_domains` — manual domain list
- `run_script` — run `vpn-manager subscriptions run` (supports `--cached-lists`, `--download-lists`, dry-run)
- `run_routing` — run `vpn-manager routing run` (supports `--with-subscriptions`)
- `get_log` — read `/tmp/horn-vpn-manager-subscriptions.log`
- `get_routing_log` — read `/tmp/horn-vpn-manager-routing.log`
- `get_sb_status`, `set_proxy`, `test_delays`, `test_url`, `get_syslog`, `get_sync_status` — sing-box/proxy helpers

Removed methods (replaced): `run_getdomains`, `get_domains_log`

LuCI invariants (each one was a real bug):

- **A save must not drop fields the JS does not know about.** `_collectConfig` starts from a copy of the subscription as loaded (`_widgets[idx].raw`) and of `singbox` (`_rawSingbox`), then assigns or deletes known keys via `setOrDelete` — never rebuild either object from an allow-list. A no-op save must leave `config.json` byte-identical.
- Only write a field whose input was actually rendered: `interval` / `tolerance` inputs exist only for a multi-node subscription with proxy data, so an unguarded write deletes them whenever sing-box is down.
- rpcd merges `singbox` additively (`$esb + $isb`), so dropping a key on save cannot clear a stored value. Clearing a field that *was* set emits `""` (the core treats empty as unset); a field that was never set stays absent.
- Because of that merge, `handleSave` re-seeds `_rawSingbox` from the payload it just sent. The view does not reload after a save, so a key first set in this page session would otherwise stay missing from the snapshot and a later clear would drop it instead of emitting `""` — the field looks empty while the router keeps the old value.
- **`E()` string children go through `innerHTML`.** Any text that is concatenated or comes from outside the view — provider node names, backend error messages, filenames — must be set with `textContent` (`nodeNameSpan` / `textP`), never passed as an `E(...)` child.
- Maps keyed by subscription id use `Object.create(null)`: the ID field is free text, so an id like `__proto__` or `constructor` would otherwise not become an own key and the subscription would silently vanish from the save.
- Client-side validation must never be stricter than the core. A disabled subscription needs no `url`/`nodes`, so rejecting it blocks a save over something the core accepts; when in doubt let `check_with_core` deliver the error.
- `isValidNodeUri` matches `NODE_URI_SCHEMES` against `u.protocol` (already lowercased by `URL()`), not a literal prefix, so `VLESS://` passes the client and is rejected by the core — the safe direction for the rule above. Userinfo presence is `u.username || u.password`: hysteria2 auth is the **whole** userinfo and may be `user:password`, which `URL()` splits across both fields, so `u.username` alone would reject `hysteria2://:pass@host` that the core accepts. Adding a protocol to the core means adding its scheme here too.
- rpcd `jq` checks compare against `"${bad:-1}"`. An unguarded `[ "$bad" -gt 0 ]` errors out when `jq` aborts on a malformed payload and the script falls through — accepting it. Fail closed.
- `check_with_core` writes its candidate to an `mktemp` path, not a `$$`-derived one: rpcd runs as root on a world-writable `/tmp`, so a predictable name lets a local process pre-plant a symlink and have root truncate an arbitrary file.
- **Every write of `config.json` or the template goes through `write_private`**, which creates the temp file inside `(umask 077; …)` before the `mv`. Both files carry credentials — provider subscription URLs, inline node URIs (a `hysteria2://` URI *is* its password), a hand-written outbound password — and rpcd inherits a `0022` umask, so a plain redirect publishes them as `0644`. `mv` replaces the destination *together with its mode*, so it also silently un-hardens a file an operator had already chmodded. The umask applies at creation, so the file is never briefly world-readable. `rpcd-checks.test.sh` asserts the mode after every accepted `set_config` / `set_full_config` / `set_template` / `reset_template` / `set_domains_config`. The manual-IP list and `/etc/config/dhcp` are deliberately not routed through it: neither holds secrets, and dhcp's mode is system state.
- **Every `write_private` call is checked, and the update flag is only touched after it succeeds.** A failed write — read-only filesystem, full disk, a directory sitting on the `.tmp` path — otherwise still `touch`es `.needs-update-*` and replies `{"result":"ok"}`, so LuCI shows a saved config the router never received and the sync badge claims the config is merely pending. The same rule covers **every** `jq` merge that feeds a write — `set_config`, `set_full_config`, `set_template`, `reset_template`, `set_domains_config`: `jq` prints nothing when the `config.json` it was handed is malformed, so an unchecked `$merged`/`$updated` replaces the config with a blank document and still replies `{"result":"ok"}`. `rpcd-checks.test.sh` drives each writing method with a pre-created `<file>.tmp` **directory**, which makes the redirect fail as root too, and asserts an error reply, an unchanged file and no flag; a separate case feeds `set_template` a malformed `config.json`.
- **A handler that writes both the template and `config.json` must leave neither half applied on failure.** `config.json` is what points sing-box at the template, so a template swapped in before a config write that then fails silently changes what the router runs while the reply says error. `set_template` and `set_full_config` snapshot the template (`snapshot_file`) before replacing it and roll it back (`restore_file`, which removes a file that did not exist before) on either write failure; `reset_template` instead writes the config **before** `rm -f`, since the reverse order leaves the config pointing at a file that is already gone. **No `.bak.*` copy may survive either exit.** The rollback moves the snapshot back over the target rather than copying, and the success path drops it with `discard_snapshot` after the config write lands — `snapshot_file` uses `cp -p`, so a snapshot taken from a legacy `0644` template keeps that mode and would outlive the very save that hardened the original. `rpcd-checks.test.sh` asserts the config dir is free of `*.bak.*` on both the rollback and the `"result":"ok"` path.
- The rpcd backend keeps sh-level checks structural only (types, presence, XOR) and delegates schema validation to `vpn-manager check -c <tmp>` on the **merged** candidate (`check_with_core`), rather than reimplementing cross-reference logic in a regex-less `jq`. It accepts on the structural checks alone when the core is unreachable, so a partially installed system can still save.
- Error replies go through `fail_json`, which JSON-escapes the message — core errors quote subscription ids.
- **`run_script` / `run_routing` clear `.needs-update-*` only when the core exited 0, and append its stderr to the log.** Clearing the flag unconditionally makes the sync badge claim the router is up to date with a config it never applied — now a live failure mode, since a run refused by the run lock exits non-zero. The core's final `error: …` line is printed to stderr only, so `2>/dev/null` left the Run tab with a log that simply stops. `rpcd-checks.test.sh` drives both outcomes against a stub core.

## Config Model

The core config is a single JSON file at `/etc/horn-vpn-manager/config.json`.

Top-level structure:

- `singbox` — settings directly related to `sing-box` (log level, test URL, template path, `connect_timeout`)
- `fetch` — global download/runtime settings (retries, timeout, bounded parallelism, `list_cache_ttl`)
- `routing` — global routing sources (dnsmasq domains URL, subnet URLs, manual IP file)
- `subscriptions` — keyed subscription definitions; keys are stable IDs and must remain object keys, not array items

Per-subscription fields: `name`, `url`, `nodes`, `default`, `enabled` (optional, defaults true), `include`, `exclude`, `interval`, `tolerance`, `retries` (optional, overrides global), `fallback` (optional chain), `route` (optional nested routing)

Node source (`url` XOR `nodes`):

- `url` and `nodes` are mutually exclusive; an enabled subscription must have exactly one of them (an empty `url` string counts as absent)
- `nodes` is a list of inline node URIs for a self-hosted node — any scheme the dispatcher knows (`hy2://`, `hysteria2://`, `vless://`) — validated with `nodes.Parse` at config load, and fetched over no HTTP at all
- everything else (`include`/`exclude`, `route`, `default`, `fallback`) behaves identically for both sources

Fallback chains (`fallback`):

- `fallback: {subscriptions: [...], blacklist_timeout: "1m"}` is allowed on **any** enabled subscription, not just the default one
- generates a `<id>-fallback` group listing the declaring subscription's own final tag first, then each referenced subscription's final tag in declared order (a backup that itself declares a chain contributes its `<backup>-fallback` tag)
- on the **default** subscription the chain becomes `route.final`; on a **non-default** one it retargets that subscription's own route rules and leaves `route.final` alone
- validation rejects unknown/disabled/self/duplicate references, an empty chain, and cycles of any length
- a backup that produced no plan is dropped from the chain with a warning; regeneration is never aborted over a backup
- switching to a backup changes the public egress IP and does not migrate established connections

`connect_timeout` and `interrupt_exist_connections`:

- `singbox.connect_timeout` (a `time.ParseDuration` string) is emitted as a dial field on every node outbound; empty omits the field entirely. Duration fields (`connect_timeout`, `interval`, `fallback.blacklist_timeout`) must be **positive**: `time.ParseDuration` accepts `"0"` and a leading `-`, and a non-positive value would be written through to sing-box, so validation checks the sign separately
- generated `urltest` and `selector` groups always emit `interrupt_exist_connections: true`, so operators should raise per-subscription `tolerance` (~300 ms; the default is 100) to keep `urltest` from cutting live connections on benign latency jitter
- the generated `fallback` group carries exactly `type`, `tag`, `outbounds`, `blacklist_timeout` — `FallbackOutboundOptions` in the extended build and nothing else. sing-box decodes outbound options with unknown fields disallowed, so adding `interrupt_exist_connections` (which `selector` and `urltest` *do* accept) makes `sing-box check` reject the entire config. Do not harmonise the three group types

Route list cache (`route.domain_urls` / `route.ip_urls`):

- **A cached list is never served without a bounded staleness.** `--cached-lists` used to mean "if the file is there, use it", and the only writer that ever refreshed it was `routing run --with-subscriptions`. A list narrowed on the server therefore kept routing domains to the wrong outbound on every subscriptions run until the next routing run — and because subscription rules are emitted **before** the template's static rules (`mergeRoute`), a stale broad `domain_suffix` claims domains a later static rule was written to route elsewhere. The cache now carries an age (`ListMeta.FetchedAt`): younger than `fetch.list_cache_ttl` it is served as is, older it is revalidated with `If-None-Match` / `If-Modified-Since`, so a 304 costs no body and a 200 lands on the same run that noticed the change. GitHub issue #2.
- **Every list URL logs its entry count and its real source on one line** (`network`, `cache, age 2h13m`, `cache, revalidated (304)`, `cache, age … — refresh failed`), at info level, or at warning level when it contributed no entries. The old code printed `downloading N domain list URL(s)` before it even looked at the cache, so a run that downloaded nothing was indistinguishable in the log from one that did — which is exactly why the stale config in issue #2 could not be diagnosed from the log at all. Never reintroduce a source-agnostic "downloading" line.
- `ReadCachedList` treats a **zero-byte file as a miss**, not as a list of zero entries: served as a list it silently drops every route rule built from that URL, and a subscription's domains then fall through to `route.final`.
- Every cached list has a `<kind>-<hash>.meta.json` sidecar (URL, kind, validators, `fetched_at`). `index.json` is written only by the prefetch command, so without the sidecar a file written by `subscriptions run` is indistinguishable from an orphan. A cache entry with no sidecar is legacy and falls back to the list file's mtime for its age.
- **`PruneListCache` is keyed on the configured URLs, never on what a run managed to download.** A URL that failed today must keep its copy — that copy is the fallback keeping its route rules alive. Without pruning, a URL that is removed and later re-added is served from an arbitrarily old orphan without a single request.
- **A `304` is only accepted as the answer to validators the request actually carried.** `fetch.Download` hands its body straight to a cache writer, so a server or intermediary answering 304 to an unconditional request would otherwise produce a successful *empty* body: `routing.Run` writes it over the domain cache and reloads dnsmasq, emptying the routed set. An unconditional 304 is treated as any other unexpected status. Pinned by `TestDownload_unconditional_304_is_an_error`.
- **Validators are only sent when the sidecar provably describes the body on disk.** The body and the sidecar are two files and cannot be renamed as one unit, so a crash between them pairs a new list with old validators; sending those lets the server answer 304 for a body it never served and pins the wrong routing data indefinitely. `ListMeta.Digest` makes the pair checkable and `ValidatorsFor` drops them on a mismatch, forcing a full refresh. A legacy sidecar with no digest is trusted.
- **A route list is resolved once per run** (`ListRunCache`, claimed before any request is planned). Phase 2 resolves subscriptions concurrently, so a URL two of them share was fetched twice; if the list changed mid-run one fetch could answer 304 with the old revision while the other returned the new one, and a single generated config would carry both. Owners publish every list they claimed *before* waiting on any list somebody else owns — the reverse order deadlocks two subscriptions that share two URLs.
- All fetches send `Cache-Control: no-cache`: an intermediary answering from its own cache produces the same stale-list symptom with nothing to see in either the log or the config. This is also the only part of the fix that reaches the flagless "live refresh" mode, which touches no cache at all.
- **Writes to one cache entry are serialised on the entry's filename** (`lockCacheEntry`). Phase 2 processes subscriptions concurrently and the filename derives from the URL alone, so two subscriptions listing the same route list URL write the same two files: without the lock they collide on the shared `.tmp` path (the loser's rename fails with `ENOENT`) and can leave one response's body next to the other's validators — the self-inconsistent entry revalidation assumes cannot exist. Pinned by `TestSaveCachedList_ConcurrentSameURL`.

Node identity:

- `StableHash` is a **tag** function, not an identity function: the VLESS implementation hashes 13 of the node's 17 fields and omits `name`, `ALPN`, `Mode` and `HeaderType` — the last three each change the rendered outbound. Equal hash therefore does not mean identical node.
- Deduplication in `BuildOutbounds` is keyed on the marshalled tagless outbound (`n.Outbound("", connectTimeout)`), not on the hash. The `seenTags` `-N` suffix stays, because two genuinely distinct nodes can still collide on a tag; the counter advances for skipped duplicates too, so dropping a duplicate never renames a surviving node.
- Do not widen what `StableHash` covers: it would rewrite every tag and invalidate `subs-tags.json`, saved selector choices and `experimental.cache_file` state.

Conventions:

- `singbox`, not `sing-box`, for easier handling in Go and tooling
- Explicit field names: `url`, `urls`, `manual_file`, `ip_cidrs`
- Per-subscription routing lives under a nested `route` object
- When generating `sing-box` config, use the official `sing-box` documentation as the source of truth: `https://sing-box.sagernet.org/configuration/`
- **Documented exception:** the `fallback` outbound type does not exist upstream — it is provided only by [`sing-box-extended`](https://github.com/shtorm-7/sing-box-extended), like `xhttp`. A config using `fallback` is rejected by a stock build with `unknown outbound type`, which `ApplySingbox` surfaces with a hint naming the extended-build requirement. `horn-vpn-manager/Makefile` deliberately declares **no** sing-box `DEPENDS`: the stock and extended packages conflict and the extended one is usually installed by hand, so a hard dependency would break installation; the requirement is enforced at runtime by `sing-box check` instead.

## Node Protocol Layer

Node protocols are pluggable. `internal/proto` owns the contract, each protocol package implements it for its own URI scheme, and `internal/nodes` maps a scheme to its parser. `BuildOutbounds`, `decode.go` and `config.go` speak only `proto.Node` and `nodes.Parse` — none of them names a protocol.

The contract (`internal/proto`):

```go
type Node interface {
    Type() string                            // sing-box outbound type
    Server() string
    Port() int
    Name() string                            // display name from the URI fragment
    StableHash() string                      // 8 hex chars; input format frozen per protocol
    Outbound(tag, connectTimeout string) any // typed sing-box outbound struct
}
```

- `proto` also owns `OutboundTLS`, `UTLSConfig` and `RealityTLS`. They live there, not in `internal/subscription`, because that is what keeps the import graph acyclic: protocol packages import `proto`, and `subscription` imports the protocol packages.
- `Outbound` returns a protocol-specific typed struct as `any`, so each protocol keeps its own JSON tags and field order. An empty `tag` yields the tagless form `BuildOutbounds` uses as the dedup key; an empty `connectTimeout` omits the field.
- Tags are carried by `OutboundPlan.NodeTags` (parallel to `NodeOutbounds`), never read back off an outbound — the plan stores `any`, which has no `Tag` field.

Adding a protocol:

1. new package under `internal/<protocol>` implementing `proto.Node`, owning its own outbound struct;
2. one entry in the `parsers` map in `internal/nodes/nodes.go` (one per accepted scheme — `hysteria2` and `hy2` share a parser);
3. its own `StableHash` prefix (`vless|…`, `hysteria2|…`), which makes cross-protocol tag collisions structurally impossible;
4. the LuCI scheme allow-list (`NODE_URI_SCHEMES` in `config.js`), or the frontend rejects the URI the core accepts.

Two shapes both protocol packages follow, for reasons that bite otherwise:

- **`Node` fields are unexported, with an accessor each, and `Parse` is the only constructor.** `proto.Node` requires `Server()`, `Port()` and `Name()` methods, and a Go type cannot carry a field and a method under the same name.
- **The `parsers` map entries are named adapter functions, not one-line closures.** `return vless.Parse(uri)` returns a typed nil `*vless.Node` on the error path, which becomes a **non-nil** `proto.Node` interface; each adapter returns an explicit `nil` instead. Pinned by `TestParse_PropagatesProtocolError`.

Scheme matching in the core is case-sensitive, matching each parser's own `strings.HasPrefix` check: `VLESS://` is an unknown scheme, not a silently accepted one.

Nothing else changes: `decode.go` filters lines via `nodes.IsKnownScheme`, `config.go` validates inline `nodes` via `nodes.Parse`, and error messages list `nodes.Schemes()`.

Registration is an explicit map, not `init()` side-effects: `internal/nodes` holds the only non-test imports of the protocol packages, so an import-side-effect registry would be empty at runtime the moment a blank import went missing — the build would still succeed and every subscription would silently fail to parse. A map literal also gives deterministic scheme ordering for error messages for free.

Invariants:

- **The VLESS hash string is frozen.** `vless.StableHash` must keep its exact `vless|server|port|uuid|security|sni|pbk|sid|flow|fp|type|path|host|serviceName` layout. Adding a type field to the hash input, or reordering it, repoints every saved selector choice on every deployed router. `internal/vless` and `internal/hysteria2` each pin their hash with a `TestStableHash_Golden` whose md5 values were computed **outside Go** (`printf … | md5sum`), with the exact hash input recorded per case.
- **`internal/subscription/testdata/golden_vless_config.json` is the regression gate for tag stability.** `TestRenderedConfig_MatchesGolden` renders fixed VLESS subscriptions through `singbox.RenderConfig` and compares bytes. There is deliberately no `-update` flag: a diff means node tags moved, invalidating `subs-tags.json`, saved selector choices and `experimental.cache_file` on every deployed router. Never regenerate it to make a diff go away.
- **Groups are protocol-agnostic by construction.** `urltest`, `selector` and `fallback` reference members by tag only, so a subscription mixing protocols shares one `urltest`/`selector` pair and a `fallback` chain can cross protocols without any change to group generation.
- **JSON subscription decoding stays VLESS-only.** `jsondecode.go` converts V2Ray/Xray outbounds, a format that carries VLESS; there is no evidence of providers shipping hysteria2 that way, and speculative conversion is unverifiable.
- **Node URIs carry credentials and must never appear in an error.** The message travels to the subscriptions log and, through rpcd `check_with_core` → `fail_json`, to a LuCI notification. Two places have to cooperate: `nodes.Parse` and both protocol packages never quote the URI, and `internal/config` locates a bad inline node by position (`subscription %q has an invalid node at position %d`). The subtle half is `url.Parse`, whose `*url.Error` renders as `parse "<the whole URI>": <reason>` — each protocol's `Parse` unwraps it through its own `parseReason` helper and wraps only the reason. Pinned by `TestParse_ErrorsDoNotLeakCredentials` (`internal/nodes`), which covers every parser and every rejection path, not just an unknown scheme.
- **Widening the accepted schemes can change a subscription's topology.** A provider payload that yielded one VLESS node before and now also yields hysteria2 nodes becomes multi-node: its final tag moves from `<id>-single` to `<id>-manual`, so the saved selector choice and the `clash.db` entry stop resolving and a node has to be re-picked once in LuCI. `warnTopologyShift` (`decode.go`) logs exactly that on the `1 vless + n new-scheme` case; the 0→n case stays silent because such a payload did not decode at all before. This is a one-time event per affected subscription, not a bug.
- `warnTopologyShift` is called from the pipeline (`subscription.go`, both the default and the `processSub` path) **after `BuildOutbounds`**, not from `DecodePayload`. Two filters run between decoding and the plan and each one can undo the shift: include/exclude can drop the new-scheme nodes, and `BuildOutbounds` skips a node URI the parser rejects (`obfs=gecko`, empty auth), so `1 vless + 1 broken hysteria2` stays single-node. It therefore takes the built plan and warns on `len(plan.NodeTags) >= 2`, counting nodes from `NodeTags` (dedup has already run) and legacy nodes only from URIs that `nodes.Parse` accepts. Warning any earlier is a false alarm repeated on every cron run. It is not called for inline `nodes` at all — those have no pre-multi-protocol tag to invalidate — and it takes the subscription id, since phase 2 decodes concurrently and an unattributed warning cannot be traced back.

hysteria2 specifics:

- schemes `hysteria2://` and `hy2://` are both official; the **port is optional and defaults to 443**, and auth is the **whole userinfo component**, which may itself contain a colon (`url.User.String()`, percent-decoded — `Username()` alone truncates it)
- `alpn`, `upmbps` and `downmbps` are client extensions, not URI-spec fields; leaving both bandwidth values unset selects BBR instead of Brutal
- `tls` is required on the outbound and always emitted; `ignore_client_bandwidth` and `masquerade` are inbound-only and must not be
- sing-box implements only `salamander` obfuscation, so any other `obfs` value (including the spec's `gecko`) is rejected at parse — as is `salamander` with an empty password, which would otherwise fail `sing-box check` on the whole generated config instead of skipping one node. The mirror case is rejected too: `obfs-password` without `obfs` renders an **unobfuscated** outbound, because `NewOutbound` emits the block only when `obfsType` is set. Accepting it either breaks the handshake against an obfuscated server or sends plain QUIC — the thing the operator wrote the password to avoid
- port hopping (`mport` / `server_ports` / `hop_interval`), `pinSHA256` and `ech` are out of scope

The endpoint boundary (AmneziaWG / WireGuard):

- WireGuard-family protocols render into sing-box `endpoints`, not `outbounds`, and have no URI form, so they cannot satisfy `proto.Node` — whose whole purpose is producing an outbound from a URI. Do not stretch the contract to fit them.
- They would need their own `config.json` key (not inline `nodes`) and their own section in `singbox.RenderConfig`, which already preserves unknown top-level keys including `endpoints` verbatim — so nothing in this layer blocks adding them. Whether an endpoint may appear in a `fallback` chain is an open question for that work.

## CLI Model

Subscriptions and routing are independent execution units.

Command shape:

- `vpn-manager subscriptions run`
- `vpn-manager subscriptions dry-run`
- `vpn-manager routing run`
- `vpn-manager routing restore`
- `vpn-manager check`
- `vpn-manager run` — convenience: runs routing then subscriptions (for initial bootstrap)

Flags available on most subcommands:

- `-c / --config` — path to config file (default: `/etc/horn-vpn-manager/config.json`)
- `-v / -vv / -vvv` — verbosity
- `--no-color` — disable colored output
- `--debug` — local debug mode: config/template from binary dir, output to `./out`, no system actions

Additional routing flags:

- `--with-subscriptions` — after routing, also pre-fetch subscription route lists into the cache

Additional subscriptions flags:

- `-t / --template` — path to sing-box template
- `--download-lists` — always download fresh per-subscription route lists and cache them
- `--cached-lists` — prefer the pre-fetched cache; a copy older than `fetch.list_cache_ttl` is revalidated, not blindly reused

Design constraints:

- subscriptions must be runnable without touching routing caches or dnsmasq state
- routing must be runnable without downloading subscriptions or regenerating proxy groups
- both command families must be idempotent
- both command families must be safe to place on different cron schedules
- logging and exit codes must make separate cron usage operationally clear

Concurrency and applied state:

- **The lock is taken before the log file and before the config is read.** `logx.SetLogFile` truncates the log another run is still writing — the Run tab then shows a live run's log vanishing — and a config loaded before a wait of several minutes can be a generation out of date by the time it is applied. Order is: parse flags → `logx.Setup` → lock → `SetLogFile` → `config.Load`.
- **Every command that writes state takes an exclusive flock on `<config dir>/.run.lock`** (`system.AcquireRunLock`): `routing run`, `routing restore`, `subscriptions run`, `subscriptions dry-run`. `check` does not — rpcd calls it on every save and must not fail because cron is running. Routing and subscriptions share the route-list cache and both touch system services, so an overlap lets subscriptions build the config from the copy routing is replacing; the result is an applied config one revision behind with nothing in either log. The lock waits up to five minutes and then fails with `ErrLocked` rather than proceeding — sized against a whole `routing run --with-subscriptions`, since cron entries collide by construction (`0 */6` and `0 */12` fire together every 12 hours) and the right outcome is for subscriptions to wait and then build from the cache routing just refreshed. `runBoth` is safe because each phase acquires and releases in turn — do not hoist the lock around both, an flock on a second fd in the same process blocks just the same.
- **"Already applied" means an applied-revision marker, not equal file contents.** A run killed between promoting a file and restarting the service leaves the new file live and the old config in the running process; comparing files alone then reads as "already applied" on every later run and skips the restart *forever*. `OpenWrt` writes `<StateDir>/.applied-<service>` (a digest) only after a restart succeeds, drops it when a restart fails, and requires the service to be running before skipping. An empty `StateDir` disables the optimisation, which is the old unconditional-restart behaviour. Pinned by `TestApplySingbox_promoted_but_never_restarted` and `TestApplySingbox_failed_restart_clears_marker`.
- **A pipeline compares what it is about to apply with what is live, and skips the service restart when they match.** `ApplySingbox` restarting on an identical config tears down every established connection on a schedule; `ApplyDomains` restarting dnsmasq flushes the DNS cache the same way. `ApplySingbox` still restarts when `/etc/init.d/sing-box running` fails, so skipping never leaves a stopped service down — an init script without a `running` action reads as "not running" and keeps the old unconditional behaviour. **The skip sits after `sing-box check`, never before it:** that check is the only place a config meets the sing-box binary actually installed, so swapping the extended build for the stock one has to keep surfacing `unknown outbound type` even on a run where the rendered config did not change. Pinned by `TestApplySingbox_unchanged_still_validates`.
- **A partial download must not narrow what is applied.** `routing.Run` writes the subnet cache only when **every** URL succeeded: the cache is a single merged file, so writing the survivors silently drops the failed lists' entries and the next firewall reload routes less than before, behind an error the operator has not read yet. All-failed and partial-failed both keep the previous cache; only the log line differs.

## On-Device Layout

- CLI: `/usr/bin/vpn-manager`
- Config dir: `/etc/horn-vpn-manager/`
- Main config: `/etc/horn-vpn-manager/config.json`
- List/cache dir: `/etc/horn-vpn-manager/lists/` (subscription route lists under `lists/subscriptions/`, each as `<kind>-<hash>.lst` plus a `.meta.json` sidecar)
- Run lock: `/etc/horn-vpn-manager/.run.lock`
- Applied-revision markers: `/etc/horn-vpn-manager/.applied-sing-box`, `.applied-dnsmasq`
- Generated `sing-box` config: `/etc/sing-box/config.json`
- Default template: `/usr/share/horn-vpn-manager/sing-box.template.json`
- Config example: `/usr/share/horn-vpn-manager/config.example.json`
- Logs: `/tmp/horn-vpn-manager-subscriptions.log`, `/tmp/horn-vpn-manager-routing.log`

## Build, Test, and Development Commands

- `make help` — list supported local tasks
- `make build` — build `.apk` packages for current platform (core + luci)
- `make build-all` — build `.apk` packages for all platforms (core + luci)
- `make build-core` / `make build-core-all` — build core `.apk` for single / all platforms
- `make build-luci` — build LuCI `.apk` only
- `make build-ipk` / `make build-ipk-all` — build `.ipk` packages (core + luci) for OpenWrt < 25 with opkg
- `make build-ipk-core` / `make build-ipk-core-all` / `make build-ipk-luci` — granular `.ipk` builds
- `make shell` — open an interactive shell inside the SDK container
- `make go-build` — build the `vpn-manager` binary natively into `bin/`
- `make go-test` — run Go tests
- `make go-fmt` / `make go-lint` — Go formatting check / `golangci-lint`
- `make lint` — aggregate Go checks (`go-fmt` + `go-lint`)
- `make luci-test` — LuCI view tests (`node --test`) and rpcd backend tests (`dash`), plus the `node --check` / `dash -n` syntax gates
- `make clean` — remove build output

Preferred checks before opening a change:

- `gofmt -w` on changed Go files
- `golangci-lint run`
- `go test ./...`
- `make lint`
- `make luci-test` when `config.js` or the rpcd script changes
- affected `make build*` target when packaging/build flow changes

If the task touches OpenWrt runtime integration, validate on an OpenWrt device or container rather than on the host OS.

## Coding Style & Naming Conventions

Primary language for the core is Go.

- Follow `.editorconfig` as the source of truth where applicable
- Keep the Go core free of CGO
- Prefer the standard library unless an external dependency clearly pays for itself
- Optimize for readability and testability over clever abstractions
- Keep public/package boundaries explicit; avoid dumping all logic into `main`
- Use `golangci-lint` as the primary Go lint runner
- Use typed models for generated `sing-box` config instead of shell-style string assembly
- When in doubt about `sing-box` fields, schema shape, or behavior, check the official docs first: `https://sing-box.sagernet.org/configuration/`
- Treat config schema changes as API design; name fields for long-term clarity
- Keep concurrency bounded and explicit; router-class hardware is slow and memory-constrained

For the init script and any remaining shell:

- Use POSIX `sh`
- Avoid Bash-only features
- Keep shell scripts thin; business logic belongs in Go

For LuCI JS, preserve the existing plain LuCI style unless the LuCI rewrite phase explicitly starts:

- RPC declarations at top
- DOM creation via `E(...)`
- no framework additions

## Testing Guidelines

Expected test coverage areas:

- config loading and validation
- node URI parsing, per protocol package: minimal URI, fully-populated URI, and each rejection
- scheme dispatch in `internal/nodes`, including the `hy2` alias and the unknown-scheme error text
- stable node hash generation, pinned per protocol against md5 values computed outside Go
- rendered sing-box output against `golden_vless_config.json` (tag-stability gate)
- payload decoding: raw, base64, base64url, gzip, and mixed-protocol payloads
- domain/IP/subnet validation and deduplication
- route rule generation
- `sing-box` config generation
- restore/apply planning
- independent execution of subscriptions and routing commands
- command behavior under separate cron-style invocation patterns
- route list cache freshness: a fresh copy served with no request, a stale one revalidated, a changed one picked up on the same run, a 304 refreshing the stored age, and a failed refresh falling back to the cache
- change detection: an unchanged sing-box config skipping the restart, a stopped service still restarting, an unchanged dnsmasq drop-in skipping the reload
- the run lock: a second caller rejected, a released lock reusable, a waiting caller cut short by its context

Preferred test layout:

- unit tests near packages
- `testdata/` for fixtures and golden outputs
- integration-style tests with `httptest.Server` for fetch/retry scenarios

Non-Go checks:

- LuCI JS is covered by `horn-vpn-manager-luci/tests/`. `load-view.js` evaluates the shipped `config.js` with `new Function`, the way LuCI itself does, against `stub-dom.js` — a dependency-free DOM/LuCI stub, no jsdom — so tests drive the **real** `_makeCard` / `_collectConfig` / `_validate`. Never assert on a reimplementation, and mutation-check every new test: revert the fix it covers and confirm the test fails, so it cannot pass vacuously.
- A stub for a **standard** global must extend the real one, never replace it. `load-view.js` used to pass `URL` as a plain object carrying only `createObjectURL`/`revokeObjectURL`, so `new URL(s)` inside `isValidNodeUri` threw and every node URI read as invalid under test — the rejection test passed for the wrong reason and no acceptance test could pass. It is now `class URLStub extends URL` with the two blob statics attached. A stub that makes a code path throw turns a negative assertion into a vacuous one, which is exactly what mutation-checking catches.
- `load-view.js` offers two entry points, and the difference matters. `mountSubscriptions` reproduces `render()`'s setup by hand, which keeps card-level tests small; `renderView` drives the **real** `render()` with the array `load()` resolves to and attaches the result to the document. Anything `render()` itself wires — the `_rawSingbox` snapshot, `_subIdx` — is only covered by the second, because the first assigns those values itself and would pass with the wiring deleted.
- `stub-dom.js` models `<select>.value` off the selected `<option>`, since that is the only channel `render()` uses for the stored log level, and resolves `getElementById` by walking the document rather than through a shared id map — a module-level map is overwritten by the next `loadView()` in the same process, which turns an assertion against the older ctx into a vacuous pass. Its value backing field is `_stubValue`, deliberately not `_value`: `config.js` keeps chain-picker bookkeeping in `sel._value`.
- `rpcd-checks.test.sh` covers the backend at two levels. It sources the real script with an unmatched `$1` so the `case` dispatcher falls through, then drives `check_sub_sources` / `fail_json` / `check_with_core` directly; and it runs the real `call set_config` / `call set_full_config` dispatcher against a temporary tree via `HORN_VPN_MANAGER_CONF_DIR` (the only reason `CONF_DIR` is overridable — rpcd never sets it). Only the second level catches a validation call being dropped or a write happening before it. Both stub `vpn-manager` on `PATH` to reach the core-rejection path.
- The `error: <msg>` line rpcd parses out of the core's stderr is a **cross-component contract**, pinned from both ends: `rpcd-checks.test.sh` drives the awk extraction against a stub, and `cmd/vpn-manager/main_test.go` builds the real binary and asserts a rejected config produces that line (and a valid one does not). Without the second, changing `main()`'s format silently downgrades every core rejection LuCI shows to a generic message.
- Run them with `make luci-test` (or `node --test horn-vpn-manager-luci/tests/*.test.js` — a bare directory argument does not work — and `dash horn-vpn-manager-luci/tests/rpcd-checks.test.sh`). `node --check` on `config.js` and `dash -n` on the rpcd script remain the minimum gates.
- Gate the rpcd script with `dash -n`, not the host `sh`: macOS `sh` is bash 3.2 and mis-parses a pre-existing `case`-inside-`$()`. `dash` is the closest available shell to OpenWrt `ash`. `shellcheck -s sh` is useful but reports pre-existing SC2221/SC2222.

## Performance & Binary Size Constraints

The binary must stay small and OpenWrt-friendly.

- avoid CGO
- build with size-conscious flags such as `-trimpath` and stripped symbols where appropriate
- avoid heavy CLI/config frameworks unless there is a strong reason
- use bounded concurrency; default low, scale only when justified
- avoid unbounded goroutine fan-out for subscriptions or list downloads

Operationally, favor a design where subscription updates can run more frequently than routing updates. Do not assume both flows always run together.

## Commit & Pull Request Guidelines

Use short imperative commit messages, preferably scoped:

- `feat: add go config loader`
- `fix: preserve stable node tags in go parser`
- `refactor: split routing pipeline from system apply`
- `build: compile go binary in openwrt package`

PRs should state:

- which package(s) are affected: `horn-vpn-manager`, `horn-vpn-manager-luci`, build tooling
- whether the change targets the Go core or OpenWrt packaging (`.apk` for OpenWrt ≥ 25, `.ipk` for OpenWrt < 25 with opkg)
- which checks were run

## Security & Configuration Tips

Do not commit live subscription URLs, credentials, router-specific configs, or generated `sing-box` output.

- device-local config files are configuration, not repository data
- `bin/` contains build artifacts and can become stale; rebuild instead of inferring behavior from old packages
- treat `/etc/config/dhcp`, firewall state, and dnsmasq state as user/system state, not source-controlled data
