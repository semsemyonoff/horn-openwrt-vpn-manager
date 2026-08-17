#!/bin/sh
# Drives the real check_sub_sources / fail_json / check_with_core from the rpcd
# backend, by sourcing the script with $1 unset so its `case` dispatcher falls
# through without running a method.
#
# Run with dash (closest available shell to OpenWrt ash), not the host sh:
# macOS sh is bash 3.2 and mis-parses a pre-existing `case`-inside-$().
#
#   dash horn-vpn-manager-luci/tests/rpcd-checks.test.sh

set -u

SCRIPT_DIR=$(dirname "$0")
RPCD="${SCRIPT_DIR}/../root/usr/libexec/rpcd/horn-vpn-manager"

command -v jq >/dev/null 2>&1 || { echo "SKIP: jq not installed"; exit 0; }

fails=0
checks=0

# Sourcing with an argument that matches no branch: the trailing
# `case "$1" in list|call)` falls through, so only the function definitions and
# the variable assignments take effect.
set -- __no_such_method__
# shellcheck disable=SC1090
. "$RPCD"
set --

# expect_sources <want:ok|bad> <label> <config json>
expect_sources() {
    want="$1"; label="$2"; cfg="$3"
    checks=$((checks + 1))
    if msg=$(check_sub_sources "$cfg"); then
        got=ok
    else
        got=bad
    fi
    if [ "$got" != "$want" ]; then
        echo "FAIL: $label: want $want, got $got${msg:+ ($msg)}"
        fails=$((fails + 1))
    fi
}

echo "── check_sub_sources ──"

expect_sources ok "url only" \
    '{"subscriptions":{"a":{"name":"A","url":"https://a.invalid/s"}}}'
expect_sources ok "inline nodes only" \
    '{"subscriptions":{"a":{"name":"A","nodes":["vless://u@h:443"]}}}'
expect_sources ok "disabled with no source" \
    '{"subscriptions":{"a":{"name":"A","enabled":false}}}'
expect_sources ok "no subscriptions at all" '{"subscriptions":{}}'
expect_sources ok "subscriptions key absent" '{}'
# JSON reaches jq through `printf '%s\n'`, never `echo`: dash's builtin echo
# expands backslash escapes, so `echo` would turn the \\ and \t below back into
# a bare backslash and a raw control character and jq would reject the payload.
expect_sources ok "name carrying backslash and tab escapes" \
    '{"subscriptions":{"a":{"name":"home\\vps\tnode","url":"https://a.invalid/s"}}}'

expect_sources bad "url and nodes together" \
    '{"subscriptions":{"a":{"name":"A","url":"https://a.invalid/s","nodes":["vless://u@h:443"]}}}'
expect_sources bad "enabled with no source" \
    '{"subscriptions":{"a":{"name":"A"}}}'
expect_sources bad "enabled with an empty url" \
    '{"subscriptions":{"a":{"name":"A","url":""}}}'
expect_sources bad "missing name" \
    '{"subscriptions":{"a":{"url":"https://a.invalid/s"}}}'
expect_sources bad "empty name" \
    '{"subscriptions":{"a":{"name":"","url":"https://a.invalid/s"}}}'
expect_sources bad "non-string name" \
    '{"subscriptions":{"a":{"name":42,"url":"https://a.invalid/s"}}}'
expect_sources bad "non-string url" \
    '{"subscriptions":{"a":{"name":"A","url":42}}}'
expect_sources bad "nodes not an array" \
    '{"subscriptions":{"a":{"name":"A","nodes":"vless://u@h:443"}}}'
expect_sources bad "empty string node" \
    '{"subscriptions":{"a":{"name":"A","nodes":[""]}}}'
expect_sources bad "non-string node" \
    '{"subscriptions":{"a":{"name":"A","nodes":[42]}}}'

# The reason every check compares against "${bad:-1}": on these payloads jq
# aborts and prints nothing, and an unguarded [ "$bad" -gt 0 ] would error out
# and let the payload through.
expect_sources bad "subscription is a string, not an object" \
    '{"subscriptions":{"a":"oops"}}'
expect_sources bad "subscriptions is an array" \
    '{"subscriptions":[{"name":"A","url":"https://a.invalid/s"}]}'
