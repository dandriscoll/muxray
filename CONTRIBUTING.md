# Contributing to muxray

Thanks for considering a contribution. `muxray` is deliberately small — a single,
zero-dependency Go binary that reads tmux panes and adapts their output for LLM
agents. Contributions that keep it small, deterministic, and honest are very welcome.

## Build and test

```sh
make build         # build ./muxray
make test          # full suite (uses a real tmux if one is on PATH)
make test-short    # skip the tmux integration + mock-harness layers
make lint          # gofmt check + go vet
make release-check VERSION=vX.Y.Z   # pre-release: validate the curl|sh install path
```

## Cutting a release

`muxray` releases on a pushed `v*` tag (`.github/workflows/release.yml`). Before
tagging, `make release-check VERSION=vX.Y.Z` validates that the advertised
`curl | sh` install path is internally consistent (curl URL, install.sh ↔
build-release.sh artifact names, platform matrix, and that a binary built with the
tag reports it). It runs automatically as a prerequisite of `make release` and in
the release workflow. **Step zero of announcing** a release: after the tag's
release is published, run `scripts/release-check.sh vX.Y.Z --online` to confirm
the GitHub release exists and a real `curl | sh` install succeeds.

The default `go test ./...` lane is deterministic and network-free; the
tmux-dependent layers run when tmux is present and skip cleanly when it is not (or
under `-short`). Please run `make lint` before opening a PR — CI enforces `gofmt`
and `go vet`.

## The easiest, most useful contribution: a program fixture

The program parsers (Claude / Codex / Copilot) are validated by committed transcript
fixtures, so adding coverage — or fixing a misclassification when a provider's TUI
changes — is the highest-leverage contribution. The full flow is in the README:
**[Contributing a program fixture](README.md#contributing-a-program-fixture)**.
In short: drop a captured screen at
`internal/program/testdata/fixtures/<program>/<state>.txt`, run `make fixtures`,
then `go test ./internal/program`. The fixture test self-checks that the detected
program and status match the file's location, so a mislabeled fixture fails rather
than silently passing.

If you hit a misclassification in the wild, the **program-drift** issue template is
the fastest way to report it — include the `--explain` trace and a sanitized screen.

## Scope

muxray does one thing: make a tmux pane legible to an agent (list, snapshot, diff,
status). Feature requests that keep that focus — a new program parser, a new
deterministic output field, a sharper state model — are in scope. A request that
turns muxray into a session manager, a TUI, or a network service is probably out of
scope; open an issue to discuss before building.

## Non-negotiable invariant: it stays local and content-free

muxray is trusted because tmux pane output can contain secrets, and muxray
**cannot leak it by construction**: no network egress, and the telemetry event type
has no field that can hold raw pane text. Please preserve this:

- **No network calls** outside of `muxray shim`'s explicit loopback-only backend.
- **No new telemetry field** that could carry raw pane text, prompts, completions,
  secrets, or environment — only counts, classes, booleans, and irreversible hashes.
  Run `muxray telemetry show` to see the exact, content-free event shape.

A change that weakens this will be asked to change before merge.

## Code style

Match the surrounding code. `make lint` (gofmt + `go vet`) is the bar; small, named,
ordered parser rules over clever ones. Keep new dependencies out — muxray is
stdlib-only by design.

## Reporting bugs

Use the issue templates (bug report, program drift, feature request). For bug
reports, `muxray version` and `muxray doctor` output help a lot, and
`muxray bundle` produces a **sanitized** diagnostic bundle (pane content omitted by
default) when more detail is needed.

By contributing, you agree your contributions are licensed under the repository's
[MIT License](LICENSE).
