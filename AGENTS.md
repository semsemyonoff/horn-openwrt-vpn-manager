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
- `bin/` — local build output (`.apk` / `.ipk` artifacts); treat as generated output, not source of truth

### `horn-vpn-manager` (core package)

The core package is a Go binary named `vpn-manager`.

Package layout:

- `horn-vpn-manager/Makefile` — OpenWrt package definition for the Go-based core
- `horn-vpn-manager/files/horn-vpn-manager.init` — boot-time init script (thin POSIX sh wrapper)
- `horn-vpn-manager/files/sing-box.template.default.json` — default sing-box template shipped with the package
- `horn-vpn-manager/files/config.example.json` — annotated config example shipped with the package
- `horn-vpn-manager/cmd/vpn-manager` — CLI bootstrap
- `horn-vpn-manager/internal/` — application internals
- `horn-vpn-manager/testdata/` — fixtures and golden files for parser/config generation tests

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

Tab order: Subscriptions → Routing → Sing-box template config → Additional domains → Sing-box logs → Test → Run

UI features:
- Import/export config buttons available on all tabs
- Subscription cards include `include` field (same shape as `exclude`)
- Run tab replaces old Update tab; has independent Subscriptions and Routing sections with per-command flag options and live log polling

rpcd methods (current):
- `get_config` / `set_config` — reads/writes `config.json` (subscriptions + singbox settings)
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

## Config Model

The core config is a single JSON file at `/etc/horn-vpn-manager/config.json`.

Top-level structure:

- `singbox` — settings directly related to `sing-box` (log level, test URL, template path)
- `fetch` — global download/runtime settings (retries, timeout, bounded parallelism)
- `routing` — global routing sources (dnsmasq domains URL, subnet URLs, manual IP file)
- `subscriptions` — keyed subscription definitions; keys are stable IDs and must remain object keys, not array items

Conventions:

- `singbox`, not `sing-box`, for easier handling in Go and tooling
- Explicit field names: `url`, `urls`, `manual_file`, `ip_cidrs`
- Per-subscription routing lives under a nested `route` object
- When generating `sing-box` config, use the official `sing-box` documentation as the source of truth: `https://sing-box.sagernet.org/configuration/`

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
- `make build` — build `.apk` packages with the SNAPSHOT SDK
- `make build-ipk OPENWRT_RELEASE=23.05.5` — build `.ipk` packages against a release SDK
- `make shell` / `make shell-ipk` — open an interactive shell inside the SDK container
- `make lint` — run local static checks configured by the repository, including `golangci-lint` for Go code

Preferred checks before opening a change:

- `gofmt -w` on changed Go files
- `golangci-lint run`
- `go test ./...`
- `make lint`
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
- whether the change targets the Go core or OpenWrt packaging
- which checks were run

## Security & Configuration Tips

Do not commit live subscription URLs, credentials, router-specific configs, or generated `sing-box` output.

- device-local config files are configuration, not repository data
- `bin/` contains build artifacts and can become stale; rebuild instead of inferring behavior from old packages
- treat `/etc/config/dhcp`, firewall state, and dnsmasq state as user/system state, not source-controlled data
