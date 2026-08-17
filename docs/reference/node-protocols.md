# Node protocol layer reference

Detail behind the one-line rules in `AGENTS.md` → *Node Protocol Layer*.

Node protocols are pluggable. `internal/proto` owns the contract, each protocol package implements it for its own URI scheme, and `internal/nodes` maps a scheme to its parser. `BuildOutbounds`, `decode.go` and `config.go` speak only `proto.Node` and `nodes.Parse` — none of them names a protocol.

## The contract (`internal/proto`)

```go
type Node interface {
    Type() string                            // sing-box outbound type
    Server() string
    Port() int
    Name() string                            // display name from the URI fragment
    StableHash() string                      // 8 hex chars; input format frozen per protocol
    Outbound(tag, connectTimeout string) any // typed sing-box outbound struct
}
```

- `proto` also owns `OutboundTLS`, `UTLSConfig` and `RealityTLS`. They live there, not in `internal/subscription`, because that is what keeps the import graph acyclic: protocol packages import `proto`, and `subscription` imports the protocol packages.
- `Outbound` returns a protocol-specific typed struct as `any`, so each protocol keeps its own JSON tags and field order. An empty `tag` yields the tagless form `BuildOutbounds` uses as the dedup key; an empty `connectTimeout` omits the field.
- Tags are carried by `OutboundPlan.NodeTags` (parallel to `NodeOutbounds`), never read back off an outbound — the plan stores `any`, which has no `Tag` field.

## Adding a protocol

1. new package under `internal/<protocol>` implementing `proto.Node`, owning its own outbound struct;
2. one entry in the `parsers` map in `internal/nodes/nodes.go` (one per accepted scheme — `hysteria2` and `hy2` share a parser);
3. its own `StableHash` prefix (`vless|…`, `hysteria2|…`), which makes cross-protocol tag collisions structurally impossible;
4. the LuCI scheme allow-list (`NODE_URI_SCHEMES` in `config.js`), or the frontend rejects the URI the core accepts.

Two shapes both protocol packages follow, for reasons that bite otherwise:

- **`Node` fields are unexported, with an accessor each, and `Parse` is the only constructor.** `proto.Node` requires `Server()`, `Port()` and `Name()` methods, and a Go type cannot carry a field and a method under the same name.
- **The `parsers` map entries are named adapter functions, not one-line closures.** `return vless.Parse(uri)` returns a typed nil `*vless.Node` on the error path, which becomes a **non-nil** `proto.Node` interface; each adapter returns an explicit `nil` instead. Pinned by `TestParse_PropagatesProtocolError`.

Scheme matching in the core is case-sensitive, matching each parser's own `strings.HasPrefix` check: `VLESS://` is an unknown scheme, not a silently accepted one.

Nothing else changes: `decode.go` filters lines via `nodes.IsKnownScheme`, `config.go` validates inline `nodes` via `nodes.Parse`, and error messages list `nodes.Schemes()`.

Registration is an explicit map, not `init()` side-effects: `internal/nodes` holds the only non-test imports of the protocol packages, so an import-side-effect registry would be empty at runtime the moment a blank import went missing — the build would still succeed and every subscription would silently fail to parse. A map literal also gives deterministic scheme ordering for error messages for free.

## Node identity

- `StableHash` is a **tag** function, not an identity function: the VLESS implementation hashes 13 of the node's 17 fields and omits `name`, `ALPN`, `Mode` and `HeaderType` — the last three each change the rendered outbound. Equal hash therefore does not mean identical node.
- Deduplication in `BuildOutbounds` is keyed on the marshalled tagless outbound (`n.Outbound("", connectTimeout)`), not on the hash. The `seenTags` `-N` suffix stays, because two genuinely distinct nodes can still collide on a tag; the counter advances for skipped duplicates too, so dropping a duplicate never renames a surviving node.
- Do not widen what `StableHash` covers: it would rewrite every tag and invalidate `subs-tags.json`, saved selector choices and `experimental.cache_file` state.

## Invariants

