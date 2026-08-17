# Repository Guidelines

`AGENTS.md` is the canonical guidelines file; `CLAUDE.md` is a symlink to it — edit `AGENTS.md`.

Deep rationale lives in `docs/reference/`. The rules here are binding on their own; read the
reference when you are about to change the code they guard.

- [`docs/reference/luci-invariants.md`](docs/reference/luci-invariants.md) — LuCI view + rpcd backend
- [`docs/reference/node-protocols.md`](docs/reference/node-protocols.md) — the `proto.Node` contract, adding a protocol, node identity
- [`docs/reference/route-list-cache.md`](docs/reference/route-list-cache.md) — per-subscription route list cache
- [`docs/reference/concurrency-and-apply.md`](docs/reference/concurrency-and-apply.md) — run lock, applied-revision markers
- [`docs/reference/testing.md`](docs/reference/testing.md) — coverage areas and the LuCI/rpcd test harnesses
- `docs/plans/` — design notes and implementation plans

## Status

The core is a Go binary named `vpn-manager`; the shell implementation is gone. Shell survives only as
OpenWrt lifecycle glue (the init script) and the LuCI rpcd backend. A single `config.json` replaces
the old `subs.json` + `domains.json` split, subscriptions and routing run independently and can sit on
separate cron schedules, and `jq` / `curl` / `awk` / `sed` / `grep` / `base64` / `md5sum` / `gzip` are
gone from the core path (the init script still calls `curl`, and rpcd still needs `jq`).

## Project Structure & Module Organization

Two OpenWrt packages plus local build tooling.

### Root tooling

- `Makefile` — main entry point: Go cross-compile + local packaging into `.apk` / `.ipk`, lint and test targets. Package builds need **no** OpenWrt SDK, but they do need a running Docker daemon: the packaging scripts run `apk mkpkg` and GNU `ar`/`tar` inside `alpine:latest`
- `Dockerfile` + `docker/entrypoint.sh` — OpenWrt SNAPSHOT SDK image, used only by `make shell` for an interactive SDK session
- `scripts/` — packaging helpers (`package-apk.sh`, `package-ipk.sh`, `package-luci-apk.sh`, `package-luci-ipk.sh`) and `check-release-version.sh`
- `.github/workflows/` — `ci.yml` (lint, tests, one-platform packaging) and `release.yml` (tag → all platforms → draft release)
- `cliff.toml` — git-cliff config; groups conventional commit subjects into the release notes
- `docs/release-notes/<tag>.md` — optional hand-written intro placed above the generated changelog
- `bin/` — generated build output; never a source of truth

### `horn-vpn-manager` (core package)

- `Makefile` — OpenWrt package definition; also the source of `PKG_VERSION` for the root `Makefile`
- `files/horn-vpn-manager.init` — boot-time init script (thin POSIX sh wrapper)
- `files/sing-box.template.default.json` — default template shipped with the package
- `files/config.example.json` — annotated config example shipped with the package
- `cmd/vpn-manager` — CLI bootstrap
- `internal/<pkg>/testdata/` — per-package fixtures and golden files

Internal package split:

- `internal/config` — single config schema, loading, validation
- `internal/fetch` — HTTP fetch, retries, gzip/base64 detection, bounded parallelism
- `internal/subscription` — subscription orchestration, tag planning, route list cache
- `internal/proto` — the `Node` contract every protocol implements, plus the shared TLS structs
- `internal/nodes` — scheme → parser dispatcher; the only non-test importer of the protocol packages
- `internal/vless`, `internal/hysteria2` — per-protocol parsing, stable node identity, outbound
- `internal/routing` — domain/IP/subnet aggregation and route rule assembly
- `internal/singbox` — typed `sing-box` config generation
- `internal/system` — atomic writes, service reloads, run lock, firewall and dnsmasq integration
- `internal/logx` — structured, colored CLI logging

**`internal/singbox/sing-box.template.default.json` and `horn-vpn-manager/files/sing-box.template.default.json` must stay byte-identical.** The first is `go:embed`ed as the fallback template, the second is installed to `/usr/share/`; nothing enforces the match, so edit both.

### `horn-vpn-manager-luci` (LuCI addon)

Tab order: Subscriptions → Routing → Run → Sing-box template config → Additional domains → Sing-box logs → Test.

Hard rules — see [`docs/reference/luci-invariants.md`](docs/reference/luci-invariants.md) for the bug behind each:

