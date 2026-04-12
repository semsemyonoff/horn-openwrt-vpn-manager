"use strict";
"require view";
"require rpc";
"require ui";

// Inject stylesheet once
(function () {
    var id = "horn-vpn-manager-css";
    if (!document.getElementById(id)) {
        var link = document.createElement("link");
        link.id = id;
        link.rel = "stylesheet";
        link.href = L.resource("horn-vpn-manager/style.css");
        document.head.appendChild(link);
    }
})();

// No `expect` on any call — handle raw response objects everywhere
var callGetConfig = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_config",
});
var callSetConfig = rpc.declare({
    object: "horn-vpn-manager",
    method: "set_config",
    params: ["config"],
});
var callRunScript = rpc.declare({
    object: "horn-vpn-manager",
    method: "run_script",
    params: ["dry_run", "cached_lists", "download_lists"],
});
var callRunRouting = rpc.declare({
    object: "horn-vpn-manager",
    method: "run_routing",
    params: ["with_subscriptions"],
});
var callGetLog = rpc.declare({ object: "horn-vpn-manager", method: "get_log" });
var callGetRoutingLog = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_routing_log",
});
var callGetSyslog = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_syslog",
    params: ["lines"],
});
var callTestUrl = rpc.declare({
    object: "horn-vpn-manager",
    method: "test_url",
    params: ["url"],
});
var callGetSbStatus = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_sb_status",
});
var callSetProxy = rpc.declare({
    object: "horn-vpn-manager",
    method: "set_proxy",
    params: ["proxy", "selected"],
});
var callTestDelays = rpc.declare({
    object: "horn-vpn-manager",
    method: "test_delays",
    params: ["tags", "url"],
});
var callGetTemplate = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_template",
});
var callSetTemplate = rpc.declare({
    object: "horn-vpn-manager",
    method: "set_template",
    params: ["template"],
});
var callResetTemplate = rpc.declare({
    object: "horn-vpn-manager",
    method: "reset_template",
});

var callGetDomainsConfig = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_domains_config",
});
var callSetDomainsConfig = rpc.declare({
    object: "horn-vpn-manager",
    method: "set_domains_config",
    params: ["config"],
});
var callGetManualIps = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_manual_ips",
});
var callGetManualDomains = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_manual_domains",
});
var callSetManualDomains = rpc.declare({
    object: "horn-vpn-manager",
    method: "set_manual_domains",
    params: ["domains"],
});
var callGetSyncStatus = rpc.declare({
    object: "horn-vpn-manager",
    method: "get_sync_status",
});

// ── Helpers ───────────────────────────────────────────────────────────────────

function sanitizeId(name) {
    return (name || "")
        .toLowerCase()
        .replace(/[^a-z0-9]+/g, "-")
        .replace(/^-+|-+$/g, "");
}

// Normalize node display name:
// 1. Convert "A T ..." or "AT ..." country code prefixes to flag emoji (e.g. "G B" → 🇬🇧)
// 2. Ensure a space between a leading flag emoji and the following text
function cleanNodeName(name) {
    if (!name) return name;
    // Some providers use "G B Англия" (spaced) or "GB Англия" (compact) instead of 🇬🇧
    name = name.replace(/^([A-Z])\s?([A-Z])(?=\s|$)/, function (m, a, b) {
        return (
            String.fromCodePoint(0x1f1e6 + a.charCodeAt(0) - 65) +
            String.fromCodePoint(0x1f1e6 + b.charCodeAt(0) - 65)
        );
    });
    try {
        return name
            .replace(/([\u{1F1E0}-\u{1F1FF}]{1,2})([\S])/u, "$1 $2")
            .replace(/([\u{1F300}-\u{1FAFF}])([\S])/u, "$1 $2");
    } catch (e) {
        return name;
    }
}

function getLastDelay(proxy) {
    if (!proxy || !proxy.history || !proxy.history.length) return null;
    var last = proxy.history[proxy.history.length - 1];
    return last && last.delay > 0 ? last.delay : null;
}

function makeLatencySpan(ms) {
    var cls =
        "vpnsub-latency " +
        (ms < 1000 ? "vpnsub-latency-good" : "vpnsub-latency-warn");
    return E("span", { class: cls }, "\u00a0" + ms + "\u00a0ms");
}

// Extract 1-based line number from a JSON SyntaxError.
// Chrome reports "at position N"; Firefox/Safari report "at line N column M".
function getJsonErrorLine(text, err) {
    var msg = err.message || "";
    var m = msg.match(/line[:\s]+(\d+)/i);
    if (m) return parseInt(m[1], 10);
    m = msg.match(/position[:\s]+(\d+)/i);
    if (m) {
        var pos = Math.min(parseInt(m[1], 10), text.length);
        return text.substring(0, pos).split("\n").length;
    }
    return null;
}

// Minimal JSON syntax highlighter for the template editor overlay.
// Returns HTML string. Input is plain text (not yet HTML-escaped).
// errLine (optional, 1-based) wraps that line with hj-err-line.
function highlightJson(raw, errLine) {
    var s = raw
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
    // Groups: 1=placeholder, 2=key-string, 3=colon, 4=value-string, 5=keyword, 6=number
    var highlighted = s.replace(
        /("__[A-Z_]+__")|("(?:[^"\\]|\\.)*")(\s*:)|("(?:[^"\\]|\\.)*")|(true|false|null)|(-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?)/g,
        function (m, ph, key, colon, str, kw, num) {
            if (ph) return '<span class="hj-ph">' + ph + "</span>";
            if (key)
                return (
                    '<span class="hj-key">' + key + "</span>" + (colon || "")
                );
            if (str) return '<span class="hj-str">' + str + "</span>";
            if (kw) return '<span class="hj-kw">' + kw + "</span>";
            if (num) return '<span class="hj-num">' + num + "</span>";
            return m;
        },
    );
    if (errLine != null) {
        var lines = highlighted.split("\n");
        var idx = errLine - 1;
        if (idx >= 0 && idx < lines.length) {
            lines[idx] = '<span class="hj-err-line">' + lines[idx] + "</span>";
        }
        return lines.join("\n");
    }
    return highlighted;
}