expect_sources bad "subscriptions is a string" '{"subscriptions":"oops"}'
expect_sources bad "not JSON at all" 'not json'

echo "── fail_json ──"

# Core errors quote subscription ids verbatim, so the reply must stay valid JSON
# no matter what the id contained.
for msg in \
    'plain message' \
    'subscription "a\"b" is invalid' \
    'tab	and newline
inside' \
    'unicode ✓ and backslash \'
do
    checks=$((checks + 1))
    out=$(fail_json "$msg")
    if ! printf '%s' "$out" | jq -e . >/dev/null 2>&1; then
        echo "FAIL: fail_json produced invalid JSON for [$msg]: $out"
        fails=$((fails + 1))
        continue
    fi
    got=$(printf '%s' "$out" | jq -r '.error')
    if [ "$got" != "$msg" ]; then
        echo "FAIL: fail_json round trip: want [$msg], got [$got]"
        fails=$((fails + 1))
    fi
done

echo "── check_with_core ──"

# An unreachable core must accept, so a partially installed system can save.
# Only vpn-manager is hidden: mktemp and awk stay on PATH, so this isolates the
# `command -v` branch instead of passing for whichever escape fires first.
EMPTY_BIN=$(mktemp -d)
checks=$((checks + 1))
if (PATH="${EMPTY_BIN}:/usr/bin:/bin"; check_with_core '{"subscriptions":{}}') >/dev/null 2>&1; then
    :
else
    echo "FAIL: check_with_core must accept when vpn-manager is not installed"
    fails=$((fails + 1))
fi

# expect_core <want:ok|bad> <label> <stub body> <want message, empty when ok>
# Installs a stub `vpn-manager` at the head of PATH so the rejection path — the
# one that actually enforces "delegate schema validation to the core" — is driven
# rather than assumed.
STUB_BIN=$(mktemp -d)
expect_core() {
    want="$1"; label="$2"; body="$3"; want_msg="$4"
    checks=$((checks + 1))
    printf '#!/bin/sh\n%s\n' "$body" > "${STUB_BIN}/vpn-manager"
    chmod +x "${STUB_BIN}/vpn-manager"
    if msg=$(PATH="${STUB_BIN}:${PATH}" check_with_core '{"subscriptions":{}}'); then
        got=ok
    else
        got=bad
    fi
    if [ "$got" != "$want" ]; then
        echo "FAIL: $label: want $want, got $got${msg:+ ($msg)}"
        fails=$((fails + 1))
        return
    fi
    if [ "$msg" != "$want_msg" ]; then
        echo "FAIL: $label: want message [$want_msg], got [$msg]"
        fails=$((fails + 1))
    fi
}

expect_core ok "core accepts the candidate" 'exit 0' ''
# The core prints its failure as a trailing "error: <msg>" line on stderr; that
# text must reach the user verbatim, ids and all.
expect_core bad "core rejection is relayed verbatim" \
    'echo "checking config" >&2; echo "error: subscription \"a\" has neither url nor nodes" >&2; exit 1' \
    'subscription "a" has neither url nor nodes'
expect_core bad "rejection with no error: line falls back to a generic message" \
    'echo "segfault" >&2; exit 1' \
    'config rejected by vpn-manager check'
# stdout is discarded, so a core that prints its error there still rejects.
expect_core bad "rejection printing only on stdout still rejects" \
    'echo "error: bad config"; exit 1' \
    'config rejected by vpn-manager check'

rm -rf "$STUB_BIN" "$EMPTY_BIN"

echo "── call dispatcher ──"

# The checks above drive the functions directly, which says nothing about
# set_config/set_full_config actually calling them before writing. These run the
# real `call` dispatcher against a temporary CONF_DIR, so dropping either
# validation call — or writing the file before it — fails here.

DISPATCH_BIN=$(mktemp -d)
# dash, not the host sh, for the same reason the file header gives: macOS sh is
# bash 3.2 and mis-parses a pre-existing case-inside-$().
DISPATCH_SH=$(command -v dash 2>/dev/null || echo sh)