- **A save must not drop fields the JS does not know about.** `_collectConfig` starts from the loaded subscription (`_widgets[idx].raw`) and `_rawSingbox`, then uses `setOrDelete`; a no-op save leaves `config.json` byte-identical. Only write a field whose input was actually rendered.
- **`E()` string children go through `innerHTML`.** Anything concatenated or coming from outside the view goes in via `textContent`.
- Maps keyed by subscription id use `Object.create(null)` — ids are free text and `__proto__` is a legal one.
- **Client-side validation must never be stricter than the core.** When in doubt, let `check_with_core` deliver the error. Adding a protocol to the core means adding its scheme to `NODE_URI_SCHEMES`.
- **Every write of `config.json` or the template goes through `write_private`, and every write and every feeding `jq` merge is checked before the `.needs-update-*` flag is touched.** Both files carry credentials; an unchecked write or merge replies `ok` over a config the router never got.
- **A handler that writes both the template and `config.json` must leave neither half applied on failure, and no `.bak.*` may survive either exit.**
- sh-level checks stay structural (types, presence, XOR); schema validation is delegated to `vpn-manager check` on the merged candidate. `jq` comparisons fail closed (`"${bad:-1}"`), temp paths come from `mktemp`, errors go through `fail_json`.
- **`run_script` / `run_routing` clear `.needs-update-*` only on exit code 0, and append the core's stderr to the log.**

## Config Model

Single JSON file at `/etc/horn-vpn-manager/config.json`.

Top-level structure:

- `singbox` — log level, test URL, template path, `connect_timeout`
- `fetch` — retries, timeout, bounded parallelism, `list_cache_ttl`
- `routing` — global routing sources (dnsmasq domains URL, subnet URLs, manual IP file)
- `subscriptions` — keyed subscription definitions; keys are stable IDs and must remain object keys, not array items

Per-subscription fields: `name`, `url`, `nodes`, `default`, `enabled` (optional, defaults true),
`include`, `exclude`, `interval`, `tolerance`, `retries` (optional, overrides global),
`fallback` (optional chain), `route` (optional nested routing).

### Node source (`url` XOR `nodes`)

`url` and `nodes` are mutually exclusive; an enabled subscription must have exactly one of them (an
empty `url` string counts as absent). `nodes` is a list of inline node URIs for a self-hosted node —
any scheme the dispatcher knows — validated with `nodes.Parse` at config load, fetched over no HTTP at
all. Everything else (`include`/`exclude`, `route`, `default`, `fallback`) behaves identically for both
sources.

### Fallback chains (`fallback`)

- `fallback: {subscriptions: [...], blacklist_timeout: "1m"}` is allowed on **any** enabled subscription, not just the default one
- generates a `<id>-fallback` group listing the declaring subscription's own final tag first, then each referenced subscription's final tag in declared order (a backup that itself declares a chain contributes its `<backup>-fallback` tag)
- on the **default** subscription the chain becomes `route.final`; on a **non-default** one it retargets that subscription's own route rules and leaves `route.final` alone
- validation rejects unknown/disabled/self/duplicate references, an empty chain, and cycles of any length
- a backup that produced no plan is dropped from the chain with a warning; regeneration is never aborted over a backup
- switching to a backup changes the public egress IP and does not migrate established connections

### `connect_timeout` and `interrupt_exist_connections`

- `singbox.connect_timeout` is emitted as a dial field on every node outbound; empty omits it entirely
- **Duration fields (`connect_timeout`, `interval`, `fallback.blacklist_timeout`) must be positive.** `time.ParseDuration` accepts `"0"` and a leading `-`, and a non-positive value would be written through to sing-box, so validation checks the sign separately. `fetch.list_cache_ttl` is the exception: `"0"` legitimately means "revalidate every run"
- generated `urltest` and `selector` groups always emit `interrupt_exist_connections: true`, so operators should raise per-subscription `tolerance` (~300 ms; the default is 100) to keep `urltest` from cutting live connections on benign latency jitter
- **the generated `fallback` group carries exactly `type`, `tag`, `outbounds`, `blacklist_timeout`** — `FallbackOutboundOptions` in the extended build — **and nothing else.** sing-box decodes outbound options with unknown fields disallowed, so adding `interrupt_exist_connections` (which `selector` and `urltest` *do* accept) makes `sing-box check` reject the entire config. Do not harmonise the three group types

