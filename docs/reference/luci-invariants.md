# LuCI addon reference

Detail behind the one-line rules in `AGENTS.md` → *`horn-vpn-manager-luci` (LuCI addon)*.

## Package contents

- `Makefile` — LuCI package definition
- `root/usr/libexec/rpcd/horn-vpn-manager` — rpcd backend (reads/writes `config.json`, calls the `vpn-manager` binary)
- `root/www/luci-static/resources/view/horn-vpn-manager/config.js` — main LuCI view
- `root/www/luci-static/resources/horn-vpn-manager/style.css` — frontend styles
- `root/usr/share/rpcd/acl.d/horn-vpn-manager.json` — ubus ACL
- `root/usr/share/luci/menu.d/horn-vpn-manager.json` — menu entry
- `po/{en,ru}/horn-vpn-manager.po` — translations
- `tools/po2lmo.py` — PO→LMO compiler; changing a msgid means a device needs the regenerated `.lmo`, which both the LuCI `Makefile` and `scripts/package-luci-{apk,ipk}.sh` produce at package time — no `.lmo` is checked in, so `make build-luci` is the whole step
- `tests/` — Node/`dash` harness for the view and the rpcd backend; not shipped in the package (`package-luci-apk.sh` copies only `root/`)

## UI surface

Tab order: Subscriptions → Routing → Run → Sing-box template config → Additional domains → Sing-box logs → Test.

- Import/export config buttons on all tabs
- Subscription cards carry `include` (same shape as `exclude`)
- Subscription cards toggle between a remote `url` and inline `nodes`; a node URI is accepted when its scheme is in `NODE_URI_SCHEMES`
- Subscription cards carry a fallback-chain editor (ordered backup pickers plus `blacklist_timeout`) with client-side unknown/self/duplicate/cycle checks
- Subscriptions tab carries the global `singbox.connect_timeout` input
- Run tab has independent Subscriptions and Routing sections with per-command flag options and live log polling

## rpcd methods

- `get_config` / `set_config` — read/write `config.json` (subscriptions + singbox settings)
- `set_full_config` — atomic write of both config and template in one call
- `get_template` / `set_template` / `reset_template` — manage the sing-box template
- `get_domains_config` / `set_domains_config` — read/write `config.json → routing`
- `get_manual_ips` / `set_manual_ips` — manual IP/CIDR list
- `get_manual_domains` / `set_manual_domains` — manual domain list
- `run_script` — `vpn-manager subscriptions run` (supports `--cached-lists`, `--download-lists`, dry-run)
- `run_routing` — `vpn-manager routing run` (supports `--with-subscriptions`)
- `get_log` / `get_routing_log` — read `/tmp/horn-vpn-manager-{subscriptions,routing}.log`
- `get_sb_status`, `set_proxy`, `test_delays`, `test_url`, `get_syslog`, `get_sync_status` — sing-box/proxy helpers

Both run methods always pass `--logs` to the core.

## Invariants (each one was a real bug)

### Saving must not lose data

- **A save must not drop fields the JS does not know about.** `_collectConfig` starts from a copy of the subscription as loaded (`_widgets[idx].raw`) and of `singbox` (`_rawSingbox`), then assigns or deletes known keys via `setOrDelete` — never rebuild either object from an allow-list. A no-op save must leave `config.json` byte-identical.
- Only write a field whose input was actually rendered: `interval` / `tolerance` inputs exist only for a multi-node subscription with proxy data, so an unguarded write deletes them whenever sing-box is down.
- rpcd merges `singbox` additively (`$esb + $isb`), so dropping a key on save cannot clear a stored value. Clearing a field that *was* set emits `""` (the core treats empty as unset); a field that was never set stays absent.
- Because of that merge, `handleSave` re-seeds `_rawSingbox` from the payload it just sent. The view does not reload after a save, so a key first set in this page session would otherwise stay missing from the snapshot and a later clear would drop it instead of emitting `""` — the field looks empty while the router keeps the old value.
- Maps keyed by subscription id use `Object.create(null)`: the ID field is free text, so an id like `__proto__` or `constructor` would otherwise not become an own key and the subscription would silently vanish from the save.

### Frontend safety and validation

