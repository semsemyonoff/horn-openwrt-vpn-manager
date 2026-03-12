#!/bin/sh

# ============================================================
# sing-box subscription update script
# Downloads VLESS URIs from subscription URLs, parses them,
# generates per-subscription outbound groups and route rules,
# and updates the sing-box config.
#
# Requires: curl, base64, awk, sed, grep, jqы
# ============================================================

SCRIPT_DIR=$(cd "$(dirname "$0")" && pwd)
SUBS_CONF="${SCRIPT_DIR}/subs.json"
CONFIG_TEMPLATE="${SCRIPT_DIR}/config.template.json"
CONFIG="${SCRIPT_DIR}/config.json"
TMPDIR="/tmp/sing-box-sub"
LOG="/tmp/sing-box-sub.log"
DRY_RUN=0
VERBOSE=0

# Parse arguments
for arg in "$@"; do
    case "$arg" in
        --dry-run|-n) DRY_RUN=1 ;;
        -vvv) VERBOSE=3 ;;
        -vv)  VERBOSE=2 ;;
        -v)   VERBOSE=1 ;;
    esac
done

log() {
    if [ "$DRY_RUN" -eq 1 ]; then
        echo "[DRY-RUN] $1"
    else
        echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG"
        echo "$1"
    fi
}

# vlog LEVEL MESSAGE — log only if VERBOSE >= LEVEL
vlog() {
    [ "$VERBOSE" -ge "$1" ] && log "$2"
}

# Returns 0 (true) if server name matches an exclude pattern
# shellcheck disable=SC3043
is_excluded() {
    local name="$1"
    local name_lower
    name_lower=$(echo "$name" | tr '[:upper:]' '[:lower:]')

    while IFS= read -r pattern; do
        local pattern_lower
        pattern_lower=$(echo "$pattern" | tr '[:upper:]' '[:lower:]')
        case "$name_lower" in
            *"$pattern_lower"*) return 0 ;;
        esac
    done < "$TMPDIR/exclude.tmp"

    return 1
}

# Parse a VLESS URI and print a sing-box outbound JSON object
# Format: vless://uuid@server:port?security=reality&sni=...&fp=...&pbk=...&sid=...&flow=...#name
# shellcheck disable=SC3043
parse_uri() {
    local line="$1"
    local sub_name="$2"
    local idx="$3"
    # Optional 4th arg overrides the generated tag (used for the default outbound)
    local tag="${4:-${sub_name}-${idx}}"

    local uri="${line#vless://}"
    local uuid="${uri%%@*}"
    local rest="${uri#*@}"
    local server_port="${rest%%\?*}"
    local server="${server_port%%:*}"
    local port="${server_port##*:}"
    local params="${rest#*\?}"
    params="${params%%#*}"

    local sni pbk sid flow fp security
    sni=$(echo "$params" | tr '&' '\n' | grep "^sni=" | cut -d= -f2-)
    pbk=$(echo "$params" | tr '&' '\n' | grep "^pbk=" | cut -d= -f2-)
    sid=$(echo "$params" | tr '&' '\n' | grep "^sid=" | cut -d= -f2-)
    flow=$(echo "$params" | tr '&' '\n' | grep "^flow=" | cut -d= -f2-)
    fp=$(echo "$params" | tr '&' '\n' | grep "^fp=" | cut -d= -f2-)
    security=$(echo "$params" | tr '&' '\n' | grep "^security=" | cut -d= -f2-)

    local json="    {
      \"type\": \"vless\",
      \"tag\": \"${tag}\",
      \"server\": \"${server}\",
      \"server_port\": ${port},
      \"uuid\": \"${uuid}\""

    [ -n "$flow" ] && json="$json,
      \"flow\": \"${flow}\""

    json="$json,
      \"tls\": {
        \"enabled\": true,
        \"insecure\": false"

    [ -n "$sni" ] && json="$json,
        \"server_name\": \"${sni}\""

    [ -n "$fp" ] && json="$json,
        \"utls\": { \"enabled\": true, \"fingerprint\": \"${fp}\" }"

    if [ "$security" = "reality" ] && [ -n "$pbk" ]; then
        json="$json,
        \"reality\": { \"enabled\": true, \"public_key\": \"${pbk}\""
        [ -n "$sid" ] && json="$json, \"short_id\": \"${sid}\""
        json="$json }"
    fi

    json="$json
      }
    }"

    echo "$json"
}