# expect_call <want:written|rejected> <label> <method> <payload> <core stub body> <want reply substring>
expect_call() {
    want="$1"; label="$2"; method="$3"; payload="$4"; body="$5"; want_reply="$6"
    checks=$((checks + 1))

    conf=$(mktemp -d)
    printf '#!/bin/sh\n%s\n' "$body" > "${DISPATCH_BIN}/vpn-manager"
    chmod +x "${DISPATCH_BIN}/vpn-manager"

    reply=$(printf '%s\n' "$payload" | \
        HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
        "$DISPATCH_SH" "$RPCD" call "$method" 2>/dev/null)

    if [ -f "${conf}/config.json" ]; then
        got=written
    else
        got=rejected
    fi
    # config.json holds provider URLs and inline node URIs, so a save must not
    # publish it to every local user. `ls`, not `stat`, whose flags differ
    # between GNU and BSD.
    if [ "$got" = written ]; then
        checks=$((checks + 1))
        mode=$(ls -l "${conf}/config.json" | cut -c1-10)
        if [ "$mode" != "-rw-------" ]; then
            echo "FAIL: $label: config.json mode is $mode, want -rw-------"
            fails=$((fails + 1))
        fi
    fi
    if [ "$got" != "$want" ]; then
        echo "FAIL: $label: want $want, got $got (reply: $reply)"
        fails=$((fails + 1))
        rm -rf "$conf"
        return
    fi
    case "$reply" in
        *"$want_reply"*) ;;
        *)
            echo "FAIL: $label: reply [$reply] does not contain [$want_reply]"
            fails=$((fails + 1))
            ;;
    esac
    rm -rf "$conf"
}

VALID_SUB='{"main":{"name":"Main","default":true,"url":"https://a.invalid/s"}}'
BOTH_SOURCES='{"main":{"name":"Main","default":true,"url":"https://a.invalid/s","nodes":["vless://u@h:443"]}}'

for method in set_config set_full_config; do
    expect_call written "$method: accepted config is written" "$method" \
        "{\"config\":{\"subscriptions\":${VALID_SUB}}}" 'exit 0' '"result":"ok"'

    # check_with_core rejects → nothing may reach disk, and the core's own
    # message has to survive to the reply.
    expect_call rejected "$method: core rejection blocks the write" "$method" \
        "{\"config\":{\"subscriptions\":${VALID_SUB}}}" \
        'echo "error: subscription \"main\" has an invalid node at position 1: missing port in VLESS URI" >&2; exit 1' \
        'has an invalid node at position 1'

    # check_sub_sources rejects → the core is never even consulted, so a stub
    # that would have accepted must not rescue the payload.
    expect_call rejected "$method: structural rejection blocks the write" "$method" \
        "{\"config\":{\"subscriptions\":${BOTH_SOURCES}}}" 'exit 0' \
        'either a url or inline nodes'
done

echo "── credential file modes ──"

# expect_mode <label> <path relative to CONF_DIR>
expect_mode() {
    label="$1"; rel="$2"
    checks=$((checks + 1))
    mode=$(ls -l "${conf}/${rel}" 2>/dev/null | cut -c1-10)
    if [ "$mode" != "-rw-------" ]; then
        echo "FAIL: $label: ${rel} mode is ${mode:-missing}, want -rw-------"
        fails=$((fails + 1))
    fi
}

# The template can hold a hand-written outbound with its password, and `mv`
# replaces the destination together with its mode — so a save over a file that
# was already 0600 must not hand it back as 0644 either.
printf '#!/bin/sh\nexit 0\n' > "${DISPATCH_BIN}/vpn-manager"
chmod +x "${DISPATCH_BIN}/vpn-manager"

conf=$(mktemp -d)
printf '{}\n' > "${conf}/config.json"
chmod 644 "${conf}/config.json"
printf '%s\n' "{\"config\":{\"subscriptions\":${VALID_SUB}},\"template_contents\":\"{\\\"log\\\":{}}\"}" | \
    HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
    "$DISPATCH_SH" "$RPCD" call set_full_config >/dev/null 2>&1
expect_mode "set_full_config replaces a 0644 config privately" config.json
expect_mode "set_full_config writes the template privately" sing-box.template.json
rm -rf "$conf"

