# LuCI Rewrite for Go Core

## Overview

Rewrite `horn-vpn-manager-luci` to work with the new Go-based `vpn-manager` binary and redesign the UI according to new requirements:

1. Import/export config buttons on all tabs
2. `include` field for subscriptions (same UI as `exclude`)
3. Rename "Domains" tab → "Routing"
4. Remove "Update" tab; move run/log functionality to new "Run" tab with per-command options
5. Update sing-box template tab for new Go core (no old placeholder strings)
6. New tab order: Subscriptions → Routing → Sing-box template config → Additional domains → Sing-box logs → Test

**Go core changes** (if required): run linters and tests after any Go changes.

## Context (from discovery)

**LuCI addon files:**
- `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager` — 427-line shell rpcd backend (reads `subs.json`, calls old shell scripts)
- `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js` — 2533-line LuCI frontend
- `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/style.css` — CSS
- `horn-vpn-manager-luci/root/usr/share/rpcd/acl.d/horn-vpn-manager.json` — ubus ACL
- `horn-vpn-manager-luci/po/{en,ru}/horn-vpn-manager.po` — i18n

**Go core relevant paths:**
- Config: `/etc/horn-vpn-manager/config.json` (new unified format, keyed subscriptions object)
- Subscription log: `/tmp/horn-vpn-manager-subscriptions.log`
- Routing log: `/tmp/horn-vpn-manager-routing.log`
- Default template (on-device): `/usr/share/horn-vpn-manager/sing-box.template.json`
- Custom template: `config.json → singbox.template` path (empty = use embedded default)
- CLI: `vpn-manager subscriptions run [--cached-lists|--download-lists]`
- CLI: `vpn-manager routing run [--with-subscriptions]`
- Go `singbox.RenderConfig()`: merges outbounds/rules into template JSON (no string placeholders; strips bare-string entries from template arrays)

**Config format delta (old → new):**
- Old: `/etc/horn-vpn-manager/subs.json` — array of subscription objects with `code` field
- New: `/etc/horn-vpn-manager/config.json` — keyed object with `singbox`, `fetch`, `routing`, `subscriptions` sections
- Subscription key = stable ID (generated from name via `sanitizeId()`)
- Per-subscription routing lives under `route` nested object
- New field: `include` (same shape as `exclude`, array of strings)

## Development Approach

- Testing approach: Regular (code first, verify via build)
- Complete each task fully before moving to the next
- After any Go changes: run `golangci-lint run` and `go test ./...` inside `horn-vpn-manager/`
- After all LuCI changes: run `make build` and fix any build errors

## Progress Tracking

- Mark completed items with `[x]` immediately when done
- Add newly discovered tasks with ➕ prefix
- Document issues/blockers with ⚠️ prefix

## Implementation Steps

---

### Task 1: Update rpcd backend — config read/write for new `config.json` format

The backend currently reads `subs.json` (old array format). Must switch to `config.json` (new unified format).

- [x] Replace `CONFIG_FILE=/etc/horn-vpn-manager/subs.json` with `CONFIG_FILE=/etc/horn-vpn-manager/config.json`
- [x] Rewrite `get_config`: read `config.json`, return `singbox` settings + `subscriptions` keyed object (include `include`, `exclude`, `route.*` fields per subscription)
- [x] Rewrite `set_config`: accept new format, write `config.json` preserving `fetch` and `routing` sections; validate: at least one subscription exists, exactly one default, default not disabled
- [x] Update `get_template` to read template from path in `config.json → singbox.template` (fallback: `/usr/share/horn-vpn-manager/sing-box.template.json`; if that file absent, use embedded content via `vpn-manager` binary if available)
- [x] Update `set_template`: save to `/etc/horn-vpn-manager/sing-box.template.json`, update `config.json → singbox.template` to that path
- [x] Update `reset_template`: delete `/etc/horn-vpn-manager/sing-box.template.json`, clear `config.json → singbox.template` field (empty = Go uses embedded default)
- [x] Update `get_domains_config` / `set_domains_config` to read/write `config.json → routing` section
- [x] Update `get_manual_ips` / `set_manual_ips` to use path from `config.json → routing.subnets.manual_file` (default `/etc/horn-vpn-manager/lists/manual-ip.lst`)

---

