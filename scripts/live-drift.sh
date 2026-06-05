#!/usr/bin/env bash
#
# live-drift.sh — best-effort live drift check for muxray's provider parsers.
#
# For each supported harness (Claude, Codex, Copilot) it drives a minimal real
# flow inside tmux, captures the pane through muxray, and checks that muxray
# detects a plausible provider state. It is intended for the nightly `live-drift`
# CI job, which runs only when the repository is configured with provider access.
#
# Failure classification (the directive's requirement to distinguish a parser
# regression from operational noise):
#
#   PARSER_FAIL      -> muxray failed to classify recognizable provider output.
#                       This is the only condition that fails the job (exit 1).
#   HARNESS_MISSING  -> the provider CLI is not installed. Skipped, exit 0.
#   AUTH_FAIL        -> the provider rejected credentials. Reported, not failed.
#   RATE_LIMITED     -> the provider rate-limited us. Reported, not failed.
#   OUTAGE           -> the provider/network was unreachable. Reported, not failed.
#
# Secrets are never echoed. Artifacts written to drift-artifacts/ contain only
# muxray's structured (content-free / sanitized) output.

set -u

MUXRAY="${MUXRAY:-./muxray}"
ARTIFACTS="drift-artifacts"
mkdir -p "$ARTIFACTS"
SESSION="muxray-drift-$$"
parser_failures=0

log() { printf '%s\n' "$*" ; }

cleanup() { tmux kill-session -t "$SESSION" 2>/dev/null || true ; }
trap cleanup EXIT

if ! command -v tmux >/dev/null 2>&1 ; then
  log "HARNESS_MISSING: tmux not installed; cannot run live drift checks"
  exit 0
fi

# classify_output inspects captured provider output for operational signatures
# before muxray runs, so an outage is not misread as a parser regression.
classify_problem() {
  local text="$1"
  local low; low="$(printf '%s' "$text" | tr '[:upper:]' '[:lower:]')"
  case "$low" in
    *"invalid api key"*|*"unauthorized"*|*"authentication"*|*"please log in"*|*"not logged in"*) echo "AUTH_FAIL" ;;
    *"rate limit"*|*"429"*|*"too many requests"*) echo "RATE_LIMITED" ;;
    *"network"*|*"connection refused"*|*"timeout"*|*"could not reach"*|*"503"*) echo "OUTAGE" ;;
    *) echo "" ;;
  esac
}

# check_provider <name> <cli-binary> <launch-cmd> <expected-substring-of-status>
check_provider() {
  local name="$1" bin="$2" launch="$3"
  if ! command -v "$bin" >/dev/null 2>&1 ; then
    log "HARNESS_MISSING: $name CLI ($bin) not installed; skipping"
    return 0
  fi

  tmux new-session -d -s "$SESSION" -x 120 -y 40 2>/dev/null || {
    log "HARNESS_MISSING: could not start tmux session"; return 0; }
  tmux send-keys -t "$SESSION" "$launch" Enter
  sleep 8 # give the harness time to start and render

  local raw; raw="$(tmux capture-pane -p -t "$SESSION" 2>/dev/null)"
  local problem; problem="$(classify_problem "$raw")"
  if [ -n "$problem" ]; then
    log "$problem: $name reported an operational problem; not a parser regression"
    tmux kill-session -t "$SESSION" 2>/dev/null || true
    return 0
  fi

  # Ask muxray to classify the live pane and save the (sanitized) result.
  local out
  out="$("$MUXRAY" status --pane "$SESSION" --explain 2>"$ARTIFACTS/$name.stderr")"
  printf '%s\n' "$out" > "$ARTIFACTS/$name.status.json"

  local program status
  program="$(printf '%s' "$out" | grep -o '"program": *"[^"]*"' | head -1 | sed 's/.*: *"//;s/"//')"
  status="$(printf '%s' "$out" | grep -o '"status": *"[^"]*"' | head -1 | sed 's/.*: *"//;s/"//')"

  if [ "$program" = "$name" ] && [ "$status" != "unknown" ]; then
    log "OK: $name classified as $program/$status"
  else
    log "PARSER_FAIL: $name pane classified as ${program:-?}/${status:-?} (expected program=$name, a known status)"
    # Save a sanitized bundle to help diagnose the drift without leaking secrets.
    "$MUXRAY" bundle --out "$ARTIFACTS/$name.bundle.json" >/dev/null 2>&1 || true
    parser_failures=$((parser_failures + 1))
  fi

  tmux kill-session -t "$SESSION" 2>/dev/null || true
}

# The launch commands send a trivial prompt so the harness produces output.
# These are intentionally minimal; the goal is to observe the harness's chrome,
# not to complete a task.
check_provider "claude"  "claude"  "claude --print 'say ok' || claude"
check_provider "codex"   "codex"   "codex exec 'say ok' || codex"
check_provider "copilot" "copilot" "copilot --help"

if [ "$parser_failures" -gt 0 ]; then
  log ""
  log "DRIFT DETECTED: $parser_failures provider parser(s) failed to classify live output."
  log "See drift-artifacts/ for sanitized status JSON and bundles."
  exit 1
fi

log "All available provider parsers classified live output successfully."
exit 0