- **The VLESS hash string is frozen.** `vless.StableHash` must keep its exact `vless|server|port|uuid|security|sni|pbk|sid|flow|fp|type|path|host|serviceName` layout. Adding a type field to the hash input, or reordering it, repoints every saved selector choice on every deployed router. `internal/vless` and `internal/hysteria2` each pin their hash with a `TestStableHash_Golden` whose md5 values were computed **outside Go** (`printf … | md5sum`), with the exact hash input recorded per case.
- **`internal/subscription/testdata/golden_vless_config.json` is the regression gate for tag stability.** `TestRenderedConfig_MatchesGolden` renders fixed VLESS subscriptions through `singbox.RenderConfig` and compares bytes. There is deliberately no `-update` flag: a diff means node tags moved, invalidating `subs-tags.json`, saved selector choices and `experimental.cache_file` on every deployed router. Never regenerate it to make a diff go away.
- **Groups are protocol-agnostic by construction.** `urltest`, `selector` and `fallback` reference members by tag only, so a subscription mixing protocols shares one `urltest`/`selector` pair and a `fallback` chain can cross protocols without any change to group generation.
- **JSON subscription decoding stays VLESS-only.** `jsondecode.go` converts V2Ray/Xray outbounds, a format that carries VLESS; there is no evidence of providers shipping hysteria2 that way, and speculative conversion is unverifiable.
- **Node URIs carry credentials and must never appear in an error.** The message travels to the subscriptions log and, through rpcd `check_with_core` → `fail_json`, to a LuCI notification. Two places have to cooperate: `nodes.Parse` and both protocol packages never quote the URI, and `internal/config` locates a bad inline node by position (`subscription %q has an invalid node at position %d`). The subtle half is `url.Parse`, whose `*url.Error` renders as `parse "<the whole URI>": <reason>` — each protocol's `Parse` unwraps it through its own `parseReason` helper and wraps only the reason. Pinned by `TestParse_ErrorsDoNotLeakCredentials` (`internal/nodes`), which covers every parser and every rejection path, not just an unknown scheme.
- **Widening the accepted schemes can change a subscription's topology.** A provider payload that yielded one VLESS node before and now also yields hysteria2 nodes becomes multi-node: its final tag moves from `<id>-single` to `<id>-manual`, so the saved selector choice and the `clash.db` entry stop resolving and a node has to be re-picked once in LuCI. `warnTopologyShift` (`decode.go`) logs exactly that on the `1 vless + n new-scheme` case; the 0→n case stays silent because such a payload did not decode at all before. This is a one-time event per affected subscription, not a bug.
- `warnTopologyShift` is called from the pipeline (`subscription.go`, both the default and the `processSub` path) **after `BuildOutbounds`**, not from `DecodePayload`. Two filters run between decoding and the plan and each one can undo the shift: include/exclude can drop the new-scheme nodes, and `BuildOutbounds` skips a node URI the parser rejects (`obfs=gecko`, empty auth), so `1 vless + 1 broken hysteria2` stays single-node. It therefore takes the built plan and warns on `len(plan.NodeTags) >= 2`, counting nodes from `NodeTags` (dedup has already run) and legacy nodes only from URIs that `nodes.Parse` accepts. Warning any earlier is a false alarm repeated on every cron run. It is not called for inline `nodes` at all — those have no pre-multi-protocol tag to invalidate — and it takes the subscription id, since phase 2 decodes concurrently and an unattributed warning cannot be traced back.

## hysteria2 specifics

- schemes `hysteria2://` and `hy2://` are both official; the **port is optional and defaults to 443**, and auth is the **whole userinfo component**, which may itself contain a colon (`url.User.String()`, percent-decoded — `Username()` alone truncates it)
- `alpn`, `upmbps` and `downmbps` are client extensions, not URI-spec fields; leaving both bandwidth values unset selects BBR instead of Brutal
- `tls` is required on the outbound and always emitted; `ignore_client_bandwidth` and `masquerade` are inbound-only and must not be
- sing-box implements only `salamander` obfuscation, so any other `obfs` value (including the spec's `gecko`) is rejected at parse — as is `salamander` with an empty password, which would otherwise fail `sing-box check` on the whole generated config instead of skipping one node. The mirror case is rejected too: `obfs-password` without `obfs` renders an **unobfuscated** outbound, because `NewOutbound` emits the block only when `obfsType` is set. Accepting it either breaks the handshake against an obfuscated server or sends plain QUIC — the thing the operator wrote the password to avoid
- port hopping (`mport` / `server_ports` / `hop_interval`), `pinSHA256` and `ech` are out of scope

## The endpoint boundary (AmneziaWG / WireGuard)

- WireGuard-family protocols render into sing-box `endpoints`, not `outbounds`, and have no URI form, so they cannot satisfy `proto.Node` — whose whole purpose is producing an outbound from a URI. Do not stretch the contract to fit them.
- They would need their own `config.json` key (not inline `nodes`) and their own section in `singbox.RenderConfig`, which already preserves unknown top-level keys including `endpoints` verbatim — so nothing in this layer blocks adding them. Whether an endpoint may appear in a `fallback` chain is an open question for that work.
