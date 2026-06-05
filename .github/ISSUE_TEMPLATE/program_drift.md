---
name: Program misclassification / TUI drift
about: muxray classified the wrong program (or status) because a CLI's terminal UI changed
title: "drift: <program> <state> misclassified"
labels: program-drift
---

When Claude / Codex / Copilot change their terminal UI, muxray's parser can fall
behind. These reports are the most useful thing you can file — they turn directly
into a fixture that locks the behavior in.

**Program**
claude / codex / copilot

**What muxray reported vs. what was true**

- `program` / `status` reported:
- what it *should* have been (program + status — one of idle / running / blocked /
  waiting_for_input / needs_approval / error / completed):

**The `--explain` trace**
Run the same command with `--explain` and paste the trace (it shows every rule
considered and why):

```json
muxray status --pane <target> --explain
```

**A captured screen (so we can make it a fixture)**
Paste the relevant pane text — **sanitized**: scrub any secrets, tokens, file
contents, or paths you don't want public. A few lines around the state indicator is
usually enough.

```text
...
```

**Versions**

- `muxray version`:
- Program CLI version (e.g. `claude --version`):
