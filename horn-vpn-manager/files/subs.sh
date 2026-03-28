#!/bin/sh
# shellcheck disable=SC3043

# ============================================================
# sing-box subscription update script
# Downloads VLESS URIs from subscription URLs, parses them,
# generates per-subscription outbound groups and route rules,
# and updates the sing-box config.
#
# Tag scheme:
#   <id>-single      — subscription with a single proxy
#   <id>-auto        — urltest group for multi-proxy subscription
#   <id>-manual      — selector for manual proxy choice
#   <id>-node-<hash> — individual proxy (stable hash of conn params)
#
# Requires: curl, base64, awk, sed, grep, jq, md5sum
# ============================================================

SCRIPT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
CONF_DIR="/etc/horn-vpn-manager"
SUBS_CONF=""
CONFIG_TEMPLATE=""
CONFIG=""
TAGS_FILE=""
UNSYNC_FLAG=""
TMPDIR=""
LOG=""
DRY_RUN=0
VERBOSE=0
COLOR=1
DEBUG=0

# ---- Argument parsing --------------------------------------------------------

show_help() {
    printf 'sing-box subscription updater\n\n'
    printf 'Downloads VLESS URIs from subscription URLs, builds per-subscription\n'
    printf 'outbound groups (urltest + selector), and reloads sing-box config.\n\n'
    printf 'Usage: %s <command> [options]\n\n' "$(basename "$0")"
    printf 'Commands:\n'
    printf '  run        Download subscriptions and update sing-box config\n'
    printf '  dry-run    Simulate run without writing config or restarting\n'
    printf '  help       Show this help message\n'
    printf '\nOptions:\n'
    printf '  --debug          Use local files next to subs.sh and force dry-run mode\n'
    printf '  --no-color       Disable colored output\n'
    printf '  -v / -vv / -vvv  Verbosity level\n'
    printf '\nDefault config:  /etc/horn-vpn-manager/subs.json\n'
    printf 'Debug config:    %s/subs.json\n' "$SCRIPT_DIR"
    printf 'Default log:     /tmp/horn-vpn-manager-sub.log\n'
    printf 'Debug log:       %s/subs.debug.log\n' "$SCRIPT_DIR"
}

CMD=""
for arg in "$@"; do
    case "$arg" in
        run|dry-run|help) [ -z "$CMD" ] && CMD="$arg" ;;
        --debug) DEBUG=1 ;;
        --no-color) COLOR=0 ;;
        -vvv) VERBOSE=3 ;;
        -vv)  VERBOSE=2 ;;
        -v)   VERBOSE=1 ;;
    esac
done

case "$CMD" in
    run)     DRY_RUN=0 ;;
    dry-run) DRY_RUN=1 ;;
    help|'')
        show_help
        exit 0
        ;;
esac

if [ "$DEBUG" -eq 1 ]; then
    DRY_RUN=1
    CONF_DIR="$SCRIPT_DIR"
    SUBS_CONF="${SCRIPT_DIR}/subs.json"
    CONFIG_TEMPLATE="${SCRIPT_DIR}/config.template.json"
    CONFIG="${SCRIPT_DIR}/config.debug.json"
    TAGS_FILE="${SCRIPT_DIR}/subs-tags.debug.json"
    UNSYNC_FLAG="${SCRIPT_DIR}/.needs-update.debug"
    TMPDIR="${SCRIPT_DIR}/.tmp-horn-vpn-manager-sub"
    LOG="${SCRIPT_DIR}/subs.debug.log"
else
    SUBS_CONF="${CONF_DIR}/subs.json"
    CONFIG_TEMPLATE="${CONF_DIR}/config.template.json"
    CONFIG="/etc/sing-box/config.json"
    TAGS_FILE="${CONF_DIR}/subs-tags.json"
    UNSYNC_FLAG="${CONF_DIR}/.needs-update"
    TMPDIR="/tmp/horn-vpn-manager-sub"
    LOG="/tmp/horn-vpn-manager-sub.log"
fi

# Set up ANSI color codes (empty strings when disabled)
if [ "$COLOR" -eq 1 ]; then
    C_ERR=$(printf '\033[1;31m')
    C_WARN=$(printf '\033[0;33m')
    C_OK=$(printf '\033[0;32m')
    C_INFO=$(printf '\033[0;36m')
    C_DIM=$(printf '\033[0;90m')
    C_BOLD=$(printf '\033[1m')
    RST=$(printf '\033[0m')
else
    C_ERR='' C_WARN='' C_OK='' C_INFO='' C_DIM='' C_BOLD='' RST=''
fi

log() {
    local msg="$1"
    printf '[%s] %s\n' "$(date '+%Y-%m-%d %H:%M:%S')" "$msg" >> "$LOG"
    if [ "$DRY_RUN" -eq 1 ]; then
        printf '%s[DRY-RUN]%s %s\n' "$C_WARN" "$RST" "$msg"
    else
        printf '%s\n' "$msg"
    fi
}

