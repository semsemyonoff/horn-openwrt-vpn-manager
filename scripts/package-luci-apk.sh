#!/bin/sh
# Creates an OpenWrt .apk package for horn-vpn-manager-luci from source files.
# No SDK required — the package is arch-independent (PKGARCH=all).
# Install on device: apk add --allow-untrusted <file>.apk
#
# Usage: package-luci-apk.sh <luci-src-dir> <output-dir>
#
# Environment:
#   PKG_VERSION  — package version (default: 2.0.0)
#   PKG_RELEASE  — package release (default: 1)
set -eu

PKG_NAME="horn-vpn-manager-luci"
PKG_VERSION="${PKG_VERSION:-2.0.0}"
PKG_RELEASE="${PKG_RELEASE:-1}"

SRC_DIR="$1"
OUTPUT_DIR="$2"

WORK=$(TMPDIR=/tmp mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ── compile translations ────────────────────────────────────
I18N="$WORK/i18n"
mkdir -p "$I18N"
python3 "$SRC_DIR/tools/po2lmo.py" "$SRC_DIR/po/en/horn-vpn-manager.po" "$I18N/horn-vpn-manager.en.lmo"
python3 "$SRC_DIR/tools/po2lmo.py" "$SRC_DIR/po/ru/horn-vpn-manager.po" "$I18N/horn-vpn-manager.ru.lmo"

# ── build data directory ────────────────────────────────────
DATA="$WORK/data"

# Copy root overlay (static files, rpcd backend, ACL, menu)
cp -a "$SRC_DIR/root/." "$DATA/"
chmod 755 "$DATA/usr/libexec/rpcd/horn-vpn-manager"

# Install compiled translations
mkdir -p "$DATA/usr/lib/lua/luci/i18n"
cp "$I18N"/*.lmo "$DATA/usr/lib/lua/luci/i18n/"

# ── package via apk mkpkg ──────────────────────────────────
APK_FILE="${PKG_NAME}-${PKG_VERSION}-r${PKG_RELEASE}.apk"

docker run --rm \
  -v "$WORK:/pkg" \
  alpine:latest \
  apk mkpkg \
    --info "name:${PKG_NAME}" \
    --info "version:${PKG_VERSION}-r${PKG_RELEASE}" \
    --info "arch:all" \
    --info "description:LuCI interface for horn-vpn-manager" \
    --info "license:GPL-2.0" \
    --info "origin:${PKG_NAME}" \
    --info "maintainer:horn" \
    --files "/pkg/data" \
    --output "/pkg/${APK_FILE}"

cp "$WORK/$APK_FILE" "$OUTPUT_DIR/"

echo ">> Created: $OUTPUT_DIR/$APK_FILE"
ls -lh "$OUTPUT_DIR/$APK_FILE"
