"use strict";

// Drives the shipped config.js — real _makeCard, _collectConfig, _validate and
// chain picker — against the stub DOM in stub-dom.js. Nothing here reimplements
// view logic; every assertion is on what the view actually produced.
//
// Run with: make luci-test
// (directly: node --test horn-vpn-manager-luci/tests/*.test.js — a bare
// directory argument is resolved as a module and fails)
//
// Every test here has been mutation-checked: reverting the fix it covers makes
// it fail. Keep it that way when adding one.

const test = require("node:test");
const assert = require("node:assert");
const { loadView, mountSubscriptions, renderView } = require("./load-view.js");

// ── helpers ──────────────────────────────────────────────────────────────────

function setInput(card, cls, value) {
    const el = card.querySelector(cls);
    assert.ok(el, "no element matching " + cls);
    el.value = value;
    el.dispatch("input");
    el.dispatch("change");
    return el;
}

function setChecked(card, cls, on) {
    const el = card.querySelector(cls);
    assert.ok(el, "no element matching " + cls);
    el.checked = on;
    el.dispatch("change");
    return el;
}

function notificationTexts(ctx) {
    return ctx.notifications.map((n) => n.node.textContent);
}

// ── render() wiring ──────────────────────────────────────────────────────────
//
// The tests below go through the real render() instead of mountSubscriptions,
// because the setup mountSubscriptions performs by hand — seeding _rawSingbox
// and _subIdx, resetting the chain pickers — is exactly what is under test here.

test("a config loaded through render() survives a no-op save byte for byte", () => {
    const ctx = loadView();
    const stored = {
        singbox: {
            log_level: "info",
            test_url: "https://probe.invalid/generate_204",
            connect_timeout: "5s",
            template: "",
            // Not rendered anywhere: the additive rpcd merge means dropping it
            // here would still be invisible on the router, but the next clear of
            // a sibling key would be written against a wrong snapshot.
            future_singbox_key: "keep",
        },
        subscriptions: {
            main: {
                name: "Main",
                default: true,
                url: "https://a.invalid/s",
                interval: "5m",
                tolerance: 300,
                future_key: 7,
                fallback: { subscriptions: ["backup"], blacklist_timeout: "1m" },
            },
            backup: {
                name: "Backup",
                nodes: ["hysteria2://pass@h.example.invalid:443#B"],
                exclude: ["ru"],
            },
        },
    };
    const before = JSON.stringify(stored);

    renderView(ctx, JSON.parse(before));
    const collected = ctx.view._collectConfig();

    assert.strictEqual(
        JSON.stringify(collected.subscriptions),
        JSON.stringify(stored.subscriptions),
        "render() → _collectConfig() must round-trip the subscriptions unchanged",
    );
    assert.strictEqual(
        JSON.stringify(collected.singbox),
        JSON.stringify(stored.singbox),
        "render() → _collectConfig() must round-trip singbox unchanged",
    );
    assert.strictEqual(JSON.stringify(stored), before, "the loaded config was mutated in place");
});

test("render() seeds _rawSingbox, so a key it never renders is not dropped", () => {
    const ctx = loadView();
    renderView(ctx, {
        singbox: { log_level: "warn", connect_timeout: "3s", future_singbox_key: "keep" },
        subscriptions: { main: { name: "Main", default: true, url: "https://a.invalid/s" } },
    });

    // _collectConfig starts from the _rawSingbox snapshot render() took. Without
    // that seeding it starts from {} and every unrendered key disappears.
    assert.strictEqual(ctx.view._rawSingbox.future_singbox_key, "keep");
    assert.strictEqual(ctx.view._collectConfig().singbox.future_singbox_key, "keep");
});

test("render() takes _subIdx from the stored config so a new card gets a fresh key", () => {
    const ctx = loadView();
    renderView(ctx, {
        singbox: {},
        subscriptions: {
            main: { name: "Main", default: true, url: "https://a.invalid/s" },
            other: { name: "Other", url: "https://b.invalid/s" },
        },
    });

    assert.strictEqual(ctx.view._subIdx, 2, "_subIdx must start past the stored subscriptions");
});

// ── field preservation ───────────────────────────────────────────────────────

