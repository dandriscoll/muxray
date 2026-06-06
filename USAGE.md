# Using muxray from an agent

This is the calling contract for an **agent driving muxray as a tool** (not for
contributing to the repo — see `CONTRIBUTING.md` for that). It is also available
in-binary as `muxray usage`.

muxray turns a live tmux pane into deterministic JSON so a control loop can read what
a program is doing without scraping terminal bytes. **Output is JSON on stdout; errors
are JSON on stderr.** Runs locally — no network egress; pane content (which may hold
secrets) never leaves the machine.

## Commands you call

| Command | What you get |
| ------- | ------------ |
| `muxray list` | every tmux session/window/pane, structured |
| `muxray status --pane <t>` | the program + state classified for that pane |
| `muxray snapshot --pane <t>` | capture the pane (stored locally; `--out <file>` to also write one) |
| `muxray diff --pane <t> [--since <file\|id>]` | what changed vs a previous snapshot |
| `muxray inspect --pane <t>` | snapshot + diff + status in one call |

`--text` gives a terse human line instead; JSON is the default. `muxray <command> -h`
lists command-specific flags.

## The JSON contract

Every result is a JSON object carrying the envelope: `schema_version` (currently
`"2"`), `command`, `muxray_version`, `generated_at`. **Branch on `schema_version`** —
if it isn't the version you coded against, the shape may have changed.

`status` and `inspect` carry a `classification` object:

```json
{ "program": "codex", "status": "running", "rule_id": "codex.running",
  "match_source": "rule:codex.running", "confidence": 0.88, "evidence": "Working (esc to interrupt)" }
```

- **`program`** — the program muxray recognized in the pane: `claude`, `codex`,
  `copilot`, `shell` (the pane is at an interactive shell prompt — the harness is
  not live; e.g. the agent exited or a remote/VM connection dropped back to the
  shell — reported as `idle`), or `unknown` (any pane it doesn't recognize — an
  editor, a pager, …).
- **`status`** — one of: `idle`, `running`, `blocked`, `waiting_for_input`,
  `needs_approval`, `error`, `completed`, `unknown`.
- Pass `--explain` to attach a `trace` of every rule considered (use it to diagnose an
  `unknown`).
- muxray reports a state only for a **genuine live Claude/Codex/Copilot frame** or
  for a **shell prompt**: it reads the current state from the footer. A pane that
  merely *mentions* a harness in scrolled content (a transcript, muxray's own
  output) returns `program=unknown`/`status=unknown` — "not a recognized live
  frame, parse it yourself," not "something failed." A footer that is a shell
  prompt returns `program=shell`/`status=idle`: a transport error or agent exit
  that dropped to the shell is **not** an agent `error`.

`diff` carries `changed` (bool — **both `true` and `false` are exit 0**; change is not
an error), `summary`, `added` / `removed` / `context` line arrays, `hunks`, and the
`previous_snapshot` / `current_snapshot` ids.

## The poll loop (typical use)

1. Each tick, `muxray status --pane <t>` and branch on `status`:
   - `needs_approval` / `waiting_for_input` → hand off to a human;
   - `error` → restart or alert;
   - `completed` / `idle` → assign the next task (if `program=shell` the pane
     dropped to a shell — relaunch the agent rather than assigning to a live one);
   - `running` → keep waiting.
2. To detect change cheaply: `muxray snapshot --pane <t> --out before.json`, let the
   program work, then `muxray diff --pane <t> --since before.json` (or `muxray diff`
   against the latest stored snapshot). `changed: false` is deterministic and
   reproducible across machines (the hash is over cleaned text only).

## Pane targets (`--pane` / `--session`)

`--pane` accepts: `session` · `session:window.pane` (`work:1.0`) · pane id (`%3`) ·
session id (`$0`) · omitted = the current pane when run inside tmux. To target a
session by name, `--session <name>` is a clearer equivalent (mutually exclusive with
`--pane`).

## Exit codes

`0` ok (including `changed:true`/`false`) · `1` internal · `2` usage · `3` tmux/capture
· `4` snapshot not found. On failure, stderr carries a JSON object whose `error.class`
is a stable, branchable identifier and whose `error.hint` names the next action.

## Keeping muxray current

`muxray update --check` reports whether a newer release exists; `muxray update`
installs it in place (verifying the checksum). This is the only command that touches
the network, it is explicit/opt-in, and it only downloads — it sends nothing.

