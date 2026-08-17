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

echo
if [ "$fails" -eq 0 ]; then
    echo "ok — $checks checks passed"
    exit 0
fi
echo "FAILED — $fails of $checks checks"
exit 1
