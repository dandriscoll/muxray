---
name: Bug report
about: Something muxray did wrong (a crash, a bad value, an unexpected exit code)
title: ""
labels: bug
---

**What happened**
A clear description of the bug.

**Command**
The exact `muxray` command you ran (with flags).

```sh
muxray ...
```

**Expected vs. actual**
What you expected, and what you got instead (paste the JSON/stderr if you can).

**Environment**

- `muxray version`:
- `muxray doctor` (paste the output):
- OS / arch:
- tmux version (if relevant):
- Provider (claude / codex / copilot / n/a):

**Extra detail (optional)**
`muxray bundle` produces a sanitized diagnostic bundle (pane content is omitted by
default). Attach it if the above isn't enough — but please double-check it for
anything sensitive before posting.
