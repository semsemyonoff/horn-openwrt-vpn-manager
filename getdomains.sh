#!/bin/sh

# getdomains.sh — download and apply domain/IP lists for VPN routing
#
# Usage: getdomains.sh [run|restore|help] [--no-color] [-v|-vv|-vvv]
#   run      download lists, save to persistent cache, apply to system (default)
#   restore  apply from existing cache (fast, for boot init script)

# ── Paths ─────────────────────────────────────────────────────────────────────
DOMAINS_JSON="/etc/horn-vpn-manager/domains.json"
LISTS_DIR="/etc/horn-vpn-manager/lists"
DOMAINS_CACHE="$LISTS_DIR/domains.lst"
SUBNETS_CACHE="$LISTS_DIR/subnets.lst"
MANUAL_IPS="$LISTS_DIR/manual-ip.lst"
VPN_IP_LIST="/etc/vpn-ip-list.lst"
DNSMASQ_DIR="/tmp/dnsmasq.d"
LOG="/tmp/getdomains.log"
TMPDIR="/tmp/getdomains-tmp"

# ── Verbosity / color ─────────────────────────────────────────────────────────
VERBOSE=0
USE_COLOR=1

log() {
  local level="$1"; shift
  [ "$level" -gt "$VERBOSE" ] && return
  local ts; ts=$(date '+%H:%M:%S')
  if [ "$USE_COLOR" = "1" ]; then
    case "$level" in
      0) printf '\033[1;32m[%s]\033[0m %s\n' "$ts" "$*" ;;
      1) printf '\033[0;36m[%s]\033[0m %s\n' "$ts" "$*" ;;
      *) printf '[%s] %s\n' "$ts" "$*" ;;
    esac
  else
    printf '[%s] %s\n' "$ts" "$*"
  fi | tee -a "$LOG"
}

err() {
  local ts; ts=$(date '+%H:%M:%S')
  if [ "$USE_COLOR" = "1" ]; then
    printf '\033[1;31m[%s] ERROR:\033[0m %s\n' "$ts" "$*"
  else
    printf '[%s] ERROR: %s\n' "$ts" "$*"
  fi | tee -a "$LOG"
}

# ── Wait for network ──────────────────────────────────────────────────────────
wait_for_network() {
  local count=0
  local retries=12
  log 0 "Waiting for network..."
  while true; do
    if curl -s -m 3 "https://github.com" > /dev/null 2>&1; then
      log 0 "Network OK"
      return 0
    fi
    count=$((count + 1))
    if [ "$count" -ge "$retries" ]; then
      err "Network unreachable after $((retries * 5))s, giving up"
      return 1
    fi
    log 1 "Not reachable, retry [$count/$retries]..."
    sleep 5
  done
}

# ── Download with retries ─────────────────────────────────────────────────────
# download <url> <dest> [retries]
download() {
  local url="$1"
  local dest="$2"
  local retries="${3:-3}"
  local attempt=0
  while [ "$attempt" -lt "$retries" ]; do
    attempt=$((attempt + 1))
    log 1 "Fetching $url (attempt $attempt/$retries)..."
    if curl -f -s -m 30 "$url" -o "${dest}.tmp" 2>/dev/null; then
      if [ -s "${dest}.tmp" ]; then
        mv "${dest}.tmp" "$dest"
        log 1 "Saved $(wc -l < "$dest" | tr -d ' ') lines → $dest"
        return 0
      else
        err "Downloaded file is empty: $url"
      fi
    else
      err "curl failed for $url"
    fi
    rm -f "${dest}.tmp"
    [ "$attempt" -lt "$retries" ] && sleep 3
  done
  return 1
}

# ── Apply domain list to dnsmasq ──────────────────────────────────────────────
apply_domains() {
  if [ ! -f "$DOMAINS_CACHE" ] || [ ! -s "$DOMAINS_CACHE" ]; then
    log 0 "No domain cache, skipping dnsmasq update"
    return 0
  fi
  mkdir -p "$DNSMASQ_DIR"
  if dnsmasq --conf-file="$DOMAINS_CACHE" --test 2>&1 | grep -q "syntax check OK"; then
    cp "$DOMAINS_CACHE" "$DNSMASQ_DIR/domains.lst"
    log 0 "Domain list applied to $DNSMASQ_DIR/domains.lst, restarting dnsmasq..."
    /etc/init.d/dnsmasq restart
  else
    err "Domain list syntax check failed, skipping dnsmasq update"
    return 1
  fi
}

