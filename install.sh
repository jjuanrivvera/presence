#!/bin/sh
# install.sh — install the presence binary from a GitHub release
# (checksum-verified, no Go toolchain required).
#
# Usage:
#   ./install.sh                 # latest release
#   ./install.sh v0.2.0          # specific version
#   PRESENCE_INSTALL_DIR=/usr/local/bin ./install.sh
set -eu

REPO="jjuanrivvera/presence"
INSTALL_DIR="${PRESENCE_INSTALL_DIR:-$HOME/.local/bin}"
VERSION="${1:-}"

err() { printf 'install.sh: %s\n' "$*" >&2; exit 1; }

# --- resolve version ---------------------------------------------------------
if [ -z "$VERSION" ]; then
  VERSION=$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep '"tag_name"' | head -1 | cut -d'"' -f4) || true
  [ -n "$VERSION" ] || err "could not resolve latest release; pass a version explicitly"
fi
VERSION_NO_V="${VERSION#v}"

# --- detect platform ---------------------------------------------------------
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
case "$OS" in
  darwin|linux) ;;
  *) err "unsupported OS: $OS" ;;
esac

ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  arm64|aarch64) ARCH=arm64 ;;
  *) err "unsupported arch: $ARCH" ;;
esac

ASSET="presence_${VERSION_NO_V}_${OS}_${ARCH}.tar.gz"
BASE_URL="https://github.com/$REPO/releases/download/$VERSION"

# --- download + verify checksum ----------------------------------------------
TMP=$(mktemp -d)
trap 'rm -rf "$TMP"' EXIT

echo "Downloading $ASSET ($VERSION)..."
curl -fsSL -o "$TMP/$ASSET" "$BASE_URL/$ASSET" || err "download failed: $BASE_URL/$ASSET"
curl -fsSL -o "$TMP/checksums.txt" "$BASE_URL/checksums.txt" || err "checksums.txt download failed"

echo "Verifying checksum..."
EXPECTED=$(grep " $ASSET\$" "$TMP/checksums.txt" | cut -d' ' -f1)
[ -n "$EXPECTED" ] || err "no checksum entry for $ASSET"
if command -v sha256sum >/dev/null 2>&1; then
  ACTUAL=$(sha256sum "$TMP/$ASSET" | cut -d' ' -f1)
else
  ACTUAL=$(shasum -a 256 "$TMP/$ASSET" | cut -d' ' -f1)
fi
[ "$EXPECTED" = "$ACTUAL" ] || err "checksum mismatch: expected $EXPECTED got $ACTUAL"

# --- install -----------------------------------------------------------------
tar -xzf "$TMP/$ASSET" -C "$TMP"
mkdir -p "$INSTALL_DIR"
install -m 0755 "$TMP/presence" "$INSTALL_DIR/presence"

echo "Installed $("$INSTALL_DIR/presence" version) to $INSTALL_DIR/presence"
case ":$PATH:" in
  *":$INSTALL_DIR:"*) ;;
  *) echo "NOTE: $INSTALL_DIR is not in your PATH" ;;
esac