- **`E()` string children go through `innerHTML`.** Any text that is concatenated or comes from outside the view — provider node names, backend error messages, filenames — must be set with `textContent` (`nodeNameSpan` / `textP`), never passed as an `E(...)` child.
- Client-side validation must never be stricter than the core. A disabled subscription needs no `url`/`nodes`, so rejecting it blocks a save over something the core accepts; when in doubt let `check_with_core` deliver the error.
- `isValidNodeUri` matches `NODE_URI_SCHEMES` against `u.protocol` (already lowercased by `URL()`), not a literal prefix, so `VLESS://` passes the client and is rejected by the core — the safe direction for the rule above. Userinfo presence is `u.username || u.password`: hysteria2 auth is the **whole** userinfo and may be `user:password`, which `URL()` splits across both fields, so `u.username` alone would reject `hysteria2://:pass@host` that the core accepts. Adding a protocol to the core means adding its scheme here too.

### rpcd backend

- rpcd `jq` checks compare against `"${bad:-1}"`. An unguarded `[ "$bad" -gt 0 ]` errors out when `jq` aborts on a malformed payload and the script falls through — accepting it. Fail closed.
- `check_with_core` writes its candidate to an `mktemp` path, not a `$$`-derived one: rpcd runs as root on a world-writable `/tmp`, so a predictable name lets a local process pre-plant a symlink and have root truncate an arbitrary file.
- **Every write of `config.json` or the template goes through `write_private`**, which creates the temp file inside `(umask 077; …)` before the `mv`. Both files carry credentials — provider subscription URLs, inline node URIs (a `hysteria2://` URI *is* its password), a hand-written outbound password — and rpcd inherits a `0022` umask, so a plain redirect publishes them as `0644`. `mv` replaces the destination *together with its mode*, so it also silently un-hardens a file an operator had already chmodded. The umask applies at creation, so the file is never briefly world-readable. `rpcd-checks.test.sh` asserts the mode after every accepted `set_config` / `set_full_config` / `set_template` / `reset_template` / `set_domains_config`. The manual-IP list and `/etc/config/dhcp` are deliberately not routed through it: neither holds secrets, and dhcp's mode is system state.
- **Every `write_private` call is checked, and the update flag is only touched after it succeeds.** A failed write — read-only filesystem, full disk, a directory sitting on the `.tmp` path — otherwise still `touch`es `.needs-update-*` and replies `{"result":"ok"}`, so LuCI shows a saved config the router never received and the sync badge claims the config is merely pending. The same rule covers **every** `jq` merge that feeds a write — `set_config`, `set_full_config`, `set_template`, `reset_template`, `set_domains_config`: `jq` prints nothing when the `config.json` it was handed is malformed, so an unchecked `$merged`/`$updated` replaces the config with a blank document and still replies `{"result":"ok"}`. `rpcd-checks.test.sh` drives each writing method with a pre-created `<file>.tmp` **directory**, which makes the redirect fail as root too, and asserts an error reply, an unchanged file and no flag; a separate case feeds `set_template` a malformed `config.json`.
- **A handler that writes both the template and `config.json` must leave neither half applied on failure.** `config.json` is what points sing-box at the template, so a template swapped in before a config write that then fails silently changes what the router runs while the reply says error. `set_template` and `set_full_config` snapshot the template (`snapshot_file`) before replacing it and roll it back (`restore_file`, which removes a file that did not exist before) on either write failure; `reset_template` instead writes the config **before** `rm -f`, since the reverse order leaves the config pointing at a file that is already gone. **No `.bak.*` copy may survive either exit.** The rollback moves the snapshot back over the target rather than copying, and the success path drops it with `discard_snapshot` after the config write lands — `snapshot_file` uses `cp -p`, so a snapshot taken from a legacy `0644` template keeps that mode and would outlive the very save that hardened the original. `rpcd-checks.test.sh` asserts the config dir is free of `*.bak.*` on both the rollback and the `"result":"ok"` path.
- The rpcd backend keeps sh-level checks structural only (types, presence, XOR) and delegates schema validation to `vpn-manager check -c <tmp>` on the **merged** candidate (`check_with_core`), rather than reimplementing cross-reference logic in a regex-less `jq`. It accepts on the structural checks alone when the core is unreachable, so a partially installed system can still save.
- Error replies go through `fail_json`, which JSON-escapes the message — core errors quote subscription ids.
- **`run_script` / `run_routing` clear `.needs-update-*` only when the core exited 0, and append its stderr to the log.** Clearing the flag unconditionally makes the sync badge claim the router is up to date with a config it never applied — a live failure mode, since a run refused by the run lock exits non-zero. The core's final `error: …` line is printed to stderr only, so `2>/dev/null` left the Run tab with a log that simply stops. `rpcd-checks.test.sh` drives both outcomes against a stub core.