mkdir -p "$TMPDIR"

# ---- Validate subscriptions config ----
if [ ! -f "$SUBS_CONF" ]; then
    log "ERROR: $SUBS_CONF not found"
    exit 1
fi

SUB_COUNT=$(jq '.subscriptions | length // 0' "$SUBS_CONF" 2>/dev/null || echo 0)
if [ "$SUB_COUNT" -eq 0 ]; then
    log "ERROR: No subscriptions defined in $SUBS_CONF"
    exit 1
fi
log "Found $SUB_COUNT subscription(s)"

DEF_COUNT=$(jq '[.subscriptions[] | select(.default == true)] | length' "$SUBS_CONF" 2>/dev/null || echo 0)
if [ "$DEF_COUNT" -eq 0 ]; then
    log "ERROR: No default subscription defined (set \"default\": true on one subscription)"
    exit 1
fi
if [ "$DEF_COUNT" -gt 1 ]; then
    log "ERROR: Multiple default subscriptions defined (only one allowed)"
    exit 1
fi

# ---- Process each subscription ----
def_name=$(jq -r '.subscriptions[] | select(.default == true) | .name' "$SUBS_CONF")
VLESS_OUTBOUNDS=""
URLTEST_OUTBOUNDS=""
ROUTE_RULES=""
TOTAL_SERVERS=0
: > "$TMPDIR/default.tmp"
: > "$TMPDIR/tag-names.tsv"

i=0
while [ "$i" -lt "$SUB_COUNT" ]; do
    sub_name=$(jq -r ".subscriptions[$i].name" "$SUBS_CONF")
    sub_url=$(jq -r ".subscriptions[$i].url" "$SUBS_CONF")
    is_default=$(jq -r ".subscriptions[$i].default // false" "$SUBS_CONF")
    rawfile="$TMPDIR/${sub_name}.raw"
    outfile="$TMPDIR/${sub_name}.txt"

    jq -r ".subscriptions[$i].exclude[]? | ascii_downcase" "$SUBS_CONF" > "$TMPDIR/exclude.tmp"

    vlog 3 "  [dbg] rawfile=${rawfile} outfile=${outfile}"
    log "Downloading ${sub_name}..."
    http_code=$(curl -sL -m 15 -o "$rawfile" -w "%{http_code}" "$sub_url" 2>/dev/null)

    if [ "$http_code" = "000" ] || [ -z "$http_code" ]; then
        log "  ${sub_name}: connection error, skipping"
        i=$((i + 1))
        continue
    fi
    if [ "$http_code" != "200" ]; then
        log "  ${sub_name}: HTTP ${http_code}, skipping"
        i=$((i + 1))
        continue
    fi
    vlog 3 "  [dbg] ${sub_name}: HTTP ${http_code}, $(wc -c < "$rawfile" | tr -d ' ') bytes"

    # Auto-detect encoding: raw VLESS or base64
    if grep -q "^vless://" "$rawfile" 2>/dev/null; then
        cp "$rawfile" "$outfile"
        vlog 1 "  ${sub_name}: raw format"
    elif base64 -d < "$rawfile" > "$outfile" 2>/dev/null && grep -q "^vless://" "$outfile"; then
        vlog 1 "  ${sub_name}: base64 format"
    else
        log "  ${sub_name}: no VLESS URIs found, skipping"
        i=$((i + 1))
        continue
    fi

    lines=$(grep -c "^vless://" "$outfile" 2>/dev/null || echo 0)
    log "  ${sub_name}: $lines server(s) found"

    sub_tags=""
    idx=0
    skipped=0
    default_written=0

    while IFS= read -r line || [ -n "$line" ]; do
        echo "$line" | grep -q "^vless://" || continue

        # Extract server name (after #) for filtering
        raw_name="${line##*#}"
        # URL-decode common cases
        server_name=$(echo "$raw_name" | sed 's/%20/ /g; s/%23/#/g; s/%2F/\//g; s/+/ /g')

        if is_excluded "$server_name"; then
            skipped=$((skipped + 1))
            vlog 1 "  SKIP: $server_name (matched filter)"
            continue
        fi

        # First URI of the default subscription becomes the default outbound
        if [ "$is_default" = "true" ] && [ "$default_written" -eq 0 ]; then
            printf '%s,\n' "$(parse_uri "$line" "$sub_name" "" "$sub_name")" > "$TMPDIR/default.tmp"
            default_written=1
            log "  default outbound: OK (tag: ${sub_name})"
        fi

        idx=$((idx + 1))
        tag="${sub_name}-${idx}"
        outbound=$(parse_uri "$line" "$sub_name" "$idx")
        printf '%s\t%s\n' "$tag" "$server_name" >> "$TMPDIR/tag-names.tsv"

        vlog 2 "  KEEP: ${tag} (${server_name})"

        if [ "$VERBOSE" -ge 3 ]; then
            _rest="${line#vless://*@}"
            _sp="${_rest%%\?*}"
            _params="${_rest#*\?}"; _params="${_params%%#*}"
            _sni=$(echo "$_params" | tr '&' '\n' | grep "^sni=" | cut -d= -f2-)
            _sec=$(echo "$_params" | tr '&' '\n' | grep "^security=" | cut -d= -f2-)
            _fp=$(echo "$_params" | tr '&' '\n' | grep "^fp=" | cut -d= -f2-)
            _flow=$(echo "$_params" | tr '&' '\n' | grep "^flow=" | cut -d= -f2-)
            log "    [dbg] addr=${_sp} security=${_sec} sni=${_sni} fp=${_fp} flow=${_flow}"
        fi

        [ -n "$VLESS_OUTBOUNDS" ] && VLESS_OUTBOUNDS="$VLESS_OUTBOUNDS,
