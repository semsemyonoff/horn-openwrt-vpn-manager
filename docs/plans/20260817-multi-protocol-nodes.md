# Multi-Protocol Nodes

## Overview

`horn-vpn-manager` understands exactly one node protocol: VLESS. `internal/vless` rejects any URI
without a `vless://` prefix, `extractVLESSLines` drops every other line from a downloaded
subscription, `config.Subscription.Nodes` is validated with `vless.Parse`, and
`OutboundPlan.NodeOutbounds` is typed `[]*VLESSOutbound`.

This plan makes the node layer protocol-agnostic and adds **hysteria2** as the second supported
protocol, through both node sources introduced by the previous plan: a remote subscription `url`
and inline `nodes`.

### Problem being solved

The production deployment already runs hysteria2 and had to bypass the tool to do it. TSPU
silently freezes established TCP flows one way — the Claude API stream dies mid-response after
35–51 KB, the VPS retransmits the same `seq` 10–11 times and not one retransmission reaches the
router's WAN port, while the reverse direction stays alive and nobody sends RST. The freeze is
transport- and port-agnostic for TCP (moving the API to xhttp `:8443` produced 7 fatal freezes in
9 minutes) and leaves UDP alone (2.1 MB in 90 s, zero loss up to the WAN port).

The fix — hysteria2 for `api.anthropic.com` — could not be expressed in `config.json`. It is
currently a hand-written outbound in a custom sing-box template plus a hand-written route rule,
which costs:

- the node is invisible to everything the tool provides: no `urltest`, no `selector`, no
  `fallback` chain, no entry in `subs-tags.json`, no LuCI card, no delay test;
- its route rule lands **after** every subscription-generated rule (`mergeRoute` appends static
  template rules last), so a broader `domain_suffix` in any subscription list silently hijacks the
  domains — which is exactly what happened with `anthropic.com` and cost a debugging session;
- template and `config.json` drift apart, and the template now carries a secret.

A second protocol is not a hypothetical: the same freeze will keep pushing traffic off TCP, and
the `fallback` groups added by the previous plan are most valuable when the backup uses a
*different transport* than the node that failed.

## Context (from discovery)

### Baseline: what the previous plan left in place

`docs/plans/completed/20260816-fallback-and-personal-providers.md` shipped on this branch and this
plan builds directly on three of its outcomes:

- **Inline `nodes`** — `Subscription.Nodes []string` as an XOR alternative to `url`
  (`internal/config/config.go:47`, `validateSource` at `:209-230`). The self-hosted hysteria2 node
  is exactly the case inline nodes exist for.
- **Fallback chains** — `OutboundPlan.FallbackGroup`, generated `<id>-fallback` groups. Group
  outbounds reference members **by tag only**, so they are already protocol-agnostic; nothing in
  this plan needs to touch them.
- **`singbox.connect_timeout`** — emitted as a dial field on every node outbound
  (`VLESSOutbound.ConnectTimeout`).

Also inherited: dedup by marshalled outbound JSON, the `-2` collision suffix, and
`interrupt_exist_connections` on groups.

### Coupling points to VLESS

Every one of these was verified on this branch.

