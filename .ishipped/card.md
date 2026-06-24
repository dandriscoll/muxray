---
title: "muxray"
summary: "Reads live tmux panes as deterministic JSON so an agent's control loop knows what each pane is doing — without scraping terminal bytes."
shipped: 2026-06-04
version: "1.0.0"
tags:
  - tmux
  - ai-agents
  - coding-agents
  - cli
  - observability
  - json
  - go
author:
  name: "Dan Driscoll"
  github: "dandriscoll"
links:
  - label: "Releases"
    url: "https://github.com/dandriscoll/muxray/releases"
    primary: true
  - label: "Calling Contract (USAGE.md)"
    url: "https://github.com/dandriscoll/muxray/blob/main/USAGE.md"
  - label: "OpenClaw Skill"
    url: "https://github.com/dandriscoll/muxray/blob/main/skills/muxray/SKILL.md"
---

## What is muxray?

muxray turns a live tmux pane into deterministic JSON so an agent can read
what a program is doing without scraping raw terminal bytes. It is built for
**supervising interactive CLIs in tmux** — especially terminal coding agents
like **Claude Code, Codex, and Copilot** — and answers three questions
repeatedly and cheaply:

1. **What is in this pane?** — structured capture and snapshots.
2. **Did it change since I last looked?** — a deterministic `changed: false`,
   or a compact, LLM-friendly diff when it did.
3. **What is the agent in this pane doing?** — program-specific status parsing
   into a stable state model: `idle`, `running`, `blocked`,
   `waiting_for_input`, `needs_approval`, `error`, `completed`, `unknown`.

Single static Go binary. Zero runtime dependencies. Output is JSON on stdout;
errors are JSON on stderr.

## Why an agent needs this

An orchestrator driving Claude/Codex/Copilot inside tmux otherwise has to
capture raw pane bytes, strip ANSI, guess whether anything changed, and
pattern-match a moving terminal UI on every poll. muxray does that once,
deterministically, behind a stable JSON contract — so the control loop can
poll `muxray status` / `muxray diff` and trust the answer.

- **Deterministic** — every result is self-describing (schema version,
  command, timestamp, target), so an agent branches on it instead of scraping.
- **Local-first** — pane content (which may hold secrets) is read locally and
  **never leaves the machine**; the only network egress is the opt-in
  `muxray update`.
- **Read-only** — muxray observes panes. It never sends keystrokes or mutates
  sessions.
- **Fleet-aware** — `muxray scan` classifies every pane in one call, so a
  multi-agent orchestrator can answer "what is each session doing right now?"
  on every tick.

## Quick start

```sh
# Install the latest release binary (Linux/macOS)
curl -fsSL https://raw.githubusercontent.com/dandriscoll/muxray/main/install.sh | sh

# Classify what the agent in a pane is doing
muxray status --pane work:1.0

# Classify EVERY pane in one call (the fleet view; --text for a quick glance)
muxray scan --text

# Block until that pane is free / needs you, then act on the final state
muxray watch --pane work:1.0 --until idle,needs_approval --timeout 10m

# Snapshot a pane, do other things, then see what changed
muxray inspect --pane %3
```

`muxray watch` and `muxray scan` *are* the control loop — wait until a pane
settles, then see the whole fleet — so you don't hand-roll poll + sleep +
compare.

## Status

v1.0.0 is the first stable release: a frozen `schema_version: "2"` JSON
contract, program-specific state parsing for Claude / Codex / Copilot,
deterministic snapshot + diff, the `scan` fleet view, and a bundled
[OpenClaw](https://github.com/openclaw/openclaw) skill for multi-agent
orchestrators. Cross-platform builds for Linux and macOS (amd64 + arm64).
MIT-licensed.

---
[View on ishipped.io](https://ishipped.io/card/dandriscoll/muxray)