test("a no-op save preserves fields the view does not render", () => {
    const ctx = loadView();
    const stored = {
        id: "prov",
        name: "Provider",
        url: "https://example.invalid/sub",
        default: true,
        // Neither key has an input in this view; both must survive.
        some_future_key: { nested: true },
        route: { domains: ["corp.example"], future_route_key: 1 },
    };
    mountSubscriptions(ctx, [stored], {
        rawSingbox: { connect_timeout: "3s", future_singbox_key: "keep" },
    });

    const cfg = ctx.view._collectConfig();
    assert.deepStrictEqual(cfg.subscriptions.prov.some_future_key, {
        nested: true,
    });
    assert.strictEqual(cfg.subscriptions.prov.route.future_route_key, 1);
    assert.strictEqual(cfg.singbox.future_singbox_key, "keep");
    assert.strictEqual(cfg.singbox.connect_timeout, "3s");
});

test("the redundant halves of default/enabled are only emitted when stored", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a.invalid/s", default: true },
        { id: "b", name: "B", url: "https://b.invalid/s" },
    ]);

    const subs = ctx.view._collectConfig().subscriptions;
    assert.strictEqual(subs.a.default, true);
    assert.ok(
        !("default" in subs.b),
        '"default": false must not be written for a subscription that never carried it',
    );
    assert.ok(
        !("enabled" in subs.a) && !("enabled" in subs.b),
        '"enabled": true must not be written for a subscription that never carried it',
    );
});

test("interval and tolerance survive a save made while their inputs are absent", () => {
    const ctx = loadView();
    // No proxies → no urltest widget → no interval/tolerance inputs rendered.
    mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a.invalid/s",
            default: true,
            interval: "5m",
            tolerance: 300,
        },
    ]);
    const card = ctx.document.querySelector(".vpnsub-sub-card");
    assert.strictEqual(card.querySelector(".vpnsub-sub-interval"), null);

    const sub = ctx.view._collectConfig().subscriptions.a;
    assert.strictEqual(sub.interval, "5m");
    assert.strictEqual(sub.tolerance, 300);
});

test("clearing connect_timeout sends an empty string, an unset one stays absent", () => {
    let ctx = loadView();
    mountSubscriptions(ctx, [{ id: "a", name: "A", url: "https://a/s", default: true }], {
        rawSingbox: { connect_timeout: "3s" },
        connectTimeout: "",
    });
    assert.strictEqual(
        ctx.view._collectConfig().singbox.connect_timeout,
        "",
        "a cleared field must be sent as \"\" — rpcd merges additively, so dropping the key keeps the old value",
    );

    ctx = loadView();
    mountSubscriptions(ctx, [{ id: "a", name: "A", url: "https://a/s", default: true }], {
        connectTimeout: "",
    });
    assert.ok(
        !("connect_timeout" in ctx.view._collectConfig().singbox),
        "a field that was never set must stay absent",
    );
});

test("connect_timeout survives a save made while its input is absent", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [{ id: "a", name: "A", url: "https://a/s", default: true }], {
        rawSingbox: { connect_timeout: "3s" },
    });
    // Same shape as the interval/tolerance rule: a field whose input was never
    // rendered must not take the "cleared" branch and emit "".
    const el = ctx.document.getElementById("vpnsub-connect-timeout");
    el.parentNode.removeChild(el);
    assert.strictEqual(ctx.document.getElementById("vpnsub-connect-timeout"), null);

    assert.strictEqual(ctx.view._collectConfig().singbox.connect_timeout, "3s");
});

test("connect_timeout can be cleared after being set in the same page session", async () => {
    const ctx = loadView();
    // Stored config has no connect_timeout, so _rawSingbox starts without it.
    mountSubscriptions(ctx, [{ id: "a", name: "A", url: "https://a/s", default: true }], {
        connectTimeout: "3s",
    });

    await ctx.view.handleSave();

    // handleSave must re-seed the snapshot from what it just sent: rpcd merges
    // singbox additively, so a stale snapshot would make the clear below drop
    // the key instead of emitting "" and the stored "3s" would survive.
    ctx.document.getElementById("vpnsub-connect-timeout").value = "";
    assert.strictEqual(ctx.view._collectConfig().singbox.connect_timeout, "");
});

