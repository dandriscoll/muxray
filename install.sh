#!/usr/bin/env sh
#
# muxray installer — download the latest release binary for this OS/arch and
# install it. Usage:
#
#   curl -fsSL https://raw.githubusercontent.com/dandriscoll/muxray/main/install.sh | sh
#
# Environment overrides:
#   MUXRAY_VERSION=v1.2.3        install a specific version (default: latest)
#   MUXRAY_INSTALL_DIR=/path     install location (default: /usr/local/bin or ~/.local/bin)

set -eu

REPO="dandriscoll/muxray"
BIN="muxray"

os="$(uname -s | tr '[:upper:]' '[:lower:]')"
arch="$(uname -m)"
case "$arch" in
  x86_64|amd64) arch="amd64" ;;
  arm64|aarch64) arch="arm64" ;;
  *) echo "muxray: unsupported architecture: $arch" >&2; exit 1 ;;
esac
case "$os" in
  linux|darwin) ;;
  *) echo "muxray: unsupported OS: $os (muxray needs tmux; Linux and macOS only)" >&2; exit 1 ;;
esac

version="${MUXRAY_VERSION:-latest}"
if [ "$version" = "latest" ]; then
  version="$(curl -fsSL "https://api.github.com/repos/$REPO/releases/latest" \
    | grep -o '"tag_name"[ ]*:[ ]*"[^"]*"' | head -1 | sed 's/.*"tag_name"[ ]*:[ ]*"//;s/"//')"
fi
if [ -z "$version" ]; then
  echo "muxray: could not determine the latest version; set MUXRAY_VERSION explicitly" >&2
  exit 1
fi

asset="muxray_${version}_${os}_${arch}.tar.gz"
base="https://github.com/$REPO/releases/download/$version"

tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo "muxray: downloading $version ($os/$arch)..."
curl -fsSL "$base/$asset" -o "$tmp/$asset"

# Verify the checksum when checksums.txt is available.
if curl -fsSL "$base/checksums.txt" -o "$tmp/checksums.txt" 2>/dev/null; then
  ( cd "$tmp" && grep " *$asset\$" checksums.txt > expected.txt \
    && { sha256sum -c expected.txt 2>/dev/null || shasum -a 256 -c expected.txt ; } ) \
    || { echo "muxray: checksum verification failed" >&2; exit 1; }
  echo "muxray: checksum verified"
fi

tar -C "$tmp" -xzf "$tmp/$asset"

install_dir="${MUXRAY_INSTALL_DIR:-}"
if [ -z "$install_dir" ]; then
  if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    install_dir="/usr/local/bin"
  else
    install_dir="$HOME/.local/bin"
  fi
fi
mkdir -p "$install_dir"
install -m 0755 "$tmp/muxray_${os}_${arch}/muxray" "$install_dir/$BIN"

echo "muxray: installed to $install_dir/$BIN"
case ":$PATH:" in
  *":$install_dir:"*) ;;
  *) echo "muxray: add $install_dir to your PATH to use 'muxray'" ;;
esac
"$install_dir/$BIN" version 2>/dev/null || true