"
        VLESS_OUTBOUNDS="${VLESS_OUTBOUNDS}${outbound}"

        [ -n "$sub_tags" ] && sub_tags="$sub_tags, "
        sub_tags="${sub_tags}\"${tag}\""

        TOTAL_SERVERS=$((TOTAL_SERVERS + 1))
    done < "$outfile"

    log "  ${sub_name}: kept $idx, skipped $skipped"

    # urltest + route rules for subscriptions with domains or ip defined
    domains=$(jq -c ".subscriptions[$i].domains // empty" "$SUBS_CONF")
    ip_cidrs=$(jq -c ".subscriptions[$i].ip // empty" "$SUBS_CONF")
    if [ -n "$sub_tags" ] && { [ -n "$domains" ] || [ -n "$ip_cidrs" ]; }; then
        urltest="    {
      \"type\": \"urltest\",
      \"tag\": \"${sub_name}-best\",
      \"outbounds\": [${sub_tags}],
      \"url\": \"${TEST_URL}\",
      \"interval\": \"5m\",
      \"tolerance\": 100
    }"
        [ -n "$URLTEST_OUTBOUNDS" ] && URLTEST_OUTBOUNDS="$URLTEST_OUTBOUNDS,
"
        URLTEST_OUTBOUNDS="${URLTEST_OUTBOUNDS}${urltest}"

        if [ -n "$domains" ]; then
            rule="      {
        \"domain_suffix\": ${domains},
        \"outbound\": \"${sub_name}-best\"
      }"
            [ -n "$ROUTE_RULES" ] && ROUTE_RULES="$ROUTE_RULES,
"
            ROUTE_RULES="${ROUTE_RULES}${rule}"
        fi

        if [ -n "$ip_cidrs" ]; then
            ip_rule="      {
        \"ip_cidr\": ${ip_cidrs},
        \"outbound\": \"${sub_name}-best\"
      }"
            [ -n "$ROUTE_RULES" ] && ROUTE_RULES="$ROUTE_RULES,