test("a subscription id that names an Object.prototype member is still saved", () => {
    const ctx = loadView();
    // The ID field is free text, so "__proto__" is reachable; on a "{}" map the
    // assignment sets the prototype and the subscription vanishes from the save.
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "__proto__", name: "Odd", url: "https://odd/s" },
    ]);

    const subs = ctx.view._collectConfig().subscriptions;
    assert.deepStrictEqual(Object.keys(subs).sort(), ["__proto__", "a"]);
    assert.strictEqual(subs["__proto__"].url, "https://odd/s");
});

// ── inline nodes ─────────────────────────────────────────────────────────────

test("inline-node mode emits nodes and drops url, url mode does the reverse", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "self",
            name: "Self hosted",
            default: true,
            nodes: ["vless://uuid@vps.example:443?encryption=none#VPS"],
        },
        { id: "prov", name: "Provider", url: "https://p.invalid/s" },
    ]);

    const subs = ctx.view._collectConfig().subscriptions;
    assert.deepStrictEqual(subs.self.nodes, [
        "vless://uuid@vps.example:443?encryption=none#VPS",
    ]);
    assert.ok(!("url" in subs.self), "inline-node subscription must not carry a url");
    assert.strictEqual(subs.prov.url, "https://p.invalid/s");
    assert.ok(!("nodes" in subs.prov), "url subscription must not carry nodes");
});

test("a disabled inline-node subscription with no nodes emits no empty array", () => {
    const ctx = loadView();
    // A disabled card needs no source (validateSource lets it through), so the
    // save must not manufacture a meaningless "nodes": [].
    const { cards } = mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", enabled: false },
    ]);
    // The user switches the disabled card to inline nodes and saves before
    // entering any.
    setInput(cards[1], ".vpnsub-sub-source", "nodes");

    assert.notStrictEqual(
        ctx.view._validate(),
        false,
        "should be saveable: " + notificationTexts(ctx),
    );
    const sub = ctx.view._collectConfig().subscriptions.b;
    assert.ok(!("nodes" in sub), 'must not write "nodes": []');
    assert.strictEqual(sub.enabled, false);
});

test("a disabled sourceless subscription emits no empty url", () => {
    const ctx = loadView();
    // A sourceless card falls back to URL mode, so a no-op save must not add a
    // "url": "" the stored config never had.
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", enabled: false },
        { id: "c", name: "C", enabled: false, url: "" },
    ]);

    assert.notStrictEqual(
        ctx.view._validate(),
        false,
        "should be saveable: " + notificationTexts(ctx),
    );
    const subs = ctx.view._collectConfig().subscriptions;
    assert.ok(!("url" in subs.b), 'must not write "url": ""');
    assert.strictEqual(subs.c.url, "", "a stored empty url must survive as-is");
});

// isValidNodeUri is module-scoped, so every case below drives it through the
// real _validate on a mounted card rather than calling it directly.

test("every scheme the core dispatches on is accepted as an inline node", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "self",
            name: "Self",
            default: true,
            nodes: [
                "vless://uuid@vps.example:443?encryption=none#VPS",
                "hysteria2://secret@vps.example:443?sni=vps.example#HY2",
                "hy2://secret@vps.example:443#Alias",
                // hysteria2 auth is the whole userinfo and may hold a colon;
                // URL() splits it across username and password.
                "hysteria2://user:pass@vps.example:443#Userpass",
                // Odd but accepted by the core, which takes the whole userinfo
                // verbatim — the client must never be the stricter of the two.
                "hysteria2://:pass@vps.example:443#EmptyUser",
                // The port is optional and defaults to 443 in the hysteria2 spec.
                "hy2://secret@vps.example#NoPort",
            ],
        },
    ]);

    assert.notStrictEqual(
        ctx.view._validate(),
        false,
        "should be saveable: " + notificationTexts(ctx),
    );
});

test("a node URI without host or userinfo is rejected", () => {
    ["vless://", "hysteria2://", "hy2://", "hysteria2://vps.example:443"].forEach(
        (uri) => {
            const ctx = loadView();
            mountSubscriptions(ctx, [
                { id: "self", name: "Self", default: true, nodes: [uri] },
            ]);

            assert.strictEqual(ctx.view._validate(), false, "accepted " + uri);
            assert.ok(
                notificationTexts(ctx).some((t) => /must be valid/.test(t)),
                "expected a node URI validation message for " +
                    uri +
                    ", got: " +
                    notificationTexts(ctx),
            );
        },
    );
});