conf=$(mktemp -d)
printf '{"singbox":{}}\n' > "${conf}/config.json"
printf '%s\n' '{"template":"{\"log\":{}}"}' | \
    HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
    "$DISPATCH_SH" "$RPCD" call set_template >/dev/null 2>&1
expect_mode "set_template writes the template privately" sing-box.template.json
expect_mode "set_template rewrites the config privately" config.json
rm -rf "$conf"

conf=$(mktemp -d)
printf '{"singbox":{"template":"/etc/horn-vpn-manager/sing-box.template.json"}}\n' > "${conf}/config.json"
chmod 644 "${conf}/config.json"
HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
    "$DISPATCH_SH" "$RPCD" call reset_template >/dev/null 2>&1
expect_mode "reset_template rewrites the config privately" config.json
rm -rf "$conf"

conf=$(mktemp -d)
printf '{"routing":{}}\n' > "${conf}/config.json"
printf '%s\n' '{"config":{"dnsmasq_url":"https://a.invalid/d"}}' | \
    HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
    "$DISPATCH_SH" "$RPCD" call set_domains_config >/dev/null 2>&1
expect_mode "set_domains_config writes the config privately" config.json
rm -rf "$conf"

echo "── failed writes are reported ──"

# A write that fails — read-only filesystem, full disk, a leftover directory in
# the temp path — must not leave the handler touching the update flags and
# replying "ok": LuCI would show a saved config the router never received.
#
# The failure is injected by pre-creating the `.tmp` path write_private redirects
# into as a *directory*, so `cat >` fails and `mv` never runs. Works as root too,
# unlike revoking write permission on the directory.
#
# expect_write_failure <label> <method> <payload> <blocked file relative to CONF_DIR>
expect_write_failure() {
    label="$1"; method="$2"; payload="$3"; blocked="$4"
    checks=$((checks + 1))

    conf=$(mktemp -d)
    printf '{"singbox":{},"routing":{}}\n' > "${conf}/config.json"
    before=$(cat "${conf}/config.json")
    mkdir -p "${conf}/${blocked}.tmp"

    reply=$(printf '%s\n' "$payload" | \
        HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
        "$DISPATCH_SH" "$RPCD" call "$method" 2>/dev/null)

    case "$reply" in
        *'"error"'*) ;;
        *)
            echo "FAIL: $label: want an error reply, got [$reply]"
            fails=$((fails + 1))
            ;;
    esac

    checks=$((checks + 1))
    if [ "$(cat "${conf}/${blocked}" 2>/dev/null)" != "$before" ]; then
        echo "FAIL: $label: ${blocked} changed despite the failed write"
        fails=$((fails + 1))
    fi

    checks=$((checks + 1))
    if [ -f "${conf}/.needs-update-subs" ] || [ -f "${conf}/.needs-update-routing" ]; then
        echo "FAIL: $label: an update flag was set despite the failed write"
        fails=$((fails + 1))
    fi

    rm -rf "$conf"
}

printf '#!/bin/sh\nexit 0\n' > "${DISPATCH_BIN}/vpn-manager"
chmod +x "${DISPATCH_BIN}/vpn-manager"

expect_write_failure "set_config reports a failed config write" set_config \
    "{\"config\":{\"subscriptions\":${VALID_SUB}}}" config.json
expect_write_failure "set_full_config reports a failed config write" set_full_config \
    "{\"config\":{\"subscriptions\":${VALID_SUB}}}" config.json
expect_write_failure "set_template reports a failed config write" set_template \
    '{"template":"{\"log\":{}}"}' config.json
expect_write_failure "reset_template reports a failed config write" reset_template \
    '' config.json
expect_write_failure "set_domains_config reports a failed config write" set_domains_config \
    '{"config":{"dnsmasq_url":"https://a.invalid/d"}}' config.json

echo "── a failed config write leaves the template alone ──"

