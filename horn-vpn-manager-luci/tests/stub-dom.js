"use strict";

// Minimal stand-in for the browser DOM and the handful of LuCI globals
// config.js closes over, so the real render/_makeCard/_collectConfig/_validate
// can be driven from Node with no dependencies.
//
// The point is fidelity on the parts the view actually relies on, not
// completeness. Two behaviours are deliberately faithful to the real thing
// because bugs have hidden behind them:
//
//   - dom.append assigns a *string* child via innerHTML, so a message built by
//     string concatenation is parsed as markup. innerHTML here therefore runs a
//     real (tiny) tag parser instead of storing the text, which is what lets a
//     test tell an escaped message from an injected element.
//   - dom.attr routes a function-valued attribute to addEventListener and
//     everything else to setAttribute; setAttribute("value"/"checked") also
//     updates the property, the way a freshly created input behaves.

// ── Nodes ────────────────────────────────────────────────────────────────────

class TextNode {
    constructor(text) {
        this.nodeType = 3;
        this.data = String(text);
        this.parentNode = null;
    }
    get textContent() {
        return this.data;
    }
    set textContent(v) {
        this.data = String(v);
    }
    get outerHTML() {
        return escapeHtml(this.data);
    }
}

function escapeHtml(s) {
    return String(s)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
}

class ClassList {
    constructor(el) {
        this._el = el;
    }
    _list() {
        var v = this._el.attributes["class"];
        return v ? String(v).split(/\s+/).filter(Boolean) : [];
    }
    _set(list) {
        this._el.attributes["class"] = list.join(" ");
    }
    contains(c) {
        return this._list().indexOf(c) !== -1;
    }
    add(c) {
        var l = this._list();
        if (l.indexOf(c) === -1) l.push(c);
        this._set(l);
    }
    remove(c) {
        this._set(
            this._list().filter(function (x) {
                return x !== c;
            }),
        );
    }
    toggle(c, on) {
        if (on === undefined) on = !this.contains(c);
        if (on) this.add(c);
        else this.remove(c);
        return on;
    }
}

class Element {
    constructor(tag) {
        this.nodeType = 1;
        this.tagName = String(tag).toUpperCase();
        this.childNodes = [];
        this.parentNode = null;
        this.attributes = Object.create(null);
        this.style = {};
        this.classList = new ClassList(this);
        this._listeners = Object.create(null);
        this.value = "";
        this.checked = false;
        this.disabled = false;
    }

    // ── attributes ──
    setAttribute(k, v) {
        this.attributes[k] = String(v);
        if (k === "id") registry.byId[String(v)] = this;
        if (k === "value") this.value = String(v);
        if (k === "checked") this.checked = v !== false && v !== "false";
    }
    getAttribute(k) {
        return k in this.attributes ? this.attributes[k] : null;
    }
    removeAttribute(k) {
        delete this.attributes[k];
    }
    get id() {
        return this.attributes["id"] || "";
    }
    set id(v) {
        this.setAttribute("id", v);
    }
    get className() {
        return this.attributes["class"] || "";
    }

    // ── tree ──
    appendChild(node) {
        if (node.parentNode) node.parentNode.removeChild(node);
        node.parentNode = this;
        this.childNodes.push(node);
        return node;
    }
    removeChild(node) {
        var i = this.childNodes.indexOf(node);
        if (i === -1) throw new Error("removeChild: not a child");
        this.childNodes.splice(i, 1);
        node.parentNode = null;
        return node;
    }
    insertBefore(node, ref) {
        if (node.parentNode) node.parentNode.removeChild(node);
        var i = ref ? this.childNodes.indexOf(ref) : -1;
        node.parentNode = this;
        if (i === -1) this.childNodes.push(node);
        else this.childNodes.splice(i, 0, node);
        return node;
    }
    get firstChild() {
        return this.childNodes[0] || null;
    }
    get lastChild() {
        return this.childNodes[this.childNodes.length - 1] || null;
    }
    get children() {
        return this.childNodes.filter(function (n) {
            return n.nodeType === 1;
        });
    }
    _siblingBy(offset) {
        if (!this.parentNode) return null;
        var sibs = this.parentNode.children;
        var i = sibs.indexOf(this);
        return i === -1 ? null : sibs[i + offset] || null;
    }
    get previousElementSibling() {
        return this._siblingBy(-1);
    }
    get nextElementSibling() {
        return this._siblingBy(1);
    }

    // ── content ──
    get textContent() {
        return this.childNodes
            .map(function (n) {
                return n.textContent;
            })
            .join("");
    }
    set textContent(v) {
        this.childNodes.forEach(function (n) {
            n.parentNode = null;
        });
        this.childNodes = [];
        if (v !== "" && v != null) this.appendChild(new TextNode(v));
    }
    // Faithful to the sink that matters: markup in the assigned string becomes
    // elements. Only the subset config.js could plausibly produce is parsed.
    set innerHTML(html) {
        this.childNodes.forEach(function (n) {
            n.parentNode = null;
        });
        this.childNodes = [];
        var self = this;
        parseHtml(String(html)).forEach(function (n) {
            self.appendChild(n);
        });
    }
    get innerHTML() {
        return this.childNodes
            .map(function (n) {
                return n.outerHTML;
            })
            .join("");
    }
    get outerHTML() {
        var attrs = Object.keys(this.attributes)
            .map(
                function (k) {
                    return " " + k + '="' + escapeHtml(this.attributes[k]) + '"';
                }.bind(this),
            )
            .join("");
        var tag = this.tagName.toLowerCase();
        return "<" + tag + attrs + ">" + this.innerHTML + "</" + tag + ">";
    }