# vlog LEVEL MESSAGE — log only if VERBOSE >= LEVEL
vlog() {
    [ "$VERBOSE" -ge "$1" ] && log "$2"
}

normalize_count() {
    case "$1" in
        ''|*[!0-9]*)
            printf '0\n'
            ;;
        *)
            printf '%s\n' "$1"
            ;;
    esac
}

debug_dump_file_lines() {
    [ "$VERBOSE" -ge 3 ] || return 0

    local label="$1"
    local file="$2"

    [ -s "$file" ] || return 0

    log "${C_DIM}    [dbg] ${label}:${RST}"
    while IFS= read -r line || [ -n "$line" ]; do
        [ -n "$line" ] || continue
        log "${C_DIM}      ${line}${RST}"
    done < "$file"
}

# Returns 0 (true) if server name matches an exclude pattern
# shellcheck disable=SC3043
is_excluded() {
    local name="$1"
    local name_lower
    name_lower=$(echo "$name" | tr 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' 'abcdefghijklmnopqrstuvwxyz')

    while IFS= read -r pattern; do
        local pattern_lower
        pattern_lower=$(echo "$pattern" | tr 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' 'abcdefghijklmnopqrstuvwxyz')
        case "$name_lower" in
            *"$pattern_lower"*) return 0 ;;
        esac
    done < "$TMPDIR/exclude.tmp"

    return 1
}

# Compute 8-char MD5 hash from stable VLESS connection parameters.
# Inputs: type, server, port, uuid, security, sni, pbk, sid, flow, fp.
# The hash is stable as long as the connection parameters do not change.
# shellcheck disable=SC3043
compute_node_hash() {
    local line="$1"
    local uri="${line#vless://}"
    local uuid="${uri%%@*}"
    local rest="${uri#*@}"
    local sp="${rest%%\?*}"
    local server="${sp%%:*}"
    local port="${sp##*:}"
    local params="${rest#*\?}"; params="${params%%#*}"

    local sni pbk sid flow fp security
    sni=$(echo "$params"      | tr '&' '\n' | grep "^sni="      | cut -d= -f2-)
    pbk=$(echo "$params"      | tr '&' '\n' | grep "^pbk="      | cut -d= -f2-)
    sid=$(echo "$params"      | tr '&' '\n' | grep "^sid="      | cut -d= -f2-)
    flow=$(echo "$params"     | tr '&' '\n' | grep "^flow="     | cut -d= -f2-)
    fp=$(echo "$params"       | tr '&' '\n' | grep "^fp="       | cut -d= -f2-)
    security=$(echo "$params" | tr '&' '\n' | grep "^security=" | cut -d= -f2-)

    printf 'vless|%s|%s|%s|%s|%s|%s|%s|%s|%s' \
        "$server" "$port" "$uuid" "$security" "$sni" "$pbk" "$sid" "$flow" "$fp" \
        | md5sum | cut -c1-8
}

# Parse a VLESS URI and print a sing-box outbound JSON object.
# Usage: parse_uri <uri_line> <tag>
# shellcheck disable=SC3043
parse_uri() {
    local line="$1"
    local tag="$2"

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

if [ "$DEBUG" -eq 1 ]; then
    log "${C_INFO}Debug mode: using local files from ${C_BOLD}${SCRIPT_DIR}${RST}"
    log "${C_DIM}  debug implies dry-run; no config replace or service restart${RST}"
    log "${C_DIM}  subs=${SUBS_CONF}${RST}"
    log "${C_DIM}  template=${CONFIG_TEMPLATE}${RST}"
    log "${C_DIM}  output=${CONFIG}${RST}"
fi

# ---- Validate subscriptions config ----
if [ ! -f "$SUBS_CONF" ]; then
    log "${C_ERR}ERROR: $SUBS_CONF not found${RST}"
    exit 1
fi

SUB_COUNT=$(jq '.subscriptions | length // 0' "$SUBS_CONF" 2>/dev/null || echo 0)
if [ "$SUB_COUNT" -eq 0 ]; then
    log "${C_ERR}ERROR: No subscriptions defined in $SUBS_CONF${RST}"
    exit 1
fi
log "${C_INFO}Found ${C_BOLD}${SUB_COUNT}${RST}${C_INFO} subscription(s)${RST}"

DEF_COUNT=$(jq '[.subscriptions[] | select(.default == true)] | length' "$SUBS_CONF" 2>/dev/null || echo 0)
if [ "$DEF_COUNT" -eq 0 ]; then
    log "${C_ERR}ERROR: No default subscription defined (set \"default\": true on one subscription)${RST}"
    exit 1
fi
if [ "$DEF_COUNT" -gt 1 ]; then
    log "${C_ERR}ERROR: Multiple default subscriptions defined (only one allowed)${RST}"
    exit 1
fi

# Default subscriptions must be enabled (enabled!=false)
DEF_DISABLED=$(jq '[.subscriptions[] | select(.default == true and ((if has("enabled") then .enabled else true end) | tostring | ascii_downcase) == "false")] | length' "$SUBS_CONF" 2>/dev/null || echo 0)
if [ "$DEF_DISABLED" -gt 0 ]; then
    log "${C_ERR}ERROR: Default subscription cannot be disabled${RST}"
    exit 1
fi

# Read global settings before the loop (TEST_URL is needed for urltest groups)
LOG_LEVEL=$(jq -r '.log_level // "warn"' "$SUBS_CONF")
TEST_URL=$(jq -r '.test_url // "https://www.gstatic.com/generate_204"' "$SUBS_CONF")
GLOBAL_RETRIES=$(jq -r '.retries // 5' "$SUBS_CONF")

# ---- Process each subscription ----
VLESS_OUTBOUNDS=""
GROUP_OUTBOUNDS=""
ROUTE_RULES=""
TOTAL_SERVERS=0
DEF_ROUTE_TAG=""
: > "$TMPDIR/tag-names.tsv"

jq -r '.subscriptions | keys_unsorted[]' "$SUBS_CONF" > "$TMPDIR/sub_ids.txt"
while IFS= read -r sub_id; do
    sub_name=$(jq -r ".subscriptions[\"$sub_id\"].name" "$SUBS_CONF")
    sub_url=$(jq -r ".subscriptions[\"$sub_id\"].url" "$SUBS_CONF")
    is_default=$(jq -r ".subscriptions[\"$sub_id\"].default // false" "$SUBS_CONF")
    is_enabled=$(jq -r ".subscriptions[\"$sub_id\"] | (if has(\"enabled\") then .enabled else true end) | tostring | ascii_downcase" "$SUBS_CONF")
    sub_interval=$(jq -r ".subscriptions[\"$sub_id\"].interval // \"5m\"" "$SUBS_CONF")

    # Skip disabled subscriptions (default subscriptions cannot be disabled — validated above)
    if [ "$is_enabled" = "false" ]; then
        log "${C_WARN}Skipping disabled ${C_BOLD}${sub_name}${RST}${C_WARN}...${RST}"
        continue
    fi
    sub_tolerance=$(jq -r ".subscriptions[\"$sub_id\"].tolerance // 100" "$SUBS_CONF")
    rawfile="$TMPDIR/${sub_id}.raw"
    outfile="$TMPDIR/${sub_id}.txt"
    uri_file="$TMPDIR/${sub_id}.uris"

    jq -r ".subscriptions[\"$sub_id\"].exclude[]? | ascii_downcase" "$SUBS_CONF" > "$TMPDIR/exclude.tmp"

    sub_retries=$(jq -r ".subscriptions[\"$sub_id\"].retries // $GLOBAL_RETRIES" "$SUBS_CONF")
    vlog 3 "${C_DIM}  [dbg] sub_id=${sub_id} retries=${sub_retries} rawfile=${rawfile}${RST}"
    log "${C_INFO}Downloading ${C_BOLD}${sub_name}${RST}${C_INFO}...${RST}"

    attempt=1
    download_ok=0
    while [ "$attempt" -le "$((sub_retries + 1))" ]; do
        [ "$attempt" -gt 1 ] && log "  ${C_WARN}${sub_name}: retry $((attempt - 1))/${sub_retries}...${RST}"
        http_code=$(curl -sS -L -m 15 -o "$rawfile" -w "%{http_code}" \
            "$sub_url" 2>"$TMPDIR/${sub_id}.curl_err")
        curl_rc=$?
        if [ "$curl_rc" -ne 0 ] || [ "$http_code" = "000" ] || [ -z "$http_code" ]; then
            log "  ${C_WARN}${sub_name}: connection failed (rc=${curl_rc})${RST}"
            vlog 3 "${C_DIM}    $(cat "$TMPDIR/${sub_id}.curl_err" 2>/dev/null)${RST}"
            attempt=$((attempt + 1))
            [ "$attempt" -le "$((sub_retries + 1))" ] && sleep 5
            continue
        fi
        if [ "$http_code" != "200" ]; then
            log "  ${C_WARN}${sub_name}: HTTP ${http_code}${RST}"
            attempt=$((attempt + 1))
            [ "$attempt" -le "$((sub_retries + 1))" ] && sleep 5
            continue
        fi
        download_ok=1
        break
    done

    if [ "$download_ok" -eq 0 ]; then
        if [ "$is_default" = "true" ]; then
            log "${C_ERR}ERROR: Default subscription '${sub_name}' failed to download, aborting${RST}"
            exit 1
        fi
        log "  ${C_ERR}${sub_name}: all attempts failed, skipping${RST}"
        continue
    fi
    vlog 3 "${C_DIM}  [dbg] ${sub_name}: $(wc -c < "$rawfile" | tr -d ' ') bytes${RST}"

    # Auto-detect encoding: raw VLESS or base64
    if grep -q "^vless://" "$rawfile" 2>/dev/null; then
        cp "$rawfile" "$outfile"
        vlog 1 "${C_DIM}  ${sub_name}: raw format${RST}"
    elif base64 -d < "$rawfile" > "$outfile" 2>/dev/null && grep -q "^vless://" "$outfile"; then
        vlog 1 "${C_DIM}  ${sub_name}: base64 format${RST}"
    else
        log "  ${C_WARN}${sub_name}: no VLESS URIs found, skipping${RST}"
        continue
    fi

    lines=$(normalize_count "$(grep -c "^vless://" "$outfile" 2>/dev/null || true)")
    log "  ${sub_name}: ${C_BOLD}${lines}${RST} server(s) found"

    # ---- First pass: collect non-excluded URIs ----
    : > "$uri_file"
    skipped=0
    while IFS= read -r line || [ -n "$line" ]; do
        echo "$line" | grep -q "^vless://" || continue
        raw_name="${line##*#}"
        server_name=$(echo "$raw_name" | sed 's/%20/ /g; s/%23/#/g; s/%2F/\//g; s/+/ /g')
        if is_excluded "$server_name"; then
            skipped=$((skipped + 1))
            vlog 1 "  ${C_WARN}SKIP: ${server_name} (matched filter)${RST}"
            continue
        fi
        printf '%s\n' "$line" >> "$uri_file"
    done < "$outfile"

    node_count=$(normalize_count "$(grep -c '' "$uri_file" 2>/dev/null || true)")
    if [ "$skipped" -gt 0 ]; then
        log "  ${sub_name}: kept ${C_OK}${node_count}${RST}, skipped ${C_WARN}${skipped}${RST}"
    else
        log "  ${sub_name}: kept ${C_OK}${node_count}${RST}"
    fi

    if [ "$node_count" -eq 0 ]; then
        continue
    fi

    # ---- Generate outbounds based on node count ----
    if [ "$node_count" -eq 1 ]; then
        # Single mode: one node, use <id>-single tag directly
        line=$(cat "$uri_file")
        node_tag="${sub_id}-single"
        raw_name="${line##*#}"
        server_name=$(echo "$raw_name" | sed 's/%20/ /g; s/%23/#/g; s/%2F/\//g; s/+/ /g')

        outbound=$(parse_uri "$line" "$node_tag")
        [ -n "$VLESS_OUTBOUNDS" ] && VLESS_OUTBOUNDS="$VLESS_OUTBOUNDS,
"
        VLESS_OUTBOUNDS="${VLESS_OUTBOUNDS}${outbound}"

        printf '%s\t%s\n' "$node_tag" "$server_name" >> "$TMPDIR/tag-names.tsv"
        vlog 2 "  ${C_OK}SINGLE${RST}: ${C_DIM}${node_tag}${RST} (${server_name})"

        rule_tag="${sub_id}-single"
        TOTAL_SERVERS=$((TOTAL_SERVERS + 1))
    else
        # Multi mode: hash-tagged nodes + urltest (auto) + selector (manual)
        sub_node_tags=""

        while IFS= read -r line || [ -n "$line" ]; do
            [ -z "$line" ] && continue

            node_hash=$(compute_node_hash "$line")
            node_tag="${sub_id}-node-${node_hash}"
            raw_name="${line##*#}"
            server_name=$(echo "$raw_name" | sed 's/%20/ /g; s/%23/#/g; s/%2F/\//g; s/+/ /g')

            outbound=$(parse_uri "$line" "$node_tag")
            [ -n "$VLESS_OUTBOUNDS" ] && VLESS_OUTBOUNDS="$VLESS_OUTBOUNDS,
"
            VLESS_OUTBOUNDS="${VLESS_OUTBOUNDS}${outbound}"

            printf '%s\t%s\n' "$node_tag" "$server_name" >> "$TMPDIR/tag-names.tsv"
            vlog 2 "  ${C_OK}NODE${RST}: ${C_DIM}${node_tag}${RST} (${server_name})"

            if [ "$VERBOSE" -ge 3 ]; then
                _rest="${line#vless://*@}"
                _sp="${_rest%%\?*}"
                _params="${_rest#*\?}"; _params="${_params%%#*}"
                _sni=$(echo "$_params" | tr '&' '\n' | grep "^sni=" | cut -d= -f2-)
                _sec=$(echo "$_params" | tr '&' '\n' | grep "^security=" | cut -d= -f2-)
                _fp=$(echo "$_params" | tr '&' '\n' | grep "^fp=" | cut -d= -f2-)
                _flow=$(echo "$_params" | tr '&' '\n' | grep "^flow=" | cut -d= -f2-)
                log "${C_DIM}    [dbg] addr=${_sp} security=${_sec} sni=${_sni} fp=${_fp} flow=${_flow}${RST}"
            fi

            [ -n "$sub_node_tags" ] && sub_node_tags="${sub_node_tags}, "
            sub_node_tags="${sub_node_tags}\"${node_tag}\""

            TOTAL_SERVERS=$((TOTAL_SERVERS + 1))
        done < "$uri_file"

        # urltest group: auto-selects best node by latency
        auto_tag="${sub_id}-auto"
        urltest="    {
      \"type\": \"urltest\",
      \"tag\": \"${auto_tag}\",
      \"outbounds\": [${sub_node_tags}],
      \"url\": \"${TEST_URL}\",
      \"interval\": \"${sub_interval}\",
      \"tolerance\": ${sub_tolerance}
    }"
        [ -n "$GROUP_OUTBOUNDS" ] && GROUP_OUTBOUNDS="$GROUP_OUTBOUNDS,
"
        GROUP_OUTBOUNDS="${GROUP_OUTBOUNDS}${urltest}"
        printf '%s\t%s\n' "$auto_tag" "Auto" >> "$TMPDIR/tag-names.tsv"

        # selector group: manual node choice, defaults to auto
        manual_tag="${sub_id}-manual"
        selector="    {
      \"type\": \"selector\",
      \"tag\": \"${manual_tag}\",
      \"outbounds\": [\"${auto_tag}\", ${sub_node_tags}],
      \"default\": \"${auto_tag}\"
    }"
        GROUP_OUTBOUNDS="$GROUP_OUTBOUNDS,
${selector}"
        printf '%s\t%s\n' "$manual_tag" "$sub_name" >> "$TMPDIR/tag-names.tsv"

        rule_tag="${sub_id}-manual"
    fi

    if [ "$is_default" = "true" ]; then
        DEF_ROUTE_TAG="$rule_tag"
        log "  default outbound: ${C_OK}${DEF_ROUTE_TAG}${RST}"
    fi

    # Route rules for non-default subscriptions with domains/ip routing
    domains=$(jq -c ".subscriptions[\"$sub_id\"].domains // empty" "$SUBS_CONF")
    ip_cidrs=$(jq -c ".subscriptions[\"$sub_id\"].ip // empty" "$SUBS_CONF")

    # ---- Download and merge domain_urls ----
    domain_url_count=$(jq -r ".subscriptions[\"$sub_id\"].domain_urls | length // 0" "$SUBS_CONF" 2>/dev/null || echo 0)
    if [ "$domain_url_count" -gt 0 ]; then
        # Collect manual domains into a dedup file (one per line, sorted)
        if [ -n "$domains" ]; then
            echo "$domains" | jq -r '.[]' | tr 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' 'abcdefghijklmnopqrstuvwxyz' | LC_ALL=C sort -u > "$TMPDIR/${sub_id}.domains_manual.tmp"
        else
            : > "$TMPDIR/${sub_id}.domains_manual.tmp"
        fi
        : > "$TMPDIR/${sub_id}.domains_urls.tmp"

        jq -r ".subscriptions[\"$sub_id\"].domain_urls[]" "$SUBS_CONF" | while IFS= read -r durl; do
            [ -z "$durl" ] && continue
            vlog 1 "  ${C_INFO}Downloading domain list: ${C_DIM}${durl}${RST}"
            durl_file="$TMPDIR/${sub_id}.durl_$(printf '%s' "$durl" | md5sum | cut -c1-8).tmp"
            durl_ok=0
            durl_attempt=1
            while [ "$durl_attempt" -le "$((sub_retries + 1))" ]; do
                [ "$durl_attempt" -gt 1 ] && log "  ${C_WARN}domain_urls: retry $((durl_attempt - 1))/${sub_retries} for $(echo "$durl" | sed 's|.*/||')${RST}"
                http_code=$(curl -sS -L -m 15 -o "$durl_file" -w "%{http_code}" "$durl" 2>/dev/null)
                curl_rc=$?
                if [ "$curl_rc" -ne 0 ] || [ "$http_code" = "000" ] || [ -z "$http_code" ]; then
                    durl_attempt=$((durl_attempt + 1))
                    [ "$durl_attempt" -le "$((sub_retries + 1))" ] && sleep 5
                    continue
                fi
                if [ "$http_code" != "200" ]; then
                    durl_attempt=$((durl_attempt + 1))
                    [ "$durl_attempt" -le "$((sub_retries + 1))" ] && sleep 5
                    continue
                fi
                durl_ok=1
                break
            done
            if [ "$durl_ok" -eq 0 ] || [ ! -s "$durl_file" ]; then
                log "  ${C_WARN}domain_urls: failed to download ${durl} after $((sub_retries + 1)) attempt(s)${RST}"
                continue
            fi
            # Strip Windows line endings
            tr -d '\r' < "$durl_file" > "$durl_file.clean" && mv "$durl_file.clean" "$durl_file"
            # Validate: each non-empty line must look like a domain
            invalid_lines=$(normalize_count "$(grep -cvE '^\s*$|^\s*#|^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$' "$durl_file" 2>/dev/null || true)")
            total_lines=$(normalize_count "$(grep -cvE '^\s*$|^\s*#' "$durl_file" 2>/dev/null || true)")
            if [ "$total_lines" -eq 0 ]; then
                log "  ${C_WARN}domain_urls: empty list from ${durl}${RST}"
                continue
            fi
            if [ "$invalid_lines" -gt 0 ]; then
                log "  ${C_WARN}domain_urls: ${invalid_lines} invalid line(s) in ${durl}, skipping${RST}"
                vlog 2 "  ${C_DIM}$(grep -vE '^\s*$|^\s*#|^[a-zA-Z0-9]([a-zA-Z0-9._-]*[a-zA-Z0-9])?$' "$durl_file" | head -3)${RST}"
                continue
            fi
            # Append valid domains (strip comments, empty lines, lowercase)
            grep -vE '^\s*$|^\s*#' "$durl_file" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' | tr 'ABCDEFGHIJKLMNOPQRSTUVWXYZ' 'abcdefghijklmnopqrstuvwxyz' > "$durl_file.valid"
            cat "$durl_file.valid" >> "$TMPDIR/${sub_id}.domains_urls.tmp"
            debug_dump_file_lines "domains from $(echo "$durl" | sed 's|.*/||')" "$durl_file.valid"
            vlog 1 "  ${C_OK}domain_urls${RST}: ${total_lines} domain(s) from $(echo "$durl" | sed 's|.*/||')"
        done

        # Deduplicate: remove entries already in manual list or duplicated across URLs
        if [ -s "$TMPDIR/${sub_id}.domains_urls.tmp" ]; then
            LC_ALL=C sort -u "$TMPDIR/${sub_id}.domains_urls.tmp" > "$TMPDIR/${sub_id}.domains_urls_uniq.tmp"
            # Remove domains already present in the manual list
            if [ -s "$TMPDIR/${sub_id}.domains_manual.tmp" ]; then
                LC_ALL=C comm -23 "$TMPDIR/${sub_id}.domains_urls_uniq.tmp" "$TMPDIR/${sub_id}.domains_manual.tmp" > "$TMPDIR/${sub_id}.domains_urls_new.tmp"
            else
                cp "$TMPDIR/${sub_id}.domains_urls_uniq.tmp" "$TMPDIR/${sub_id}.domains_urls_new.tmp"
            fi
            new_domain_count=$(wc -l < "$TMPDIR/${sub_id}.domains_urls_new.tmp" | tr -d ' ')
            if [ "$new_domain_count" -gt 0 ]; then
                # Merge: combine manual domains array with new URL domains
                url_domains_json=$(jq -Rc '.' "$TMPDIR/${sub_id}.domains_urls_new.tmp" | jq -sc '.')
                if [ -n "$domains" ]; then
                    domains=$(echo "$domains" "$url_domains_json" | jq -sc '.[0] + .[1] | unique')
                else
                    domains="$url_domains_json"
                fi
                log "  ${sub_name}: merged ${C_OK}${new_domain_count}${RST} domain(s) from URLs"
            fi
        fi
    fi

    # ---- Download and merge ip_urls ----
    ip_url_count=$(jq -r ".subscriptions[\"$sub_id\"].ip_urls | length // 0" "$SUBS_CONF" 2>/dev/null || echo 0)
    if [ "$ip_url_count" -gt 0 ]; then
        # Collect manual IPs into a dedup file
        if [ -n "$ip_cidrs" ]; then
            echo "$ip_cidrs" | jq -r '.[]' | LC_ALL=C sort -u > "$TMPDIR/${sub_id}.ips_manual.tmp"
        else
            : > "$TMPDIR/${sub_id}.ips_manual.tmp"
        fi
        : > "$TMPDIR/${sub_id}.ips_urls.tmp"

        jq -r ".subscriptions[\"$sub_id\"].ip_urls[]" "$SUBS_CONF" | while IFS= read -r iurl; do
            [ -z "$iurl" ] && continue
            vlog 1 "  ${C_INFO}Downloading IP list: ${C_DIM}${iurl}${RST}"
            iurl_file="$TMPDIR/${sub_id}.iurl_$(printf '%s' "$iurl" | md5sum | cut -c1-8).tmp"
            iurl_ok=0
            iurl_attempt=1
            while [ "$iurl_attempt" -le "$((sub_retries + 1))" ]; do
                [ "$iurl_attempt" -gt 1 ] && log "  ${C_WARN}ip_urls: retry $((iurl_attempt - 1))/${sub_retries} for $(echo "$iurl" | sed 's|.*/||')${RST}"
                http_code=$(curl -sS -L -m 15 -o "$iurl_file" -w "%{http_code}" "$iurl" 2>/dev/null)
                curl_rc=$?
                if [ "$curl_rc" -ne 0 ] || [ "$http_code" = "000" ] || [ -z "$http_code" ]; then
                    iurl_attempt=$((iurl_attempt + 1))
                    [ "$iurl_attempt" -le "$((sub_retries + 1))" ] && sleep 5
                    continue
                fi
                if [ "$http_code" != "200" ]; then
                    iurl_attempt=$((iurl_attempt + 1))
                    [ "$iurl_attempt" -le "$((sub_retries + 1))" ] && sleep 5
                    continue
                fi
                iurl_ok=1
                break
            done
            if [ "$iurl_ok" -eq 0 ] || [ ! -s "$iurl_file" ]; then
                log "  ${C_WARN}ip_urls: failed to download ${iurl} after $((sub_retries + 1)) attempt(s)${RST}"
                continue
            fi
            # Strip Windows line endings
            tr -d '\r' < "$iurl_file" > "$iurl_file.clean" && mv "$iurl_file.clean" "$iurl_file"
            # Validate: each non-empty/non-comment line must look like an IP or CIDR
            invalid_lines=$(normalize_count "$(grep -cvE '^\s*$|^\s*#|^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+(/[0-9]+)?$|^[0-9a-fA-F:]+(/[0-9]+)?$' "$iurl_file" 2>/dev/null || true)")
            total_lines=$(normalize_count "$(grep -cvE '^\s*$|^\s*#' "$iurl_file" 2>/dev/null || true)")
            if [ "$total_lines" -eq 0 ]; then
                log "  ${C_WARN}ip_urls: empty list from ${iurl}${RST}"
                continue
            fi
            if [ "$invalid_lines" -gt 0 ]; then
                log "  ${C_WARN}ip_urls: ${invalid_lines} invalid line(s) in ${iurl}, skipping${RST}"
                vlog 2 "  ${C_DIM}$(grep -vE '^\s*$|^\s*#|^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+(/[0-9]+)?$|^[0-9a-fA-F:]+(/[0-9]+)?$' "$iurl_file" | head -3)${RST}"
                continue
            fi
            grep -vE '^\s*$|^\s*#' "$iurl_file" | sed 's/^[[:space:]]*//;s/[[:space:]]*$//' > "$iurl_file.valid"
            cat "$iurl_file.valid" >> "$TMPDIR/${sub_id}.ips_urls.tmp"
            debug_dump_file_lines "ips from $(echo "$iurl" | sed 's|.*/||')" "$iurl_file.valid"
            vlog 1 "  ${C_OK}ip_urls${RST}: ${total_lines} entry(ies) from $(echo "$iurl" | sed 's|.*/||')"
        done

        # Deduplicate
        if [ -s "$TMPDIR/${sub_id}.ips_urls.tmp" ]; then
            LC_ALL=C sort -u "$TMPDIR/${sub_id}.ips_urls.tmp" > "$TMPDIR/${sub_id}.ips_urls_uniq.tmp"
            if [ -s "$TMPDIR/${sub_id}.ips_manual.tmp" ]; then
                LC_ALL=C comm -23 "$TMPDIR/${sub_id}.ips_urls_uniq.tmp" "$TMPDIR/${sub_id}.ips_manual.tmp" > "$TMPDIR/${sub_id}.ips_urls_new.tmp"
            else
                cp "$TMPDIR/${sub_id}.ips_urls_uniq.tmp" "$TMPDIR/${sub_id}.ips_urls_new.tmp"
            fi
            new_ip_count=$(wc -l < "$TMPDIR/${sub_id}.ips_urls_new.tmp" | tr -d ' ')
            if [ "$new_ip_count" -gt 0 ]; then
                url_ips_json=$(jq -Rc '.' "$TMPDIR/${sub_id}.ips_urls_new.tmp" | jq -sc '.')
                if [ -n "$ip_cidrs" ]; then
                    ip_cidrs=$(echo "$ip_cidrs" "$url_ips_json" | jq -sc '.[0] + .[1] | unique')
                else
                    ip_cidrs="$url_ips_json"
                fi
                log "  ${sub_name}: merged ${C_OK}${new_ip_count}${RST} IP/CIDR(s) from URLs"
            fi
        fi
    fi

    if [ "$is_default" != "true" ] && { [ -n "$domains" ] || [ -n "$ip_cidrs" ]; }; then
        if [ -n "$domains" ]; then
            rule="      {
        \"domain_suffix\": ${domains},
        \"outbound\": \"${rule_tag}\"
      }"
            [ -n "$ROUTE_RULES" ] && ROUTE_RULES="$ROUTE_RULES,