// Build a proxy selection widget for a subscription.
// intervalInput / toleranceInput are the actual DOM input elements for those settings;
// for multi-node they are placed inside the Auto pane so _collectConfig can find them.
// Returns a DOM element or null if no proxy data is available.
function makeProxyWidget(
    subId,
    proxies,
    tagNames,
    intervalInput,
    toleranceInput,
) {
    if (!subId || !proxies) return null;

    var manualTag = subId + "-manual";
    var singleTag = subId + "-single";
    var autoTag = subId + "-auto";
    var manualProxy = proxies[manualTag];
    var singleProxy = proxies[singleTag];

    if (!manualProxy && !singleProxy) return null;

    // ── Single-node subscription ──────────────────────────────────────────────
    if (singleProxy) {
        var delay = getLastDelay(singleProxy);
        var name = cleanNodeName(
            (tagNames && tagNames[singleTag]) || singleTag,
        );
        return E("div", { class: "vpnsub-proxy-single" }, [
            E("span", { class: "vpnsub-proxy-node-name" }, name),
            delay ? makeLatencySpan(delay) : "",
        ]);
    }

    // ── Multi-node: Auto / Manual toggle ─────────────────────────────────────
    var autoProxy = proxies[autoTag];
    var autoNow = autoProxy && autoProxy.now;
    var allTags = manualProxy.all || [];
    var nodeTags = allTags.filter(function (t) {
        return t !== autoTag;
    });
    var currentSelected = manualProxy.now || "";

    // Auto pane — current server + settings
    var autoNowName = autoNow
        ? cleanNodeName((tagNames && tagNames[autoNow]) || autoNow)
        : null;
    var autoNowDelay = autoNow ? getLastDelay(proxies[autoNow]) : null;

    var autoPane = E("div", { class: "vpnsub-proxy-auto-pane" }, [
        E("div", { class: "vpnsub-proxy-current" }, [
            E(
                "span",
                { class: "vpnsub-proxy-current-label" },
                _("Current") + ":\u00a0",
            ),
            autoNow
                ? E(
                      "span",
                      { class: "vpnsub-proxy-current-name" },
                      autoNowName || autoNow,
                  )
                : E("span", { class: "vpnsub-proxy-current-none" }, "—"),
            autoNowDelay ? makeLatencySpan(autoNowDelay) : "",
        ]),
        E("div", { class: "vpnsub-proxy-auto-settings" }, [
            E(
                "span",
                { class: "vpnsub-proxy-setting-label" },
                _("Interval") + ":\u00a0",
            ),
            intervalInput,
            E("span", { class: "vpnsub-proxy-setting-sep" }),
            E(
                "span",
                { class: "vpnsub-proxy-setting-label" },
                _("Tolerance") + ":\u00a0",
            ),
            toleranceInput,
            E("span", { class: "vpnsub-proxy-setting-unit" }, "\u00a0ms"),
        ]),
    ]);

    // Manual pane — sorted list + test button
    var sortedNodes = nodeTags.slice().sort(function (a, b) {
        var da = getLastDelay(proxies[a]);
        var db = getLastDelay(proxies[b]);
        if (da === null && db === null) return 0;
        if (da === null) return 1;
        if (db === null) return -1;
        return da - db;
    });

    var listEl = E("div", { class: "vpnsub-proxy-list" });

    function renderNodeList() {
        while (listEl.firstChild) listEl.removeChild(listEl.firstChild);
        sortedNodes.forEach(function (tag) {
            var name = cleanNodeName((tagNames && tagNames[tag]) || tag);
            var delay = getLastDelay(proxies[tag]);
            var isSelected = tag === currentSelected;
            var delayEl =
                delay !== null
                    ? makeLatencySpan(delay)
                    : E("span", { class: "vpnsub-no-latency" }, "\u2715");
            var row = E(
                "div",
                {
                    class:
                        "vpnsub-proxy-node-row" +
                        (isSelected ? " vpnsub-proxy-selected" : ""),
                },
                [E("span", { class: "vpnsub-proxy-node-name" }, name), delayEl],
            );
            row.setAttribute("data-tag", tag);
            row.addEventListener("click", function () {
                callSetProxy(manualTag, tag).then(function () {
                    currentSelected = tag;
                    Array.prototype.forEach.call(
                        listEl.querySelectorAll(".vpnsub-proxy-node-row"),
                        function (r) {
                            r.classList.toggle(
                                "vpnsub-proxy-selected",
                                r.getAttribute("data-tag") === tag,
                            );
                        },
                    );
                });
            });
            listEl.appendChild(row);
        });
    }
    renderNodeList();

    var testBtn = E(
        "button",
        {
            type: "button",
            class: "cbi-button vpnsub-test-latency-btn",
            click: function () {
                testBtn.disabled = true;
                var testUrl =
                    (document.getElementById("vpnsub-test-url-setting") || {})
                        .value || "";
                testUrl =
                    testUrl.trim() || "https://www.gstatic.com/generate_204";
                callTestDelays(nodeTags, testUrl).then(
                    function (res) {
                        if (res && res.delays) {
                            nodeTags.forEach(function (tag) {
                                var d = res.delays[tag];
                                if (!proxies[tag])
                                    proxies[tag] = { history: [] };
                                if (!proxies[tag].history)
                                    proxies[tag].history = [];
                                proxies[tag].history = [
                                    { delay: d && d > 0 ? d : 0 },
                                ];
                            });
                            sortedNodes = nodeTags
                                .slice()
                                .sort(function (a, b) {
                                    var da = getLastDelay(proxies[a]);
                                    var db = getLastDelay(proxies[b]);
                                    if (da === null && db === null) return 0;
                                    if (da === null) return 1;
                                    if (db === null) return -1;
                                    return da - db;
                                });
                            renderNodeList();
                        }
                        testBtn.disabled = false;
                    },
                    function () {
                        testBtn.disabled = false;
                    },
                );
            },
        },
        _("Test latencies"),
    );

    var manualPane = E("div", { class: "vpnsub-proxy-manual-pane" }, [
        E("div", { class: "vpnsub-proxy-manual-toolbar" }, [testBtn]),
        listEl,
    ]);

    // Initial mode: manual if a specific node is selected, auto otherwise
    var isManualMode = !!(currentSelected && currentSelected !== autoTag);
    if (isManualMode) {
        autoPane.style.display = "none";
    } else {
        manualPane.style.display = "none";
    }

    function setModeButtonState(btn, active) {
        btn.classList.toggle("vpnsub-mode-active", active);
        btn.classList.toggle("cbi-button-action", active);
    }

    var toggleAuto = E(
        "button",
        {
            type: "button",
            class:
                "vpnsub-mode-btn cbi-button" +
                (!isManualMode ? " vpnsub-mode-active cbi-button-action" : ""),
            click: function () {
                callSetProxy(manualTag, autoTag).then(function () {
                    currentSelected = autoTag;
                    setModeButtonState(toggleAuto, true);
                    setModeButtonState(toggleManual, false);
                    autoPane.style.display = "";
                    manualPane.style.display = "none";
                });
            },
        },
        _("Auto"),
    );

    var toggleManual = E(
        "button",
        {
            type: "button",
            class:
                "vpnsub-mode-btn cbi-button" +
                (isManualMode ? " vpnsub-mode-active cbi-button-action" : ""),
            click: function () {
                setModeButtonState(toggleManual, true);
                setModeButtonState(toggleAuto, false);
                autoPane.style.display = "none";
                manualPane.style.display = "";
            },
        },
        _("Manual"),
    );

    return E("div", { class: "vpnsub-proxy-widget" }, [
        E("div", { class: "vpnsub-mode-toggle" }, [toggleAuto, toggleManual]),
        autoPane,
        manualPane,
    ]);
}

// ── DynamicList (with List ↔ Textarea toggle) ─────────────────────────────────
function dynList(values, placeholder) {
    var mode = "list"; // 'list' | 'textarea'

    // ── list mode ────────────────────────────────────────────────────────────
    var listEl = E("div", { class: "vpnsub-dynlist" });

    function makeRow(val) {
        var input = E("input", {
            type: "text",
            class: "cbi-input-text",
            value: val || "",
            placeholder: placeholder || "",
        });
        var remove = E(
            "button",
            {
                type: "button",
                class: "vpnsub-dynlist-remove cbi-button cbi-button-negative",
                click: function () {
                    listEl.removeChild(row);
                },
            },
            "✕",
        );
        var row = E("div", { class: "vpnsub-dynlist-row" }, [input, remove]);
        return row;
    }

    var addBtn = E(
        "button",
        {
            type: "button",
            class: "vpnsub-dynlist-add cbi-button cbi-button-add",
            title: _("Add"),
            click: function () {
                var r = makeRow("");
                listEl.appendChild(r);
                r.querySelector("input").focus();
            },
        },
        _("Add"),
    );
    var addWrap = E(
        "div",
        {
            class: "cbi-section-create cbi-tblsection-create vpnsub-dynlist-create",
        },
        [addBtn],
    );

    function initList(vals) {
        // Remove all rows and repopulate the editable list.
        while (listEl.firstChild) listEl.removeChild(listEl.firstChild);
        (vals || []).forEach(function (v) {
            listEl.appendChild(makeRow(v));
        });
    }

    function getListValues() {
        return Array.prototype.slice
            .call(listEl.querySelectorAll(".vpnsub-dynlist-row input"))
            .map(function (el) {
                return el.value.trim();
            })
            .filter(function (v) {
                return v !== "";
            });
    }

    // ── textarea mode ────────────────────────────────────────────────────────
    var taEl = E("textarea", {
        class: "cbi-input-textarea vpnsub-dynlist-textarea",
        rows: "6",
        placeholder: placeholder ? placeholder + "\n" + placeholder : "",
        style: "display:none",
    });

    function getTextareaValues() {
        return taEl.value
            .split("\n")
            .map(function (v) {
                return v.trim();
            })
            .filter(function (v) {
                return v !== "";
            });
    }

    // ── toggle button ────────────────────────────────────────────────────────
    var toggleBtn = E(
        "button",
        {
            type: "button",
            class: "vpnsub-dynlist-toggle cbi-button",
            click: function () {
                if (mode === "list") {
                    // list → textarea
                    taEl.value = getListValues().join("\n");
                    listEl.style.display = "none";
                    addWrap.style.display = "none";
                    taEl.style.display = "";
                    toggleBtn.textContent = _("List mode");
                    mode = "textarea";
                } else {
                    // textarea → list
                    initList(getTextareaValues());
                    taEl.style.display = "none";
                    listEl.style.display = "";
                    addWrap.style.display = "";
                    toggleBtn.textContent = _("Text mode");
                    mode = "list";
                }
            },
        },
        _("Text mode"),
    );

    // ── assemble ─────────────────────────────────────────────────────────────
    initList(values);

    var wrap = E("div", { class: "vpnsub-dynlist-wrap" }, [
        E("div", { class: "vpnsub-dynlist-toolbar" }, [toggleBtn]),
        listEl,
        addWrap,
        taEl,
    ]);

    return {
        node: wrap,
        getValue: function () {
            return mode === "list" ? getListValues() : getTextareaValues();
        },
    };
}

// ── ANSI → HTML ───────────────────────────────────────────────────────────────

var ANSI_STYLES = {
    "1;31": "color:#e74c3c;font-weight:bold",
    "0;33": "color:#f39c12",
    "0;32": "color:#2ecc71",
    "0;36": "color:#1abc9c",
    "0;90": "color:#777",
    1: "font-weight:bold",
};

