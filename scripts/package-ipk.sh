#!/bin/sh
# Creates an OpenWrt .ipk package from a pre-built binary and package files.
# No SDK required — produces a valid opkg-installable archive.
#
# Usage: package-ipk.sh <binary> <files-dir> <output-dir>
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

WORK=$(TMPDIR=/tmp mktemp -d)
trap 'rm -rf "$WORK"' EXIT

# ── data.tar.gz ──────────────────────────────────────────────
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

(cd "$DATA" && tar czf "$WORK/data.tar.gz" .)

# ── control.tar.gz ───────────────────────────────────────────
CTRL="$WORK/control"
mkdir -p "$CTRL"

cat > "$CTRL/control" <<EOF
Package: ${PKG_NAME}
Version: ${PKG_VERSION}-${PKG_RELEASE}
Architecture: ${PKG_ARCH}
Section: net
Priority: optional
Maintainer: horn
Description: VPN subscription manager for sing-box on OpenWrt.
 Downloads VLESS URIs, generates sing-box config with per-subscription
 outbound groups and routing rules. Includes domain/IP list management
 for dnsmasq-based VPN routing.
EOF

cat > "$CTRL/conffiles" <<EOF
/etc/horn-vpn-manager/config.json
/etc/horn-vpn-manager/lists/manual-ip.lst
EOF

(cd "$CTRL" && tar czf "$WORK/control.tar.gz" .)

# ── assemble .ipk (ar archive) ──────────────────────────────
echo "2.0" > "$WORK/debian-binary"

if [ -n "$PKG_PLATFORM" ]; then
  IPK="${OUTPUT_DIR}/${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_${PKG_PLATFORM}.ipk"
else
  IPK="${OUTPUT_DIR}/${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_${PKG_ARCH}.ipk"
fi

(cd "$WORK" && ar rc "$IPK" debian-binary control.tar.gz data.tar.gz)

echo ">> Created: $IPK"
ls -lh "$IPK"
