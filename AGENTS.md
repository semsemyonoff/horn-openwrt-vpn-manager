# Repository Guidelines

## Status

This repository is in the middle of a breaking rewrite of `horn-vpn-manager`.

Target state:

- `horn-vpn-manager` is a Go application.
- Shell remains only for OpenWrt package lifecycle glue such as init scripts.
- The core uses a single config file instead of `subs.json` and `domains.json`.
- The CLI must support running subscriptions and routing updates independently.
- The CLI must also allow separate cron scheduling for subscriptions and routing updates.
- Runtime dependencies on tools like `jq`, `curl`, `awk`, `sed`, `grep`, `base64`, `md5sum`, and `gzip` are being removed from the core path.
- `horn-vpn-manager-luci` is not being adapted yet; LuCI compatibility is a later phase.

Until the rewrite is finished, the repository may contain both new Go code and legacy shell-based implementation details. Prefer the new architecture for all new work unless a task explicitly targets legacy behavior.

## Project Structure & Module Organization

This repository contains two OpenWrt packages plus local Docker-based build tooling used to assemble them.

### Root tooling

- `Makefile` — main entry point for local development: builds Docker images, packages, shells, and lint checks
- `Dockerfile` — OpenWrt SDK builder image
- `docker/entrypoint.sh` — syncs package sources into the SDK and builds `horn-vpn-manager` / `horn-vpn-manager-luci`
- `bin/` — local build output (`.apk` / `.ipk` artifacts); treat as generated output, not source of truth

### `horn-vpn-manager` (core package, rewrite target)

The core package is being rewritten around a Go binary named `vpn-manager`.

Preferred direction for the package:

- `horn-vpn-manager/Makefile` — OpenWrt package definition for the Go-based core
- `horn-vpn-manager/files/horn-vpn-manager.init` — boot-time init script or thin wrapper around the binary
- `horn-vpn-manager/files/` — package assets only, not business logic
- `horn-vpn-manager/cmd/` — CLI entrypoints
- `horn-vpn-manager/internal/` — application internals
- `horn-vpn-manager/testdata/` — fixtures and golden files for parser/config generation tests

Suggested internal package split:

- `cmd/vpn-manager` — CLI bootstrap
- `internal/config` — single config schema, loading, validation
- `internal/fetch` — HTTP fetch, retries, gzip/base64 detection, bounded parallelism
- `internal/subscription` — subscription orchestration and tag planning
- `internal/vless` — VLESS parsing and stable node identity
- `internal/routing` — domain/IP/subnet aggregation and route rule assembly
- `internal/singbox` — typed `sing-box` config generation
- `internal/system` — atomic writes, service reloads, firewall and dnsmasq integration
- `internal/state` — caches, generated files, runtime state

### `horn-vpn-manager-luci` (LuCI addon)

`horn-vpn-manager-luci` is intentionally out of scope for the current rewrite phase. Do not reshape the Go core around LuCI compatibility constraints unless the task explicitly says otherwise.

Current package contents:

- `horn-vpn-manager-luci/Makefile` — LuCI package definition
- `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager` — rpcd backend
- `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js` — main LuCI view
- `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/style.css` — frontend styles
- `horn-vpn-manager-luci/root/usr/share/rpcd/acl.d/horn-vpn-manager.json` — ACL
- `horn-vpn-manager-luci/root/usr/share/luci/menu.d/horn-vpn-manager.json` — menu entry
- `horn-vpn-manager-luci/po/{en,ru}/horn-vpn-manager.po` — translations

## Config Model

The target core config is a single JSON file, for example:

- `/etc/horn-vpn-manager/config.json`

Preferred top-level structure:

- `singbox` — settings directly related to `sing-box`
- `fetch` — global download/runtime settings such as retries, timeout, and bounded parallelism
- `routing` — global routing sources such as dnsmasq domains and subnet sources
- `subscriptions` — keyed subscription definitions; keys are stable IDs and must remain object keys, not array items

Recommended conventions:

- Prefer `singbox`, not `sing-box`, for easier handling in Go and tooling
- Prefer explicit names such as `url`, `urls`, `manual_file`, `ip_cidrs`
- Per-subscription routing may live under a nested `route` object if that keeps the schema clearer
- When generating `sing-box` config, use the official `sing-box` documentation as the source of truth: `https://sing-box.sagernet.org/configuration/`

## CLI Model

The new CLI must keep subscriptions and routing as independent execution units.

Preferred command shape:

- `vpn-manager subscriptions run`
- `vpn-manager subscriptions dry-run`
- `vpn-manager routing run`
- `vpn-manager routing restore`
- `vpn-manager check`

Optional convenience commands are fine, but they must not replace the split execution model:

- `vpn-manager run` may execute both pipelines for initial bootstrap
- init scripts may run both when the device is first brought up

