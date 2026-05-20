#!/usr/bin/env bash
set -euo pipefail

REPO="jackiesre721/mydml"
LATEST=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | grep '"tag_name"' | head -1 | sed -E 's/.*"v?([^"]+)".*/\1/')

if [ -z "$LATEST" ]; then
  echo "Error: could not determine latest version" >&2
  exit 1
fi

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"
[ "$ARCH" = "x86_64" ] && ARCH="amd64"
[ "$ARCH" = "aarch64" ] && ARCH="arm64"

FILENAME="mydml_${OS}_${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/v${LATEST}/${FILENAME}"

TMPDIR=$(mktemp -d)
trap 'rm -rf "$TMPDIR"' EXIT

echo "Installing mydml v${LATEST} (${OS}/${ARCH})..."
curl -fsSL "$URL" | tar xz -C "$TMPDIR"

BINDIR="${MYDML_BINDIR:-/usr/local/bin}"
if [ ! -w "$BINDIR" ]; then
  BINDIR="$HOME/.local/bin"
  mkdir -p "$BINDIR"
fi

install -m 755 "$TMPDIR/mydml" "$BINDIR/mydml"

echo "Installed mydml to $BINDIR/mydml"
echo "  $(mydml --help 2>&1 | head -1 || echo "mydml v${LATEST}")"
