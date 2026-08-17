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
- `internal/vless` — VLESS parsing and stable node identity
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
- `horn-vpn-manager-luci/tools/po2lmo.py` — PO→LMO compiler for translations
- `horn-vpn-manager-luci/tests/` — Node/`dash` harness for the view and the rpcd backend; not shipped in the package (`package-luci-apk.sh` copies only `root/`)

Tab order: Subscriptions → Routing → Run → Sing-box template config → Additional domains → Sing-box logs → Test

UI features:
- Import/export config buttons available on all tabs
- Subscription cards include `include` field (same shape as `exclude`)
- Run tab replaces old Update tab; has independent Subscriptions and Routing sections with per-command flag options and live log polling

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
- **`E()` string children go through `innerHTML`.** Any text that is concatenated or comes from outside the view — provider node names, backend error messages, filenames — must be set with `textContent` (`nodeNameSpan` / `textP`), never passed as an `E(...)` child.
- Maps keyed by subscription id use `Object.create(null)`: the ID field is free text, so an id like `__proto__` or `constructor` would otherwise not become an own key and the subscription would silently vanish from the save.
- Client-side validation must never be stricter than the core. A disabled subscription needs no `url`/`nodes`, so rejecting it blocks a save over something the core accepts; when in doubt let `check_with_core` deliver the error.
- rpcd `jq` checks compare against `"${bad:-1}"`. An unguarded `[ "$bad" -gt 0 ]` errors out when `jq` aborts on a malformed payload and the script falls through — accepting it. Fail closed.
- `check_with_core` writes its candidate to an `mktemp` path, not a `$$`-derived one: rpcd runs as root on a world-writable `/tmp`, so a predictable name lets a local process pre-plant a symlink and have root truncate an arbitrary file.
- The rpcd backend keeps sh-level checks structural only (types, presence, XOR) and delegates schema validation to `vpn-manager check -c <tmp>` on the **merged** candidate (`check_with_core`), rather than reimplementing cross-reference logic in a regex-less `jq`. It accepts on the structural checks alone when the core is unreachable, so a partially installed system can still save.
- Error replies go through `fail_json`, which JSON-escapes the message — core errors quote subscription ids.

## Config Model

The core config is a single JSON file at `/etc/horn-vpn-manager/config.json`.

Top-level structure:

- `singbox` — settings directly related to `sing-box` (log level, test URL, template path, `connect_timeout`)
- `fetch` — global download/runtime settings (retries, timeout, bounded parallelism)
- `routing` — global routing sources (dnsmasq domains URL, subnet URLs, manual IP file)
- `subscriptions` — keyed subscription definitions; keys are stable IDs and must remain object keys, not array items

Per-subscription fields: `name`, `url`, `nodes`, `default`, `enabled` (optional, defaults true), `include`, `exclude`, `interval`, `tolerance`, `retries` (optional, overrides global), `fallback` (optional chain), `route` (optional nested routing)

Node source (`url` XOR `nodes`):

- `url` and `nodes` are mutually exclusive; an enabled subscription must have exactly one of them (an empty `url` string counts as absent)
- `nodes` is a list of inline `vless://` URIs for a self-hosted node, validated with `vless.Parse` at config load, and fetched over no HTTP at all
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

Node identity:

- `vless.StableHash` is a **tag** function, not an identity function: it hashes 13 of the `Node`'s 26 fields and omits `ALPN`, `Mode` and `HeaderType`, each of which changes the rendered outbound. Equal hash therefore does not mean identical node.
- Deduplication in `BuildOutbounds` is keyed on the marshalled outbound (`nodeToOutbound(n, "")`), not on the hash. The `seenTags` `-N` suffix stays, because two genuinely distinct nodes can still collide on a tag.
- Do not widen what `StableHash` covers: it would rewrite every tag and invalidate `subs-tags.json`, saved selector choices and `experimental.cache_file` state.

Conventions:

- `singbox`, not `sing-box`, for easier handling in Go and tooling
- Explicit field names: `url`, `urls`, `manual_file`, `ip_cidrs`
- Per-subscription routing lives under a nested `route` object
- When generating `sing-box` config, use the official `sing-box` documentation as the source of truth: `https://sing-box.sagernet.org/configuration/`
- **Documented exception:** the `fallback` outbound type does not exist upstream — it is provided only by [`sing-box-extended`](https://github.com/shtorm-7/sing-box-extended), like `xhttp`. A config using `fallback` is rejected by a stock build with `unknown outbound type`, which `ApplySingbox` surfaces with a hint naming the extended-build requirement. `horn-vpn-manager/Makefile` deliberately declares **no** sing-box `DEPENDS`: the stock and extended packages conflict and the extended one is usually installed by hand, so a hard dependency would break installation; the requirement is enforced at runtime by `sing-box check` instead.

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
- `--cached-lists` — use pre-fetched lists from cache (download only on miss)

Design constraints:

- subscriptions must be runnable without touching routing caches or dnsmasq state
- routing must be runnable without downloading subscriptions or regenerating proxy groups
- both command families must be idempotent
- both command families must be safe to place on different cron schedules
- logging and exit codes must make separate cron usage operationally clear

## On-Device Layout

- CLI: `/usr/bin/vpn-manager`
- Config dir: `/etc/horn-vpn-manager/`
- Main config: `/etc/horn-vpn-manager/config.json`
- List/cache dir: `/etc/horn-vpn-manager/lists/`
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
- VLESS parsing
- stable node hash generation
- payload decoding: raw, base64, base64url, gzip
- domain/IP/subnet validation and deduplication
- route rule generation
- `sing-box` config generation
- restore/apply planning
- independent execution of subscriptions and routing commands
- command behavior under separate cron-style invocation patterns

Preferred test layout:

- unit tests near packages
- `testdata/` for fixtures and golden outputs
- integration-style tests with `httptest.Server` for fetch/retry scenarios

Non-Go checks:

- LuCI JS is covered by `horn-vpn-manager-luci/tests/`. `load-view.js` evaluates the shipped `config.js` with `new Function`, the way LuCI itself does, against `stub-dom.js` — a dependency-free DOM/LuCI stub, no jsdom — so tests drive the **real** `_makeCard` / `_collectConfig` / `_validate`. Never assert on a reimplementation, and mutation-check every new test: revert the fix it covers and confirm the test fails, so it cannot pass vacuously.
- `rpcd-checks.test.sh` sources the real rpcd script with an unmatched `$1` so the `case` dispatcher falls through, then drives `check_sub_sources` / `fail_json` / `check_with_core` directly. It stubs `vpn-manager` on `PATH` to cover the core-rejection path.
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