| File | What is coupled |
|---|---|
| `internal/vless/vless.go:46` | `Parse` rejects anything but `vless://` |
| `internal/vless/vless.go:123-133` | `StableHash` builds a `vless\|…` string; node tags derive from it |
| `internal/subscription/outbound.go:37` | `NodeOutbounds []*VLESSOutbound` |
| `internal/subscription/outbound.go:67-167` | `VLESSOutbound`, `OutboundTLS`, `UTLSConfig`, `RealityTLS`, `OutboundTransport` + its `MarshalJSON` |
| `internal/subscription/outbound.go:236-330` | `BuildOutbounds` calls `vless.Parse` / `vless.StableHash` |
| `internal/subscription/outbound.go:260,266` | user-visible strings "skipping unparseable VLESS URI", "no valid VLESS nodes found in subscription %q" |
| `internal/subscription/outbound.go:366-460` | `nodeToOutbound`, `buildTransport` |
| `internal/subscription/subscription.go:114-128` | `extractNodeName`, documented as reading a **VLESS** URI fragment; drives `include`/`exclude` for every protocol after this plan |
| `internal/subscription/subscription.go:301,662` | `ob.Tag` read off `NodeOutbounds` in debug logging — breaks the moment the slice element type stops being `*VLESSOutbound` |
| `internal/subscription/decode.go:184-200` | `extractVLESSLines` filters by `vless://` prefix |
| `internal/subscription/decode.go:21,79,82,92,109,131,196` | `FormatRaw` comment, four `try*` doc comments, the "no supported encoding" error, the "vless line scan truncated" warning |
| `internal/subscription/jsondecode.go` | V2Ray/Xray JSON → `vless://` URI conversion |
| `internal/config/config.go:42-43,218,224,226` | `Nodes` doc comment, two error strings, `vless.Parse` call |
| `…/view/horn-vpn-manager/config.js:1047-1060` | `isValidNodeUri` hardcodes the `vless://` prefix |
| `…/view/horn-vpn-manager/config.js:148,2437,2503,2523,2974,2980` | comments, placeholder, help text, empty-list warning, invalid-URI warning |
| `horn-vpn-manager-luci/tests/config.test.js:238-247` | existing test asserting `/valid vless/` against the notification text |
| `AGENTS.md:50,124,144,265` | package description, inline-`nodes` description, StableHash note, testing-coverage list |

### Load-bearing invariant

Node tags are `<id>-node-<StableHash>`. Those tags are written to `subs-tags.json`, referenced by
saved selector choices, and persisted in `experimental.cache_file` (`/etc/sing-box/clash.db`).
**Any change to the VLESS hash input silently repoints every saved choice on every deployed
router.** The hash string therefore has to stay byte-identical, which rules out "just add a type
field to the hash input".

There is no automated gate for this today: `internal/subscription/testdata` holds only
`raw_subscription.txt` (a decode fixture read at `decode_test.go:108`), and no golden of rendered
output exists anywhere in the Go tree. Task 1 creates one before anything else moves.

### Protocol facts