### Route list cache (`route.domain_urls` / `route.ip_urls`)

Full rationale: [`docs/reference/route-list-cache.md`](docs/reference/route-list-cache.md).

- **A cached list is never served without a bounded staleness.** Younger than `fetch.list_cache_ttl` it is served as is; older it is revalidated with `If-None-Match` / `If-Modified-Since`. A stale broad `domain_suffix` claims domains a later static rule was written to route elsewhere (GitHub issue #2)
- **Every list URL logs its entry count and its real source on one line** (`network`, `cache, age 2h13m`, `cache, revalidated (304)`, `cache, age … — refresh failed`). Never reintroduce a source-agnostic "downloading" line
- a zero-byte cached file is a **miss**, not an empty list
- **`PruneListCache` is keyed on the configured URLs, never on what a run managed to download** — a URL that failed today must keep its copy
- **validators are only sent when the sidecar's digest matches the body on disk**, and an unconditional `304` is an error, not an empty body to cache
- **each list is resolved once per run** (`ListRunCache`) and **writes to one cache entry are serialised on its filename** (`lockCacheEntry`); phase 2 is concurrent and two subscriptions may share a URL
- all fetches send `Cache-Control: no-cache`

### Node identity

- **`StableHash` is a tag function, not an identity function**, and its input string is frozen per protocol. Widening it rewrites every tag and invalidates `subs-tags.json`, saved selector choices and `experimental.cache_file` on every deployed router
- deduplication in `BuildOutbounds` is keyed on the marshalled tagless outbound, not on the hash

### Conventions

- `singbox`, not `sing-box`, for easier handling in Go and tooling
- explicit field names: `url`, `urls`, `manual_file`, `ip_cidrs`
- per-subscription routing lives under a nested `route` object
- when generating `sing-box` config, use the official docs as the source of truth: <https://sing-box.sagernet.org/configuration/>
- **Documented exception:** the `fallback` outbound type does not exist upstream — it is provided only by [`sing-box-extended`](https://github.com/shtorm-7/sing-box-extended), like `xhttp`. A stock build rejects it with `unknown outbound type`, which `ApplySingbox` surfaces with a hint naming the extended-build requirement. `horn-vpn-manager/Makefile` deliberately declares **no** sing-box `DEPENDS`: the stock and extended packages conflict and the extended one is usually installed by hand, so a hard dependency would break installation; the requirement is enforced at runtime by `sing-box check`

## Node Protocol Layer

Protocols are pluggable: `internal/proto` owns the `Node` contract, each protocol package implements it
for its own URI scheme, `internal/nodes` maps scheme → parser. `BuildOutbounds`, `decode.go` and
`config.go` name no protocol. Contract, the add-a-protocol checklist and the hysteria2 URI details are
in [`docs/reference/node-protocols.md`](docs/reference/node-protocols.md).

Rules that bind without reading it:

- **Registration is an explicit map, not `init()` side-effects**, and its entries are named adapter functions, not one-line closures (a typed nil `*vless.Node` becomes a non-nil `proto.Node`)
- **Scheme matching is case-sensitive**: `VLESS://` is an unknown scheme, not a silently accepted one
- **Node URIs carry credentials and must never appear in an error** — not from `nodes.Parse`, not from a protocol's `Parse` (unwrap `*url.Error`, which quotes the whole URI), not from `internal/config` (it locates a bad inline node by position)
- **`internal/subscription/testdata/golden_vless_config.json` is the tag-stability gate.** There is deliberately no `-update` flag; never regenerate it to make a diff go away
- **Groups are protocol-agnostic by construction** — `urltest`, `selector` and `fallback` reference members by tag only
- **JSON subscription decoding stays VLESS-only** (`jsondecode.go` converts V2Ray/Xray outbounds)
- **Widening the accepted schemes can change a subscription's topology** (`<id>-single` → `<id>-manual`); `warnTopologyShift` logs it, and it runs from the pipeline **after `BuildOutbounds`**, never from `DecodePayload`
- **WireGuard-family protocols are out of this layer**: they render into sing-box `endpoints`, have no URI form, and must not be forced into `proto.Node`

## CLI Model

Subscriptions and routing are independent execution units.

```
vpn-manager run                       # convenience: routing then subscriptions (bootstrap)
vpn-manager subscriptions run|dry-run
vpn-manager routing run|restore
vpn-manager check
vpn-manager version
vpn-manager help
```

Common flags:

- `-c / --config` — config path (default `/etc/horn-vpn-manager/config.json`)
- `-v / -vv / -vvv` — verbosity
- `--no-color` — disable colored output
- `--logs` — mirror output into the command's log file (in addition to stderr); truncates it on start
- `--debug` — local debug mode: config/template from binary dir, output to `./out`, no system actions

`check` accepts only `-c`, `-v` and `--no-color`. `routing run` adds `--with-subscriptions` (pre-fetch
subscription route lists into the cache). `subscriptions` adds `-t / --template`, `--download-lists`
(always download fresh lists and cache them) and `--cached-lists` (prefer the cache; a copy older than
`fetch.list_cache_ttl` is revalidated, not blindly reused).

Design constraints:

- subscriptions must be runnable without touching routing caches or dnsmasq state
- routing must be runnable without downloading subscriptions or regenerating proxy groups
- both command families must be idempotent and safe on separate cron schedules
- logging and exit codes must make separate cron usage operationally clear

### Concurrency and applied state

Full rationale: [`docs/reference/concurrency-and-apply.md`](docs/reference/concurrency-and-apply.md).

- **Every command that writes state takes an exclusive flock on `<config dir>/.run.lock`** (`routing run|restore`, `subscriptions run|dry-run`). `check` does not — rpcd calls it on every save. The lock waits up to five minutes, then fails with `ErrLocked`. `runBoth` acquires and releases per phase; do not hoist it around both
- **Order is: parse flags → `logx.Setup` → lock → `SetLogFile` → `config.Load`.** Taking the log file first truncates a live run's log; loading the config first can apply a generation-old config after the wait
- **"Already applied" means an applied-revision marker, not equal file contents** — a run killed between promote and restart would otherwise skip the restart forever
- **A pipeline skips the service restart when what it is about to apply equals what is live**, but **the skip sits after `sing-box check`, never before it**
- **A partial download must not narrow what is applied** — `routing.Run` writes the subnet cache only when every URL succeeded

## On-Device Layout

- CLI: `/usr/bin/vpn-manager`
- Config dir: `/etc/horn-vpn-manager/`
- Main config: `/etc/horn-vpn-manager/config.json`
- Tag map for LuCI: `/etc/horn-vpn-manager/subs-tags.json`
- List/cache dir: `/etc/horn-vpn-manager/lists/` (subscription route lists under `lists/subscriptions/`, each as `<kind>-<hash>.lst` plus a `.meta.json` sidecar)
- Run lock: `/etc/horn-vpn-manager/.run.lock`
- Applied-revision markers: `/etc/horn-vpn-manager/.applied-sing-box`, `.applied-dnsmasq`
- Pending-save flags written by rpcd: `/etc/horn-vpn-manager/.needs-update-subs`, `.needs-update-routing`
- Generated `sing-box` config: `/etc/sing-box/config.json`
- Default template: `/usr/share/horn-vpn-manager/sing-box.template.json`
- Config example: `/usr/share/horn-vpn-manager/config.example.json`
- Logs: `/tmp/horn-vpn-manager-subscriptions.log`, `/tmp/horn-vpn-manager-routing.log`

## Build, Test, and Development Commands

- `make help` — list supported local tasks
- `make build` / `make build-all` — `.apk` for the current platform / all platforms (core + luci)
- `make build-core` / `make build-core-all` / `make build-luci` — granular `.apk` builds
- `make build-ipk` / `make build-ipk-all` — `.ipk` for OpenWrt < 25 with opkg
- `make build-ipk-core` / `make build-ipk-core-all` / `make build-ipk-luci` — granular `.ipk` builds
- `make shell` — interactive shell inside the SDK container
- `make go-build` / `make go-test` / `make go-fmt` / `make go-lint`
- `make lint` — aggregate Go checks (`go-fmt` + `go-lint`)
- `make luci-test` — LuCI view tests (`node --test`) and rpcd backend tests (`dash`), plus the `node --check` / `dash -n` syntax gates
- `make clean` — remove build output

Preferred checks before opening a change:

- `gofmt -w` on changed Go files
- `make lint` and `make go-test`
- `make luci-test` when `config.js` or the rpcd script changes
- the affected `make build*` target when packaging/build flow changes

If the task touches OpenWrt runtime integration, validate on an OpenWrt device or container rather
than on the host OS.

## Coding Style & Naming Conventions

Primary language for the core is Go.

- Follow `.editorconfig` as the source of truth where applicable
- Keep the Go core free of CGO
- Prefer the standard library unless an external dependency clearly pays for itself
- Optimize for readability and testability over clever abstractions
- Keep public/package boundaries explicit; avoid dumping all logic into `main`
- Use `golangci-lint` as the primary Go lint runner
- Use typed models for generated `sing-box` config instead of shell-style string assembly
- When in doubt about `sing-box` fields, schema shape, or behavior, check the official docs first
- Treat config schema changes as API design; name fields for long-term clarity
- Keep concurrency bounded and explicit; router-class hardware is slow and memory-constrained

Shell (init script, rpcd backend): POSIX `sh`, no Bash-only features, business logic belongs in Go.

LuCI JS: preserve the existing plain LuCI style — RPC declarations at top, DOM creation via `E(...)`,
no framework additions.

## Testing Guidelines

Coverage areas and the harness details are in [`docs/reference/testing.md`](docs/reference/testing.md).

- unit tests near packages, `testdata/` for fixtures and golden outputs, `httptest.Server` for fetch/retry scenarios
- **LuCI tests drive the real shipped `config.js` and the real rpcd script**, never a reimplementation
- **mutation-check every new test**: revert the fix it covers and confirm it fails, so it cannot pass vacuously
- **a stub for a standard global must extend the real one, never replace it** — a stub that makes a code path throw turns a negative assertion into a vacuous one
- the `error: <msg>` line rpcd parses out of the core's stderr is a **cross-component contract**, pinned from both ends (`rpcd-checks.test.sh` and `cmd/vpn-manager/main_test.go`)
- gate the rpcd script with `dash -n`, not the host `sh`

## Performance & Binary Size Constraints

The binary must stay small and OpenWrt-friendly.

- avoid CGO
- build with size-conscious flags such as `-trimpath` and stripped symbols
- avoid heavy CLI/config frameworks unless there is a strong reason
- use bounded concurrency; default low, scale only when justified
- avoid unbounded goroutine fan-out for subscriptions or list downloads

Operationally, favor a design where subscription updates can run more frequently than routing updates.
Do not assume both flows always run together.

## Commit & Pull Request Guidelines

Short imperative commit messages, preferably scoped: `feat: add go config loader`,
`fix: preserve stable node tags in go parser`, `refactor: split routing pipeline from system apply`,
`build: compile go binary in openwrt package`.

PRs should state which package(s) are affected (`horn-vpn-manager`, `horn-vpn-manager-luci`, build
tooling), whether the change targets the Go core or OpenWrt packaging (`.apk` for OpenWrt ≥ 25, `.ipk`
for OpenWrt < 25 with opkg), and which checks were run.

The commit prefix is not cosmetic: `cliff.toml` groups release notes by it, and anything it does not
recognize lands under "Other". Review fixups (`fix: address … review findings`) are skipped entirely —
they correct code that ships in the same release under its own entry — so a change that must appear in
the notes needs a subject of its own, not a fixup subject.

## Releases

Releases are cut by pushing a tag; `.github/workflows/release.yml` does the rest.

1. bump `PKG_VERSION` in **both** `horn-vpn-manager/Makefile` and `horn-vpn-manager-luci/Makefile`
2. optionally write `docs/release-notes/v<version>.md` — free-form text placed above the generated
   changelog, for releases that need more than a commit list
3. commit, then `git tag v<version> && git push --tags`

The workflow runs `ci.yml` first, then verifies the tag against `PKG_VERSION`
(`scripts/check-release-version.sh` — a mismatch fails the release rather than shipping a package
whose metadata lies about its version), builds all five platforms as `.apk` **and** `.ipk` plus both
LuCI packages, generates the changelog with git-cliff, and creates a **draft** release with the 12
artifacts and `SHA256SUMS`. Review the notes and publish by hand.

Re-pushing the same tag updates the existing draft in place instead of failing.

## Security & Configuration Tips

Do not commit live subscription URLs, credentials, router-specific configs, or generated `sing-box`
output.

- device-local config files are configuration, not repository data
- `bin/` contains build artifacts and can become stale; rebuild instead of inferring behavior from old packages
- treat `/etc/config/dhcp`, firewall state, and dnsmasq state as user/system state, not source-controlled data
