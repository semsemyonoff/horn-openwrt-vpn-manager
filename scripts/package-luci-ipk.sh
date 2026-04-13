#!/bin/sh
# Creates an OpenWrt .ipk package for horn-vpn-manager-luci from source files.
# No SDK required — the package is arch-independent (PKGARCH=all).
#
# Usage: package-luci-ipk.sh <luci-src-dir> <output-dir>
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

# ── data.tar.gz ─────────────────────────────────────────────
DATA="$WORK/data"

# Copy root overlay
cp -a "$SRC_DIR/root/." "$DATA/"
chmod 755 "$DATA/usr/libexec/rpcd/horn-vpn-manager"

# Install compiled translations
mkdir -p "$DATA/usr/lib/lua/luci/i18n"
cp "$I18N"/*.lmo "$DATA/usr/lib/lua/luci/i18n/"

(cd "$DATA" && tar czf "$WORK/data.tar.gz" .)

# ── control.tar.gz ──────────────────────────────────────────
CTRL="$WORK/control"
mkdir -p "$CTRL"

cat > "$CTRL/control" <<EOF
Package: ${PKG_NAME}
Version: ${PKG_VERSION}-${PKG_RELEASE}
Architecture: all
Section: luci
Priority: optional
Maintainer: horn
Description: LuCI interface for horn-vpn-manager.
EOF

cat > "$CTRL/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] || /etc/init.d/rpcd restart
EOF
chmod 755 "$CTRL/postinst"

(cd "$CTRL" && tar czf "$WORK/control.tar.gz" .)

# ── assemble .ipk ───────────────────────────────────────────
echo "2.0" > "$WORK/debian-binary"

OUTPUT_DIR="$(cd "$OUTPUT_DIR" && pwd)"
IPK="${OUTPUT_DIR}/${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_all.ipk"

(cd "$WORK" && ar rc "$IPK" debian-binary control.tar.gz data.tar.gz)

echo ">> Created: $IPK"
ls -lh "$IPK"