Confirmed against the [Hysteria 2 URI scheme spec](https://v2.hysteria.network/docs/developers/URI-Scheme/):

- scheme is `hysteria2` **or** `hy2` — both official;
- **the port is optional and defaults to 443**;
- **auth is the entire userinfo component**, which may itself contain a colon (`username:password`
  for userpass auth), so `u.User.Username()` alone silently truncates it — use `u.User.String()`
  and percent-decode;
- spec query parameters: `obfs`, `obfs-password`, `sni`, `insecure`, `pinSHA256`, `ech`;
- `alpn`, `upmbps`, `downmbps` are **not in the spec** — they are v2rayN/Nekoray-lineage client
  extensions. Widely emitted by providers, so worth parsing, but they must be documented as
  extensions rather than spec fields.

sing-box side (`Hysteria2OutboundOptions`, cross-checked against the deployed extended build's
struct tags):

- outbound fields: `password`, `obfs{type,password}`, `up_mbps`, `down_mbps`, `network`,
  `brutal_debug`, `server_ports` + `hop_interval` (port hopping), plus the shared `tls` block;
- `tls` is **required** on a hysteria2 outbound;
- `ignore_client_bandwidth` and `masquerade` are **inbound-only** and must not be emitted;
- the spec allows `obfs=gecko`, but sing-box implements **only `salamander`** — a gecko URI has to
  be rejected with a message that says so, not silently downgraded;
- omitting `up_mbps`/`down_mbps` selects BBR instead of Brutal. That is the right default here: the
  measured path has zero loss, and Brutal's whole point is ignoring loss;
- hysteria2 needs a QUIC-enabled build. Both deployed binaries carry `with_quic`, and
  `system.ApplySingbox` already runs `sing-box check` against a staging file, so a build without it
  fails loudly before the live config is touched.

Note on `connect_timeout`: it is a `DialerOptions` field governing TCP dialing. hysteria2 dials a
UDP packet conn and then runs a QUIC handshake with its own idle timeout, so emitting it is
schema-valid and harmless but probably does **not** shorten a fallback switch the way it does for
VLESS. It is emitted for consistency, not for a benefit this plan claims to have measured.

## Development Approach

- **testing approach**: Regular (code first, then tests in the same task)
- complete each task fully before moving to the next
- make small, focused changes
- **CRITICAL: every task MUST include new/updated tests** for code changes in that task
  - unit tests for new and modified functions
  - new test cases for new code paths, updated cases when behaviour changes
  - both success and error scenarios
- **CRITICAL: all tests must pass before starting the next task** — each task must compile and test
  on its own, which is why the contract, both parsers and the dispatcher land in that order
- **CRITICAL: update this plan file when scope changes during implementation**
- run `go test ./...` after each change
- maintain backward compatibility: an existing VLESS-only `config.json` must produce
  byte-identical sing-box output

## Testing Strategy

- **unit tests**: required for every task
- **golden file**: created in Task 1 from the pre-refactor build, then held byte-identical through
  every later task. It is the regression gate for the tag-stability invariant and must never be
  regenerated to make a diff go away.
- **table-driven parser tests**: per protocol — minimal URI, fully-populated URI, and each
  rejection.
- **mixed-protocol fixture**: a subscription payload with VLESS and hysteria2 lines interleaved,
  asserting both survive into one plan with correct tags and one shared `urltest`/`selector` pair.
- **LuCI**: `make luci-test` (runs `tests/config.test.js` and `tests/rpcd-checks.test.sh`). Note
  that `rpcd-checks.test.sh` skips entirely without `jq`, and `check_with_core` returns 0 early when
  `vpn-manager` is not on `PATH` (`rpcd:88`) — a backend test that expects a core rejection must
  therefore assert the skip explicitly rather than passing vacuously.
- **on-device**: `sing-box check` is the only authoritative validator of generated JSON; the
  acceptance task runs it on the router.

## Progress Tracking

- mark completed items with `[x]` immediately when done
- add newly discovered tasks with ➕ prefix
- document issues/blockers with ⚠️ prefix
- update plan if implementation deviates from original scope
- keep plan in sync with actual work done

## Solution Overview

**A node contract plus an explicit dispatcher.**

`internal/proto` owns the interface every protocol node satisfies and the TLS structs shared
between protocols. `internal/vless` and `internal/hysteria2` each implement the contract for their
own protocol and own their own outbound structs. `internal/nodes` imports both and holds a plain
map from scheme to parser; `BuildOutbounds` and `config` call `nodes.Parse`.

Adding a protocol afterwards is one new package plus one map entry — no edits to `BuildOutbounds`,
`decode.go` or `config.go`.

### Why a dispatcher rather than `init()` registration

The obvious shape is `proto.Register(scheme, fn)` called from each protocol's `init()`. It was
rejected after review:

- the Go core currently contains **zero** `func init()` and **zero** `panic(` outside tests;
  introducing both for this is a style break AGENTS.md argues against ("readability and testability
  over clever abstractions", "keep public/package boundaries explicit");
- registration by import side-effect is a trap here specifically: this plan removes the last
  non-test import of `internal/vless` (from `outbound.go` and `config.go`), so with `init()`
  registration and no blank import the registry would be **empty at runtime** — the build succeeds
  and every subscription silently fails to parse;
- a map literal gives deterministic scheme ordering for error messages for free, where a registry
  needs an explicit sort.

Also rejected: a **union struct** (one `Node` with every protocol's fields and a `switch`) — every
new protocol edits a shared file; and **parsers emitting `json.RawMessage`** — AGENTS.md requires
typed models for generated sing-box config.

### Key design decisions

1. **The VLESS hash string is frozen.** `StableHash()` returns a protocol-specific string; the
   VLESS implementation keeps its exact `vless|…` layout. New protocols use their own prefix, so
   cross-protocol collisions are structurally impossible while old tags never move.
2. **`Outbound()` returns a typed struct as `any`.** Each protocol keeps its own typed outbound
   struct with its own JSON tags; the existing pipeline marshals it.
3. **Tags are carried by the plan, not read back off the outbound.** `BuildOutbounds` currently
   builds a tagless outbound, marshals it as the dedup key, then assigns `ob.Tag` — impossible
   through an opaque `any`. Instead `Outbound("", ct)` produces the dedup key and `Outbound(tag, ct)`
   produces the final value, and `OutboundPlan` gains `NodeTags []string` for the debug logging that
   reads `ob.Tag` today.
4. **Groups stay untouched.** `urltest`, `selector` and `fallback` reference tags, so a
   mixed-protocol subscription works without changes there.
5. **JSON subscription decoding stays VLESS-only.** `jsondecode.go` converts V2Ray/Xray outbounds,
   a format that carries VLESS; there is no evidence of providers shipping hysteria2 that way, and
   speculative conversion is unverifiable. Only its comments get corrected.
6. **Port hopping (`mport` / `server_ports` / `hop_interval`), `pinSHA256` and `ech` are out of
   scope.** Port hopping is genuinely interesting against this DPI, but it needs its own testing
   story; parsing it half-way would produce configs nobody validated.
7. **AmneziaWG is deliberately out of scope.** It renders into sing-box `endpoints`, not
   `outbounds`, and has no URI form, so it cannot satisfy a contract whose purpose is producing an
   outbound. Task 10 records where it would attach so the abstraction does not block it.

## Technical Details

### The contract

```go
// internal/proto
type Node interface {
    Type() string                          // sing-box outbound type
    Server() string
    Port() int
    Name() string                          // display name from the URI fragment
    StableHash() string                    // 8 hex chars; input format frozen per protocol
    Outbound(tag, connectTimeout string) any
}
```

`internal/proto` also owns `OutboundTLS`, `UTLSConfig` and `RealityTLS`, moved out of
`internal/subscription`. This placement is what keeps the import graph acyclic: `vless → proto` and
`hysteria2 → proto` are fine, whereas leaving the TLS structs in `subscription` would force
`vless → subscription`, and `subscription` already imports `vless`.

### The dispatcher

```go
// internal/nodes
var parsers = map[string]func(string) (proto.Node, error){
    "vless":     func(u string) (proto.Node, error) { return vless.Parse(u) },
    "hysteria2": func(u string) (proto.Node, error) { return hysteria2.Parse(u) },
    "hy2":       func(u string) (proto.Node, error) { return hysteria2.Parse(u) },
}

func Parse(uri string) (proto.Node, error)  // ErrUnknownScheme lists Schemes() when it misses
func Schemes() []string                     // sorted, for error messages and line extraction
func IsKnownScheme(uri string) bool         // used by decode.go
```

### Hash inputs

| Protocol | Input |
|---|---|
| VLESS | `vless\|server\|port\|uuid\|security\|sni\|pbk\|sid\|flow\|fp\|type\|path\|host\|serviceName` **(unchanged)** |
| hysteria2 | `hysteria2\|server\|port\|auth\|obfsType\|obfsPassword\|sni\|insecure` |

### hysteria2 outbound shape

```json
{
  "type": "hysteria2",
  "tag": "<id>-single",
  "server": "…",
  "server_port": 443,
  "password": "…",
  "obfs": { "type": "salamander", "password": "…" },
  "tls": { "enabled": true, "server_name": "…", "insecure": true, "alpn": ["h3"] },
  "connect_timeout": "3s"
}
```

`up_mbps`/`down_mbps` are emitted only when the URI carried them, so the default stays BBR.

### Flow

```
subscription payload / inline nodes
        │
        ├─ decode.go: keep lines whose scheme nodes.IsKnownScheme accepts
        │
        ├─ nodes.Parse(uri) → proto.Node          (map lookup on scheme)
        │
        └─ BuildOutbounds: node.StableHash() → tag
                           node.Outbound("", ct)  → dedup key
                           node.Outbound(tag, ct) → typed struct
                           urltest / selector / fallback by tag  (unchanged)
```

## What Goes Where

- **Implementation Steps** (`[ ]`): Go core, LuCI, tests, docs and the example config in this repo.
- **Post-Completion** (no checkboxes): migrating the production router off the hand-written
  template, and the on-device verification that needs the real VPS.

## Implementation Steps

### Task 1: Pin current output with a golden file

**Files:**
- Create: `horn-vpn-manager/internal/subscription/testdata/golden_vless_config.json`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`

- [x] add a test that builds plans from fixed VLESS subscriptions — multi-node (reality + xhttp +
      ws), single-node, a duplicate node, a `StableHash` collision exercising the `-2` suffix, and
      `connect_timeout` set — renders them through `singbox.RenderConfig`, and compares the bytes
      against the golden
- [x] generate the golden from the **current** build, before any other task changes this code
- [x] add a comment naming what a diff means: node tags moved, invalidating `subs-tags.json`, saved
      selector choices and `experimental.cache_file` on every deployed router
- [x] run `go test ./...` — must pass before task 2

Notes:
- `TestRenderedConfig_MatchesGolden` + `renderGoldenConfig` / `goldenSubscriptions` live in
  `outbound_test.go`; the fixture also covers per-subscription route rules and `connect_timeout`
  omitted, and the duplicate + collision case lands on the `-3` suffix (the counter advances for the
  dropped duplicate), which pins that invariant too.
- The template is inlined in the test rather than read from
  `sing-box.template.default.json`, so editing the shipped template cannot force a regeneration.
- No `-update` flag was added, deliberately: the golden must not be regenerable as a reflex. The
  file was generated once from the pre-refactor build via a throwaway test that was then removed.
- Mutation-checked: perturbing the `vless|` hash prefix makes the test fail on the moved tag.
- ⚠️ `make lint` already reports 24 pre-existing `staticcheck` SA5011 findings on this branch,
  unrelated to this plan; verified the diff adds none.

### Task 2: Introduce the `proto` contract and move the shared TLS structs

**Files:**
- Create: `horn-vpn-manager/internal/proto/proto.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Create: `horn-vpn-manager/internal/proto/proto_test.go`

- [ ] define the `Node` interface from Technical Details
- [ ] move `OutboundTLS`, `UTLSConfig` and `RealityTLS` from `internal/subscription/outbound.go`
      into `internal/proto`, preserving field order verbatim (JSON key order follows struct field
      order and Task 11 demands byte-identical output)
- [ ] leave `VLESSOutbound` and `OutboundTransport` in `subscription` for now — Task 3 moves them
- [ ] update `subscription` to reference the moved types
- [ ] write tests asserting the moved structs still marshal to the same JSON as before
- [ ] run `go test ./...` including the Task 1 golden — must be byte-identical before task 3

### Task 3: Make `vless` satisfy the contract and own its outbound

**Files:**
- Modify: `horn-vpn-manager/internal/vless/vless.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/vless/vless_test.go`

- [ ] move `VLESSOutbound`, `OutboundTransport` (with its custom `MarshalJSON`), `nodeToOutbound`
      and `buildTransport` from `internal/subscription/outbound.go` into `internal/vless`,
      preserving field order and the per-transport JSON shapes verbatim
- [ ] add methods on `*Node`: `Type()`, `Server()`, `Port()`, `Name()`, `StableHash()` (delegating
      to the existing function so the string layout is untouched) and `Outbound(tag, ct string) any`
      wrapping the moved `nodeToOutbound`, keeping `packet_encoding: xudp`
- [ ] keep `Parse` and `StableHash` exported and behaviour-identical
- [ ] add a golden test asserting `StableHash` output for a fixed URI set, commenting what breaks if
      it changes
- [ ] write tests asserting the contract methods return the parsed values and that the moved
      outbound marshals identically
- [ ] run `go test ./...` including the Task 1 golden — must be byte-identical before task 4

### Task 4: Add the hysteria2 protocol package

**Files:**
- Create: `horn-vpn-manager/internal/hysteria2/hysteria2.go`
- Create: `horn-vpn-manager/internal/hysteria2/hysteria2_test.go`

- [ ] define `Node` (`Auth`, `Server`, `Port`, `Name`, `SNI`, `Insecure`, `ALPN`, `ObfsType`,
      `ObfsPassword`, `UpMbps`, `DownMbps`) and `Outbound`/`Obfs` structs reusing `proto.OutboundTLS`
- [ ] implement `Parse` for `hysteria2://` and `hy2://`: auth from the **whole userinfo**
      (`u.User.String()`, percent-decoded, colon allowed), **port optional defaulting to 443**,
      `sni`, `insecure`, `obfs`, `obfs-password` from the query, `alpn`/`upmbps`/`downmbps` as
      documented non-spec extensions, name from the fragment with the same `+`→space handling as
      VLESS
- [ ] reject: wrong scheme, missing auth, missing host, invalid port, and `obfs=gecko` with a
      message saying sing-box implements only `salamander`
- [ ] implement `StableHash` with the `hysteria2|…` input from Technical Details
- [ ] implement `Outbound(tag, ct string) any` emitting `connect_timeout` when set and
      `up_mbps`/`down_mbps` only when the URI carried them; add a comment recording that
      `ignore_client_bandwidth`/`masquerade` are inbound-only and that `tls` is required
- [ ] write table-driven parse tests: minimal URI, fully-populated URI, `hy2://` alias, colon in
      auth, omitted port, each rejection
- [ ] write marshalling tests: minimal node, node with obfs, node with bandwidth, node with
      `connect_timeout` — asserting exact JSON
- [ ] run `go test ./...` — must pass before task 5

### Task 5: Add the `nodes` dispatcher

**Files:**
- Create: `horn-vpn-manager/internal/nodes/nodes.go`
- Create: `horn-vpn-manager/internal/nodes/nodes_test.go`

- [ ] implement the map, `Parse`, `Schemes` (sorted) and `IsKnownScheme` from Technical Details
- [ ] define `ErrUnknownScheme` whose message lists `Schemes()`, so config errors tell the user what
      is accepted
- [ ] write tests: dispatch to each scheme, `hy2` alias reaching the same parser, unknown scheme
      error text, `Schemes()` ordering, `IsKnownScheme` on a line with no scheme at all
- [ ] run `go test ./...` — must pass before task 6

### Task 6: Rewire `BuildOutbounds` onto the contract

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/outbound.go`
- Modify: `horn-vpn-manager/internal/subscription/subscription.go`
- Modify: `horn-vpn-manager/internal/subscription/outbound_test.go`

- [ ] change `OutboundPlan.NodeOutbounds` to `[]any` and add `NodeTags []string` kept in the same
      order
- [ ] replace `vless.Parse`/`vless.StableHash` with `nodes.Parse` and `node.StableHash()`; drop the
      `vless` import
- [ ] build the dedup key from `node.Outbound("", ct)` and the stored value from
      `node.Outbound(tag, ct)`, so no post-marshal tag mutation is needed
- [ ] keep the suffix counter advancing for skipped duplicates (invariant from the previous plan)
- [ ] update the two debug log sites that read `ob.Tag` (`subscription.go:301,662`) to use
      `NodeTags`
- [ ] reword the two user-visible strings at `outbound.go:260,266` to name the scheme rather than
      VLESS
- [ ] update `extractNodeName` (`subscription.go:114-128`) and its doc comment — it feeds
      `include`/`exclude` for every protocol now
- [ ] verify the Task 1 golden is still byte-identical; update tests referencing `*VLESSOutbound`
- [ ] write a test building a mixed VLESS + hysteria2 subscription: both nodes tagged, one shared
      `urltest`/`selector` pair, `FinalTag` unchanged
- [ ] run `go test ./...` — must pass before task 7

### Task 7: Accept every dispatcher scheme when decoding subscriptions

**Files:**
- Modify: `horn-vpn-manager/internal/subscription/decode.go`
- Modify: `horn-vpn-manager/internal/subscription/decode_test.go`
- Modify: `horn-vpn-manager/internal/subscription/jsondecode.go`

- [ ] rename `extractVLESSLines` to `extractNodeLines` and match via `nodes.IsKnownScheme`
- [ ] update the "no supported encoding" error (`:79`) to list the supported schemes, and the
      truncation warning (`:196`) plus the `FormatRaw` and `try*` doc comments (`:21,82,92,109,131`)
- [ ] keep the base64 / base64url / gzip detection paths unchanged — they gate on extracted line
      count, which now covers more schemes
- [ ] **log a warning when a subscription's node count changes because newly-accepted schemes were
      picked up**: a payload that used to yield one node yields `<id>-single`, and two or more
      yields `<id>-node-<hash>` plus `<id>-auto`/`<id>-manual` with `FinalTag` moving to
      `<id>-manual` — which silently invalidates that subscription's saved selector choice and
      `clash.db` entry
- [ ] correct `jsondecode.go` comments to state it converts V2Ray/Xray **VLESS** outbounds only, and
      why (decision 5 in Solution Overview)
- [ ] write tests: raw payload with mixed schemes, base64 payload with hysteria2 lines, payload with
      an unregistered scheme (silently skipped, not an error), and the single→multi topology shift
      emitting the warning
- [ ] run `go test ./...` — must pass before task 8

### Task 8: Validate inline `nodes` through the dispatcher

**Files:**
- Modify: `horn-vpn-manager/internal/config/config.go`
- Modify: `horn-vpn-manager/internal/config/config_test.go`

- [ ] replace `vless.Parse` in `validateSource` (`:226`) with `nodes.Parse`
- [ ] update the two error strings (`:218,224`) and the `Nodes` doc comment (`:42-43`) to name the
      supported schemes rather than `vless://`, keeping the subscription id quoted
- [ ] confirm the XOR rule and the disabled-subscription exemption are untouched
- [ ] write tests: inline hysteria2 node accepted, mixed-protocol node list accepted, unknown scheme
      rejected with a message listing supported schemes, disabled subscription still exempt
- [ ] run `go test ./...` — must pass before task 9

### Task 9: Update the LuCI frontend, backend comment and translations

**Files:**
- Modify: `horn-vpn-manager-luci/root/www/luci-static/resources/view/horn-vpn-manager/config.js`
- Modify: `horn-vpn-manager-luci/root/usr/libexec/rpcd/horn-vpn-manager`
- Modify: `horn-vpn-manager-luci/tests/config.test.js`
- Modify: `horn-vpn-manager-luci/po/en/horn-vpn-manager.po`
- Modify: `horn-vpn-manager-luci/po/ru/horn-vpn-manager.po`

- [ ] generalise `isValidNodeUri` (`:1047-1060`) to a scheme allow-list (`vless`, `hysteria2`,
      `hy2`) requiring host plus userinfo, keeping it strictly looser than the core (existing
      invariant: client-side validation must never be stricter than `vpn-manager check`)
- [ ] update the placeholder (`:2437`), help text (`:2523`), empty-list warning (`:2974`) and
      invalid-URI warning (`:2980`), plus the comments at `:148,2503,2979`
- [ ] update all three msgid pairs in both `.po` files and note that changed msgids need the `.lmo`
      regenerated via `tools/po2lmo.py`
- [ ] update the existing assertion on `/valid vless/` in `tests/config.test.js:238-247`
- [ ] update the rpcd comment at `:78`; confirm the backend carries no structural `vless://` check
      to remove, and leave schema validation delegated to `check_with_core`
- [ ] write `isValidNodeUri` tests: each accepted scheme, bare scheme with no host, unknown scheme,
      mixed list
- [ ] run `make luci-test` — must pass before task 10

### Task 10: Document the protocol layer and the endpoint boundary

**Files:**
- Modify: `AGENTS.md`
- Modify: `horn-vpn-manager/files/config.example.json`

- [ ] refresh the stale lines at `AGENTS.md:50,124,144,265` (package list, inline-`nodes`
      description, StableHash note, testing-coverage list) — they all name VLESS as the only protocol
- [ ] document the `internal/proto` contract, the `internal/nodes` dispatcher, and how to add a
      protocol: new package, one map entry, own hash prefix
- [ ] document the frozen-VLESS-hash invariant and the golden file that guards it
- [ ] note that group outbounds are tag-based and therefore protocol-agnostic by construction
- [ ] record the AmneziaWG/WireGuard boundary: it renders into sing-box `endpoints`, has no URI
      form, cannot satisfy `proto.Node`, and would need its own config key and its own section in
      `RenderConfig` — which already preserves `endpoints` verbatim, so nothing here blocks it
- [ ] add a commented hysteria2 inline-node example to `config.example.json`
- [ ] verify the example parses with `vpn-manager check`
- [ ] run `go test ./...` and `make lint` — must pass before task 11

### Task 11: Verify acceptance criteria

- [ ] a subscription with inline `hysteria2://` nodes generates a valid outbound and route rules
- [ ] a remote subscription mixing VLESS and hysteria2 lines produces both, under one
      `urltest`/`selector` pair
- [ ] a hysteria2 node participates in a `fallback` chain as either the declaring subscription or a
      backup
- [ ] a hysteria2 URI with a colon in the auth and no explicit port round-trips correctly
- [ ] `obfs=gecko` is rejected with the sing-box-only-implements-salamander message
- [ ] the Task 1 golden is byte-identical, and a VLESS-only config on the router produces an
      unchanged `/etc/sing-box/config.json` (diff before/after)
- [ ] the single→multi topology warning fires when a previously VLESS-only payload gains hysteria2
      lines, and the consequence is documented for operators
- [ ] an unknown scheme in inline `nodes` fails `vpn-manager check` with a message listing the
      supported schemes
- [ ] run the full suite: `go test ./...`, `make lint`, `gofmt -l`, `make luci-test`
- [ ] on the router: `vpn-manager subscriptions dry-run` followed by `sing-box check -c` on the
      generated config

### Task 12: [Final] Update documentation

- [ ] update AGENTS.md if new patterns emerged during implementation
- [ ] update the LuCI invariants list if any new one was discovered
- [ ] move this plan to `docs/plans/completed/`

## Post-Completion

*Items requiring manual intervention or external systems — no checkboxes, informational only*

**Migrating production off the hand-written template**

The router currently carries the hysteria2 node as a static outbound `vps-api-hy2` in
`/etc/horn-vpn-manager/sing-box.template.json`, plus a static route rule for `api.anthropic.com`.
Once this plan ships, that becomes a normal subscription with inline `nodes`, which also removes
the rule-ordering hazard that forced `domains-web.lst` to spell out `www`/`console`/`docs`/
`support`/`status` instead of the `anthropic.com` suffix.

Migration order matters — the node must exist under its new tag before the old one is removed:

1. add the hysteria2 URI as inline `nodes` on a new subscription (or on `vps-api`, replacing its
   `url`), with the API domains in `route.domain_urls`;
2. `vpn-manager subscriptions dry-run`, confirm the generated rule now precedes the web
   subscription's rules;
3. remove the static outbound and rule from the template, and clear `singbox.template` if nothing
   else needs it;
4. apply, then re-run the long-stream harness (`claude -p` generating ~2000 lines) to confirm
   nothing regressed;
5. optionally restore the `anthropic.com` suffix in `domains-web.lst` once the API domains are
   matched by a subscription rule that sorts earlier.

The template still holds the hysteria2 password; after migration the secret lives only in
`config.json`, which is already `0600`.

**One-time re-selection after upgrade**

Any subscription whose provider payload already contains non-VLESS lines will gain nodes on the
first run after this ships, and a subscription that was single-node becomes multi-node — its
`FinalTag` moves from `<id>-single` to `<id>-manual` and its saved selector choice no longer
resolves. This is a one-time event per affected subscription, not a bug; operators should expect to
re-pick a node in LuCI.

**Manual verification**

- confirm on-device that a hysteria2 node appears in the LuCI subscription card, in
  `subs-tags.json`, and in the delay test
- confirm a `fallback` chain actually switches to a hysteria2 backup when the primary is
  unreachable (block the primary's port on the VPS rather than stopping sing-box, so the failure
  looks like a dead path rather than a clean refusal)

**Follow-up protocols**

`tuic`, `trojan`, `shadowsocks` and `vmess` each become a single package plus a map entry once this
lands. Port hopping (`mport`/`server_ports`/`hop_interval`) is a natural follow-up for hysteria2
specifically, given the DPI behaviour that motivated this work. AmneziaWG/WireGuard remains a
separate plan: it needs an `endpoints` section, a non-URI config shape, and a decision about whether
an endpoint may appear in a `fallback` chain.