"
            ROUTE_RULES="${ROUTE_RULES}${rule}"
        fi

        if [ -n "$ip_cidrs" ]; then
            ip_rule="      {
        \"ip_cidr\": ${ip_cidrs},
        \"outbound\": \"${rule_tag}\"
      }"
            [ -n "$ROUTE_RULES" ] && ROUTE_RULES="$ROUTE_RULES,
"
            ROUTE_RULES="${ROUTE_RULES}${ip_rule}"
        fi
    fi

done < "$TMPDIR/sub_ids.txt"

if [ -z "$DEF_ROUTE_TAG" ]; then
    log "${C_ERR}ERROR: default subscription did not produce a valid outbound${RST}"
    exit 1
fi

if [ "$TOTAL_SERVERS" -eq 0 ]; then
    log "${C_ERR}ERROR: No valid servers parsed from any subscription${RST}"
    exit 1
fi

log "Total servers: ${C_BOLD}${TOTAL_SERVERS}${RST}"

# ---- Build final config ----
if [ -n "$VLESS_OUTBOUNDS" ]; then
    printf '%s,\n' "$VLESS_OUTBOUNDS"  > "$TMPDIR/vless.tmp"
else
    : > "$TMPDIR/vless.tmp"
fi
if [ -n "$GROUP_OUTBOUNDS" ]; then
    printf '%s,\n' "$GROUP_OUTBOUNDS"  > "$TMPDIR/groups.tmp"
