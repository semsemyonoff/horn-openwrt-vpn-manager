# Testing reference

Detail behind the one-line rules in `AGENTS.md` → *Testing Guidelines*.

## Go coverage areas

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

Layout: unit tests near packages, `testdata/` for fixtures and golden outputs, integration-style
tests with `httptest.Server` for fetch/retry scenarios.

## LuCI JS harness (`horn-vpn-manager-luci/tests/`)

- `load-view.js` evaluates the shipped `config.js` with `new Function`, the way LuCI itself does, against `stub-dom.js` — a dependency-free DOM/LuCI stub, no jsdom — so tests drive the **real** `_makeCard` / `_collectConfig` / `_validate`. Never assert on a reimplementation, and mutation-check every new test: revert the fix it covers and confirm the test fails, so it cannot pass vacuously.
- A stub for a **standard** global must extend the real one, never replace it. `load-view.js` used to pass `URL` as a plain object carrying only `createObjectURL`/`revokeObjectURL`, so `new URL(s)` inside `isValidNodeUri` threw and every node URI read as invalid under test — the rejection test passed for the wrong reason and no acceptance test could pass. It is now `class URLStub extends URL` with the two blob statics attached. A stub that makes a code path throw turns a negative assertion into a vacuous one, which is exactly what mutation-checking catches.
- `load-view.js` offers two entry points, and the difference matters. `mountSubscriptions` reproduces `render()`'s setup by hand, which keeps card-level tests small; `renderView` drives the **real** `render()` with the array `load()` resolves to and attaches the result to the document. Anything `render()` itself wires — the `_rawSingbox` snapshot, `_subIdx` — is only covered by the second, because the first assigns those values itself and would pass with the wiring deleted.
- `stub-dom.js` models `<select>.value` off the selected `<option>`, since that is the only channel `render()` uses for the stored log level, and resolves `getElementById` by walking the document rather than through a shared id map — a module-level map is overwritten by the next `loadView()` in the same process, which turns an assertion against the older ctx into a vacuous pass. Its value backing field is `_stubValue`, deliberately not `_value`: `config.js` keeps chain-picker bookkeeping in `sel._value`.

## rpcd backend harness

- `rpcd-checks.test.sh` covers the backend at two levels. It sources the real script with an unmatched `$1` so the `case` dispatcher falls through, then drives `check_sub_sources` / `fail_json` / `check_with_core` directly; and it runs the real `call set_config` / `call set_full_config` dispatcher against a temporary tree via `HORN_VPN_MANAGER_CONF_DIR` (the only reason `CONF_DIR` is overridable — rpcd never sets it). Only the second level catches a validation call being dropped or a write happening before it. Both stub `vpn-manager` on `PATH` to reach the core-rejection path.
- The `error: <msg>` line rpcd parses out of the core's stderr is a **cross-component contract**, pinned from both ends: `rpcd-checks.test.sh` drives the awk extraction against a stub, and `cmd/vpn-manager/main_test.go` builds the real binary and asserts a rejected config produces that line (and a valid one does not). Without the second, changing `main()`'s format silently downgrades every core rejection LuCI shows to a generic message.

## Running them

`make luci-test`, or by hand:

```sh
node --check horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js
node --test horn-vpn-manager-luci/tests/*.test.js   # a bare directory argument does not work
dash -n horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager
dash horn-vpn-manager-luci/tests/rpcd-checks.test.sh
```

Gate the rpcd script with `dash -n`, not the host `sh`: macOS `sh` is bash 3.2 and mis-parses a
pre-existing `case`-inside-`$()`. `dash` is the closest available shell to OpenWrt `ash`.
`shellcheck -s sh` is useful but reports pre-existing SC2221/SC2222.
