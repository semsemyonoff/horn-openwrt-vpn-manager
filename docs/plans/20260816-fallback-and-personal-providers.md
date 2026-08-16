# Fallback Chains and Personal Providers

## Overview

Two related changes to `horn-vpn-manager`:

1. **Fallback chains (issue #1)** — any subscription may declare an ordered list of backup
   subscriptions. The declaring subscription's final tag becomes a generated `fallback` outbound
   group instead of a single node tag; for the default subscription that is `route.final`, for the
   others it is the tag their own route rules point at. Plus three adjacent stability fixes found
   during on-device investigation.
2. **Personal providers** — a subscription may carry inline `vless://` node URIs instead of a
   remote subscription `url`, so a self-hosted node can be configured without publishing a
   subscription endpoint.

### Problem being solved

Observed in a production deployment and reproduced against generated output:

- When the default subscription resolves to a single node, `route.final` points straight at that
  node tag. Single-node subscriptions get no `urltest`/`selector` group, so **redundancy for
  `route.final` cannot be expressed at all** in the current config model — everything not matched by
  a route rule depends on one outbound.
- A shared provider node can keep answering probes yet stall under concurrency: `ESTABLISHED=60`
  against `SYN_SENT=515` in conntrack, with 62% of all outbound router connections stuck half-open,
  producing 3127 `dial ... i/o timeout` errors in 72 s, while single `urltest` probes to that same
  node succeeded in ~106 ms. `urltest` selects the lowest-latency node, which is systematically the
  most contended one, and cannot detect this failure mode. `fallback` can, because it reacts to
  dial failure rather than probe latency.
- Each failing dial hangs ~5 s before `i/o timeout` — exactly how long a `fallback` switch would be
  delayed, hence a configurable `connect_timeout`.
- One deployment generated 132 node outbounds from 32 unique server IPs, 13 of them producing
  byte-identical outbound JSON, which `BuildOutbounds` keeps with a `-2` tag suffix. Each is probed
  on every `urltest` interval.
- Neither `interrupt_exist_connections` nor `connect_timeout` is emitted anywhere, so when a group
  re-selects, connections to the old node are left hanging.

Redundancy for `route.final` is also a precondition for inline `nodes`: a self-hosted node has no
provider replacing it when it dies, so pointing `route.final` at one without a chain would make the
single point of failure worse than a subscription-backed node.

## Context (from discovery)

### Extended build dependency

`fallback` is **not** an upstream sing-box outbound type — it exists only in the extended build.
AGENTS.md names the upstream docs as source of truth, so this is a deliberate, documented exception.
Verified on device against `sing-box-extended 1.13.18-extended-2.6.5` (OpenWrt 25.12.1): each shape
below passed `sing-box check` with exit 0, while a deliberately invalid outbound type was rejected
with `unknown outbound type`, confirming the check is meaningful.

- `{"type":"fallback","outbounds":[...],"blacklist_timeout":"1m"}`
- `interrupt_exist_connections: true` on both `urltest` and `selector`
- `connect_timeout: "3s"` as a dial field on a `vless` outbound
- the full combined shape (vless + urltest + selector + fallback + `route.final`)

`horn-vpn-manager/Makefile` currently declares no `DEPENDS` on sing-box at all; whether to record
the extended-build requirement there is an open question raised in Task 13.

### Go core

- `internal/config/config.go` — `Singbox` (`:22-27`), `Subscription` (`:30-41`),
  `ValidateSubscriptions()` (`:124-160`)
- `internal/subscription/outbound.go` — `BuildOutbounds()` (`:174`), `URLTestOutbound` (`:149-157`),
  `SelectorOutbound` (`:160-165`), `nodeToOutbound()` (`:260`), duplicate-suffix logic
  (`:213-227`), defaults `defaultInterval = "5m"` / `defaultTolerance = 100` (`:12-13`)
- `internal/subscription/subscription.go` — `plans []*OutboundPlan` is a **flat slice** with no id
  association (`:198`, `:289`, `:343`); `collectSingboxParts()` (`:425`) flattens plans into the
  outbounds/rules slices and is where a fallback group must be appended; `sub.URL` downloads at
  `:225` and `:475`; `urlCache[sub.URL] = decoded` at `:237`; empty-URL skip at `:315`;
  `defaultFinalTag == ""` error at `:353`; `tagNames` written to `subs-tags.json` at `:389-397`
- `internal/singbox/singbox.go` — `RenderConfig(templateData, outbounds, routeRules,
  defaultFinalTag, logLevel)` (`:45-51`). Pure rendering, no process execution.
- `internal/system/system.go` — **`ApplySingbox()` (`:100-108`) already runs
  `sing-box check -c <staging>`**, removes staging on failure and leaves the live config untouched,
  wrapping the error as `sing-box check failed: %s: %w`. Covered by `system_test.go:154-209` via the
  existing `Cmd` fake. A device without the extended build therefore already surfaces
  `unknown outbound type` verbatim — no new validation mechanism is needed, only a better message.
- `internal/vless/vless.go` — `Node` (`:15-41`) carries **26 fields**, but `StableHash()`
  (`:123-133`) hashes only 13. `ALPN`, `Mode` (xhttp mode) and `HeaderType` are **not** hashed, yet
  all three change the generated outbound (`outbound.go:279-286`, `:339-349`, `:315-317`).
  **Equal hash therefore does NOT mean an identical node** — see Task 1.
- `internal/subscription/decode.go` — `DecodePayload()` supports `FormatRaw` (plain `vless://`
  lines), so inline URIs reuse the existing `vless.Parse` path.

### Test layout — current reality

The **only** testdata in the module is `internal/subscription/testdata/raw_subscription.txt`, a
decode fixture. There are **no golden files anywhere** and `internal/singbox/` has no `testdata/`
directory. Existing assertions use the marshal style of
`outbound_test.go:393 TestBuildOutbounds_JSONMarshal`. This plan follows that style and does not
introduce a golden-file harness.

### LuCI

- `root/usr/libexec/rpcd/horn-vpn-manager` — `get_config` (`:28-32`) already returns
  `.subscriptions` verbatim, and `set_config`'s merge (`:110-120`) is `$esb + $isb` for `singbox`
  with `subscriptions: ($inp.subscriptions // {})`, so new fields **already pass through** read and
  write. The real blocker is the mandatory-`url` rejection, duplicated verbatim at **`:83-88`
  (`set_config`) and `:180-183` (`set_full_config`)**.
- `root/www/luci-static/resources/view/horn-vpn-manager/config.js` — `:2296` rebuilds each
  subscription as `var sub = { name: name, url: url };` plus an allow-list of known fields, and
  `:2323` rebuilds `singbox: { log_level, test_url, template }`. **Fields the JS does not know are
  dropped on save** — see Task 10.
- `cmd/vpn-manager/check.go:52-82` — `check` runs `Load` + `ValidateSubscriptions` and accepts
  `-c <path>`, so the backend can delegate validation instead of reimplementing it in sh.

**Carried constraint:** OpenWrt `jq` lacks ONIGURUMA — no `test`/`match`/`sub`/`gsub` in rpcd shell.
Use shell `case` or `awk`.

**Backward compatibility:** applying the generated config reloads `sing-box`, dropping all active
connections. Every change here must be safe to land in a single regeneration, and a config using
none of the new fields must render as before apart from the added group field.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - write unit tests for new functions/methods
  - write unit tests for modified functions/methods
  - add new test cases for new code paths
  - update existing test cases if behavior changes
  - tests cover both success and error scenarios
- **CRITICAL: all tests must pass before starting next task** — no exceptions
- **CRITICAL: update this plan file when scope changes during implementation**
- run tests after each change
- backward compatibility: for a config with no duplicate nodes and none of the new fields, generated
  output must be unchanged apart from the added `interrupt_exist_connections` field on groups

## Testing Strategy

- **unit tests**: required for every task, in the marshal-assertion style already used in
  `outbound_test.go` — no golden-file harness exists and none is introduced here
- **integration tests**: `internal/subscription/integration_test.go` uses `httptest.Server`; add a
  case proving an inline-node subscription performs **no** HTTP request
- **shell**: rpcd changes gated by `sh -n` plus `make build`; device ubus verification is
  Post-Completion, not an in-repo gate
- **e2e tests**: project has no UI-based e2e harness, so none apply

Go gates, run inside `horn-vpn-manager/`:

```
gofmt -l .
golangci-lint run
go test ./...
```

Packaging gates from repo root: `make lint`, and `make build` when LuCI files change.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

### Design decisions

| Decision | Choice | Rationale |
|---|---|---|
| Who may declare `fallback` | **any subscription** | Issue #1 proposes default-only, but that is too narrow in practice. A personal VPS carries named domains as a *non-default* subscription, so default-only would leave exactly the node that most needs redundancy without any. The subscription that actually failed in production was also non-default. Widening costs one extra validation rule (real cycle detection) and is far cheaper now than retrofitting later. |
| Personal node schema | **`nodes: []`, mutually exclusive with a non-empty `url`** | A subscription stays one entity, so `route` / `include` / `exclude` / `default` / `fallback` keep working unchanged; validation is a simple XOR |
| `connect_timeout` | **global, `singbox.connect_timeout`** | One knob; empty default emits nothing, preserving current behavior |
| `interrupt_exist_connections` | **`true` on all generated groups** (resolved) | Stranding connections on a dead node is never desirable — observed connections hung 22 s and 1 m 25 s after their node stopped answering. **Coupling to be documented:** on `urltest` the field also fires on benign latency-driven re-selections. At the default `tolerance: 100` with an observed node spread of 103–230 ms that is noise, so live downloads/streams/WebSockets would be cut for no reason. The mitigation is `tolerance`, not disabling the flag: it is already a per-subscription config field requiring no code, and raising it (~300 ms) leaves re-selection to genuine degradation, where interrupting is always correct. |
| Backup subscription failure | **degrade, do not abort** | A transient outage on a *backup* must not abort regeneration of a healthy primary; matches the existing skip-and-continue policy at `subscription.go:337-345` |
| Dedup key | **marshalled outbound, not `StableHash`** | `StableHash` omits `ALPN`, `Mode`, `HeaderType`; hashing more fields would rewrite every tag and invalidate `subs-tags.json`, saved selector choices, and `experimental.cache_file` state |
| rpcd validation | **delegate to `vpn-manager check -c <tmp>`** | Avoids reimplementing XOR + cross-reference logic in POSIX sh with a regex-less `jq` |

### Generated shape

For a subscription `primary` (inline single node) with fallback to `backup` (multi-node):

```json
{
  "type": "fallback",
  "tag": "primary-fallback",
  "outbounds": ["primary-single", "backup-manual"],
  "blacklist_timeout": "1m"
}
```

with `route.final = "primary-fallback"`. Each referenced subscription resolves to its own
`plan.FinalTag`, so single-node (`<id>-single`) and multi-node (`<id>-manual`) backups both work
without special-casing.

### Behavior

- A new connection tries the primary first.
- On dial failure the primary is blacklisted for `blacklist_timeout`; the connection is retried
  through the next outbound.
- While blacklisted, new connections prefer the backup.
- After the timeout, a new connection retries the primary.
- Existing connections are **not** migrated between providers; switching providers changes the
  public egress IP.

## Technical Details

### Config additions

```jsonc
{
  "singbox": {
    "connect_timeout": "3s"        // new, optional; empty = omit field entirely
  },
  "subscriptions": {
    "primary": {
      "name": "Self-hosted",
      "default": true,
      "nodes": ["vless://uuid@203.0.113.10:443?..."],  // new; mutually exclusive with non-empty url
      "fallback": {                                     // new; allowed on any subscription
        "subscriptions": ["backup"],
        "blacklist_timeout": "1m"
      }
    }
  }
}
```

### New Go types

```go
// config package
type Fallback struct {
    Subscriptions    []string `json:"subscriptions"`
    BlacklistTimeout string   `json:"blacklist_timeout"`
}
// Subscription gains: Nodes []string, Fallback *Fallback
// Singbox gains: ConnectTimeout string

// subscription package
type FallbackOutbound struct {
    Type             string   `json:"type"`               // "fallback"
    Tag              string   `json:"tag"`
    Outbounds        []string `json:"outbounds"`
    BlacklistTimeout string   `json:"blacklist_timeout,omitempty"`
}

// BuildOutbounds signature collapses into options (already 5 positionals, adding a
// second duration string next to Interval invites transposition bugs)
type BuildOptions struct {
    Interval       string
    Tolerance      int
    TestURL        string
    ConnectTimeout string
}
```

`URLTestOutbound` and `SelectorOutbound` each gain
`InterruptExistConnections bool` with tag `json:"interrupt_exist_connections"`.
`VLESSOutbound` gains `ConnectTimeout string` with tag `json:"connect_timeout,omitempty"`.

### Validation rules

- **non-empty** `url` and `nodes` are mutually exclusive; an enabled subscription must have exactly
  one of them (note `config.js:2296` always emits `"url"`, possibly as `""`)
- every entry in `nodes` must parse via `vless.Parse`; empty strings rejected
- `fallback` may be declared on **any** enabled subscription
- each `fallback.subscriptions` entry must exist, be enabled, and must not be the declaring
  subscription itself
- no duplicate entries within one chain; empty chain rejected
- **no cycles**: a chain may not lead back to any subscription already on the path
- `blacklist_timeout` and `singbox.connect_timeout`, when set, must parse via `time.ParseDuration`

Cycle detection is a real requirement, not insurance: with chains allowed on every subscription,
`a → b → a` is expressible, and so is a longer loop. Validation must walk the chain graph, not just
compare against the declaring id. A referenced subscription may itself declare a chain, so
resolution is recursive — the generated `fallback` group lists the referenced subscription's own
final tag, which may itself be a `fallback` group.

## What Goes Where

- **Implementation Steps** (`[ ]` checkboxes): Go core, LuCI, tests, docs inside this repo
- **Post-Completion** (no checkboxes): device verification requiring the extended sing-box build

## Implementation Steps

---

### Task 1: Deduplicate identical nodes in `BuildOutbounds`

⚠️ `StableHash` is **not** a safe dedup key: it omits `ALPN`, `Mode` and `HeaderType`, each of
which changes the rendered outbound. Dedup on the marshalled outbound instead.

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`

- [x] **Decision: dedup runs inside the `len(nodes) > 1` branch.** An all-duplicate subscription
      keeps a 1-member urltest/selector and `FinalTag` stays `<id>-manual`, so `route.final` and
      existing route rules never move as a side effect of dedup.
- [x] dedup keyed on the marshalled outbound built with an empty tag (`nodeToOutbound(n, "")`), so
      the tag does not affect the key; the tag is assigned after the dedup check
- [x] ⚠️ **scope note:** the `seenTags` suffix logic was **kept, not replaced**. Because tags stay
      `StableHash`-derived and the hash omits `ALPN` / `Mode` / `HeaderType`, two *distinct* nodes
      can still collide on a tag; the suffix is what keeps them addressable. Dedup removes the
      duplicates the suffix used to paper over, so `-2` now only appears for genuine collisions.
- [x] keep `StableHash` for tag generation, unchanged, so `subs-tags.json`, saved selector choices
      and `experimental.cache_file` state stay valid
- [x] report skipped duplicates once via `logx.Detail` (`skipped N duplicate nodes`), not one
      line per duplicate
- [x] write tests: byte-identical URIs collapse to one outbound; no `-2` suffix in `nodeTags` or
      `TagNames`; distinct nodes unaffected
      (`TestBuildOutbounds_DeduplicatesIdenticalNodes`, `..._DeduplicationKeepsDistinctNodes`)
- [x] write a regression test proving two nodes that share a `StableHash` but differ in `ALPN`,
      `Mode` or `HeaderType` are **both** kept
      (`TestBuildOutbounds_DeduplicationIgnoresStableHash`, one subtest per field)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues as `HEAD`, none new

### Task 2: Emit `interrupt_exist_connections` on group outbounds

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`

- [x] add `InterruptExistConnections bool` with tag `json:"interrupt_exist_connections"` to
      `URLTestOutbound` and `SelectorOutbound`
- [x] set it to `true` on both groups in `BuildOutbounds`, and on `FallbackOutbound` in Task 7
      (the `FallbackOutbound` half stays with Task 7, where the type is introduced)
- [x] write tests asserting the field is present and `true` in the marshalled JSON for each group
      (`TestBuildOutbounds_GroupsInterruptExistConnections`)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

Because this makes every `urltest` re-selection cut live connections, `defaultTolerance`
(`outbound.go:13`, currently `100`) becomes safety-critical rather than cosmetic. Raising the
default is **out of scope here** — it changes behavior for every existing deployment — but Task 13
must document that operators should raise per-subscription `tolerance` (~300 ms) alongside this
change, and the reasoning belongs in `README.md` next to the `tolerance` field.

### Task 3: Add global `singbox.connect_timeout` and collapse `BuildOutbounds` args

**Files:**
- Modify: `horn-vpn-manager/internal/config/config.go`
- Modify: `horn-vpn-manager/internal/config/config_test.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription_test.go`

- [x] add `ConnectTimeout string` with tag `json:"connect_timeout"` to `Singbox`, validated with
      `time.ParseDuration` when non-empty, error style matching the existing `interval` message
      — ⚠️ **scope note:** validated in `Config.validate()` (runs on every `Load`, so both the
      routing and subscription pipelines reject it) rather than in `ValidateSubscriptions`, since
      `connect_timeout` is a global sing-box setting, not a subscription constraint
- [x] introduce `BuildOptions` and change `BuildOutbounds(id string, uris []string, opts
      BuildOptions)` — the function already takes 5 positionals and the new value is a second
      duration string adjacent to `Interval`
- [x] add `ConnectTimeout string` with tag `json:"connect_timeout,omitempty"` to `VLESSOutbound` and
      set it in `nodeToOutbound`; empty value must omit the field entirely
- [x] update both call sites (`subscription.go:271`, `:524`) to the new signature — both now go
      through a new `Runner.buildOptsForSub(sub, testURL)` helper that merges the per-subscription
      group settings with `Cfg.Singbox.ConnectTimeout`
- [x] write tests: valid duration emitted on every node outbound; empty value omits the field;
      invalid duration fails config validation (`TestBuildOutbounds_ConnectTimeout`,
      `TestLoad_singbox_connect_timeout`). Also asserted: the dial field is not emitted on group
      outbounds, and it does not perturb the Task 1 dedup key
- [x] write tests covering both call sites through the pipeline
      (`TestRunner_Run_connect_timeout_applied`, default + non-default subscription)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

### Task 4: Config schema and validation for inline `nodes`

**Files:**
- Modify: `horn-vpn-manager/internal/config/config.go`
- Modify: `horn-vpn-manager/internal/config/config_test.go`

- [x] add `Nodes []string` with tag `json:"nodes,omitempty"` to `Subscription`
- [x] in `ValidateSubscriptions`, reject a subscription with both a **non-empty** `url` and `nodes`
      — all source checks live in a `validateSource(id, sub)` helper called from the existing loop
- [x] reject an enabled subscription with neither; a disabled one with neither must not error
- [x] reject empty strings inside `nodes`, matching the existing `include`/`exclude` checks
- [x] ⚠️ **scope note:** each entry is also run through `vless.Parse` (per the Validation rules
      section), so `check` catches a malformed URI at config time rather than mid-pipeline. This
      adds a `config` → `vless` import; `vless` imports nothing from the module, so no cycle.
- [x] write tests: `nodes` only; `url` only; `{"url": "", "nodes": [...]}` valid; both non-empty
      (error); neither on an enabled subscription (error); empty string in `nodes` (error)
      (`TestValidateSubscriptions_source`, `..._disabled_without_source`, `TestLoad_subscription_nodes`,
      `TestSubscription_nodes_omitted_when_empty`)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

### Task 5: Pipeline support for inline `nodes`

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/subscription.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription_test.go`
- Modify: `horn-vpn-manager/internal/subscription/integration_test.go`

- [x] at both download sites (`:225`, `:475`) use `sub.Nodes` directly when non-empty, performing no
      HTTP request — `processSub` now selects the source with a three-way `switch`
      (inline / cache hit / download)
- [x] guard `urlCache[sub.URL] = decoded` (`:237`) so an inline default never writes `urlCache[""]`
      — otherwise the lookup at `:468` returns the default's nodes for any *other* inline
      subscription. The lookup side is guarded too (`sub.URL != "" && cacheHit`), so an empty key
      can never match even if one were written.
- [x] relax the empty-URL skip (`:315`) so a subscription with `nodes` is not skipped; keep the
      warning only when both are absent
- [x] update the error text at `:353` — "check that the default subscription has a URL configured"
      is wrong once `nodes` exists
- [x] ensure `include`/`exclude` filtering applies to inline nodes exactly as to downloaded ones
      (filters run after source selection, unchanged, at both call sites)
- [x] write a test "two inline-node subscriptions produce distinct node sets" covering the
      `urlCache[""]` collision (`TestRunner_Run_inline_nodes_distinct_per_subscription`); plus
      `..._inline_nodes_filtering` and `..._inline_nodes_skipped_when_disabled_sibling_has_no_source`
- [x] write an integration test with `httptest.Server` asserting **zero** requests for an
      inline-node subscription (`TestIntegration_Run_inline_nodes_no_http`: inline default +
      URL-backed non-default, exactly 1 request total; plus `..._inline_nodes_only`)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

### Task 6: Config schema and validation for `fallback`

**Files:**
- Modify: `horn-vpn-manager/internal/config/config.go`
- Modify: `horn-vpn-manager/internal/config/config_test.go`

- [x] add the `Fallback` struct and `Fallback *Fallback` with tag `json:"fallback,omitempty"` on
      `Subscription`
- [x] allow `fallback` on any enabled subscription
- [x] validate each referenced id: exists, enabled, not the declaring subscription itself
      — ⚠️ **scope note:** a chain declared on a **disabled** subscription is left unvalidated, the
      same way `validateSource` skips a disabled subscription with no source: it is never generated,
      so rejecting the config over it would block an otherwise healthy run
- [x] reject duplicate ids within a chain and an empty `subscriptions` list (also an empty id string)
- [x] walk the chain graph and reject cycles of any length (`a → b → a`, `a → b → c → a`) —
      `validateFallbackCycles()` runs a three-colour DFS over enabled subscriptions, seeded in sorted
      id order so the reported cycle path does not depend on map iteration order. References that
      `validateFallback` already rejects (unknown, disabled) are skipped so the more specific error
      wins.
- [x] validate `blacklist_timeout` with `time.ParseDuration` when non-empty
- [x] write tests for every rejection path, each asserting an actionable message
      (`TestValidateSubscriptions_fallback`: empty chain, empty id, self-reference, duplicate,
      unknown, disabled, invalid `blacklist_timeout`; `..._fallback_cycles`: 2-node, 3-node, cycle
      not touching the DFS entry point, plus linear-chain and diamond negatives)
- [x] write tests for the valid case, including a two-backup chain preserving order
      (`TestLoad_subscription_fallback`, `..._fallback_chain_order_preserved`,
      `TestSubscription_fallback_omitted_when_absent`,
      `TestValidateSubscriptions_fallback_disabled_declarer_unvalidated`)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

### Task 7: Generate `fallback` outbounds and rewire the tags that point at them

A chain on the **default** subscription changes `route.final`; a chain on a **non-default**
subscription changes the tag its own route rules point at. Both are the same construction — a group
tagged `<id>-fallback` that supersedes that subscription's `FinalTag` — so implement it once and
apply it per subscription.

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription.go`
- Modify: `horn-vpn-manager/internal/subscription/route.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription_test.go`
- Modify: `horn-vpn-manager/internal/subscription/route_test.go`

- [x] add the `FallbackOutbound` type, with `InterruptExistConnections` set to `true` per Task 2
- [x] add an id→plan association: an `ID` field on `OutboundPlan`, set by `BuildOutbounds`, from
      which `applyFallbackChains` builds the `map[string]*OutboundPlan` it needs
- [x] for every subscription declaring a chain, emit a group tagged `<id>-fallback` whose outbounds
      are that subscription's own `FinalTag` followed by each referenced subscription's `FinalTag`
      in declared order
- [x] resolve references **after** every plan exists, so a backup that itself declares a chain
      contributes its `<id>-fallback` tag rather than its bare final tag — `applyFallbackChains`
      first settles which subscriptions get a group, then resolves member tags in a second pass;
      config validation rejects cycles, so one resolution pass suffices. Subscriptions are walked in
      sorted id order so log output does not depend on map iteration order.
- [x] when the declaring subscription is the default, pass the fallback tag as `defaultFinalTag` to
      `singbox.RenderConfig`; otherwise rewrite that subscription's generated route rules to target
      the fallback tag instead of its plain `FinalTag` (new `RetargetRouteRules` in `route.go`)
- [x] append the groups in `collectSingboxParts` (`:425`)
- [x] register `plan.TagNames[fallbackTag]` so rpcd `get_sb_status` and `test_url` (which reads
      `subs-tags.json`, backend `:379`, `:392-398`) show a name rather than a bare tag
- [x] when a referenced backup produced no plan, drop it from the chain with a `logx.Warn` and
      continue — do **not** abort, matching the existing skip-and-continue policy at `:337-345`
- [x] handle the degenerate case where every backup failed: emit no fallback group and leave the
      subscription's tag as it was, logging why
- [x] emit `blacklist_timeout` only when configured, letting sing-box apply its own default
- [x] write tests: single-node primary + multi-node backup resolve to `<id>-single` / `<id>-manual`;
      order preserved; tag name registered (`TestRunner_Run_fallback_on_default`,
      `TestFallbackOutbound_JSONMarshal`, `TestBuildOutbounds_PlanCarriesID`, `TestRetargetRouteRules`)
- [x] write tests for a chain on the **default** subscription (`route.final` becomes the fallback
      tag) and on a **non-default** one (its route rules retarget, `route.final` untouched)
      (`TestRunner_Run_fallback_on_default`, `..._fallback_on_non_default`)
- [x] write a test for a backup that itself declares a chain (nested resolution)
      (`TestRunner_Run_fallback_nested`)
- [x] write tests for the degraded chain, the all-backups-failed case, and unchanged behavior when
      `fallback` is absent (`TestRunner_Run_fallback_degraded`, `..._fallback_all_backups_failed`,
      `..._no_fallback_unchanged`)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues as `HEAD`, none new

### Task 8: Enrich the apply-time error for builds without `fallback` support

`ApplySingbox` (`internal/system/system.go:100-108`) already runs `sing-box check` and surfaces
`unknown outbound type` verbatim. This task adds a hint, not a new mechanism, and must not put
process execution into the pure-rendering `internal/singbox` package.

**Files:**
- Modify: `horn-vpn-manager/internal/system/system.go`
- Modify: `horn-vpn-manager/internal/system/system_test.go`

- [x] when a check failure mentions an unknown `fallback` outbound, wrap the error with a hint naming
      the extended-build requirement; leave all other failures untouched — `isUnknownFallbackType`
      requires both `fallback` and an `unknown outbound type` / `unknown type` phrasing
      (case-insensitive), so a fallback group rejected for a real config bug is not mis-hinted
- [x] keep the failure explicit — no silent degradation to the previous `route.final`, per the
      project rule against silent fallbacks that hide failure; the original `sing-box` output is
      preserved in the wrapped error alongside the hint
- [x] note in the code comment that `--dry-run` and `--debug` never reach `ApplySingbox`, so this is
      not a substitute for the Post-Completion device check
- [x] write tests using the existing `Cmd` fake for both the hinted and unhinted failure paths
      (`TestApplySingbox_check_failure_fallback_hint`,
      `..._check_failure_unrelated_not_hinted`; both assert the failure stays hard — no promotion,
      no restart)
- [x] run `go test ./...` — passes; `gofmt -l .` clean; `golangci-lint run` reports the same 10
      pre-existing `goconst` issues, none new

### Task 9: rpcd backend — allow inline-node subscriptions

`get_config` and `set_config` already pass unknown subscription fields through verbatim, so no
read/write plumbing is required. The blocker is validation.

**Files:**
- Modify: `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager`

- [x] relax the mandatory-`url` rejection to `url` XOR `nodes` in **both** `set_config` (`:83-88`)
      and `set_full_config` (`:180-183`) — the duplicated block is now a single `check_sub_sources`
      helper called from both, so the two paths cannot drift apart again
- [x] keep sh-level checks structural only (types, XOR presence); delegate schema validation by
      running `vpn-manager check -c <tmp>` on the candidate file and returning its error string,
      rather than reimplementing cross-reference logic in a regex-less `jq` — `check_with_core`
      validates the **merged** config (not the raw input), so an inline node's URI syntax, a
      `fallback` cross-reference and `singbox.connect_timeout` are all rejected at save time with
      the core's own message
- [x] ⚠️ **scope note:** `check_with_core` accepts the candidate on the structural checks alone when
      the core is unreachable (binary absent, `/tmp` unwritable). Documented in the function comment;
      it keeps a LuCI save working exactly as it did before the delegation existed rather than
      bricking config edits on a partially installed system.
- [x] error replies go through a `fail_json` helper that JSON-escapes the message — core errors
      quote subscription ids, which would otherwise break the reply JSON
- [x] implement any string matching with shell `case` or `awk` — the core's `error: <msg>` line is
      extracted with `awk`/`index`, no `jq` regex anywhere
- [x] run `sh -n` on the script and `make build` — `dash -n` clean (macOS `sh` is bash 3.2 and
      mis-parses a pre-existing `case`-inside-`$()` at `:756`, on `HEAD` too; `dash` is the closest
      shell to OpenWrt `ash`). `shellcheck -s sh` reports only the 2 pre-existing SC2221/SC2222
      warnings. `make build-luci` produced `bin/horn-vpn-manager-luci-2.2.1-r1.apk`.
- [x] validated behaviorally by sourcing both helpers into a `dash` harness: url-only, nodes-only,
      `{"url":"","nodes":[...]}`, and a disabled sourceless subscription accepted; both-sources,
      neither-source, empty name, non-array `nodes`, non-string `url` and an empty string inside
      `nodes` rejected with their specific messages; and `check_with_core` rejecting a malformed
      URI, a `a → b → a` fallback cycle and an invalid `connect_timeout`, leaving no temp file

### Task 10: LuCI — stop dropping unknown fields, add inline-node mode

⚠️ **Blocking before release.** `config.js:2296` rebuilds each subscription from an allow-list and
`:2323` rebuilds `singbox` from three known keys. Until this ships, *any* save from LuCI — even an
unrelated one such as toggling a log level — silently wipes `nodes`, `fallback` and
`connect_timeout` from `config.json`, making Tasks 4-7 unusable on a device with the addon installed.

**Files:**
- Modify: `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js`

- [ ] preserve unknown subscription and `singbox` fields across a save instead of rebuilding from an
      allow-list
- [ ] let a subscription be defined by inline `vless://` nodes instead of a URL, switching the card
      between modes and hiding the irrelevant field
- [ ] keep client-side URI checking minimal — `vless://` prefix plus a `new URL()` sanity check; the
      authoritative error comes from Go, do not reimplement `vless.Parse` in JS
- [ ] verify a no-op save round-trips `config.json` unchanged
- [ ] run `make build`

### Task 11: LuCI — fallback chain editor

**Files:**
- Modify: `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js`

- [ ] on every subscription card, add an ordered picker of the other enabled subscriptions
- [ ] add a `blacklist_timeout` field
- [ ] prevent self-reference, disabled references, duplicates and cycles in the picker
- [ ] verify the chain round-trips through save and reload
- [ ] run `make build`

### Task 12: LuCI — connect_timeout, warnings, i18n, import/export

**Files:**
- Modify: `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js`
- Modify: `horn-vpn-manager-luci/po/en/horn-vpn-manager.po`
- Modify: `horn-vpn-manager-luci/po/ru/horn-vpn-manager.po`
- Modify: `horn-vpn-manager-luci/root/www/luci-static/resources/horn-vpn-manager/style.css` (if needed)

- [ ] expose `singbox.connect_timeout` alongside the other sing-box settings
- [ ] warn that switching providers changes the public egress IP and that established sessions are
      not migrated
- [ ] verify import/export round-trips all new fields
- [ ] add en/ru translations for every new string
- [ ] run `make build`

### Task 13: Documentation and example config

**Files:**
- Modify: `horn-vpn-manager/files/config.example.json`
- Modify: `README.md`
- Modify: `AGENTS.md`
- Modify: `horn-vpn-manager/Makefile` (only if the DEPENDS question below is answered yes)

- [ ] add an annotated personal-node subscription, a `fallback` chain, and
      `singbox.connect_timeout` to the example config
- [ ] update `README.md` — it documents the config schema in Russian (subscription fields, generated
      tag naming, singbox settings) and is the user-facing doc
- [ ] document the new fields in the Config Model section of `AGENTS.md`
      (`CLAUDE.md` is a symlink — edit `AGENTS.md`)
- [ ] document that `fallback` requires the sing-box **extended** build and is not an upstream
      outbound type, noting the deliberate exception to the upstream-docs-as-source-of-truth rule
- [ ] document that groups now emit `interrupt_exist_connections: true`, and that operators should
      raise per-subscription `tolerance` (~300 ms, default is 100) so `urltest` re-selects only on
      genuine degradation — otherwise benign latency jitter will cut live downloads, streams and
      WebSockets. Place this next to the `tolerance` field in `README.md`.
- [ ] document that `url` and `nodes` are mutually exclusive, that `fallback` works on any
      subscription, and what a chain changes (`route.final` for the default, the subscription's own
      route-rule target otherwise)
- [ ] document the egress-IP change and the lack of live-session migration
- [ ] decide whether `horn-vpn-manager/Makefile` should declare a sing-box `DEPENDS` (it currently
      declares none) and record the decision either way

### Task 14: Verify acceptance criteria

- [ ] verify all requirements from Overview are implemented
- [ ] verify a config with no duplicate nodes and none of the new fields renders unchanged apart
      from the added group field
- [ ] verify a config containing duplicate nodes loses exactly the duplicates and keeps nodes that
      share a `StableHash` but differ in `ALPN` / `Mode` / `HeaderType`
- [ ] run `gofmt -l .` and `golangci-lint run` inside `horn-vpn-manager/`
- [ ] run the full test suite: `go test ./...`
- [ ] run `sh -n` on the rpcd script
- [ ] run `make lint` and `make build` from the repo root

### Task 15: [Final] Update documentation

- [ ] update `AGENTS.md` if new patterns were discovered during implementation
- [ ] close issue #1 with a note on the delivered schema
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Manual verification on an OpenWrt device** (the host `sing-box` is not authoritative, and
`--dry-run` / `--debug` never invoke `ApplySingbox`, so these need a real device with the extended
build):

- Install the rebuilt core and LuCI packages, then regenerate the sing-box config.
- Confirm a chain on the default subscription makes `route.final` point at `<id>-fallback`, and a
  chain on a non-default one retargets that subscription's route rules while `route.final` stays put.
  In both cases the group must list the primary tag followed by each backup's final tag in order.
- Confirm duplicate node outbounds are gone from the generated config.
- Exercise fallback end to end: primary blacklisted on dial failure, new connections served by the
  backup, primary retried after `blacklist_timeout`.
- **Regression check for the field-drop bug:** open LuCI, save without making changes, and `diff`
  `config.json` before and after — it must be unchanged.
- Verify `get_sb_status` and `test_url` render a sensible name for a fallback `route.final`.
- Call each changed rpcd method over ubus and diff `config.json` before and after.

**External system updates:**

- None. The only consumer of the config schema is the bundled LuCI addon, updated in Tasks 10-12.
