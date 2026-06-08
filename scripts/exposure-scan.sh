#!/usr/bin/env bash
#
# exposure-scan.sh — reject real personal identifiers in tracked files.
#
# muxray's fixtures and tests are derived from real terminal captures, which can
# carry a real `user@host` prompt, a personal email, or a private path. This is a
# PUBLIC, world-readable repo, so a real identifier pasted from a capture is an
# exposure. The failure this guards against actually happened: a real `user@host`
# was committed inline in internal/program/shell_test.go (NOT under testdata/) and
# only later swapped for a synthetic one — so this scans ALL tracked text, not just
# the fixture tree.
#
# Strategy is allowlist-by-shape, so no real hostname is ever written into this
# (public) script: every login@host / email token in the tree is matched, then
# cleared only if its HOST segment is a known synthetic placeholder (host, vm,
# example.com, ...) or the token is a recognized tooling ref (an action pin like
# `@v4`, a module `@latest`, or `git@github.com`). Anything else — e.g. a login
# against a real machine name — is reported and fails the scan.
#
# Offline and deterministic. Runs in CI on every push/PR and as part of `make
# lint`; also usable as a local pre-commit hook (see .githooks/pre-commit).
#
# Usage:
#   scripts/exposure-scan.sh         # scan the tracked tree
#   make exposure-scan

set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

# Synthetic placeholder hosts a sanitized prompt/email is allowed to use.
# SYNTHETIC ONLY — never add a real machine name or real mail domain here; doing
# so would both defeat the scan and write the very identifier we are scrubbing
# into a public file. Sanitize captures to one of these instead.
ALLOWED_HOSTS="host vm localhost example example.com example.net example.org muxray test invalid"

# login@host / email shape. login starts with a letter or underscore (a username
# or local-part), so bare "v4"-style version refs are not mistaken for logins.
PATTERN='[A-Za-z_][A-Za-z0-9_.+-]*@[A-Za-z0-9][A-Za-z0-9.-]*'

is_allowed() {
  local tok="$1" host="${tok##*@}"
  # Recognized non-identifier tooling refs.
  case "$tok" in
    git@github.com) return 0 ;;
  esac
  # GitHub Action pins (uses: foo@v4) and Go module pseudo-refs (pkg@latest).
  case "$host" in
    v[0-9]*|latest|main|master|HEAD) return 0 ;;
  esac
  # Synthetic placeholder hosts.
  local h
  for h in $ALLOWED_HOSTS; do
    [ "$host" = "$h" ] && return 0
  done
  return 1
}

findings=0
# -I skips binary files; -o emits one match per line as `path:line:token`. The
# scanner excludes itself (its allowlist/comments are not findings) and go.sum.
while IFS= read -r m; do
  [ -z "$m" ] && continue
  path="${m%%:*}"; rest="${m#*:}"; lineno="${rest%%:*}"; tok="${rest#*:}"
  is_allowed "$tok" && continue
  if [ "$findings" -eq 0 ]; then
    echo "exposure-scan: FAIL — real-looking identifier(s) in tracked files:" >&2
  fi
  echo "  $path:$lineno: $tok" >&2
  findings=$((findings + 1))
done < <(git grep -nIoE "$PATTERN" -- . ':(exclude)scripts/exposure-scan.sh' ':(exclude)go.sum' || true)

if [ "$findings" -ne 0 ]; then
  {
    echo "exposure-scan: $findings finding(s)."
    echo "If this is a real user@host / email / domain pasted from a capture,"
    echo "sanitize it to a synthetic placeholder (e.g. dev@host, user@example.com)."
    echo "If it is a legitimate synthetic identifier, add its HOST to ALLOWED_HOSTS"
    echo "in scripts/exposure-scan.sh — never add a real machine name or mail domain."
  } >&2
  exit 1
fi

echo "exposure-scan: ok — no real-looking identifiers in tracked files."
