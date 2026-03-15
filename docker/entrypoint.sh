#!/bin/bash
set -euo pipefail

SDK_DIR="/home/builder/sdk"
SRC_CORE="/src/horn-vpn-manager"
SRC_LUCI="/src/horn-vpn-manager-luci"
PKG_CORE="${SDK_DIR}/package/horn-vpn-manager"
PKG_LUCI="${SDK_DIR}/package/horn-vpn-manager-luci"
OUT_DIR="/out"

# Sync source into SDK package directories
sync_sources() {
  if [ -d "$SRC_CORE" ]; then
    rsync -a --delete "$SRC_CORE/" "$PKG_CORE/"
    echo ">> Synced horn-vpn-manager source"
  fi
  if [ -d "$SRC_LUCI" ]; then
    rsync -a --delete "$SRC_LUCI/" "$PKG_LUCI/"
    echo ">> Synced horn-vpn-manager-luci source"
  fi
}

# Run defconfig (always re-run after source sync to pick up Makefile changes)
setup_config() {
  cd "$SDK_DIR"
  echo ">> Running make defconfig..."
  make defconfig 2>&1
  echo ">> defconfig done"
}

# Build a single package
build_package() {
  local pkg="$1"
  echo ">> Building ${pkg}..."
  cd "$SDK_DIR"
  make "package/${pkg}/compile" V=s -j"$(nproc)" 2>&1
  echo ">> ${pkg} built successfully"
}

# Copy built packages to output
collect_output() {
  if [ -d "$OUT_DIR" ]; then
    echo ">> Collecting packages to /out..."
    find "${SDK_DIR}/bin" -name 'horn-vpn-manager*.apk' -exec cp -v {} "$OUT_DIR/" \; 2>/dev/null || true
    find "${SDK_DIR}/bin" -name 'horn-vpn-manager*.ipk' -exec cp -v {} "$OUT_DIR/" \; 2>/dev/null || true
    echo ">> Done. Packages in /out:"
    ls -lh "$OUT_DIR"/horn-vpn-manager* 2>/dev/null || echo "   (no packages found)"
  fi
}

case "${1:-all}" in
  all)
    sync_sources
    setup_config
    build_package horn-vpn-manager
    build_package horn-vpn-manager-luci
    collect_output
    ;;
  core)
    sync_sources
    setup_config
    build_package horn-vpn-manager
    collect_output
    ;;
  luci)
    sync_sources
    setup_config
    build_package horn-vpn-manager-luci
    collect_output
    ;;
  shell)
    sync_sources
    echo ">> SDK shell — packages synced to ${PKG_CORE} and ${PKG_LUCI}"
    echo ">> Build with: make package/horn-vpn-manager/compile V=s"
    exec /bin/bash
    ;;
  *)
    echo "Usage: entrypoint.sh {all|core|luci|shell}"
    exit 1
    ;;
esac
