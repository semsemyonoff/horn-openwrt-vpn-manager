#!/bin/sh
# Verifies that a release tag matches PKG_VERSION in both package Makefiles.
# The tag is what the workflow builds and names artifacts after, while
# PKG_VERSION is what ends up in the package metadata on the router — a mismatch
# ships a package whose version lies about which release it came from.
#
# Usage: check-release-version.sh <tag>   (tag with or without the leading "v")
set -eu

TAG="${1:-}"
if [ -z "$TAG" ]; then
  echo "usage: $0 <tag>" >&2
  exit 2
fi

VERSION="${TAG#v}"
STATUS=0

for MK in horn-vpn-manager/Makefile horn-vpn-manager-luci/Makefile; do
  PKG=$(sed -n 's/^PKG_VERSION[[:space:]]*:=[[:space:]]*//p' "$MK")
  if [ -z "$PKG" ]; then
    echo "error: $MK declares no PKG_VERSION" >&2
    STATUS=1
  elif [ "$PKG" != "$VERSION" ]; then
    echo "error: $MK has PKG_VERSION $PKG, tag $TAG expects $VERSION" >&2
    STATUS=1
  fi
done

[ "$STATUS" -eq 0 ] && echo "PKG_VERSION $VERSION matches tag $TAG"
exit "$STATUS"