test("an inline node with a scheme the core does not dispatch is rejected", () => {
    // trojan:// parses as a URL with host and userinfo, so only the scheme
    // allow-list catches it.
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "self",
            name: "Self",
            default: true,
            nodes: ["trojan://secret@vps.example:443#T"],
        },
    ]);

    assert.strictEqual(ctx.view._validate(), false);
    assert.ok(
        notificationTexts(ctx).some((t) => /must be valid/.test(t)),
        "expected a node URI validation message, got: " + notificationTexts(ctx),
    );
});

test("one bad URI in an otherwise valid mixed list blocks the save", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "self",
            name: "Self",
            default: true,
            nodes: [
                "vless://uuid@vps.example:443?encryption=none#VPS",
                "hysteria2://",
                "hy2://secret@vps.example#NoPort",
            ],
        },
    ]);

    assert.strictEqual(ctx.view._validate(), false);
});

// ── fallback chains ──────────────────────────────────────────────────────────

test("a declared chain is collected with its blacklist_timeout", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a/s",
            default: true,
            fallback: { subscriptions: ["b"], blacklist_timeout: "1m" },
        },
        { id: "b", name: "B", url: "https://b/s" },
    ]);

    const sub = ctx.view._collectConfig().subscriptions.a;
    assert.deepStrictEqual(sub.fallback, {
        subscriptions: ["b"],
        blacklist_timeout: "1m",
    });
});

test("a chain row left unpicked hides the blacklist-timeout row it would discard", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", url: "https://b/s" },
    ]);
    const card = ctx.document.querySelector(".vpnsub-sub-card");
    const btEl = card.querySelector(".vpnsub-sub-blacklist-timeout");
    const btRow = btEl.parentNode.parentNode;

    assert.strictEqual(btRow.style.display, "none", "hidden with no chain rows");

    // Add a row but leave it on "—": getValue() drops it, so _collectConfig
    // emits no fallback object and would silently discard the timeout.
    const picker = ctx.view._widgets[0].fallback;
    picker.node.querySelector(".vpnsub-dynlist-add").dispatch("click");
    assert.deepStrictEqual(picker.getValue(), []);
    assert.strictEqual(
        btRow.style.display,
        "none",
        "an unpicked row must not reveal a field whose value the save drops",
    );

    // Picking a backup reveals it again.
    const sel = picker.node.querySelector(".vpnsub-chain-select");
    sel.value = "b";
    sel.dispatch("change");
    assert.deepStrictEqual(picker.getValue(), ["b"]);
    assert.strictEqual(btRow.style.display, "");
});

test("a candidate reachable only through a disabled subscription is still offered", () => {
    const ctx = loadView();
    // c → b is declared but c is disabled, so the core ignores that edge: a may
    // still name b as a backup. b → a keeps the graph interesting.
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", url: "https://b/s" },
        {
            id: "c",
            name: "C",
            url: "https://c/s",
            enabled: false,
            fallback: { subscriptions: ["a"] },
        },
    ]);
    // b's own chain goes through the disabled c, which leads back to a.
    const bPicker = ctx.view._widgets[1].fallback;
    bPicker.node.querySelector(".vpnsub-dynlist-add").dispatch("click");
    const bSel = bPicker.node.querySelector(".vpnsub-chain-select");
    bSel.value = "c";
    bSel.dispatch("change");

    const aPicker = ctx.view._widgets[0].fallback;
    aPicker.node.querySelector(".vpnsub-dynlist-add").dispatch("click");
    const offered = aPicker.node
        .querySelector(".vpnsub-chain-select")
        .querySelectorAll("option")
        .map((o) => o.getAttribute("value"));

    assert.ok(
        offered.includes("b"),
        "b only loops back to a through the disabled c, which the core ignores; offered: " +
            JSON.stringify(offered),
    );
    assert.ok(!offered.includes("c"), "a disabled subscription must not be offered");
    assert.ok(!offered.includes("a"), "a subscription must not be offered itself");
});

