# Repository Guidelines

## Project Structure & Module Organization

This repository contains two OpenWrt packages plus the local Docker-based build tooling used to assemble them.

### Root tooling

- `Makefile` — main entry point for local development: builds Docker images, packages, shells, and lint checks
- `Dockerfile` — OpenWrt SDK builder image
- `docker/entrypoint.sh` — syncs package sources into the SDK and builds `horn-vpn-manager` / `horn-vpn-manager-luci`
- `bin/` — local build output (`.apk` / `.ipk` artifacts); treat as generated output, not source of truth

### `horn-vpn-manager` (core package)

- `horn-vpn-manager/Makefile` — OpenWrt package definition
- `horn-vpn-manager/files/vpn-manager.sh` — installed CLI entry point (`vpn-manager`)
- `horn-vpn-manager/files/subs.sh` — downloads subscription data and generates `/etc/sing-box/config.json`
- `horn-vpn-manager/files/getdomains.sh` — downloads dnsmasq domain/subnet lists and rebuilds the VPN IP list
- `horn-vpn-manager/files/horn-vpn-manager.init` — boot-time init script
- `horn-vpn-manager/files/config.template.json` — default sing-box template
- `horn-vpn-manager/files/subs.example.json` — example subscription config
- `horn-vpn-manager/files/domains.example.json` — example domain/subnet config

### `horn-vpn-manager-luci` (LuCI addon)

- `horn-vpn-manager-luci/Makefile` — LuCI package definition
- `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager` — rpcd backend
- `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js` — main LuCI view
- `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/style.css` — frontend styles
- `horn-vpn-manager-luci/root/usr/share/rpcd/acl.d/horn-vpn-manager.json` — ACL
- `horn-vpn-manager-luci/root/usr/share/luci/menu.d/horn-vpn-manager.json` — menu entry
- `horn-vpn-manager-luci/po/{en,ru}/horn-vpn-manager.po` — translations

### On-device layout

- CLI: `/usr/bin/vpn-manager`
- Core scripts: `/usr/libexec/horn-vpn-manager/`
- Config dir: `/etc/horn-vpn-manager/`
- List/cache dir: `/etc/horn-vpn-manager/lists/`
- Generated sing-box config: `/etc/sing-box/config.json`
- Default template shipped by package: `/usr/share/horn-vpn-manager/config.template.default.json`
- Tag/name cache for LuCI: `/etc/horn-vpn-manager/subs-tags.json`
- Logs: `/tmp/horn-vpn-manager-sub.log`, `/tmp/horn-vpn-manager-domains.log`

## Build, Test, and Development Commands

- `make help` — list supported local tasks
- `make build` — build `.apk` packages with the SNAPSHOT SDK
- `make build-ipk OPENWRT_RELEASE=23.05.5` — build `.ipk` packages against a release SDK
- `make shell` / `make shell-ipk` — open an interactive shell inside the SDK container
- `make lint` — run `sh -n`, `shellcheck` when available, and `jq` validation for packaged JSON files

Useful focused checks:

- `sh -n horn-vpn-manager/files/subs.sh`
- `sh -n horn-vpn-manager/files/getdomains.sh`
- `sh -n horn-vpn-manager/files/vpn-manager.sh`
- `sh -n horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager`
- `jq . horn-vpn-manager/files/config.template.json`
- `jq . horn-vpn-manager/files/subs.example.json`
- `jq . horn-vpn-manager/files/domains.example.json`

Do not run the OpenWrt scripts against your macOS/Linux host as if they were local utilities. They expect OpenWrt paths, services, and package layout. For safe runtime inspection, use an OpenWrt device/container and prefer `vpn-manager subscriptions dry-run -v`.

## Coding Style & Naming Conventions

Use POSIX `sh`; avoid Bash-only features in packaged scripts and the rpcd backend.

- Follow `.editorconfig` as the source of truth.
- Use 2-space indentation in `*.sh` and `*.json`.
- Keep shell constants uppercase (`CONF_DIR`, `TMPDIR`, `LOG_FILE`).
- Keep shell functions lowercase snake_case (`compute_node_hash`, `wait_for_network`).
- Keep log lines short and operational.
- Preserve sing-box template placeholders exactly: `__LOG_LEVEL__`, `__VLESS_OUTBOUNDS__`, `__GROUP_OUTBOUNDS__`, `__ROUTE_RULES__`, `__DEFAULT_TAG__`.
- `subs.json` uses a keyed object under `subscriptions`; those keys become stable tag prefixes (`<id>-single`, `<id>-auto`, `<id>-manual`, `<id>-node-<hash>`). Do not silently convert it to an array-based format.

For LuCI JS, stay consistent with the existing plain LuCI style: RPC declarations at top, DOM creation via `E(...)`, no framework additions.

## Testing Guidelines

There is no automated integration test suite yet. Before opening a change:

- Run `make lint`.
- If you touch shell logic, run the relevant `sh -n` checks directly.
- If you touch JSON templates/examples, validate them with `jq`.
- If you change package manifests or Docker build flow, run the affected `make build*` target when feasible.
- If you change sing-box generation logic, verify on-device with `sing-box check -c /etc/sing-box/config.json.new`.
- If you change domain-list behavior, validate with `vpn-manager domains run -v` on OpenWrt and ensure dnsmasq/firewall reload still succeeds.

## Commit & Pull Request Guidelines

Use short imperative commit messages, preferably scoped:

- `feat: add manual selector for multi-node subscriptions`
- `fix: preserve stable node tags across refreshes`
- `docs: refresh package installation guide`
- `build: add ipk build target`

PRs should state which package(s) are affected (`horn-vpn-manager`, `horn-vpn-manager-luci`, build tooling) and which checks were run.

## Security & Configuration Tips

Do not commit live subscription URLs, credentials, router-specific configs, or generated sing-box output.

- Device-local files such as `subs.json`, `domains.json`, and `lists/manual-ip.lst` are configuration, not repository data.
- The LuCI app reads and writes `/etc/config/dhcp` for manual `vpn_domains` ipset entries; treat that as user state.
- `bin/` contains build artifacts and can become stale; rebuild instead of inferring behavior from old packages.
