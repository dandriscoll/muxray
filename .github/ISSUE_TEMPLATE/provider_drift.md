---
name: Provider misclassification / TUI drift
about: muxray reported the wrong provider or status because a CLI's terminal UI changed
title: "drift: <provider> <state> misclassified"
labels: provider-drift
---

When Claude / Codex / Copilot change their terminal UI, muxray's parser can fall
behind. These reports are the most useful thing you can file — they turn directly
into a fixture that locks the behavior in.

**Provider**
claude / codex / copilot

**What muxray reported vs. what was true**

- `status` reported:
- `status` it *should* have been (idle / running / blocked / waiting_for_input /
  needs_approval / error / completed):

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
- Provider CLI version (e.g. `claude --version`):