else
    : > "$TMPDIR/groups.tmp"
fi
printf '%s\n' "$ROUTE_RULES" > "$TMPDIR/rules.tmp"

awk \
    -v def_tag="$DEF_ROUTE_TAG" \
    -v log_level="$LOG_LEVEL" \
    -v vless_file="$TMPDIR/vless.tmp" \
    -v groups_file="$TMPDIR/groups.tmp" \
    -v rules_file="$TMPDIR/rules.tmp" \
'/__LOG_LEVEL__/ {
    sub(/__LOG_LEVEL__/, log_level)
    print
    next
}
/__DEFAULT_TAG__/ {
    sub(/__DEFAULT_TAG__/, def_tag)
    print
    next
}
/"__VLESS_OUTBOUNDS__"/ {
    while ((getline line < vless_file) > 0) print line
    next
}
/"__GROUP_OUTBOUNDS__"/ {
    while ((getline line < groups_file) > 0) print line
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
    log "${C_DIM}Dry-run: generated config preview (${CONFIG}.new):${RST}"
    cat "${CONFIG}.new"
    log "${C_DIM}Dry-run: skipping sing-box check, apply, and restart${RST}"
    rm -f "${CONFIG}.new"
    exit 0
fi

if ! command -v sing-box > /dev/null 2>&1; then
    log "${C_ERR}ERROR: sing-box not found, cannot validate config${RST}"
    rm -f "${CONFIG}.new"
    exit 1
fi

check_out=$(sing-box check -c "${CONFIG}.new" 2>&1)
check_rc=$?
[ -n "$check_out" ] && log "${C_DIM}${check_out}${RST}"

if [ "$check_rc" -ne 0 ]; then
    log "${C_ERR}ERROR: Invalid config, keeping old one${RST}"
    rm -f "${CONFIG}.new"
    exit 1
fi

[ -f "$CONFIG" ] && cp "$CONFIG" "${CONFIG}.bak"
mv "${CONFIG}.new" "$CONFIG"

# Save tag->name mapping for LuCI display
if [ -s "$TMPDIR/tag-names.tsv" ]; then
    jq -Rn '[inputs | split("\t") | {(.[0]): .[1]}] | add // {}' \
        "$TMPDIR/tag-names.tsv" > "$TAGS_FILE"
fi

# Clear unsync flag — config is now applied
rm -f "$UNSYNC_FLAG"

log "${C_OK}Config OK, restarting sing-box...${RST}"
restart_out=$(service sing-box restart 2>&1)
[ -n "$restart_out" ] && log "${C_DIM}${restart_out}${RST}"
sleep 2
if pidof sing-box > /dev/null; then
    log "${C_OK}${C_BOLD}sing-box restarted successfully${RST}"
else
    log "${C_WARN}WARNING: sing-box may not have started${RST}"
fi