"
            ROUTE_RULES="${ROUTE_RULES}${ip_rule}"
        fi
    fi

    i=$((i + 1))
done

if [ ! -s "$TMPDIR/default.tmp" ]; then
    log "ERROR: default subscription (${def_name}) did not produce a valid outbound"
    exit 1
fi

if [ "$SUB_COUNT" -gt 0 ] && [ "$TOTAL_SERVERS" -eq 0 ]; then
    log "ERROR: No valid servers parsed from any subscription"
    exit 1
fi

log "Total servers: $TOTAL_SERVERS"

LOG_LEVEL=$(jq -r '.log_level // "warn"' "$SUBS_CONF")
TEST_URL=$(jq -r '.test_url // "https://www.gstatic.com/generate_204"' "$SUBS_CONF")

# ---- Build final config ----
# Write non-empty blocks with trailing comma (more items follow in the outbounds array).
# Empty files → awk finds nothing to print → placeholder line is silently dropped.
if [ -n "$VLESS_OUTBOUNDS" ]; then
    printf '%s,\n' "$VLESS_OUTBOUNDS"   > "$TMPDIR/vless.tmp"
else
    : > "$TMPDIR/vless.tmp"
fi
if [ -n "$URLTEST_OUTBOUNDS" ]; then
    printf '%s,\n' "$URLTEST_OUTBOUNDS" > "$TMPDIR/urltest.tmp"
else
    : > "$TMPDIR/urltest.tmp"
fi
printf '%s\n' "$ROUTE_RULES" > "$TMPDIR/rules.tmp"

awk \
    -v default_file="$TMPDIR/default.tmp" \
    -v default_tag="$def_name" \
    -v log_level="$LOG_LEVEL" \
    -v vless_file="$TMPDIR/vless.tmp" \
    -v urltest_file="$TMPDIR/urltest.tmp" \
    -v rules_file="$TMPDIR/rules.tmp" \
'/__LOG_LEVEL__/ {
    sub(/__LOG_LEVEL__/, log_level)
    print
    next
}
/"__DEFAULT_OUTBOUND__"/ {
    while ((getline line < default_file) > 0) print line
    next
}
/__DEFAULT_TAG__/ {
    sub(/__DEFAULT_TAG__/, default_tag)
    print
    next
}
/"__VLESS_OUTBOUNDS__"/ {
    while ((getline line < vless_file) > 0) print line
    next
}
/"__URLTEST_OUTBOUNDS__"/ {
    while ((getline line < urltest_file) > 0) print line
    next
}
/"__ROUTE_RULES__"/ {
    while ((getline line < rules_file) > 0) print line
    next
}
{ print }
' "$CONFIG_TEMPLATE" > "${CONFIG}.new"

# ---- Validate and apply ----
if [ "$DRY_RUN" -eq 1 ]; then
    log "Dry-run: generated config preview (${CONFIG}.new):"
    cat "${CONFIG}.new"
    log "Dry-run: skipping sing-box check, apply, and restart"
    rm -f "${CONFIG}.new"
    exit 0
fi

if sing-box check -c "${CONFIG}.new" 2>&1; then
    [ -f "$CONFIG" ] && cp "$CONFIG" "${CONFIG}.bak"
    mv "${CONFIG}.new" "$CONFIG"
    # Save tag→full_name mapping for LuCI plugin display
    if [ -s "$TMPDIR/tag-names.tsv" ]; then
        jq -Rn '[inputs | split("\t") | {(.[0]): .[1]}] | add // {}' \
            "$TMPDIR/tag-names.tsv" > "$(dirname "$CONFIG")/subs-tags.json"
    fi
    log "Config OK, restarting sing-box..."
    service sing-box restart
    sleep 2
    if pidof sing-box > /dev/null; then
        log "sing-box restarted successfully"
    else
        log "WARNING: sing-box may not have started"
    fi
else
    log "ERROR: Invalid config, keeping old one"
    rm -f "${CONFIG}.new"
    exit 1
fi