test("a cycle between enabled subscriptions is rejected at save time", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a/s",
            default: true,
            fallback: { subscriptions: ["b"] },
        },
        {
            id: "b",
            name: "B",
            url: "https://b/s",
            fallback: { subscriptions: ["a"] },
        },
    ]);

    assert.strictEqual(ctx.view._validate(), false);
    assert.ok(
        notificationTexts(ctx).some((t) => /cycle/i.test(t)),
        "expected a cycle message, got: " + notificationTexts(ctx),
    );
});

// A pick whose subscription stopped existing must survive as data — silently
// rewriting it would drop a chain entry the operator meant to keep, and hiding
// it would leave the save failing with nothing to point at.
test("a chain pick whose subscription was removed stays visible and blocks the save", () => {
    const ctx = loadView();
    const { cards } = mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a/s",
            default: true,
            fallback: { subscriptions: ["gone"] },
        },
    ]);

    const sel = cards[0].querySelector(".vpnsub-chain-select");
    assert.ok(sel, "no chain select rendered");
    assert.strictEqual(sel.value, "gone", "the stale pick must stay selected, not be reset");
    assert.ok(
        sel.classList.contains("vpnsub-invalid"),
        "a pick that matches no candidate must be marked invalid",
    );

    // It is still collected, so the message names something the config contains.
    assert.deepStrictEqual(
        ctx.view._collectConfig().subscriptions.a.fallback.subscriptions,
        ["gone"],
    );
    assert.strictEqual(ctx.view._validate(), false);
    assert.ok(
        notificationTexts(ctx).some((t) => /unknown subscription/i.test(t) && t.includes("gone")),
        "expected an unknown-subscription message naming it, got: " + notificationTexts(ctx),
    );
});

// ── enabled / disabled ───────────────────────────────────────────────────────

test("a disabled subscription may carry no source, matching the core", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", enabled: false },
    ]);

    assert.notStrictEqual(
        ctx.view._validate(),
        false,
        "config.go validateSource accepts a disabled subscription with neither url nor nodes; blocking the save here makes such a config unsaveable: " +
            notificationTexts(ctx),
    );
});

test("an enabled subscription with no source is still rejected", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B" },
    ]);

    assert.strictEqual(ctx.view._validate(), false);
});

test("disabling a subscription drops it from another card's chain candidates", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: "B", url: "https://b/s" },
    ]);
    const picker = ctx.view._widgets[0].fallback;
    picker.node.querySelector(".vpnsub-dynlist-add").dispatch("click");

    const options = () =>
        picker.node
            .querySelector(".vpnsub-chain-select")
            .querySelectorAll("option")
            .map((o) => o.getAttribute("value"));

    assert.ok(options().includes("b"));
    setChecked(ctx.document.querySelectorAll(".vpnsub-sub-card")[1], ".vpnsub-sub-enabled", false);
    assert.ok(!options().includes("b"));
});

// ── escaping ─────────────────────────────────────────────────────────────────

test("a node name from the provider is not parsed as markup in the proxy widget", () => {
    const ctx = loadView();
    // Node names come from the fragment of the provider's vless:// URI, so they
    // are attacker-influenced text reaching the DOM through nodeNameSpan.
    const hostile = "<img src=x onerror=alert(1)>";

    // Guard: a bare string child still goes through innerHTML, so the stub does
    // model the sink this test is about.
    assert.ok(
        ctx.E("span", hostile).querySelector("img"),
        "stub-dom no longer models dom.append's innerHTML sink, so this test proves nothing",
    );

    const { cards } = mountSubscriptions(
        ctx,
        [{ id: "a", name: "A", url: "https://a/s", default: true }],
        {
            proxies: { "a-single": { type: "vless", now: "", history: [] } },
            tagNames: { "a-single": hostile },
        },
    );

    const widget = cards[0].querySelector(".vpnsub-proxy-single");
    assert.ok(widget, "single-node proxy widget was not rendered");
    assert.strictEqual(
        widget.querySelector("img"),
        null,
        "node name was parsed as HTML: " + widget.innerHTML,
    );
    assert.ok(
        widget.textContent.indexOf("<img") !== -1,
        "the name should still be visible, as text",
    );
});