Design constraints for the CLI:

- subscriptions must be runnable without touching routing caches or dnsmasq state
- routing must be runnable without downloading subscriptions or regenerating proxy groups
- both command families must be idempotent
- both command families must be safe to place on different cron schedules
- logging and exit codes should make separate cron usage operationally clear

## On-Device Layout

Target layout for the rewritten core:

- CLI: `/usr/bin/vpn-manager`
- Config dir: `/etc/horn-vpn-manager/`
- Main config: `/etc/horn-vpn-manager/config.json`
- List/cache dir: `/etc/horn-vpn-manager/lists/`
- Generated `sing-box` config: `/etc/sing-box/config.json`
- Default template shipped by package: `/usr/share/horn-vpn-manager/sing-box.template.default.json`
- Runtime logs: prefer separate logs per pipeline, for example `/tmp/horn-vpn-manager-subscriptions.log` and `/tmp/horn-vpn-manager-routing.log`

During migration there may still be extra legacy files in the package. Do not treat them as long-term design targets.

## Build, Test, and Development Commands

- `make help` — list supported local tasks
- `make build` — build `.apk` packages with the SNAPSHOT SDK
- `make build-ipk OPENWRT_RELEASE=23.05.5` — build `.ipk` packages against a release SDK
- `make shell` / `make shell-ipk` — open an interactive shell inside the SDK container
- `make lint` — run local static checks configured by the repository, including `golangci-lint` for Go code

Preferred checks for the rewritten core:

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

For shell that remains in the package:

- Use POSIX `sh`
- Avoid Bash-only features
- Keep shell scripts thin; business logic belongs in Go

For LuCI JS, preserve the existing plain LuCI style unless the LuCI rewrite phase explicitly starts:

- RPC declarations at top
- DOM creation via `E(...)`
- no framework additions

## Testing Guidelines

The rewrite should add automated tests as a first-class part of the core.

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

Before opening a change:

- run `golangci-lint run`
- run `go test ./...` for the affected package set
- run `make lint`
- run the affected `make build*` target when package/build logic changes
- if you change OpenWrt integration behavior, validate on-device when feasible

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
- whether the change targets the new Go core or legacy transition code
- which checks were run

## Security & Configuration Tips

Do not commit live subscription URLs, credentials, router-specific configs, or generated `sing-box` output.

- device-local config files are configuration, not repository data
- `bin/` contains build artifacts and can become stale; rebuild instead of inferring behavior from old packages
- treat `/etc/config/dhcp`, firewall state, and dnsmasq state as user/system state, not source-controlled data

## Legacy Transition Blocks

These sections describe the shell-based implementation that is being replaced. They exist only to help migration work. Delete them once the rewrite is complete and the old code is gone.

### Legacy Core Layout

The previous core implementation is shell-based:

- `horn-vpn-manager/files/vpn-manager.sh` — installed CLI entry point
- `horn-vpn-manager/files/subs.sh` — downloads subscription data and generates `/etc/sing-box/config.json`
- `horn-vpn-manager/files/getdomains.sh` — downloads dnsmasq domain/subnet lists and rebuilds the VPN IP list
- `horn-vpn-manager/files/config.template.json` — default `sing-box` template
- `horn-vpn-manager/files/subs.example.json` — legacy subscription config example
- `horn-vpn-manager/files/domains.example.json` — legacy domain/subnet config example

### Legacy Runtime Contract

The old shell version currently relies on:

- `/etc/horn-vpn-manager/subs.json`
- `/etc/horn-vpn-manager/domains.json`
- `/etc/horn-vpn-manager/lists/manual-ip.lst`
- `/etc/horn-vpn-manager/subs-tags.json`
- `/tmp/horn-vpn-manager-sub.log`
- `/tmp/horn-vpn-manager-domains.log`

Legacy CLI split:

- `vpn-manager subscriptions ...`
- `vpn-manager domains ...`

Legacy init behavior:

- wait for internet
- run domain update
- run subscription update
- or restore partial cached state when offline

### Legacy Schema Notes

The old `subs.json` schema uses a keyed object under `subscriptions`; those keys become stable tag prefixes:

- `<id>-single`
- `<id>-auto`
- `<id>-manual`
- `<id>-node-<hash>`

If a migration task needs to preserve behavior while porting shell logic to Go, keep that tag semantics stable unless the task explicitly changes it.

The old implementation also separates:

- global dnsmasq domain and subnet sources in `domains.json`
- per-subscription routing rules inside `subs.json`

That split is legacy design baggage from the two-script architecture and should not be carried forward into the new unified config.

### Legacy Tooling Notes

The old shell core depends on external runtime tools such as:

- `jq`
- `curl`
- `awk`
- `sed`
- `grep`
- `base64`
- `md5sum`
- `gzip`

The rewrite is explicitly removing these dependencies from the core execution path.
