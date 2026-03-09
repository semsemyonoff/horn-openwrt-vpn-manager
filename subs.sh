#!/bin/sh

# ============================================================
# sing-box subscription update script
# Downloads VLESS URIs from subscription URLs, parses them,
# generates per-subscription outbound groups and route rules,
# and updates the sing-box config.
#
# Requires: curl, base64, awk, sed, grep, jqы
# ============================================================

SUBS_CONF="/etc/sing-box/subs.json"
CONFIG_TEMPLATE="/etc/sing-box/config.template.json"
CONFIG="/etc/sing-box/config.json"
TMPDIR="/tmp/sing-box-sub"
LOG="/tmp/sing-box-sub.log"

log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG"
    echo "$1"
}

# Returns 0 (true) if server name matches an exclude pattern
is_excluded() {
    local name="$1"
    local name_lower=$(echo "$name" | tr 'A-Z' 'a-z')

    while IFS= read -r pattern; do
        local pattern_lower=$(echo "$pattern" | tr 'A-Z' 'a-z')
        case "$name_lower" in
            *"$pattern_lower"*) return 0 ;;
        esac
    done < "$TMPDIR/exclude.tmp"

    return 1
}

# Parse a VLESS URI and print a sing-box outbound JSON object
# Format: vless://uuid@server:port?security=reality&sni=...&fp=...&pbk=...&sid=...&flow=...#name
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

    local sni=$(echo "$params" | tr '&' '\n' | grep "^sni=" | cut -d= -f2-)
    local pbk=$(echo "$params" | tr '&' '\n' | grep "^pbk=" | cut -d= -f2-)
    local sid=$(echo "$params" | tr '&' '\n' | grep "^sid=" | cut -d= -f2-)
    local flow=$(echo "$params" | tr '&' '\n' | grep "^flow=" | cut -d= -f2-)
    local fp=$(echo "$params" | tr '&' '\n' | grep "^fp=" | cut -d= -f2-)
    local security=$(echo "$params" | tr '&' '\n' | grep "^security=" | cut -d= -f2-)

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
[ "$SUB_COUNT" -gt 0 ] && log "Found $SUB_COUNT subscription(s)" || log "No subscriptions defined, default only"

# ---- Fetch and parse default outbound ----
def_name=$(jq -r '.default.name' "$SUBS_CONF")
def_url=$(jq -r '.default.url' "$SUBS_CONF")

log "Downloading default outbound (${def_name})..."
def_line=$(curl -sL -m 15 "$def_url" | grep "^vless://")

if [ -z "$def_line" ]; then
    log "ERROR: default URL did not return a VLESS URI"
    exit 1
fi

# Parse using the name as tag directly (no index suffix)
printf '%s,\n' "$(parse_uri "$def_line" "$def_name" "" "$def_name")" > "$TMPDIR/default.tmp"
log "  default: OK (tag: ${def_name})"

# ---- Process each subscription ----
VLESS_OUTBOUNDS=""
URLTEST_OUTBOUNDS=""
ROUTE_RULES=""
TOTAL_SERVERS=0

i=0
while [ "$i" -lt "$SUB_COUNT" ]; do
    sub_name=$(jq -r ".subscriptions[$i].name" "$SUBS_CONF")
    sub_url=$(jq -r ".subscriptions[$i].url" "$SUBS_CONF")
    outfile="$TMPDIR/${sub_name}.txt"

    # Extract exclude patterns for this subscription
    jq -r ".subscriptions[$i].exclude[]? | ascii_downcase" "$SUBS_CONF" > "$TMPDIR/exclude.tmp"

    log "Downloading ${sub_name}..."
    if ! curl -sL -m 15 "$sub_url" | base64 -d > "$outfile" 2>/dev/null; then
        log "  ${sub_name}: download failed, skipping"
        i=$((i + 1))
        continue
    fi

    lines=$(grep -c "^vless://" "$outfile" 2>/dev/null || echo 0)
    log "  ${sub_name}: $lines servers found"

    # Parse VLESS URIs for this subscription
    sub_tags=""
    idx=0
    skipped=0

    while IFS= read -r line; do
        echo "$line" | grep -q "^vless://" || continue

        # Extract server name (after #) for filtering
        raw_name="${line##*#}"
        # URL-decode common cases
        server_name=$(echo "$raw_name" | sed 's/%20/ /g; s/%23/#/g; s/%2F/\//g; s/+/ /g')

        if is_excluded "$server_name"; then
            skipped=$((skipped + 1))
            log "  SKIP: $server_name (matched filter)"
            continue
        fi

        idx=$((idx + 1))
        tag="${sub_name}-${idx}"
        outbound=$(parse_uri "$line" "$sub_name" "$idx")

        [ -n "$VLESS_OUTBOUNDS" ] && VLESS_OUTBOUNDS="$VLESS_OUTBOUNDS,
"
        VLESS_OUTBOUNDS="${VLESS_OUTBOUNDS}${outbound}"

        [ -n "$sub_tags" ] && sub_tags="$sub_tags, "
        sub_tags="${sub_tags}\"${tag}\""

        TOTAL_SERVERS=$((TOTAL_SERVERS + 1))
    done < "$outfile"

    log "  ${sub_name}: kept $idx, skipped $skipped"

    if [ -n "$sub_tags" ]; then
        # urltest outbound — selects best server within this subscription
        urltest="    {
      \"type\": \"urltest\",
      \"tag\": \"${sub_name}-best\",
      \"outbounds\": [${sub_tags}],
      \"url\": \"https://www.gstatic.com/generate_204\",
      \"interval\": \"5m\",
      \"tolerance\": 100
    }"
        [ -n "$URLTEST_OUTBOUNDS" ] && URLTEST_OUTBOUNDS="$URLTEST_OUTBOUNDS,
"
        URLTEST_OUTBOUNDS="${URLTEST_OUTBOUNDS}${urltest}"

        # Route rule — domains for this subscription routed to its urltest group
        domain_arr=$(jq -c ".subscriptions[$i].domains" "$SUBS_CONF")
        rule="      {
        \"domain_suffix\": ${domain_arr},
        \"outbound\": \"${sub_name}-best\"
      }"
        [ -n "$ROUTE_RULES" ] && ROUTE_RULES="$ROUTE_RULES,
"
        ROUTE_RULES="${ROUTE_RULES}${rule}"
    fi

    i=$((i + 1))
done

if [ "$SUB_COUNT" -gt 0 ] && [ "$TOTAL_SERVERS" -eq 0 ]; then
    log "ERROR: No valid servers parsed from any subscription"
    exit 1
fi

log "Total servers: $TOTAL_SERVERS"

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
    -v vless_file="$TMPDIR/vless.tmp" \
    -v urltest_file="$TMPDIR/urltest.tmp" \
    -v rules_file="$TMPDIR/rules.tmp" \
'/"__DEFAULT_OUTBOUND__"/ {
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
if sing-box check -c "${CONFIG}.new" 2>&1; then
    [ -f "$CONFIG" ] && cp "$CONFIG" "${CONFIG}.bak"
    mv "${CONFIG}.new" "$CONFIG"
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
