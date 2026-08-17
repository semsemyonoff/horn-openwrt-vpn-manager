#!/bin/sh
# Fails when a built package carries files owned by anything but root.
#
# The packaging scripts stage their payload in a bind-mounted temp dir, so the
# uid of whoever ran the build is what lands in the archive unless the payload is
# re-staged under root inside the container. That uid means nothing on the
# router: OpenWrt installed /usr/bin/vpn-manager as nobody:nogroup once already.
#
# Usage: check-package-ownership.sh [dir]   (default: bin)
set -eu

DIR="${1:-bin}"
[ -d "$DIR" ] || { echo "error: $DIR is not a directory" >&2; exit 2; }
ABS_DIR=$(cd "$DIR" && pwd)

STATUS=0
FOUND=0

# .ipk is ar(1) wrapping tarballs, so it reads on the host.
for f in "$ABS_DIR"/*.ipk; do
  [ -e "$f" ] || continue
  command -v ar >/dev/null 2>&1 || { echo "error: ar (binutils) is required to read .ipk" >&2; exit 2; }
  FOUND=$((FOUND + 1))
  BAD=$(ar p "$f" data.tar.gz | tar tzvf - | grep -v " root/root " || true)
  if [ -n "$BAD" ]; then
    echo "FAIL $(basename "$f") — entries not owned by root:"
    echo "$BAD" | sed 's|^|  |'
    STATUS=1
  else
    echo "ok   $(basename "$f")"
  fi
done

# APKv3 is not a tar, so only apk-tools can unpack it — hence the container.
# It runs offline: the package is a local file and no index is needed.
if [ -n "$(find "$ABS_DIR" -maxdepth 1 -name '*.apk' -print -quit)" ]; then
  FOUND=$((FOUND + 1))
  docker run --rm --network none -v "$ABS_DIR:/pkg:ro" alpine:latest sh -c '
    set -eu
    status=0
    for f in /pkg/*.apk; do
      [ -e "$f" ] || continue
      d=$(mktemp -d)
      apk extract --allow-untrusted --destination "$d" "$f" >/dev/null
      bad=$(find "$d" \( ! -user root -o ! -group root \) -print)
      if [ -n "$bad" ]; then
        echo "FAIL ${f#/pkg/} — entries not owned by root:"
        echo "$bad" | sed "s|$d|  |"
        status=1
      else
        echo "ok   ${f#/pkg/}"
      fi
    done
    exit $status
  ' || STATUS=1
fi

# An empty directory must not read as a pass: the caller believes it just
# checked the packages it built.
if [ "$FOUND" -eq 0 ]; then
  echo "error: no .apk or .ipk found in $DIR" >&2
  exit 2
fi

exit "$STATUS"
