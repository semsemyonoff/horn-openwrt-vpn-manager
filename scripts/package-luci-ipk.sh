#!/bin/sh
# Creates an OpenWrt .ipk package for horn-vpn-manager-luci from source files.
# No SDK required — the package is arch-independent (PKGARCH=all).
# Install on device: opkg install <file>.ipk
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

# ── stage data tree ─────────────────────────────────────────
DATA="$WORK/data"
mkdir -p "$DATA"
cp -a "$SRC_DIR/root/." "$DATA/"
chmod 755 "$DATA/usr/libexec/rpcd/horn-vpn-manager"

mkdir -p "$DATA/usr/lib/lua/luci/i18n"
cp "$I18N"/*.lmo "$DATA/usr/lib/lua/luci/i18n/"

# ── stage control tree ──────────────────────────────────────
CTRL="$WORK/control"
mkdir -p "$CTRL"

cat > "$CTRL/control" <<EOF
Package: ${PKG_NAME}
Version: ${PKG_VERSION}-${PKG_RELEASE}
Architecture: all
Section: luci
Priority: optional
Maintainer: horn
Depends: horn-vpn-manager
Description: LuCI interface for horn-vpn-manager.
EOF

cat > "$CTRL/postinst" <<'EOF'
#!/bin/sh
[ -n "${IPKG_INSTROOT}" ] || /etc/init.d/rpcd restart
EOF
chmod 755 "$CTRL/postinst"

echo "2.0" > "$WORK/debian-binary"

# ── assemble tarballs and .ipk inside Alpine ────────────────
# GNU tar avoids macOS resource forks; GNU ar produces the SysV format opkg expects.
IPK_NAME="${PKG_NAME}_${PKG_VERSION}-${PKG_RELEASE}_all.ipk"

docker run --rm \
  -v "$WORK:/pkg" \
  -w /pkg \
  alpine:latest \
  sh -c '
    set -eu
    apk add --no-cache binutils >/dev/null
    # See package-ipk.sh: re-stage under root ownership so the .ipk does not
    # carry the uid of the building account onto the router.
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
