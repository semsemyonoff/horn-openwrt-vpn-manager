#!/bin/sh
# Creates an OpenWrt .apk package from a pre-built binary and package files.
# Uses a lightweight Alpine container with `apk mkpkg` for correct APK v3 format.
# Install on device: apk add --allow-untrusted <file>.apk
#
# Usage: package-apk.sh <binary> <files-dir> <output-dir>
#
# Environment:
#   PKG_VERSION   — package version   (default: 2.0.0)
#   PKG_RELEASE   — package release   (default: 1)
#   PKG_ARCH      — target arch       (default: aarch64_cortex-a53)
#   PKG_PLATFORM  — platform label for filename (e.g. linux-arm64)
set -eu

PKG_NAME="horn-vpn-manager"
PKG_VERSION="${PKG_VERSION:-2.0.0}"
PKG_RELEASE="${PKG_RELEASE:-1}"
PKG_ARCH="${PKG_ARCH:-aarch64_cortex-a53}"
PKG_PLATFORM="${PKG_PLATFORM:-}"

BINARY="$1"
FILES_DIR="$2"
OUTPUT_DIR="$3"

# Use /tmp for macOS Docker volume compatibility (mktemp default uses /var/folders which Docker can't mount)
WORK=$(TMPDIR=/tmp mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ── build data directory ─────────────────────────────────────
DATA="$WORK/data"

mkdir -p "$DATA/usr/bin"
cp "$BINARY" "$DATA/usr/bin/vpn-manager"
chmod 755 "$DATA/usr/bin/vpn-manager"

mkdir -p "$DATA/usr/share/horn-vpn-manager"
cp "$FILES_DIR/sing-box.template.default.json" "$DATA/usr/share/horn-vpn-manager/sing-box.template.json"
cp "$FILES_DIR/config.example.json"             "$DATA/usr/share/horn-vpn-manager/"

mkdir -p "$DATA/etc/horn-vpn-manager/lists"

mkdir -p "$DATA/etc/init.d"
cp "$FILES_DIR/horn-vpn-manager.init" "$DATA/etc/init.d/horn-vpn-manager"
chmod 755 "$DATA/etc/init.d/horn-vpn-manager"

# ── package via apk mkpkg ───────────────────────────────────
if [ -n "$PKG_PLATFORM" ]; then
  APK_FILE="${PKG_NAME}-${PKG_VERSION}-r${PKG_RELEASE}-${PKG_PLATFORM}.apk"
else
  APK_FILE="${PKG_NAME}-${PKG_VERSION}-r${PKG_RELEASE}.apk"
fi

docker run --rm \
  -v "$WORK:/pkg" \
  alpine:latest \
  apk mkpkg \
    --info "name:${PKG_NAME}" \
    --info "version:${PKG_VERSION}-r${PKG_RELEASE}" \
    --info "arch:${PKG_ARCH}" \
    --info "description:VPN subscription manager for sing-box on OpenWrt" \
    --info "license:GPL-2.0" \
    --info "origin:${PKG_NAME}" \
    --info "maintainer:horn" \
    --files "/pkg/data" \
    --output "/pkg/${APK_FILE}"

cp "$WORK/$APK_FILE" "$OUTPUT_DIR/"

echo ">> Created: $OUTPUT_DIR/$APK_FILE"
ls -lh "$OUTPUT_DIR/$APK_FILE"