# ── Rebuild combined IP list and reload firewall ──────────────────────────────
apply_ips() {
  local has_data=0
  > "$VPN_IP_LIST"

  if [ -f "$SUBNETS_CACHE" ] && [ -s "$SUBNETS_CACHE" ]; then
    grep -v '^[[:space:]]*$' "$SUBNETS_CACHE" | grep -v '^#' >> "$VPN_IP_LIST"
    has_data=1
  fi

  if [ -f "$MANUAL_IPS" ] && [ -s "$MANUAL_IPS" ]; then
    grep -v '^[[:space:]]*$' "$MANUAL_IPS" | grep -v '^#' >> "$VPN_IP_LIST"
    has_data=1
  fi

  if [ "$has_data" = "0" ]; then
    log 0 "No IP entries, skipping firewall reload"
    return 0
  fi

  local count; count=$(wc -l < "$VPN_IP_LIST" | tr -d ' ')
  log 0 "IP list updated: $count entries → $VPN_IP_LIST"
  log 0 "Reloading firewall..."
  if command -v fw4 > /dev/null 2>&1; then
    fw4 reload
  else
    /etc/init.d/firewall reload
  fi
}

# ── run: download fresh lists, cache, apply ───────────────────────────────────
cmd_run() {
  mkdir -p "$LISTS_DIR" "$TMPDIR"
  > "$LOG"

  log 0 "=== getdomains run ==="

  if [ ! -f "$DOMAINS_JSON" ]; then
    err "Config not found: $DOMAINS_JSON"
    exit 1
  fi

  wait_for_network || exit 1

  local domains_url
  domains_url=$(jq -r '.domains_url // ""' "$DOMAINS_JSON")

  local domains_updated=0
  local subnets_updated=0

  # Download domain list
  if [ -n "$domains_url" ]; then
    log 0 "Downloading domain list..."
    if download "$domains_url" "$DOMAINS_CACHE" 3; then
      domains_updated=1
    else
      err "Failed to download domain list from $domains_url"
    fi
  else
    log 0 "domains_url not configured, skipping domain list"
  fi

  # Download subnet lists
  local subnet_count
  subnet_count=$(jq '.subnet_urls | length' "$DOMAINS_JSON" 2>/dev/null || echo 0)

  if [ "$subnet_count" -gt 0 ]; then
    log 0 "Downloading $subnet_count subnet list(s)..."
    > "$TMPDIR/subnets.tmp"
    local i=0
    while [ "$i" -lt "$subnet_count" ]; do
      local url
      url=$(jq -r ".subnet_urls[$i]" "$DOMAINS_JSON")
      log 1 "  [$((i+1))/$subnet_count] $url"
      local tmp="$TMPDIR/subnet-$i.lst"
      if download "$url" "$tmp" 3; then
        grep -v '^[[:space:]]*$' "$tmp" | grep -v '^#' >> "$TMPDIR/subnets.tmp"
        log 1 "  Added $(wc -l < "$tmp" | tr -d ' ') entries"
      else
        err "Failed to download subnet list: $url"
      fi
      i=$((i + 1))
    done
    sort -u "$TMPDIR/subnets.tmp" > "$SUBNETS_CACHE"
    log 0 "Subnet cache: $(wc -l < "$SUBNETS_CACHE" | tr -d ' ') unique entries"
    subnets_updated=1
  else
    log 0 "No subnet_urls configured, skipping subnets"
  fi

  # Apply to system
  [ "$domains_updated" = "1" ] && apply_domains
  if [ "$subnets_updated" = "1" ] || [ -f "$MANUAL_IPS" ]; then
    apply_ips
  fi

  log 0 "=== done ==="
  rm -rf "$TMPDIR"
}

# ── restore: apply from cache only (fast, for boot) ──────────────────────────
cmd_restore() {
  > "$LOG"
  log 0 "=== getdomains restore ==="

  local restored=0

  if [ -f "$DOMAINS_CACHE" ] && [ -s "$DOMAINS_CACHE" ]; then
    log 0 "Restoring domain list from cache..."
    apply_domains && restored=1
  else
    log 0 "No domain cache to restore"
  fi

  if { [ -f "$SUBNETS_CACHE" ] && [ -s "$SUBNETS_CACHE" ]; } || \
     { [ -f "$MANUAL_IPS" ]   && [ -s "$MANUAL_IPS" ]; }; then
    log 0 "Restoring IP list from cache..."
    apply_ips && restored=1
  else
    log 0 "No IP cache to restore"
  fi

  if [ "$restored" = "1" ]; then
    log 0 "=== restore complete ==="
  else
    log 0 "=== nothing to restore ==="
  fi
}

# ── Entry point ───────────────────────────────────────────────────────────────
CMD="${1:-run}"
[ $# -gt 0 ] && shift

for arg in "$@"; do
  case "$arg" in
    --no-color) USE_COLOR=0 ;;
    -v)         VERBOSE=1   ;;
    -vv)        VERBOSE=2   ;;
    -vvv)       VERBOSE=3   ;;
  esac
done

case "$CMD" in
  run)     cmd_run     ;;
  restore) cmd_restore ;;
  help)
    printf 'Usage: getdomains.sh [run|restore|help] [--no-color] [-v|-vv|-vvv]\n'
    printf '  run      download lists, cache, and apply to system (default)\n'
    printf '  restore  apply from existing cache without downloading (for boot)\n'
    ;;
  *)
    printf 'Unknown command: %s\n' "$CMD" >&2
    exit 1
    ;;
esac