### Task 2: Update rpcd backend — new CLI commands and log paths

The backend currently calls old shell scripts. Must call `vpn-manager` binary with correct flags.

- [x] Rewrite `run_script` (subscriptions): call `vpn-manager subscriptions run` (or `dry-run`); support `--cached-lists` and `--download-lists` flags passed from frontend; log to `/tmp/horn-vpn-manager-subscriptions.log`
- [x] Add `run_routing` method: call `vpn-manager routing run`; support `--with-subscriptions` flag; log to `/tmp/horn-vpn-manager-routing.log`; check/set running status analogously to `run_script`
- [x] Update `get_log` to read from `/tmp/horn-vpn-manager-subscriptions.log`
- [x] Add `get_routing_log`: read from `/tmp/horn-vpn-manager-routing.log`; return log content + running status
- [x] Remove dependency on `subs.sh`, `getdomains.sh` — replace `run_getdomains` with `run_routing`; remove `get_domains_log` (replaced by `get_routing_log`)
- [x] Keep `get_sb_status`, `set_proxy`, `test_delays`, `test_url`, `get_syslog`, `get_manual_domains`, `set_manual_domains`, `get_sync_status` — update paths/calls where needed
- [x] Update `list` method to expose all new/changed method signatures

---

### Task 3: Update ACL

- [x] Add `run_routing`, `get_routing_log` to write/read groups in `acl.d/horn-vpn-manager.json`
- [x] Remove `run_getdomains`, `get_domains_log` entries (replaced)
- [x] Verify all remaining method names match updated rpcd backend

---

### Task 4: Update config.js — subscriptions include field

- [x] Add `include` dynlist field to `makeSubscriptionCard()` immediately above `exclude` field (same widget structure: label + dynamic list of string inputs)
- [x] Update `_collectConfig()` to read `include` values from subscription cards and include in JSON payload
- [x] Update rendering path: when loading existing config, populate `include` inputs from subscription data
- [x] Update `_validate()` to reject empty-string include patterns (same rule as exclude)

---

### Task 5: Update config.js — tab restructuring and Run tab

Remove "Update" tab, rename tabs, add "Run" tab with two sections.

- [ ] Rename "Domains" tab section to "Routing"
- [ ] Remove the "Update" tab entirely
- [ ] Remove script output / run button from the "Routing" (ex-Domains) tab section
- [ ] Add new "Run" tab with two independent sections:
  - **Subscriptions** section: `--cached-lists` checkbox (default: checked), `--download-lists` checkbox (default: unchecked), mutually exclusive logic (checking one unchecks the other), dry-run checkbox, Run button, scrollable log output area, auto-poll log while running
  - **Routing** section: `--with-subscriptions` checkbox (default: checked), Run button, scrollable log output area, auto-poll log while running
- [ ] Wire Subscriptions Run button → `run_script` RPC with selected flag options
- [ ] Wire Routing Run button → `run_routing` RPC with `--with-subscriptions` flag
- [ ] Wire log polling to `get_log` (subscriptions) and `get_routing_log` (routing) respectively
- [ ] Reorder tabs to: Subscriptions → Routing → Sing-box template config → Additional domains → Sing-box logs → Test
- [ ] Update RPC declarations at top of file for new/changed methods (`run_routing`, `get_routing_log`)

---

### Task 6: Update config.js — import/export config on all tabs

- [ ] Add "Export config" and "Import config" buttons to the tab bar area or a persistent header (visible across all tabs)
- [ ] Export: call `get_config` (or use current in-memory config), serialize to JSON, trigger browser `Blob` download as `horn-vpn-manager-config.json`
- [ ] Import: file input (`<input type="file">`), read JSON via `FileReader`, pass to `_validate()`, then call `set_config` RPC; show success/error feedback

---

### Task 7: Update sing-box template tab

The new Go core uses JSON merging (no `__PLACEHOLDER__` strings). Update the template editor UI to reflect this.

- [ ] Remove the old placeholder legend/documentation panel from the template tab (the `__LOG_LEVEL__`, `__VLESS_OUTBOUNDS__` etc. legend)
- [ ] Add a description note explaining the new merging behavior: generated outbounds/rules are prepended to the template's arrays; bare string entries in `outbounds`/`route.rules` are stripped; `log.level` and `route.final` are always overridden by config
- [ ] Verify `get_template` / `set_template` / `reset_template` RPC calls in JS use the updated rpcd methods correctly
- [ ] Update the default template file `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/sing-box.template.default.json` if it exists (or remove if redundant — the Go binary ships the authoritative default)