test("the auto pane's current node name is not parsed as markup", () => {
    const ctx = loadView();
    const hostile = "<img src=x onerror=alert(1)>";

    const { cards } = mountSubscriptions(
        ctx,
        [{ id: "a", name: "A", url: "https://a/s", default: true }],
        {
            proxies: {
                "a-manual": {
                    type: "selector",
                    now: "a-node-1",
                    all: ["a-auto", "a-node-1"],
                    history: [],
                },
                "a-auto": { type: "urltest", now: "a-node-1", history: [] },
                "a-node-1": { type: "vless", history: [] },
            },
            tagNames: { "a-node-1": hostile },
        },
    );

    const pane = cards[0].querySelector(".vpnsub-proxy-current");
    assert.ok(pane, "auto pane was not rendered");
    assert.strictEqual(
        pane.querySelector("img"),
        null,
        "current node name was parsed as HTML: " + pane.innerHTML,
    );
    assert.ok(
        pane.textContent.indexOf("<img") !== -1,
        "the name should still be visible, as text",
    );
});

test("a subscription name is not parsed as markup in a chain option label", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
        { id: "b", name: '<img src=x onerror=alert(1)>', url: "https://b/s" },
    ]);
    const picker = ctx.view._widgets[0].fallback;
    picker.node.querySelector(".vpnsub-dynlist-add").dispatch("click");
    const sel = picker.node.querySelector(".vpnsub-chain-select");

    assert.strictEqual(sel.querySelector("img"), null, "option label was parsed as HTML");
    assert.ok(
        sel
            .querySelectorAll("option")
            .some((o) => o.textContent.indexOf("<img") !== -1),
        "the name should still be visible, as text",
    );
});

test("a chain error message is not parsed as markup", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a/s",
            default: true,
            fallback: { subscriptions: ["<img src=x onerror=alert(1)>"] },
        },
    ]);

    assert.strictEqual(ctx.view._validate(), false);
    // Asserted before the loop: an earlier gate failing first would leave
    // notifications empty and make the escaping check pass vacuously.
    assert.ok(
        notificationTexts(ctx).some((t) => t.indexOf("<img") !== -1),
        "expected a notification quoting the hostile id, got: " +
            notificationTexts(ctx),
    );
    ctx.notifications.forEach((n) => {
        assert.strictEqual(
            n.node.querySelector("img"),
            null,
            "notification parsed a subscription id as HTML: " + n.node.innerHTML,
        );
    });
});

// ── duration validation ──────────────────────────────────────────────────────

test("blacklist_timeout accepts what time.ParseDuration accepts", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        {
            id: "a",
            name: "A",
            url: "https://a/s",
            default: true,
            fallback: { subscriptions: ["b"] },
        },
        { id: "b", name: "B", url: "https://b/s" },
    ]);
    const card = ctx.document.querySelector(".vpnsub-sub-card");

    // "0" and ".5s" both parse in Go; "1" and "1m 30s" do not. Go takes both
    // micro signs, U+00B5 and U+03BC, so neither may be rejected here.
    for (const good of ["1m", "30s", "0", ".5s", "1m30s", "300ms", "5µs", "5μs"]) {
        ctx.notifications.length = 0;
        setInput(card, ".vpnsub-sub-blacklist-timeout", good);
        assert.notStrictEqual(
            ctx.view._validate(),
            false,
            good + " parses with time.ParseDuration but was rejected",
        );
    }
    for (const bad of ["1", "m", "1m 30s", "abc"]) {
        ctx.notifications.length = 0;
        setInput(card, ".vpnsub-sub-blacklist-timeout", bad);
        assert.strictEqual(
            ctx.view._validate(),
            false,
            bad + " does not parse with time.ParseDuration but was accepted",
        );
    }
});

test("a blacklist_timeout typo is not rejected when no chain is configured", () => {
    const ctx = loadView();
    mountSubscriptions(ctx, [
        { id: "a", name: "A", url: "https://a/s", default: true },
    ]);
    const card = ctx.document.querySelector(".vpnsub-sub-card");
    // The row is hidden and the value is dropped on save, so blocking here
    // would stall a save over a field the user cannot see or fix.
    setInput(card, ".vpnsub-sub-blacklist-timeout", "not-a-duration");

    assert.notStrictEqual(ctx.view._validate(), false);
    assert.ok(!("fallback" in ctx.view._collectConfig().subscriptions.a));
});
