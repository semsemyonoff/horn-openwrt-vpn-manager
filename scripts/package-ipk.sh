#!/bin/sh
# Creates an OpenWrt .ipk package from a pre-built binary and package files.
# No SDK required — produces a valid opkg-installable archive for OpenWrt < 25.
# Install on device: opkg install <file>.ipk
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

# ── stage data tree ──────────────────────────────────────────
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

# ── stage control tree ───────────────────────────────────────
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

echo "2.0" > "$WORK/debian-binary"

# ── assemble tarballs and .ipk inside Alpine ────────────────
# GNU tar avoids macOS resource forks; GNU ar produces the SysV format opkg expects.
if [ -n "$PKG_PLATFORM" ]; then
  IPK_NAME="${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_${PKG_PLATFORM}.ipk"
else
  IPK_NAME="${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_${PKG_ARCH}.ipk"
fi

docker run --rm \
  -v "$WORK:/pkg" \
  -w /pkg \
  alpine:latest \
  sh -c '
    set -eu
    apk add --no-cache binutils >/dev/null
    # Both trees come from a bind mount owned by the building account; tar would
    # record that uid and opkg would unpack a nobody-owned /usr/bin/vpn-manager.
    # Re-stage where root owns everything instead of chowning the mount, which
    # the cleanup trap on the host could no longer remove.
    # (No apostrophes here — this block is a single-quoted shell string.)
    cp -a /pkg/data /tmp/data
    cp -a /pkg/control /tmp/control
    chown -R 0:0 /tmp/data /tmp/control
    (cd /tmp/data    && tar -czf /pkg/data.tar.gz .)
    (cd /tmp/control && tar -czf /pkg/control.tar.gz .)
    ar rc /pkg/'"$IPK_NAME"' debian-binary control.tar.gz data.tar.gz
  '

cp "$WORK/$IPK_NAME" "$OUTPUT_DIR/"

echo ">> Created: $OUTPUT_DIR/$IPK_NAME"
ls -lh "$OUTPUT_DIR/$IPK_NAME"
