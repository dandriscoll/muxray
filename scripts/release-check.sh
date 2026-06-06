#!/usr/bin/env bash
#
# release-check.sh [VERSION] [--online] — pre-release gate (issue #6).
#
# Validates that muxray's advertised `curl | sh` install path is internally
# consistent BEFORE a release is cut, so an install can't silently break:
#   - the README/install.sh curl URL points at an installer that exists,
#   - install.sh's download/extract names agree with what build-release.sh
#     actually produces (the real 404/extract-failure vector),
#   - every OS/arch install.sh advertises is built by build-release.sh,
#   - VERSION is a vX.Y.Z tag (the shape release.yml triggers on),
#   - a binary built with VERSION reports VERSION (the ldflags version-constant
#     injection the release relies on actually works).
#
# These are OFFLINE and deterministic — safe to run in CI and before tagging.
# With --online (the "step zero of announcement" smoke, run only AFTER the tag
# is pushed and the release built) it also confirms the GitHub release exists and
# that a real curl|sh install into a temp dir runs. --online touches the network;
# the offline gate never does.
#
# Usage:
#   scripts/release-check.sh v0.4.0
#   scripts/release-check.sh v0.4.0 --online
#   make release-check VERSION=v0.4.0

set -euo pipefail

ONLINE=0
VERSION=""
for a in "$@"; do
  case "$a" in
    --online) ONLINE=1 ;;
    --*) echo "release-check: unknown flag: $a" >&2; exit 2 ;;
    *) [ -z "$VERSION" ] && VERSION="$a" ;;
  esac
done
if [ -z "$VERSION" ]; then
  VERSION="$(git describe --tags --always --dirty 2>/dev/null || echo dev)"
fi

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

CANON_URL="https://raw.githubusercontent.com/dandriscoll/muxray/main/install.sh"
fail=0
err() { echo "release-check: FAIL: $*" >&2; fail=1; }
ok()  { echo "release-check: ok:   $*"; }

# 1) The curl URL is documented and the file it serves exists.
[ -f install.sh ] || err "install.sh (the file the curl URL serves) is missing"
grep -qF "$CANON_URL" README.md || err "README is missing the canonical curl URL: $CANON_URL"
grep -qF "$CANON_URL" install.sh || err "install.sh header URL does not match the canonical curl URL"
[ "$fail" = 0 ] && ok "curl URL documented and install.sh present"

# 2) install.sh's download/extract names agree with build-release.sh's outputs.
#    These literals are the contract between installer and builder; if either side
#    edits its naming alone, curl|sh 404s or fails to extract.
br="scripts/build-release.sh"
grep -qF 'muxray_${version}_${os}_${arch}.tar.gz' install.sh \
  || err "install.sh download asset name changed (expected muxray_\${version}_\${os}_\${arch}.tar.gz)"
grep -qF 'muxray_${VERSION}_${os}_${arch}.tar.gz' "$br" \
  || err "build-release.sh tarball name changed (must match install.sh's download name)"
grep -qF 'muxray_${os}_${arch}/muxray' install.sh \
  || err "install.sh extract path changed (expected muxray_\${os}_\${arch}/muxray)"
grep -qF 'muxray_${os}_${arch}' "$br" \
  || err "build-release.sh stage dir changed (must match install.sh's extract path)"
ok "install.sh and build-release.sh artifact names agree"

# 3) Every OS/arch install.sh accepts is actually built.
for plat in linux/amd64 linux/arm64 darwin/amd64 darwin/arm64; do
  grep -qF "\"$plat\"" "$br" || err "build-release.sh no longer builds $plat (install.sh advertises it)"
done
ok "platform matrix covered (linux/darwin x amd64/arm64)"

# 4) VERSION must be a release tag the workflow keys on.
case "$VERSION" in
  v[0-9]*.[0-9]*.[0-9]*) ok "version tag shape: $VERSION" ;;
  *) err "VERSION '$VERSION' is not a vX.Y.Z release tag (release.yml triggers on 'v*'); pass VERSION=vX.Y.Z" ;;
esac

# 5) A binary built with VERSION reports VERSION (ldflags version-constant works).
pkg="github.com/dandriscoll/muxray/internal/version"
tmpd="$(mktemp -d)"
trap 'rm -rf "$tmpd"' EXIT
if go build -ldflags "-X ${pkg}.Version=${VERSION}" -o "$tmpd/muxray" ./cmd/muxray 2>"$tmpd/build.err"; then
  got="$("$tmpd/muxray" version 2>/dev/null || true)"
  case "$got" in
    *"$VERSION"*) ok "binary reports the release version: $got" ;;
    *) err "binary version constant does not report $VERSION (got: ${got:-<none>})" ;;
  esac
else
  err "build failed: $(cat "$tmpd/build.err")"
fi

# 6) Online (post-tag) smoke — NOT part of the offline gate.
if [ "$ONLINE" = 1 ]; then
  if ! command -v gh >/dev/null 2>&1; then
    err "--online requires the gh CLI"
  else
    gh release view "$VERSION" >/dev/null 2>&1 \
      || err "no published GitHub release $VERSION — do not announce before the release exists"
  fi
  d="$(mktemp -d)"
  if MUXRAY_VERSION="$VERSION" MUXRAY_INSTALL_DIR="$d" sh install.sh >/dev/null 2>&1 \
     && "$d/muxray" version 2>/dev/null | grep -qF "$VERSION"; then
    ok "curl|sh install smoke passed for $VERSION"
  else
    err "curl|sh install smoke failed for $VERSION (the advertised install path is broken)"
  fi
  rm -rf "$d"
fi

if [ "$fail" != 0 ]; then
  echo "release-check: FAILED — fix the above before releasing $VERSION." >&2
  exit 1
fi
echo "release-check: PASSED — install path consistent for $VERSION."