    // ── events ──
    addEventListener(type, fn) {
        (this._listeners[type] = this._listeners[type] || []).push(fn);
    }
    dispatch(type) {
        var self = this;
        (this._listeners[type] || []).forEach(function (fn) {
            fn.call(self, { type: type, target: self });
        });
    }

    // ── queries ──
    querySelectorAll(sel) {
        var out = [];
        collectMatches(this, sel, out);
        return out;
    }
    querySelector(sel) {
        return this.querySelectorAll(sel)[0] || null;
    }

    // ── stubs the view calls but that have no meaning here ──
    scrollIntoView() {}
    focus() {}
    click() {
        this.dispatch("click");
    }
}

// ── tiny HTML parser, for the innerHTML sink only ────────────────────────────

function parseHtml(html) {
    var out = [];
    var stack = [];
    var re = /<(\/?)([a-zA-Z][\w-]*)((?:\s+[\w:-]+(?:\s*=\s*(?:"[^"]*"|'[^']*'|[^\s>]+))?)*)\s*(\/?)>/g;
    var pos = 0;
    var m;
    function push(node) {
        if (stack.length) stack[stack.length - 1].appendChild(node);
        else out.push(node);
    }
    while ((m = re.exec(html)) !== null) {
        if (m.index > pos) push(new TextNode(unescapeHtml(html.slice(pos, m.index))));
        pos = re.lastIndex;
        if (m[1]) {
            if (stack.length) stack.pop();
            continue;
        }
        var el = new Element(m[2]);
        var attrRe = /([\w:-]+)(?:\s*=\s*(?:"([^"]*)"|'([^']*)'|([^\s>]+)))?/g;
        var a;
        while ((a = attrRe.exec(m[3])) !== null) {
            var val = a[2] !== undefined ? a[2] : a[3] !== undefined ? a[3] : a[4];
            el.setAttribute(a[1], val === undefined ? "" : unescapeHtml(val));
        }
        push(el);
        var voidTag = /^(br|hr|img|input|meta|link)$/i.test(m[2]);
        if (!m[4] && !voidTag) stack.push(el);
    }
    if (pos < html.length) push(new TextNode(unescapeHtml(html.slice(pos))));
    return out;
}

function unescapeHtml(s) {
    return String(s)
        .replace(/&lt;/g, "<")
        .replace(/&gt;/g, ">")
        .replace(/&quot;/g, '"')
        .replace(/&amp;/g, "&");
}

// ── selector matching: ".class", "tag", and "<sel> <sel>" descendants ────────

function matchesSimple(el, sel) {
    if (sel.charAt(0) === ".") return el.classList.contains(sel.slice(1));
    if (sel.charAt(0) === "#") return el.id === sel.slice(1);
    return el.tagName === sel.toUpperCase();
}

function collectMatches(root, sel, out) {
    var parts = sel.trim().split(/\s+/);
    walk(root, function (el) {
        if (!matchesSimple(el, parts[parts.length - 1])) return;
        var node = el.parentNode;
        for (var i = parts.length - 2; i >= 0; i--) {
            while (node && !matchesSimple(node, parts[i])) node = node.parentNode;
            if (!node) return;
            node = node.parentNode;
        }
        out.push(el);
    });
}

function walk(node, fn) {
    node.childNodes.forEach(function (child) {
        if (child.nodeType !== 1) return;
        fn(child);
        walk(child, fn);
    });
}

// ── document / registry ──────────────────────────────────────────────────────

var registry = { byId: Object.create(null) };

function makeDocument() {
    var doc = new Element("html");
    doc.createElement = function (tag) {
        return new Element(tag);
    };
    doc.createTextNode = function (t) {
        return new TextNode(t);
    };
    doc.getElementById = function (id) {
        var el = registry.byId[id];
        // Detached nodes must read as absent: card removal is how the view
        // drops a subscription, and refreshChainPickers relies on the lookup
        // failing afterwards.
        for (var n = el; n; n = n.parentNode) if (n === doc) return el;
        return null;
    };
    doc.head = new Element("head");
    doc.body = new Element("body");
    doc.appendChild(doc.head);
    doc.appendChild(doc.body);
    return doc;
}

// ── LuCI E() / dom, mirroring dom.create + dom.attr + dom.append ─────────────

function makeE(document) {
    function isElem(o) {
        return o instanceof Element;
    }

    function attr(node, key) {
        if (!key) return;
        for (var k in key) {
            if (!Object.prototype.hasOwnProperty.call(key, k)) continue;
            if (key[k] == null) continue;
            if (typeof key[k] === "function") node.addEventListener(k, key[k]);
            else if (typeof key[k] === "object")
                node.setAttribute(k, JSON.stringify(key[k]));
            else node.setAttribute(k, key[k]);
        }
    }

    function append(node, children) {
        if (Array.isArray(children)) {
            children.forEach(function (c) {
                if (isElem(c) || c instanceof TextNode) node.appendChild(c);
                else if (c !== null && c !== undefined)
                    node.appendChild(new TextNode("" + c));
            });
            return node.lastChild;
        }
        if (typeof children === "function") return append(node, children(node));
        if (isElem(children) || children instanceof TextNode)
            return node.appendChild(children);
        if (children !== null && children !== undefined) {
            // The sink: a bare string child is assigned, not appended.
            node.innerHTML = "" + children;
            return node.lastChild;
        }
        return null;
    }

    function E(html, a, data) {
        if (!(a instanceof Object) || Array.isArray(a)) {
            data = a;
            a = null;
        }
        var elem = isElem(html) ? html : document.createElement(html);
        attr(elem, a);
        append(elem, data);
        return elem;
    }

    return { E: E, dom: { append: append, attr: attr, elem: isElem } };
}

module.exports = {
    Element: Element,
    TextNode: TextNode,
    makeDocument: makeDocument,
    makeE: makeE,
    escapeHtml: escapeHtml,
};