# The template and config.json are two writes in one handler, and config.json is
# what points sing-box at the template. A config write that fails after the
# template was already replaced (or deleted) replies with an error while the
# router is already running a template nothing agreed to.
#
# expect_template_after <label> <method> <payload> <existing template, empty = none> <want contents, @absent = no file>
expect_template_after() {
    label="$1"; method="$2"; payload="$3"; setup="$4"; want="$5"
    checks=$((checks + 1))

    conf=$(mktemp -d)
    printf '{"singbox":{"template":"%s/sing-box.template.json"},"routing":{}}\n' "$conf" > "${conf}/config.json"
    [ -n "$setup" ] && printf '%s' "$setup" > "${conf}/sing-box.template.json"
    mkdir -p "${conf}/config.json.tmp"

    reply=$(printf '%s\n' "$payload" | \
        HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
        "$DISPATCH_SH" "$RPCD" call "$method" 2>/dev/null)

    case "$reply" in
        *'"error"'*) ;;
        *)
            echo "FAIL: $label: want an error reply, got [$reply]"
            fails=$((fails + 1))
            ;;
    esac

    checks=$((checks + 1))
    if [ "$want" = "@absent" ]; then
        if [ -f "${conf}/sing-box.template.json" ]; then
            echo "FAIL: $label: template was created despite the failed config write"
            fails=$((fails + 1))
        fi
    else
        got=$(cat "${conf}/sing-box.template.json" 2>/dev/null)
        if [ "$got" != "$want" ]; then
            echo "FAIL: $label: template is [${got:-missing}], want [$want]"
            fails=$((fails + 1))
        fi
    fi

    # The rollback moves the snapshot back over the target, so nothing may be
    # left lying next to it — those copies carry the same credentials.
    checks=$((checks + 1))
    if ls "${conf}"/*.bak.* >/dev/null 2>&1; then
        echo "FAIL: $label: a snapshot file was left behind"
        fails=$((fails + 1))
    fi

    rm -rf "$conf"
}

expect_template_after "set_template rolls the template back" set_template \
    '{"template":"{\"log\":{}}"}' '{"old":true}' '{"old":true}'
expect_template_after "set_template removes a template it created" set_template \
    '{"template":"{\"log\":{}}"}' '' '@absent'
expect_template_after "set_full_config rolls the template back" set_full_config \
    "{\"config\":{\"subscriptions\":${VALID_SUB}},\"template_contents\":\"{\\\"log\\\":{}}\"}" \
    '{"old":true}' '{"old":true}'
expect_template_after "set_full_config removes a template it created" set_full_config \
    "{\"config\":{\"subscriptions\":${VALID_SUB}},\"template_contents\":\"{\\\"log\\\":{}}\"}" \
    '' '@absent'
expect_template_after "reset_template keeps the template" reset_template \
    '' '{"old":true}' '{"old":true}'

echo "── a successful save leaves no snapshot behind ──"

# The rollback snapshot only exists for the window between the two writes. On the
# success path it must go: it is a byte copy of the template, and `cp -p` gives it
# the source's mode, so a snapshot of a legacy 0644 template outlives the very
# save that hardened the original.
#
# expect_no_snapshot <label> <method> <payload>
expect_no_snapshot() {
    label="$1"; method="$2"; payload="$3"
    checks=$((checks + 1))

    conf=$(mktemp -d)
    printf '{"singbox":{"template":"%s/sing-box.template.json"},"routing":{}}\n' "$conf" > "${conf}/config.json"
    printf '%s' '{"old":true}' > "${conf}/sing-box.template.json"
    chmod 644 "${conf}/sing-box.template.json"

    reply=$(printf '%s\n' "$payload" | \
        HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
        "$DISPATCH_SH" "$RPCD" call "$method" 2>/dev/null)

    case "$reply" in
        *'"result":"ok"'*) ;;
        *)
            echo "FAIL: $label: want an ok reply, got [$reply]"
            fails=$((fails + 1))
            ;;
    esac

    checks=$((checks + 1))
    if ls "${conf}"/*.bak.* >/dev/null 2>&1; then
        echo "FAIL: $label: a snapshot file was left behind: $(ls "${conf}"/*.bak.*)"
        fails=$((fails + 1))
    fi

    rm -rf "$conf"
}

expect_no_snapshot "set_template cleans up its snapshot" set_template \
    '{"template":"{\"log\":{}}"}'
expect_no_snapshot "set_full_config cleans up its snapshot" set_full_config \
    "{\"config\":{\"subscriptions\":${VALID_SUB}},\"template_contents\":\"{\\\"log\\\":{}}\"}"

echo "── malformed config.json on disk ──"

# jq prints nothing when it cannot parse the file it was handed, and writing that
# empty output through replaces config.json with a blank document while still
# replying ok.
conf=$(mktemp -d)
printf 'not json\n' > "${conf}/config.json"
reply=$(printf '%s\n' '{"template":"{\"log\":{}}"}' | \
    HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
    "$DISPATCH_SH" "$RPCD" call set_template 2>/dev/null)

checks=$((checks + 1))
case "$reply" in
    *'"error"'*) ;;
    *)
        echo "FAIL: set_template over a malformed config.json: want an error reply, got [$reply]"
        fails=$((fails + 1))
        ;;
esac

checks=$((checks + 1))
if [ "$(cat "${conf}/config.json")" != "not json" ]; then
    echo "FAIL: set_template over a malformed config.json: the config was overwritten"
    fails=$((fails + 1))
fi

checks=$((checks + 1))
if [ -f "${conf}/sing-box.template.json" ]; then
    echo "FAIL: set_template over a malformed config.json: the template was written anyway"
    fails=$((fails + 1))
fi

checks=$((checks + 1))
if [ -f "${conf}/.needs-update-subs" ]; then
    echo "FAIL: set_template over a malformed config.json: an update flag was set"
    fails=$((fails + 1))
fi
rm -rf "$conf"

# ── the unsync flag survives a failed run ──────────────────────────────────────
#
# run_script backgrounds the core and used to clear .needs-update-subs no matter
# how it exited, so a run that failed — a bad config, or a refusal because
# another run holds the lock — made the sync badge claim the router was up to
# date with a config it never applied. The stderr of that run has to reach the
# log too, or the Run tab shows a log that simply stops.
echo "── a failed run keeps the unsync flag ──"

# wait_for_run <conf dir>: run_script writes the child pid to /tmp/...; poll it.
wait_for_run() {
    i=0
    while [ "$i" -lt 100 ]; do
        pid=$(cat /tmp/horn-vpn-manager.pid 2>/dev/null)
        [ -n "$pid" ] && kill -0 "$pid" 2>/dev/null || return 0
        i=$((i + 1))
        sleep 0.1
    done
    return 1
}

for outcome in fail ok; do
    checks=$((checks + 1))
    conf=$(mktemp -d)
    : > "${conf}/.needs-update-subs"
    if [ "$outcome" = fail ]; then
        printf '#!/bin/sh\necho "error: another vpn-manager run is in progress" >&2\nexit 1\n' \
            > "${DISPATCH_BIN}/vpn-manager"
    else
        printf '#!/bin/sh\nexit 0\n' > "${DISPATCH_BIN}/vpn-manager"
    fi
    chmod +x "${DISPATCH_BIN}/vpn-manager"
    : > /tmp/horn-vpn-manager-subscriptions.log

    printf '{}\n' | HORN_VPN_MANAGER_CONF_DIR="$conf" PATH="${DISPATCH_BIN}:${PATH}" \
        "$DISPATCH_SH" "$RPCD" call run_script >/dev/null 2>&1
    wait_for_run

    if [ "$outcome" = fail ]; then
        if [ ! -f "${conf}/.needs-update-subs" ]; then
            echo "FAIL: run_script cleared the unsync flag after a failed run"
            fails=$((fails + 1))
        fi
        checks=$((checks + 1))
        if ! grep -q "another vpn-manager run is in progress" /tmp/horn-vpn-manager-subscriptions.log; then
            echo "FAIL: run_script discarded the core's stderr instead of logging it"
            fails=$((fails + 1))
        fi
    else
        if [ -f "${conf}/.needs-update-subs" ]; then
            echo "FAIL: run_script kept the unsync flag after a successful run"
            fails=$((fails + 1))
        fi
    fi
    rm -rf "$conf"
done

rm -rf "$DISPATCH_BIN"

echo
if [ "$fails" -eq 0 ]; then
    echo "ok — $checks checks passed"
    exit 0
fi
echo "FAILED — $fails of $checks checks"
exit 1
