#!/usr/bin/env bash
# Install the `brainstorm` CLI by downloading a prebuilt binary from the latest
# GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/NosFabrica/brainstorm_one_click_deployment/main/install.sh | bash
#
# Env overrides:
#   PREFIX=/usr/local        install dir is $PREFIX/bin
#   BRAINSTORM_VERSION=v0.1.0 pin a specific release (default: latest)
set -euo pipefail

REPO="NosFabrica/brainstorm_one_click_deployment"
PREFIX="${PREFIX:-/usr/local}"
BIN_DIR="$PREFIX/bin"
VERSION="${BRAINSTORM_VERSION:-latest}"

# --- detect platform ---------------------------------------------------------
os="$(uname -s)"
arch="$(uname -m)"
case "$os" in
  Linux)  goos="linux" ;;
  Darwin) goos="darwin" ;;
  *) echo "error: unsupported OS '$os'"; exit 1 ;;
esac
case "$arch" in
  x86_64|amd64) goarch="amd64" ;;
  arm64|aarch64) goarch="arm64" ;;
  *) echo "error: unsupported arch '$arch'"; exit 1 ;;
esac

# --- resolve version ---------------------------------------------------------
if [ "$VERSION" = "latest" ]; then
  VERSION="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)"
  [ -n "$VERSION" ] || { echo "error: could not determine latest release"; exit 1; }
fi

asset="brainstorm_${VERSION}_${goos}_${goarch}.tar.gz"
url="https://github.com/$REPO/releases/download/$VERSION/$asset"

echo "installing brainstorm $VERSION ($goos/$goarch)"
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

if ! curl -fSL -o "$tmp/$asset" "$url"; then
  echo "error: no prebuilt binary at $url"
  echo "       (check the release assets, or build from source with: make install)"
  exit 1
fi

tar -C "$tmp" -xzf "$tmp/$asset"
bin="$(find "$tmp" -type f -name brainstorm | head -n1)"
[ -n "$bin" ] || { echo "error: brainstorm binary not found in archive"; exit 1; }

dest="$BIN_DIR/brainstorm"
if [ -w "$BIN_DIR" ]; then
  install -m 0755 "$bin" "$dest"
else
  echo "installing to $dest (needs sudo)"
  sudo install -m 0755 "$bin" "$dest"
fi

echo "done -> $dest"
"$dest" --version || true
echo "run: brainstorm start"