function ansiToHtml(text) {
    if (!text) return "";
    var safe = text
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
    var open = false;
    var result = safe.replace(/\x1b\[([0-9;]*)m/g, function (_, code) {
        if (code === "" || code === "0") {
            if (open) {
                open = false;
                return "</span>";
            }
            return "";
        }
        var style = ANSI_STYLES[code];
        if (style) {
            var close = open ? "</span>" : "";
            open = true;
            return close + '<span style="' + style + '">';
        }
        return "";
    });
    if (open) result += "</span>";
    return result;
}

// ── Layout helpers ────────────────────────────────────────────────────────────
function formRow(label, field, description) {
    var nodes = [
        E("label", { class: "cbi-value-title" }, label),
        E(
            "div",
            { class: "cbi-value-field" },
            Array.isArray(field) ? field : [field],
        ),
    ];
    if (description)
        nodes.push(E("div", { class: "cbi-value-description" }, description));
    return E("div", { class: "cbi-value" }, nodes);
}

function setError(input, msg) {
    input.classList.add("vpnsub-invalid");
    var errEl = input.parentNode.querySelector(".vpnsub-errmsg");
    if (!errEl) {
        errEl = E("div", { class: "vpnsub-errmsg" });
        input.parentNode.appendChild(errEl);
    }
    errEl.textContent = msg;
}

function clearError(input) {
    input.classList.remove("vpnsub-invalid");
    var errEl = input.parentNode.querySelector(".vpnsub-errmsg");
    if (errEl) errEl.parentNode.removeChild(errEl);
}

function isValidUrl(s) {
    return /^https?:\/\/.+/.test(s);
}

// ── Collapsible dynList row ───────────────────────────────────────────────────
function makeCollapsible(label, widget, description) {
    var badge = E("span", { class: "vpnsub-count-badge" });
    var arrow = E("span", { class: "vpnsub-collapse-arrow" }, "▶");
    var body = E(
        "div",
        { class: "vpnsub-collapse-body", style: "display:none" },
        [widget.node],
    );
    if (description) {
        body.appendChild(
            E("div", { class: "vpnsub-collapse-description" }, description),
        );
    }

    function updateBadge() {
        var n = widget.getValue().length;
        badge.textContent = n > 0 ? String(n) : "0";
        badge.style.opacity = n > 0 ? "1" : "0.4";
    }

    updateBadge();

    var toggle = E(
        "label",
        {
            class: "cbi-value-title vpnsub-collapse-label",
            click: function () {
                var open = body.style.display !== "none";
                body.style.display = open ? "none" : "";
                arrow.textContent = open ? "▶" : "▼";
                badge.style.display = open ? "" : "none";
                if (open) updateBadge();
            },
        },
        [
            E("span", { class: "vpnsub-collapse-toggle" }, [
                arrow,
                "\u00a0",
                label,
                "\u00a0",
                badge,
            ]),
        ],
    );

    return E("div", { class: "cbi-value vpnsub-routing-row" }, [
        toggle,
        E("div", { class: "cbi-value-field" }, [body]),
    ]);
}

// ── Tab switcher ──────────────────────────────────────────────────────────────
function makeTabs(tabs) {
    // tabs = [{id, label, desc?, content}]
    var tabItems = tabs.map(function (t, i) {
        var item = E("li", {
            class: i === 0 ? "cbi-tab" : "cbi-tab-disabled",
        });
        var link = E(
            "a",
            {
                href: "#",
                "data-tab": t.id,
                role: "tab",
                "aria-selected": i === 0 ? "true" : "false",
                click: function (ev) {
                    if (ev) ev.preventDefault();
                    tabItems.forEach(function (tabItem, idx) {
                        var active = idx === i;
                        tabItem.className = active
                            ? "cbi-tab"
                            : "cbi-tab-disabled";
                        var tabLink = tabItem.querySelector("a");
                        if (tabLink)
                            tabLink.setAttribute(
                                "aria-selected",
                                active ? "true" : "false",
                            );
                    });
                    tabs.forEach(function (tab) {
                        var show = tab.id === t.id;
                        tab.content.style.display = show ? "" : "none";
                        if (tab._descEl)
                            tab._descEl.style.display = show ? "" : "none";
                    });
                },
            },
            t.label,
        );
        item.appendChild(link);
        return item;
    });

    tabs.forEach(function (t, i) {
        t.content.style.display = i === 0 ? "" : "none";
        t.content.setAttribute("role", "tabpanel");
    });

    var tabBar = E("ul", { class: "cbi-tabmenu" }, tabItems);
    var wrapper = E("div", { class: "vpnsub-tabs" }, [tabBar]);
    tabs.forEach(function (t, i) {
        if (t.desc) {
            t._descEl = E(
                "div",
                {
                    class: "cbi-map-descr",
                    style: i === 0 ? "" : "display:none",
                },
                t.desc,
            );
            wrapper.appendChild(t._descEl);
        }
        wrapper.appendChild(t.content);
    });
    return wrapper;
}

// ── Import / Export helpers ───────────────────────────────────────────────────

function downloadJson(data, filename) {
    var blob = new Blob([JSON.stringify(data, null, 2)], {
        type: "application/json",
    });
    var url = URL.createObjectURL(blob);
    var a = document.createElement("a");
    a.href = url;
    a.download = filename;
    document.body.appendChild(a);
    a.click();
    document.body.removeChild(a);
    URL.revokeObjectURL(url);
}

function importJson(accept) {
    return new Promise(function (resolve, reject) {
        var input = document.createElement("input");
        input.type = "file";
        input.accept = accept || ".json";
        input.addEventListener("change", function () {
            var file = input.files && input.files[0];
            if (!file) return reject(new Error("No file selected"));
            var reader = new FileReader();
            reader.onload = function () {
                try {
                    resolve(JSON.parse(reader.result));
                } catch (e) {
                    reject(new Error(_("Invalid JSON file")));
                }
            };
            reader.onerror = function () {
                reject(new Error("Failed to read file"));
            };
            reader.readAsText(file);
        });
        input.click();
    });
}

// ── Main view ─────────────────────────────────────────────────────────────────
return view.extend({
    _widgets: null,
    _subIdx: 0,
    _pollTimer: null,
    _syslogTimer: null,
    _routingPollTimer: null,

    load: function () {
        return Promise.all([
            callGetConfig(),
            callGetSbStatus(),
            callGetTemplate(),
            callGetDomainsConfig().catch(function () {
                return null;
            }),
            callGetManualIps().catch(function () {
                return null;
            }),
            callGetManualDomains().catch(function () {
                return null;
            }),
            callGetSyncStatus().catch(function () {
                return null;
            }),
        ]);
    },

    render: function (results) {
        var self = this;
        var data = results[0];
        var sbData = results[1];
        var tmplData = results[2];
        var domainsData = results[3];
        var manualIpsData = results[4];
        var manualDomainsData = results[5];
        var syncData = results[6];

        var cfg =
            data && data.config
                ? data.config
                : { log_level: "warn", subscriptions: {} };

        // Proxies map from sing-box REST API (may be empty if API is down)
        var proxies = sbData && sbData.proxies ? sbData.proxies : {};
        var tagNames = sbData && sbData.tag_names ? sbData.tag_names : {};

        self._widgets = {};
        self._singboxTemplate = (cfg.singbox || {}).template || "";
        var subKeys = Object.keys(cfg.subscriptions || {});
        self._subIdx = subKeys.length;

        // ── Tab 1: Settings ───────────────────────────────────────────────────
        var logLevelSel = E("select", {
            class: "cbi-input-select",
            id: "vpnsub-log-level",
        });
        ["trace", "debug", "info", "warn", "error", "fatal", "panic"].forEach(
            function (l) {
                var o = E("option", { value: l }, l);
                if (l === ((cfg.singbox || {}).log_level || "warn")) o.selected = true;
                logLevelSel.appendChild(o);
            },
        );

        var testUrlSettingInput = E("input", {
            type: "text",
            class: "cbi-input-text",
            id: "vpnsub-test-url-setting",
            value: (cfg.singbox || {}).test_url || "https://www.gstatic.com/generate_204",
            placeholder: "https://www.gstatic.com/generate_204",
        });

        var globalSection = E("div", { class: "cbi-section" }, [
            E("legend", {}, _("Global settings")),
            formRow(
                _("Log level"),
                logLevelSel,
                _("Logging verbosity for sing-box"),
            ),
            formRow(
                _("URL test"),
                testUrlSettingInput,
                _("URL used by urltest groups to measure latency"),
            ),
        ]);

        var subList = E("div", { id: "vpnsub-sub-list" });
        subKeys.forEach(function (id, i) {
            var sub = Object.assign({}, cfg.subscriptions[id], { id: id });
            subList.appendChild(self._makeCard(sub, i, proxies, tagNames));
        });

        var addBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-add",
                title: _("Add"),
                click: function () {
                    var i = self._subIdx++;
                    var card = self._makeCard(
                        { name: "", url: "", default: false, enabled: true },
                        i,
                    );
                    subList.appendChild(card);
                    card.scrollIntoView({ behavior: "smooth" });
                },
            },
            _("Add"),
        );

        var saveBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-save",
                click: function () {
                    self.handleSave();
                },
            },
            _("Save"),
        );

        var settingsDirtyEl = E(
            "span",
            {
                class: "vpnsub-template-dirty",
                style: "display:none",
            },
            "\u25cf\u00a0" + _("Unsaved changes"),
        );
        self._settingsDirtyEl = settingsDirtyEl;

        var hasPendingChanges = !!(syncData && syncData.synced === false);
        var unsyncBanner = E(
            "div",
            {
                class: "alert-message warning vpnsub-unsync-banner",
                style: hasPendingChanges ? "" : "display:none",
            },
            [
                E("h4", {}, _("Configuration not applied")),
                E(
                    "p",
                    {},
                    _(
                        "Settings have been saved but not yet applied to sing-box. Go to the Run tab and run subscriptions to apply changes.",
                    ),
                ),
            ],
        );
        self._unsyncBanner = unsyncBanner;

        var subsExportBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    callGetConfig().then(function (res) {
                        var cfg = res && res.config ? res.config : {};
                        downloadJson(cfg, "subs.json");
                    });
                },
            },
            _("Export"),
        );

        var subsImportBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    importJson(".json")
                        .then(function (data) {
                            if (
                                !data ||
                                typeof data !== "object" ||
                                !data.subscriptions
                            ) {
                                throw new Error(
                                    _("Invalid config: missing subscriptions"),
                                );
                            }
                            return callSetConfig(data);
                        })
                        .then(function (res) {
                            if (res && res.error) throw new Error(res.error);
                            ui.addNotification(
                                null,
                                E("p", _("Config imported")),
                                "info",
                            );
                            window.location.reload();
                        })
                        .catch(function (err) {
                            if (err && err.message)
                                ui.addNotification(
                                    null,
                                    E("p", _("Error: ") + err.message),
                                    "error",
                                );
                        });
                },
            },
            _("Import"),
        );

        var settingsPane = E("div", { class: "vpnsub-tab-pane" }, [
            globalSection,
            E("div", { class: "cbi-section cbi-tblsection" }, [
                E("legend", {}, _("Subscriptions")),
                subList,
                E(
                    "div",
                    { class: "cbi-section-create cbi-tblsection-create" },
                    [addBtn],
                ),
            ]),
            E("div", { class: "cbi-page-actions" }, [
                subsExportBtn,
                "\u00a0",
                subsImportBtn,
                "\u00a0\u00a0",
                settingsDirtyEl,
                "\u00a0\u00a0",
                saveBtn,
            ]),
        ]);

        // Mark dirty on any input/change inside the settings pane
        settingsPane.addEventListener("input", function () {
            settingsDirtyEl.style.display = "";
        });
        settingsPane.addEventListener("change", function () {
            settingsDirtyEl.style.display = "";
        });

        // Mark dirty when cards are added or removed
        new MutationObserver(function () {
            settingsDirtyEl.style.display = "";
        }).observe(subList, { childList: true });

        // ── Tab: Sing-box syslog ──────────────────────────────────────────────
        var syslogPre = E(
            "pre",
            { id: "vpnsub-syslog", class: "vpnsub-log" },
            _("Loading…"),
        );

        var syslogAutoChk = E("input", {
            type: "checkbox",
            id: "vpnsub-syslog-auto",
            change: function () {
                if (this.checked) {
                    self._startSyslogPoll(syslogPre);
                } else {
                    self._stopSyslogPoll();
                }
            },
        });

        var syslogRefreshBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    self._fetchSyslog(syslogPre);
                },
            },
            _("Refresh"),
        );

        var syslogPane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("sing-box system log")),
                E("div", { class: "vpnsub-run-options" }, [
                    E("label", { class: "vpnsub-run-option" }, [
                        syslogAutoChk,
                        "\u00a0",
                        _("Auto-refresh (5s)"),
                    ]),
                    syslogRefreshBtn,
                ]),
                syslogPre,
            ]),
        ]);

        // Initial syslog load when tab is first rendered
        self._fetchSyslog(syslogPre);

        // ── Tab 4: Test ───────────────────────────────────────────────────────
        var testUrlInput = E("input", {
            type: "text",
            class: "cbi-input-text vpnsub-test-urlinput",
            id: "vpnsub-test-url",
            placeholder: "https://example.com",
            keydown: function (ev) {
                if (ev.key === "Enter") testBtn.click();
            },
        });

        var testBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-action",
                click: function () {
                    self.handleTest(testBtn, testResultsEl);
                },
            },
            _("Test"),
        );

        var testResultsEl = E("div", { id: "vpnsub-test-results" });

        var testPane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("VPN connectivity test")),
                E("div", { class: "vpnsub-test-row-input" }, [
                    testUrlInput,
                    "\u00a0",
                    testBtn,
                ]),
                E("div", { class: "cbi-value-description" }, [
                    _(
                        "Sends a request through tun0 and reports which outbound handled it.",
                    ),
                ]),
                testResultsEl,
            ]),
        ]);

        // ── Tab 5: Template editor ────────────────────────────────────────────
        var initialTemplate =
            tmplData && tmplData.template ? tmplData.template : "";
        var originalTemplate = initialTemplate;

        // Highlight overlay — sits behind the transparent textarea
        var highlightEl = E("pre", { class: "vpnsub-template-highlight" });

        var templateTextarea = E("textarea", {
            class: "vpnsub-template-editor",
            id: "vpnsub-template-editor",
            spellcheck: "false",
            autocomplete: "off",
            autocorrect: "off",
            autocapitalize: "off",
        });
        templateTextarea.value = initialTemplate;

        var templateWrap = E("div", { class: "vpnsub-template-wrap" }, [
            highlightEl,
            templateTextarea,
        ]);

        var templateStatusEl = E("div", {
            class: "vpnsub-template-status",
            style: "display:none",
        });

        function updateHighlight() {
            var text = templateTextarea.value;
            var errLine = null;
            try {
                JSON.parse(text);
                templateWrap.classList.remove("vpnsub-template-wrap--error");
                templateStatusEl.style.display = "none";
                templateStatusEl.textContent = "";
            } catch (e) {
                errLine = getJsonErrorLine(text, e);
                templateWrap.classList.add("vpnsub-template-wrap--error");
                templateStatusEl.textContent = e.message;
                templateStatusEl.style.display = "";
            }
            // Trailing \n prevents the last line from being clipped in the pre
            highlightEl.innerHTML = highlightJson(text, errLine) + "\n";
        }
        updateHighlight();

        var dirtyEl = E(
            "span",
            { class: "vpnsub-template-dirty", style: "display:none" },
            "\u25cf\u00a0" + _("Unsaved changes"),
        );

        templateTextarea.addEventListener("input", function () {
            updateHighlight();
            dirtyEl.style.display =
                templateTextarea.value !== originalTemplate ? "" : "none";
        });
        templateTextarea.addEventListener("scroll", function () {
            highlightEl.scrollTop = templateTextarea.scrollTop;
            highlightEl.scrollLeft = templateTextarea.scrollLeft;
        });

        var templateErrEl = E("div", {
            class: "vpnsub-template-error",
            style: "display:none",
        });

        var templateSaveBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-save",
                click: function () {
                    var text = templateTextarea.value;
                    templateErrEl.style.display = "none";
                    try {
                        JSON.parse(text);
                    } catch (e) {
                        templateErrEl.textContent =
                            _("JSON error: ") + e.message;
                        templateErrEl.style.display = "";
                        return;
                    }
                    templateSaveBtn.disabled = true;
                    callSetTemplate(text)
                        .then(function (res) {
                            if (res && res.error) throw new Error(res.error);
                            originalTemplate = text;
                            dirtyEl.style.display = "none";
                            templateErrEl.style.display = "none";
                            return self._refreshSyncStatus();
                        })
                        .then(function () {
                            ui.addNotification(
                                null,
                                E("p", _("Template saved")),
                                "info",
                            );
                        })
                        .catch(function (err) {
                            templateErrEl.textContent =
                                _("Error: ") + (err.message || String(err));
                            templateErrEl.style.display = "";
                        })
                        .then(function () {
                            templateSaveBtn.disabled = false;
                        });
                },
            },
            _("Save"),
        );

        var templateResetBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    if (!window.confirm(_("Reset template to default?")))
                        return;
                    templateResetBtn.disabled = true;
                    callResetTemplate()
                        .then(function (res) {
                            if (res && res.error) throw new Error(res.error);
                            if (!res || !res.template)
                                throw new Error(_("No template returned"));
                            templateTextarea.value = res.template;
                            originalTemplate = res.template;
                            dirtyEl.style.display = "none";
                            updateHighlight();
                            templateErrEl.style.display = "none";
                            return self._refreshSyncStatus();
                        })
                        .then(function () {
                            ui.addNotification(
                                null,
                                E("p", _("Template reset to default")),
                                "info",
                            );
                        })
                        .catch(function (err) {
                            templateErrEl.textContent =
                                _("Error: ") + (err.message || String(err));
                            templateErrEl.style.display = "";
                        })
                        .then(function () {
                            templateResetBtn.disabled = false;
                        });
                },
            },
            _("Reset to default"),
        );

        var templateMergeNote = E("div", { class: "cbi-section" }, [
            E("legend", {}, _("How template merging works")),
            E("ul", { class: "vpnsub-tpl-merge-notes" }, [
                E("li", {}, _("Generated proxy outbounds are prepended before the template outbounds array.")),
                E("li", {}, _("Generated route rules are prepended before the template route.rules array.")),
                E("li", {}, _("Bare string entries in outbounds and route.rules arrays are stripped from the template.")),
                E("li", {}, _("log.level is always set from the Log level setting above.")),
                E("li", {}, _("route.final is always set to the default subscription proxy tag.")),
                E("li", {}, _("No placeholder strings are needed — the template is plain JSON.")),
            ]),
        ]);

        var templatePane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("config.template.json")),
                E("div", { class: "vpnsub-log-actions" }, [
                    dirtyEl,
                    "\u00a0\u00a0",
                    templateSaveBtn,
                    "\u00a0",
                    templateResetBtn,
                ]),
                templateErrEl,
                templateWrap,
                templateStatusEl,
            ]),
            templateMergeNote,
        ]);

        // ── Tab 6: Domains ────────────────────────────────────────────────────
        var domainsCfg =
            domainsData && domainsData.config
                ? domainsData.config
                : { domains: {}, subnets: {} };
        var domainsUrlInput = E("input", {
            type: "text",
            class: "cbi-input-text",
            id: "vpnsub-domains-url",
            value: (domainsCfg.domains || {}).url || "",
            placeholder:
                "https://raw.githubusercontent.com/itdoginfo/allow-domains/main/Russia/inside-dnsmasq-nfset.lst",
        });

        var subnetW = dynList(
            (domainsCfg.subnets || {}).urls || [],
            "https://raw.githubusercontent.com/itdoginfo/allow-domains/refs/heads/main/Subnets/IPv4/telegram.lst",
        );

        var manualIpsText =
            manualIpsData && manualIpsData.ips != null ? manualIpsData.ips : "";
        var manualIpsList = manualIpsText
            .split("\n")
            .map(function (s) {
                return s.trim();
            })
            .filter(function (s) {
                return s !== "";
            });

        var manualDomainsInitial =
            manualDomainsData && manualDomainsData.domains
                ? manualDomainsData.domains
                : [];
        var manualDomainsW = dynList(manualDomainsInitial, "example.com");

        var domainsDirtyEl = E(
            "span",
            { class: "vpnsub-template-dirty", style: "display:none" },
            "\u25cf\u00a0" + _("Unsaved changes"),
        );

        var domainsSaveBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-save",
                click: function () {
                    self.handleDomainsSave(
                        domainsSaveBtn,
                        domainsUrlInput,
                        subnetW,
                        domainsDirtyEl,
                    );
                },
            },
            _("Save"),
        );

        var domainsExportBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    callGetDomainsConfig().then(function (res) {
                        var cfg = res && res.config ? res.config : {};
                        downloadJson(cfg, "domains.json");
                    });
                },
            },
            _("Export"),
        );

        var domainsImportBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    importJson(".json")
                        .then(function (data) {
                            if (!data || typeof data !== "object") {
                                throw new Error(_("Invalid JSON file"));
                            }
                            return callSetDomainsConfig(data);
                        })
                        .then(function (res) {
                            if (res && res.error) throw new Error(res.error);
                            ui.addNotification(
                                null,
                                E("p", _("Config imported")),
                                "info",
                            );
                            window.location.reload();
                        })
                        .catch(function (err) {
                            if (err && err.message)
                                ui.addNotification(
                                    null,
                                    E("p", _("Error: ") + err.message),
                                    "error",
                                );
                        });
                },
            },
            _("Import"),
        );

        var domainsPane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("Routing")),
                formRow(
                    _("Domains URL"),
                    domainsUrlInput,
                    _(
                        "URL of dnsmasq nfset domain list (e.g. from itdoginfo/allow-domains)",
                    ),
                ),
                formRow(
                    _("Subnet URLs"),
                    subnetW.node,
                    _(
                        "URLs of IP subnet lists to route through VPN (one per line)",
                    ),
                ),
                (function () {
                    var count = manualIpsList.length;
                    var badge = E(
                        "span",
                        { class: "vpnsub-count-badge" },
                        count > 0 ? String(count) : "0",
                    );
                    badge.style.opacity = count > 0 ? "1" : "0.4";
                    var arrow = E(
                        "span",
                        { class: "vpnsub-collapse-arrow" },
                        "▶",
                    );
                    var pre = E(
                        "pre",
                        {
                            class: "vpnsub-log vpnsub-manual-ips-pre",
                            style: "max-height:200px;overflow-y:auto",
                        },
                        count > 0 ? manualIpsList.join("\n") : _("(empty)"),
                    );
                    var note = E(
                        "div",
                        { class: "cbi-value-description" },
                        _(
                            "Edit manually via SSH: /etc/horn-vpn-manager/lists/manual-ip.lst",
                        ),
                    );
                    var body = E(
                        "div",
                        {
                            class: "vpnsub-collapse-body",
                            style: "display:none",
                        },
                        [pre, note],
                    );
                    var toggle = E(
                        "label",
                        {
                            class: "cbi-value-title vpnsub-collapse-label",
                            click: function () {
                                var open = body.style.display !== "none";
                                body.style.display = open ? "none" : "";
                                arrow.textContent = open ? "▶" : "▼";
                                badge.style.display = open ? "" : "none";
                            },
                        },
                        [
                            E("span", { class: "vpnsub-collapse-toggle" }, [
                                arrow,
                                "\u00a0",
                                _("Manual IP / CIDR"),
                                "\u00a0",
                                badge,
                            ]),
                        ],
                    );
                    return E("div", { class: "cbi-value vpnsub-routing-row" }, [
                        toggle,
                        E("div", { class: "cbi-value-field" }, [body]),
                    ]);
                })(),
                E("div", { class: "cbi-page-actions" }, [
                    domainsExportBtn,
                    "\u00a0",
                    domainsImportBtn,
                    "\u00a0\u00a0",
                    domainsDirtyEl,
                    "\u00a0\u00a0",
                    domainsSaveBtn,
                ]),
            ]),
        ]);

        // Mark dirty on any input/change inside the domains pane
        domainsPane.addEventListener("input", function () {
            domainsDirtyEl.style.display = "";
        });
        domainsPane.addEventListener("change", function () {
            domainsDirtyEl.style.display = "";
        });

        // Mark dirty when dynList rows are added or removed
        new MutationObserver(function () {
            domainsDirtyEl.style.display = "";
        }).observe(subnetW.node, { childList: true });

        // ── Tab 7: Additional domains (dnsmasq ipset) ─────────────────────────
        var additionalDirtyEl = E(
            "span",
            { class: "vpnsub-template-dirty", style: "display:none" },
            "\u25cf\u00a0" + _("Unsaved changes"),
        );

        var additionalSaveBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-save",
                click: function () {
                    var domains = manualDomainsW.getValue();
                    additionalSaveBtn.disabled = true;
                    callSetManualDomains(domains)
                        .then(function (res) {
                            var msg =
                                res && res.restarted
                                    ? _("Settings saved (dnsmasq restarted)")
                                    : _("Settings saved");
                            ui.addNotification(null, E("p", msg), "info");
                            additionalDirtyEl.style.display = "none";
                        })
                        .catch(function (err) {
                            ui.addNotification(
                                null,
                                E(
                                    "p",
                                    _("Save error: ") +
                                        (err.message || String(err)),
                                ),
                                "error",
                            );
                        })
                        .then(function () {
                            additionalSaveBtn.disabled = false;
                        });
                },
            },
            _("Save"),
        );

        var additionalPane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("Manual dnsmasq domains")),
                formRow(
                    _("Domains"),
                    manualDomainsW.node,
                    _(
                        "Domains routed through VPN via dnsmasq ipset (/etc/config/dhcp)",
                    ),
                ),
                E("div", { class: "cbi-page-actions" }, [
                    additionalSaveBtn,
                    "\u00a0\u00a0",
                    additionalDirtyEl,
                ]),
            ]),
        ]);

        additionalPane.addEventListener("input", function () {
            additionalDirtyEl.style.display = "";
        });
        additionalPane.addEventListener("change", function () {
            additionalDirtyEl.style.display = "";
        });
        new MutationObserver(function () {
            additionalDirtyEl.style.display = "";
        }).observe(manualDomainsW.node, { childList: true });

        // ── Tab: Run ──────────────────────────────────────────────────────────
        var subLogPre = E("pre", { class: "vpnsub-log vpnsub-run-log" },
            _("No log yet. Run subscriptions to see output."));

        var subCachedChk = E("input", {
            type: "checkbox",
            id: "vpnsub-cached-lists",
            checked: true,
            change: function () {
                if (this.checked) subDownloadChk.checked = false;
            },
        });
        var subDownloadChk = E("input", {
            type: "checkbox",
            id: "vpnsub-download-lists",
            change: function () {
                if (this.checked) subCachedChk.checked = false;
            },
        });
        var subDryRunChk = E("input", {
            type: "checkbox",
            id: "vpnsub-run-dry-run",
        });

        var subRunBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-action",
                click: function () {
                    self.handleRun(subRunBtn, subLogPre);
                },
            },
            _("Run subscriptions"),
        );
        var subClearBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    subLogPre.innerHTML = "";
                },
            },
            _("Clear"),
        );

        var routingLogPre = E("pre", { class: "vpnsub-log vpnsub-run-log" },
            _("No log yet. Run routing to see output."));

        var routingWithSubsChk = E("input", {
            type: "checkbox",
            id: "vpnsub-with-subscriptions",
            checked: true,
        });

        var routingRunBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-action",
                click: function () {
                    self.handleRunRouting(routingRunBtn, routingLogPre);
                },
            },
            _("Run routing"),
        );
        var routingClearBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button",
                click: function () {
                    routingLogPre.innerHTML = "";
                },
            },
            _("Clear"),
        );

        var runPane = E("div", { class: "vpnsub-tab-pane" }, [
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("Run subscriptions")),
                E("div", { class: "vpnsub-run-options" }, [
                    E("label", { class: "vpnsub-run-option" }, [
                        subCachedChk, "\u00a0", _("--cached-lists"),
                    ]),
                    E("label", { class: "vpnsub-run-option" }, [
                        subDownloadChk, "\u00a0", _("--download-lists"),
                    ]),
                    E("label", { class: "vpnsub-run-option" }, [
                        subDryRunChk, "\u00a0", _("Dry run"),
                    ]),
                ]),
                E("div", { class: "vpnsub-log-actions" }, [
                    subRunBtn, "\u00a0", subClearBtn,
                ]),
                subLogPre,
            ]),
            E("div", { class: "cbi-section" }, [
                E("legend", {}, _("Run routing")),
                E("div", { class: "vpnsub-run-options" }, [
                    E("label", { class: "vpnsub-run-option" }, [
                        routingWithSubsChk, "\u00a0", _("--with-subscriptions"),
                    ]),
                ]),
                E("div", { class: "vpnsub-log-actions" }, [
                    routingRunBtn, "\u00a0", routingClearBtn,
                ]),
                routingLogPre,
            ]),
        ]);

        // ── Import / Export toolbar ───────────────────────────────────────────
        var exportBtn = E("button", {
            class: "btn cbi-button cbi-button-neutral",
            click: function () {
                callGetConfig()
                    .then(function (res) {
                        if (res && res.config) {
                            downloadJson(res.config, "horn-vpn-manager-config.json");
                        } else {
                            ui.addNotification(
                                null,
                                E("p", _("Failed to fetch config for export")),
                                "error",
                            );
                        }
                    })
                    .catch(function (err) {
                        ui.addNotification(
                            null,
                            E("p", _("Export error: ") + (err.message || String(err))),
                            "error",
                        );
                    });
            },
        }, _("Export config"));

        var importBtn = E("button", {
            class: "btn cbi-button cbi-button-neutral",
            click: function () {
                importJson(".json")
                    .then(function (data) {
                        if (!data || typeof data !== "object") {
                            ui.addNotification(
                                null,
                                E("p", _("Invalid config file")),
                                "warning",
                            );
                            return;
                        }
                        if (!data.subscriptions || typeof data.subscriptions !== "object") {
                            ui.addNotification(
                                null,
                                E("p", _("Invalid config: missing subscriptions")),
                                "warning",
                            );
                            return;
                        }
                        return callSetConfig(data)
                            .then(function (res) {
                                if (res && res.error) throw new Error(res.error);
                                ui.addNotification(
                                    null,
                                    E("p", _("Config imported — reloading…")),
                                    "info",
                                );
                                setTimeout(function () {
                                    location.reload();
                                }, 1500);
                            });
                    })
                    .catch(function (err) {
                        if (err && err.message !== "No file selected") {
                            ui.addNotification(
                                null,
                                E("p", _("Import error: ") + (err.message || String(err))),
                                "error",
                            );
                        }
                    });
            },
        }, _("Import config"));

        var configToolbar = E("div", { class: "vpnsub-config-toolbar" }, [
            exportBtn,
            importBtn,
        ]);

        // ── Assemble tabs ─────────────────────────────────────────────────────
        return E("div", { class: "cbi-map" }, [
            E("h2", {}, _("VPN management")),
            unsyncBanner,
            configToolbar,
            makeTabs([
                {
                    id: "settings",
                    label: _("Subscriptions"),
                    desc: _(
                        "Configure VPN subscriptions: URLs, routing rules, proxy selection and global settings",
                    ),
                    content: settingsPane,
                },
                {
                    id: "domains",
                    label: _("Routing"),
                    desc: _(
                        "Configure domain and subnet lists for routing, and static IP routes",
                    ),
                    content: domainsPane,
                },
                {
                    id: "template",
                    label: _("Sing-box template config"),
                    desc: _(
                        "Edit config.template.json — the template used to generate the sing-box configuration",
                    ),
                    content: templatePane,
                },
                {
                    id: "additional",
                    label: _("Additional domains"),
                    desc: _(
                        "Manage per-domain VPN routing via dnsmasq ipset — changes are written to /etc/config/dhcp",
                    ),
                    content: additionalPane,
                },
                {
                    id: "syslog",
                    label: _("Sing-box logs"),
                    desc: _(
                        "View recent sing-box log entries from the system journal",
                    ),
                    content: syslogPane,
                },
                {
                    id: "test",
                    label: _("Test"),
                    desc: _(
                        "Test VPN connectivity — checks which outbound handles a given URL through tun0",
                    ),
                    content: testPane,
                },
                {
                    id: "run",
                    label: _("Run"),
                    desc: _(
                        "Run subscriptions or routing updates with per-command options and live log output",
                    ),
                    content: runPane,
                },
            ]),
        ]);
    },

    // ── Render one subscription card ──────────────────────────────────────────
    _makeCard: function (sub, idx, proxies, tagNames) {
        var self = this;
        var cardId = "vpnsub-sub-" + idx;

        var subRoute = sub.route || {};
        var domainsW = dynList(subRoute.domains || [], _("example.com"));
        var domainUrlsW = dynList(
            subRoute.domain_urls || [],
            "https://raw.githubusercontent.com/.../domains.lst",
        );
        var ipW = dynList(subRoute.ip_cidrs || [], "192.168.0.0/16");
        var ipUrlsW = dynList(
            subRoute.ip_urls || [],
            "https://raw.githubusercontent.com/.../subnets.lst",
        );
        var includeW = dynList(sub.include || [], _("keyword"));
        var excludeW = dynList(sub.exclude || [], _("Russia"));
        self._widgets[idx] = {
            domains: domainsW,
            domain_urls: domainUrlsW,
            ip: ipW,
            ip_urls: ipUrlsW,
            include: includeW,
            exclude: excludeW,
        };

        var nameInput = E("input", {
            type: "text",
            class: "cbi-input-text vpnsub-sub-name",
            value: sub.name || "",
            placeholder: _("Subscription name"),
            input: function () {
                clearError(this);
            },
        });

        // ID field — stable identifier used in sing-box tag names
        var idInput = E("input", {
            type: "text",
            class: "cbi-input-text vpnsub-sub-id",
            value: sub.id || "",
            placeholder: _("e.g. mullvad"),
            input: function () {
                this._manualEdit = true;
                clearError(this);
            },
        });

        // Auto-fill ID from name for new subscriptions (no existing id)
        if (!sub.id) {
            nameInput.addEventListener("input", function () {
                if (!idInput._manualEdit) {
                    idInput.value = sanitizeId(nameInput.value);
                }
            });
        }

        var urlInput = E("input", {
            type: "text",
            class: "cbi-input-text vpnsub-sub-url",
            value: sub.url || "",
            placeholder: "https://",
            input: function () {
                clearError(this);
            },
        });

        var defInput = E("input", {
            type: "radio",
            name: "vpnsub-default",
            class: "vpnsub-sub-default",
            value: String(idx),
            change: function () {
                self._updateDomainVisibility();
            },
        });
        if (sub.default === true) defInput.checked = true;

        var enabledInput = E("input", {
            type: "checkbox",
            class: "vpnsub-sub-enabled",
        });
        if (sub.enabled !== false && sub.enabled !== "false")
            enabledInput.checked = true;

        var removeBtn = E(
            "button",
            {
                type: "button",
                class: "cbi-button cbi-button-negative vpnsub-sub-remove",
                click: function () {
                    var name = nameInput.value.trim() || _("this subscription");
                    if (
                        !window.confirm(
                            _("Remove subscription") + ' "' + name + '"?',
                        )
                    )
                        return;
                    var card = document.getElementById(cardId);
                    if (card) card.parentNode.removeChild(card);
                    delete self._widgets[idx];
                },
            },
            _("Remove"),
        );

        var subId = sub.id || sub.name;
        var hasUrltest = !!(proxies && subId && proxies[subId + "-auto"]);
        var isSingleNode = !!(proxies && subId && proxies[subId + "-single"]);

        // interval / tolerance inputs — placed in proxy widget Auto pane when multi-node,
        // otherwise rendered as plain form rows so they're always accessible to _collectConfig
        var intervalInput = E("input", {
            type: "text",
            class: "cbi-input-text vpnsub-sub-interval",
            value: sub.interval || "",
            placeholder: "5m",
        });
        var toleranceInput = E("input", {
            type: "number",
            class: "cbi-input-text vpnsub-sub-tolerance",
            value: sub.tolerance != null ? String(sub.tolerance) : "",
            placeholder: "100",
            min: "0",
        });

        var retriesInput = E("input", {
            type: "number",
            class: "cbi-input-text vpnsub-sub-retries",
            value: sub.retries != null ? String(sub.retries) : "",
            placeholder: _("Global default"),
            min: "0",
            max: "10",
        });

        // For multi-node, pass inputs into the proxy widget (Auto pane).
        // For single-node or when sing-box is down, they'll be rendered as plain rows below.
        var proxyWidget = makeProxyWidget(
            subId,
            proxies,
            tagNames,
            hasUrltest ? intervalInput : null,
            hasUrltest ? toleranceInput : null,
        );

        var domainsRow = makeCollapsible(
            _("Domains"),
            domainsW,
            _(
                "Traffic to these domains will be routed through this subscription",
            ),
        );
        if (sub.default === true) domainsRow.style.display = "none";

        var domainUrlsRow = makeCollapsible(
            _("Domain list URLs"),
            domainUrlsW,
            _(
                "URLs to download domain lists from (one domain per line); downloaded domains are merged and deduplicated with manual entries",
            ),
        );
        if (sub.default === true) domainUrlsRow.style.display = "none";

        var ipRow = makeCollapsible(
            _("IP / CIDR"),
            ipW,
            _(
                "Traffic to these IP ranges will be routed through this subscription",
            ),
        );
        if (sub.default === true) ipRow.style.display = "none";

        var ipUrlsRow = makeCollapsible(
            _("IP list URLs"),
            ipUrlsW,
            _(
                "URLs to download IP/CIDR lists from (one entry per line); downloaded entries are merged and deduplicated with manual entries",
            ),
        );
        if (sub.default === true) ipUrlsRow.style.display = "none";

        var children = [
            E("div", { class: "vpnsub-sub-header" }, [
                E("strong", { class: "vpnsub-sub-title" }, _("Subscription")),
                removeBtn,
            ]),
            formRow(
                _("Code"),
                idInput,
                _("Unique identifier used in tag names (e.g. {code}-manual)"),
            ),
            formRow(_("Name"), nameInput),
            formRow(_("URL"), urlInput),
            formRow(
                _("Retries"),
                retriesInput,
                _("Leave empty to use the global setting"),
            ),
            formRow(
                _("Default"),
                E("label", {}, [
                    defInput,
                    "\u00a0" + _("Use as default outbound (fallback route)"),
                ]),
                _("Exactly one subscription must be marked as default"),
            ),
            formRow(
                _("Enabled"),
                E("label", {}, [
                    enabledInput,
                    "\u00a0" + _("Use this subscription"),
                ]),
                _("Disabled subscriptions are skipped during updates"),
            ),
        ];

        if (proxyWidget) children.push(formRow(_("Proxy"), proxyWidget));

        // Show interval/tolerance as plain rows only when multi-node but Auto pane
        // was not created (should not normally happen). Hide for single-node and
        // when proxy data is unavailable (sing-box is down).
        if (!hasUrltest && !isSingleNode && proxyWidget) {
            children.push(
                formRow(
                    _("Interval"),
                    intervalInput,
                    _("How often urltest checks latency (e.g. 5m, 1m)"),
                ),
            );
            children.push(
                formRow(
                    _("Tolerance"),
                    toleranceInput,
                    _(
                        "Switch to a faster node only if it beats current by at least this many ms",
                    ),
                ),
            );
        }

        children.push(domainsRow);
        children.push(domainUrlsRow);
        children.push(ipRow);
        children.push(ipUrlsRow);
        children.push(
            formRow(
                _("Include filters"),
                includeW.node,
                _(
                    "Only servers whose names contain at least one of these strings will be used (leave empty to include all)",
                ),
            ),
        );
        children.push(
            formRow(
                _("Exclude filters"),
                excludeW.node,
                _(
                    "Servers whose names contain any of these strings will be skipped",
                ),
            ),
        );

        return E(
            "div",
            {
                class: "cbi-section-node vpnsub-sub-card",
                id: cardId,
                "data-idx": String(idx),
            },
            children,
        );
    },

    // ── Collect + validate ────────────────────────────────────────────────────
    _collectConfig: function () {
        var self = this;
        var level = document.getElementById("vpnsub-log-level").value;
        var testUrl =
            document.getElementById("vpnsub-test-url-setting").value.trim() ||
            "https://www.gstatic.com/generate_204";
        var subs = {};

        Array.prototype.forEach.call(
            document.querySelectorAll(".vpnsub-sub-card"),
            function (card) {
                var idx = parseInt(card.getAttribute("data-idx"));
                var w = self._widgets[idx] || {};
                var name = card.querySelector(".vpnsub-sub-name").value.trim();
                var idEl = card.querySelector(".vpnsub-sub-id");
                var id = idEl
                    ? idEl.value.trim() || sanitizeId(name)
                    : sanitizeId(name);
                var url = card.querySelector(".vpnsub-sub-url").value.trim();
                var isDef = card.querySelector(".vpnsub-sub-default").checked;
                var enabledEl = card.querySelector(".vpnsub-sub-enabled");
                var isEnabled = !enabledEl || enabledEl.checked;
                var domains = w.domains ? w.domains.getValue() : [];
                var domainUrls = w.domain_urls ? w.domain_urls.getValue() : [];
                var ip = w.ip ? w.ip.getValue() : [];
                var ipUrls = w.ip_urls ? w.ip_urls.getValue() : [];
                var include = w.include ? w.include.getValue() : [];
                var exclude = w.exclude ? w.exclude.getValue() : [];

                var intervalEl = card.querySelector(".vpnsub-sub-interval");
                var toleranceEl = card.querySelector(".vpnsub-sub-tolerance");
                var interval = intervalEl ? intervalEl.value.trim() : "";
                var toleranceRaw = toleranceEl ? toleranceEl.value.trim() : "";

                var sub = { name: name, url: url };
                if (isDef) sub.default = true;
                if (!isEnabled) sub.enabled = false;
                var route = {};
                if (domains.length) route.domains = domains;
                if (domainUrls.length) route.domain_urls = domainUrls;
                if (ip.length) route.ip_cidrs = ip;
                if (ipUrls.length) route.ip_urls = ipUrls;
                if (Object.keys(route).length) sub.route = route;
                if (include.length) sub.include = include;
                if (exclude.length) sub.exclude = exclude;
                if (interval) sub.interval = interval;
                if (toleranceRaw !== "") {
                    var tol = parseInt(toleranceRaw, 10);
                    if (!isNaN(tol)) sub.tolerance = tol;
                }
                var retriesEl = card.querySelector(".vpnsub-sub-retries");
                var retriesRaw = retriesEl ? retriesEl.value.trim() : "";
                if (retriesRaw !== "") {
                    var r = parseInt(retriesRaw, 10);
                    if (!isNaN(r)) sub.retries = r;
                }
                if (id) subs[id] = sub;
            },
        );

        return {
            singbox: { log_level: level, test_url: testUrl, template: this._singboxTemplate || "" },
            subscriptions: subs,
        };
    },

    _validate: function () {
        var self = this;
        var valid = true;

        Array.prototype.forEach.call(
            document.querySelectorAll(".vpnsub-sub-card"),
            function (card) {
                var idx = parseInt(card.getAttribute("data-idx"));
                var w = self._widgets[idx] || {};
                var nameEl = card.querySelector(".vpnsub-sub-name");
                var idEl = card.querySelector(".vpnsub-sub-id");
                var urlEl = card.querySelector(".vpnsub-sub-url");
                var name = nameEl.value.trim();
                var url = urlEl.value.trim();

                clearError(nameEl);
                if (idEl) clearError(idEl);
                clearError(urlEl);

                if (!name) {
                    setError(nameEl, _("Name is required"));
                    valid = false;
                }

                // Auto-fill ID if empty
                if (idEl && !idEl.value.trim()) {
                    var autoId = sanitizeId(name);
                    if (autoId) {
                        idEl.value = autoId;
                    } else if (name) {
                        setError(idEl, _("ID is required"));
                        valid = false;
                    }
                }

                if (!url) {
                    setError(urlEl, _("URL is required"));
                    valid = false;
                } else if (!isValidUrl(url)) {
                    setError(urlEl, _("Enter a valid URL (https://...)"));
                    valid = false;
                }

                // Reject empty-string include/exclude patterns
                if (w.include) {
                    var includeVals = w.include.getValue();
                    if (includeVals.some(function (v) { return v.trim() === ""; })) {
                        ui.addNotification(null, E("p", _("Include filters cannot contain empty entries")), "warning");
                        valid = false;
                    }
                }
                if (w.exclude) {
                    var excludeVals = w.exclude.getValue();
                    if (excludeVals.some(function (v) { return v.trim() === ""; })) {
                        ui.addNotification(null, E("p", _("Exclude filters cannot contain empty entries")), "warning");
                        valid = false;
                    }
                }
            },
        );

        if (!valid) return false;

        var cfg = this._collectConfig();
        var subVals = Object.values(cfg.subscriptions);
        var defaults = subVals.filter(function (s) {
            return s.default === true;
        });

        if (subVals.length > 0 && defaults.length === 0) {
            ui.addNotification(
                null,
                E("p", _("Please select a default subscription")),
                "warning",
            );
            return false;
        }
        if (defaults.length > 1) {
            ui.addNotification(
                null,
                E("p", _("Only one subscription can be set as default")),
                "warning",
            );
            return false;
        }

        return cfg;
    },

    // ── Show/hide routing rows (Domains + IP) based on which sub is default ──
    _updateDomainVisibility: function () {
        Array.prototype.forEach.call(
            document.querySelectorAll(".vpnsub-sub-card"),
            function (card) {
                var radio = card.querySelector(".vpnsub-sub-default");
                var isDefault = radio && radio.checked;
                Array.prototype.forEach.call(
                    card.querySelectorAll(".vpnsub-routing-row"),
                    function (row) {
                        row.style.display = isDefault ? "none" : "";
                    },
                );
            },
        );
    },

    // ── Save ──────────────────────────────────────────────────────────────────
    handleSave: function () {
        var cfg = this._validate();
        if (!cfg) return;

        var self = this;
        return callSetConfig(cfg)
            .then(function (res) {
                if (res && res.error) throw new Error(res.error);
                if (self._settingsDirtyEl)
                    self._settingsDirtyEl.style.display = "none";
                return self._refreshSyncStatus();
            })
            .then(function () {
                window.scrollTo({ top: 0, behavior: "smooth" });
                ui.addNotification(null, E("p", _("Settings saved")), "info");
            })
            .catch(function (err) {
                ui.addNotification(
                    null,
                    E("p", _("Save error: ") + (err.message || String(err))),
                    "error",
                );
            });
    },

    // ── Test ──────────────────────────────────────────────────────────────────
    handleTest: function (btn, resultsEl) {
        var url = document.getElementById("vpnsub-test-url").value.trim();
        if (!url) return;

        // Clear and show spinner
        while (resultsEl.firstChild)
            resultsEl.removeChild(resultsEl.firstChild);
        resultsEl.appendChild(
            E(
                "div",
                { class: "vpnsub-test-spinner" },
                _("Testing… (up to 30s)"),
            ),
        );
        if (btn) btn.disabled = true;

        callTestUrl(url)
            .then(function (res) {
                while (resultsEl.firstChild)
                    resultsEl.removeChild(resultsEl.firstChild);

                if (!res || res.error) {
                    resultsEl.appendChild(
                        E(
                            "p",
                            { class: "vpnsub-test-error" },
                            _("Error: ") +
                                ((res && res.error) ||
                                    _("no response from backend")),
                        ),
                    );
                    return;
                }

                var rows = [
                    [_("Domain"), res.domain || "—"],
                    [
                        _("VPN status"),
                        res.vpn_ok
                            ? "✅ " +
                              _("Connected") +
                              (res.http_code
                                  ? " (HTTP\u00a0" + res.http_code + ")"
                                  : "")
                            : "❌ " +
                              _("Failed") +
                              (res.http_code
                                  ? " (HTTP\u00a0" + res.http_code + ")"
                                  : ""),
                    ],
                    [_("Outbound"), res.outbound || "—"],
                ];

                var table = E(
                    "div",
                    { class: "vpnsub-test-table" },
                    rows.map(function (r) {
                        return E("div", { class: "vpnsub-test-kv" }, [
                            E("span", { class: "vpnsub-test-key" }, r[0]),
                            E("span", { class: "vpnsub-test-val" }, r[1]),
                        ]);
                    }),
                );
                resultsEl.appendChild(table);
            })
            .catch(function (err) {
                while (resultsEl.firstChild)
                    resultsEl.removeChild(resultsEl.firstChild);
                resultsEl.appendChild(
                    E(
                        "p",
                        { class: "vpnsub-test-error" },
                        _("Error: ") + (err.message || String(err)),
                    ),
                );
            })
            .then(function () {
                if (btn) btn.disabled = false;
            });
    },

    // ── Syslog ────────────────────────────────────────────────────────────────
    _fetchSyslog: function (pre) {
        callGetSyslog(200).then(function (res) {
            if (!res) return;
            var text = res.log != null ? res.log : "";
            pre.innerHTML =
                ansiToHtml(text) || _("(no sing-box entries in syslog)");
            pre.scrollTop = pre.scrollHeight;
        });
    },

    _startSyslogPoll: function (pre) {
        var self = this;
        self._stopSyslogPoll();
        self._syslogTimer = setInterval(function () {
            self._fetchSyslog(pre);
        }, 5000);
    },

    _stopSyslogPoll: function () {
        if (this._syslogTimer) {
            clearInterval(this._syslogTimer);
            this._syslogTimer = null;
        }
    },

    _showUnsync: function (show) {
        if (this._unsyncBanner)
            this._unsyncBanner.style.display = show ? "" : "none";
    },

    _refreshSyncStatus: function () {
        var self = this;
        return callGetSyncStatus()
            .then(function (res) {
                self._showUnsync(!!(res && res.synced === false));
                return res;
            })
            .catch(function () {
                self._showUnsync(false);
                return null;
            });
    },

    // ── Save domains config ───────────────────────────────────────────────────
    handleDomainsSave: function (btn, urlInput, subnetWidget, dirtyEl) {
        var domainsUrl = urlInput.value.trim();
        var subnetUrls = subnetWidget.getValue();

        var cfg = { domains: { url: domainsUrl }, subnets: { urls: subnetUrls } };

        if (btn) btn.disabled = true;
        Promise.all([callSetDomainsConfig(cfg)])
            .then(function () {
                ui.addNotification(null, E("p", _("Settings saved")), "info");
                if (dirtyEl) dirtyEl.style.display = "none";
            })
            .catch(function (err) {
                ui.addNotification(
                    null,
                    E("p", _("Save error: ") + (err.message || String(err))),
                    "error",
                );
            })
            .then(function () {
                if (btn) btn.disabled = false;
            });
    },

    // ── Run getdomains + poll log ─────────────────────────────────────────────
    // ── Run subscriptions + poll log ──────────────────────────────────────────
    handleRun: function (btn, logPre) {
        var self = this;
        var dryRun = document.getElementById("vpnsub-run-dry-run").checked;
        var cachedLists = document.getElementById("vpnsub-cached-lists").checked;
        var downloadLists = document.getElementById("vpnsub-download-lists").checked;

        if (self._pollTimer) {
            clearInterval(self._pollTimer);
            self._pollTimer = null;
        }

        var modeLabel = dryRun ? " [dry-run]" : "";
        logPre.innerHTML = ansiToHtml(_("Starting…") + modeLabel + "\n");
        if (btn) btn.disabled = true;

        callRunScript(dryRun, cachedLists, downloadLists)
            .then(function (res) {
                if (res && res.error) throw new Error(res.error);
                self._pollTimer = setInterval(function () {
                    callGetLog().then(function (res) {
                        if (!res) return;

                        var logText = res.log != null ? res.log : "";
                        logPre.innerHTML =
                            ansiToHtml(logText) || _("(waiting for output…)");
                        logPre.scrollTop = logPre.scrollHeight;

                        if (!res.running) {
                            clearInterval(self._pollTimer);
                            self._pollTimer = null;
                            if (btn) btn.disabled = false;
                            if (!dryRun) self._refreshSyncStatus();
                        }
                    });
                }, 2000);
            })
            .catch(function (err) {
                logPre.innerHTML += ansiToHtml(
                    "\n" + _("Error: ") + (err.message || String(err)),
                );
                if (btn) btn.disabled = false;
            });
    },

    // ── Run routing + poll log ────────────────────────────────────────────────
    handleRunRouting: function (btn, logPre) {
        var self = this;
        var withSubs = document.getElementById("vpnsub-with-subscriptions").checked;

        if (self._routingPollTimer) {
            clearInterval(self._routingPollTimer);
            self._routingPollTimer = null;
        }

        logPre.innerHTML = ansiToHtml(_("Starting…") + "\n");
        if (btn) btn.disabled = true;

        callRunRouting(withSubs)
            .then(function (res) {
                if (res && res.error) throw new Error(res.error);
                self._routingPollTimer = setInterval(function () {
                    callGetRoutingLog().then(function (res) {
                        if (!res) return;
                        var logText = res.log != null ? res.log : "";
                        logPre.innerHTML =
                            ansiToHtml(logText) || _("(waiting for output…)");
                        logPre.scrollTop = logPre.scrollHeight;
                        if (!res.running) {
                            clearInterval(self._routingPollTimer);
                            self._routingPollTimer = null;
                            if (btn) btn.disabled = false;
                        }
                    });
                }, 2000);
            })
            .catch(function (err) {
                logPre.innerHTML += ansiToHtml(
                    "\n" + _("Error: ") + (err.message || String(err)),
                );
                if (btn) btn.disabled = false;
            });
    },
});
