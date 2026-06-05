#!/usr/bin/env bash
#
# build-release.sh [VERSION] — cross-compile muxray release archives into dist/.
# Produces one .tar.gz per platform plus a checksums.txt. Zero CGO so the
# binaries are static and portable.

set -euo pipefail

VERSION="${1:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="$(git rev-parse --short HEAD 2>/dev/null || echo none)"
DATE="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
PKG="github.com/dandriscoll/muxray/internal/version"
LDFLAGS="-s -w -X ${PKG}.Version=${VERSION} -X ${PKG}.Commit=${COMMIT} -X ${PKG}.Date=${DATE}"

DIST="dist"
rm -rf "$DIST"
mkdir -p "$DIST"

platforms=("linux/amd64" "linux/arm64" "darwin/amd64" "darwin/arm64")
for p in "${platforms[@]}"; do
  os="${p%/*}"; arch="${p#*/}"
  stage="$DIST/muxray_${os}_${arch}"
  mkdir -p "$stage"
  echo "building ${os}/${arch}..."
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
    go build -trimpath -ldflags "$LDFLAGS" -o "$stage/muxray" ./cmd/muxray
  cp README.md LICENSE "$stage/" 2>/dev/null || true
  tar -C "$DIST" -czf "$DIST/muxray_${VERSION}_${os}_${arch}.tar.gz" "muxray_${os}_${arch}"
  rm -rf "$stage"
done

(
  cd "$DIST"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum ./*.tar.gz > checksums.txt
  else
    shasum -a 256 ./*.tar.gz > checksums.txt
  fi
)

echo "artifacts in $DIST:"
ls -1 "$DIST"