---

### Task 8: Update i18n (.po files)

- [ ] Add/update strings in both `po/en/horn-vpn-manager.po` and `po/ru/horn-vpn-manager.po`:
  - "Run" (new tab title)
  - "Export config", "Import config" (button labels)
  - "Include filters" (subscription field label)
  - "Routing" (renamed from "Domain list")
  - "--cached-lists", "--download-lists", "--with-subscriptions" option labels (or descriptive UI labels)
  - "Run subscriptions", "Run routing" section headings in Run tab
  - Any removed strings: "Update" tab title, old "Domain list" references, old "Script output" label
- [ ] Ensure Russian translations cover all new strings

---

### Task 9: CSS updates

- [ ] Add styles for Run tab layout (two-section layout, per-section log output, mutual-exclusive checkbox visual state)
- [ ] Add styles for import/export button placement in persistent header/tab bar area
- [ ] Adjust any Routing tab styles after removing run/log section

---

### Task 10: Build verification and fix

- [ ] Run `make build` (or `make build-ipk`) and capture full output
- [ ] If build fails: read error output, identify root cause, fix (po2lmo errors, missing files, ACL syntax, rpcd syntax errors, etc.)
- [ ] Re-run build until it passes cleanly
- [ ] If any Go core changes were made during tasks 1–9: run `go test ./...` and `golangci-lint run` inside `horn-vpn-manager/`

---

### Task 11: Update README.md and CLAUDE.md

- [ ] Update `README.md`: reflect new LuCI tab structure, import/export, `include` field, Run tab with command options
- [ ] Update `CLAUDE.md`: update LuCI addon section to reflect new tab order, new RPC methods (`run_routing`, `get_routing_log`), removed methods (`run_getdomains`, `get_domains_log`), new UI concepts (Run tab, import/export)

---

### Task 12: Final verification

- [ ] Verify all tab names match required order (Subscriptions → Routing → Sing-box template config → Additional domains → Sing-box logs → Test)
- [ ] Verify `include` field is present in subscription cards
- [ ] Verify import/export buttons are present on all tabs
- [ ] Verify Run tab has both sections with correct default checkbox states
- [ ] Verify rpcd `list` method accurately reflects all exposed methods
- [ ] Verify ACL covers all methods used by config.js

---

## Technical Details

**Subscription object shape (new config.json):**
```json
{
  "name": "Work",
  "url": "https://...",
  "default": false,
  "enabled": true,
  "include": ["keyword1"],
  "exclude": ["Russia", "traffic"],
  "interval": "5m",
  "tolerance": 100,
  "retries": 3,
  "route": {
    "domains": ["example.com"],
    "domain_urls": ["https://..."],
    "ip_cidrs": ["203.0.113.0/24"],
    "ip_urls": ["https://..."]
  }
}
```

**rpcd run_script flags (new):**
- `"cached_lists": true/false` → `--cached-lists`
- `"download_lists": true/false` → `--download-lists`
- `"dry_run": true/false` → `dry-run` subcommand

**rpcd run_routing flags (new):**
- `"with_subscriptions": true/false` → `--with-subscriptions`

**Log file paths:**
- Subscriptions: `/tmp/horn-vpn-manager-subscriptions.log`
- Routing: `/tmp/horn-vpn-manager-routing.log`

**Template rendering (Go core):**
- Template is plain JSON; no `__PLACEHOLDER__` strings needed
- Generated outbounds prepended before static template outbounds
- Generated route rules prepended before static template rules
- Bare string entries in `outbounds` and `route.rules` arrays are stripped by Go
- `log.level` always set from `config.json → singbox.log_level`
- `route.final` always set to the default subscription's proxy tag

## Post-Completion

**Manual verification (on device):**
- Install both packages on OpenWrt device or container
- Verify subscription run from Run tab applies correctly
- Verify routing run with `--with-subscriptions` pre-fetches lists
- Verify import/export round-trips config without data loss
- Verify template reset restores correct default
- Verify `include` filter actually filters nodes (Go core already supports this)
