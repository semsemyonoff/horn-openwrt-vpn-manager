"use strict";

// Loads the real config.js into the stub DOM and hands back the view object,
// so tests drive the shipped _makeCard/_collectConfig/_validate rather than a
// reimplementation of them.
//
// LuCI evaluates a view file as a function body with view/rpc/ui/E/L/dom and
// the browser globals in scope, ending in `return view.extend({...})`. new
// Function reproduces exactly that, which is why config.js needs no test hooks.

const fs = require("fs");
const path = require("path");
const stub = require("./stub-dom.js");

const VIEW_PATH = path.join(
    __dirname,
    "..",
    "root/www/luci-static/resources/view/horn-vpn-manager/config.js",
);

// config.js uses URL for two unrelated things: the WHATWG constructor in
// isValidNodeUri and the blob statics in the config export. Node's global URL
// covers the constructor — a plain object stub would make every node URI parse
// as invalid and let the validation tests pass vacuously.
class URLStub extends URL {}
URLStub.createObjectURL = () => "blob:stub";
URLStub.revokeObjectURL = () => {};

function loadView() {
    const src = fs.readFileSync(VIEW_PATH, "utf8");
    const document = stub.makeDocument();
    const { E, dom } = stub.makeE(document);

    const notifications = [];
    const ui = {
        addNotification: function (title, node, level) {
            notifications.push({ title, node, level });
        },
        showModal: function () {},
        hideModal: function () {},
    };
    const rpc = {
        declare: function () {
            return function () {
                return Promise.resolve({});
            };
        },
    };
    const view = {
        extend: function (props) {
            return Object.assign({ __isView: true }, props);
        },
    };
    const L = {
        resource: function (p) {
            return "/luci-static/resources/" + p;
        },
        Poll: { add: function () {}, remove: function () {} },
    };
    const window = {
        confirm: function () {
            return true;
        },
        scrollTo: function () {},
        location: { reload: function () {} },
    };

    // eslint-disable-next-line no-new-func
    const factory = new Function(
        "view",
        "rpc",
        "ui",
        "E",
        "L",
        "dom",
        "document",
        "window",
        "URL",
        "setTimeout",
        "clearTimeout",
        "_",
        src,
    );

    const viewObj = factory(
        view,
        rpc,
        ui,
        E,
        L,
        dom,
        document,
        window,
        URLStub,
        (fn) => fn,
        () => {},
        (s) => s,
    );

    return { view: viewObj, document, E, ui, notifications, stub };
}

// Builds the three page-level inputs _collectConfig reads by id, plus the card
// container, then renders one card per subscription through the real _makeCard.
function mountSubscriptions(ctx, subs, opts) {
    opts = opts || {};
    const { view, document, E } = ctx;

    const rawSingbox = Object.assign({}, opts.rawSingbox || {});
    const page = E("div", {}, [
        E("input", { id: "vpnsub-log-level", value: opts.logLevel || "warn" }),
        E("input", {
            id: "vpnsub-test-url-setting",
            value: opts.testUrl || "https://www.gstatic.com/generate_204",
        }),
        // Seeded from the stored value the way render() does, so "cleared" only
        // means cleared when a test passes connectTimeout: "" explicitly.
        E("input", {
            id: "vpnsub-connect-timeout",
            value:
                opts.connectTimeout !== undefined
                    ? opts.connectTimeout
                    : rawSingbox.connect_timeout || "",
        }),
    ]);
    document.body.appendChild(page);

    view._widgets = {};
    view._subIdx = subs.length;
    view._rawSingbox = rawSingbox;
    view._singboxTemplate = opts.template || "";
    view._updateDomainVisibility = function () {};

    const cards = subs.map((sub, i) => {
        const card = view._makeCard(sub, i, opts.proxies || null, opts.tagNames || null);
        page.appendChild(card);
        return card;
    });

    return { page, cards };
}

module.exports = { loadView, mountSubscriptions, VIEW_PATH };
