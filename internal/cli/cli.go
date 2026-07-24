// Package cli implements the muxray command-line interface: a thin, stdlib-only
// subcommand dispatcher over the internal packages. Output is JSON by default
// (the directive's machine-first posture); a --text mode renders a terse human
// summary. Exit codes are distinct so an agent can branch on them.
package cli

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/dandriscoll/muxray"
	"github.com/dandriscoll/muxray/internal/normalize"
	"github.com/dandriscoll/muxray/internal/schema"
	"github.com/dandriscoll/muxray/internal/snapshot"
	"github.com/dandriscoll/muxray/internal/tmux"
	"github.com/dandriscoll/muxray/internal/version"
)

// Exit codes. These are a stable contract for scripting agents.
const (
	ExitOK       = 0 // success (including changed:false — not an error)
	ExitInternal = 1 // unexpected internal error
	ExitUsage    = 2 // bad flags / unknown command
	ExitTmux     = 3 // tmux missing / capture failure
	ExitNotFound = 4 // referenced snapshot not found
	ExitTimeout  = 5 // 'watch' gave up before the pane reached a target state
)

// stdout/stderr are indirected for testability.
var (
	stdout io.Writer = os.Stdout
	stderr io.Writer = os.Stderr
)

// Run dispatches a muxray invocation and returns the process exit code.
func Run(args []string) int {
	if len(args) == 0 {
		printUsage(stderr)
		return ExitUsage
	}
	cmd, rest := args[0], args[1:]
	switch cmd {
	case "list":
		return cmdList(rest)
	case "snapshot":
		return cmdSnapshot(rest)
	case "diff":
		return cmdDiff(rest)
	case "status":
		return cmdStatus(rest)
	case "scan":
		return cmdScan(rest)
	case "watch":
		return cmdWatch(rest)
	case "inspect":
		return cmdInspect(rest)
	case "doctor":
		return cmdDoctor(rest)
	case "telemetry":
		return cmdTelemetry(rest)
	case "bundle":
		return cmdBundle(rest)
	case "shim":
		return cmdShim(rest)
	case "update":
		return cmdUpdate(rest)
	case "usage":
		fmt.Fprint(stdout, muxray.Usage)
		return ExitOK
	case "skill":
		fmt.Fprint(stdout, muxray.Skill)
		return ExitOK
	case "version", "--version", "-v":
		fmt.Fprintln(stdout, version.String())
		return ExitOK
	case "help", "-h", "--help":
		printUsage(stdout)
		return ExitOK
	default:
		fmt.Fprintf(stderr, "muxray: unknown command %q\n\n", cmd)
		printUsage(stderr)
		return ExitUsage
	}
}

// commonFlags are shared across the pane-reading commands.
type commonFlags struct {
	pane    string
	session string
	lines   int
	jsonOut bool
	text    bool
	debug   bool
	explain bool
}

func (c *commonFlags) register(fs *flag.FlagSet, withExplain bool) {
	fs.StringVar(&c.pane, "pane", "", "tmux target: session, session:window.pane, pane id (%N), or empty for the current pane")
	fs.StringVar(&c.session, "session", "", "tmux session name to target (its active pane); a clearer alternative to --pane for session-level targeting")
	fs.IntVar(&c.lines, "lines", 200, "max lines of pane history to capture and keep (tail)")
	fs.BoolVar(&c.jsonOut, "json", true, "emit JSON (default)")
	fs.BoolVar(&c.text, "text", false, "emit a terse human-readable summary instead of JSON")
	fs.BoolVar(&c.debug, "debug", false, "write a local diagnostic event to the debug log")
	if withExplain {
		fs.BoolVar(&c.explain, "explain", false, "include the parser trace explaining the classification")
	}
}

// wantJSON resolves the json/text flags. JSON is the default; --text opts out,
// and --json=false is honored consistently with the other commands.
func (c *commonFlags) wantJSON() bool { return c.jsonOut && !c.text }

// targetSpec returns the effective tmux target string from --pane / --session.
// The two are mutually exclusive ways to name what to read; setting both is a
// usage error rather than a silent precedence choice. --session is sugar for
// targeting a session by name (tmux resolves it to that session's active pane).
func (c *commonFlags) targetSpec() (string, *cmdError) {
	if c.pane != "" && c.session != "" {
		return "", &cmdError{class: "usage",
			message: "--pane and --session are mutually exclusive",
			hint:    "use --session <name> to target a session, or --pane <target> for a specific pane/window", exit: ExitUsage}
	}
	if c.session != "" {
		return c.session, nil
	}
	return c.pane, nil
}

// cmdError is a structured, user-facing command failure.
type cmdError struct {
	class   string
	message string
	hint    string
	exit    int
}

// fromTmuxErr maps a tmux failure to a user-facing command error with a hint.
func fromTmuxErr(err error) *cmdError {
	te, ok := err.(*tmux.Error)
	if !ok {
		return &cmdError{class: "tmux_error", message: err.Error(), exit: ExitTmux}
	}
	switch te.Class {
	case "not_installed":
		return &cmdError{class: "tmux_not_installed",
			message: "tmux not found on PATH",
			hint:    "muxray needs tmux to read panes; install tmux and retry", exit: ExitTmux}
	case "no_server":
		return &cmdError{class: "tmux_no_server",
			message: "no tmux server is running",
			hint:    "start tmux (or attach to a session) and retry", exit: ExitTmux}
	case "no_target":
		return &cmdError{class: "tmux_no_target",
			message: te.Stderr,
			hint:    "check the --pane value, or run 'muxray list' to see available panes", exit: ExitTmux}
	default:
		return &cmdError{class: "tmux_error", message: te.Error(),
			hint: "run 'muxray doctor' to check your tmux environment", exit: ExitTmux}
	}
}

// capturePane captures the target pane and builds a snapshot. tmux is invoked
// once (with -e) and normalization derives the cleaned text, so raw and clean
// come from a single, consistent capture.
func capturePane(t tmux.Target, lines, scrollback int) (*snapshot.Snapshot, *cmdError) {
	tmuxVer, err := tmux.Version()
	if err != nil {
		return nil, fromTmuxErr(err)
	}
	raw, err := tmux.Capture(t, tmux.CaptureOpts{Escapes: true, JoinWrapped: true, Scrollback: scrollback})
	if err != nil {
		return nil, fromTmuxErr(err)
	}
	res := normalize.Clean(raw, lines)
	snap := snapshot.New(t, tmuxVer, raw, res.Clean, res.LineCount, res.CharCount, res.Truncated, res.ANSIFound, time.Now())
	// Carried alongside Clean but outside the content hash (see Snapshot doc).
	snap.ProgramSuggestion = res.Suggestion
	return snap, nil
}

// emit writes the JSON form of v, or calls textFn for --text mode.
func emit(jsonOut bool, v any, textFn func() string) int {
	if jsonOut {
		b, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return emitError(true, "render", &cmdError{class: "render_error", message: err.Error(), exit: ExitInternal})
		}
		fmt.Fprintln(stdout, string(b))
		return ExitOK
	}
	fmt.Fprintln(stdout, textFn())
	return ExitOK
}

// emitError writes a structured error (to stderr) and returns its exit code.
func emitError(jsonOut bool, command string, e *cmdError) int {
	if jsonOut {
		resp := schema.ErrorResponse{
			Envelope: schema.NewEnvelope(command, version.Version),
			Error:    schema.Error{Class: e.class, Message: e.message, Hint: e.hint},
		}
		b, _ := json.MarshalIndent(resp, "", "  ")
		fmt.Fprintln(stderr, string(b))
	} else {
		msg := "muxray: " + e.message
		if e.hint != "" {
			msg += "\n  hint: " + e.hint
		}
		fmt.Fprintln(stderr, msg)
	}
	return e.exit
}

// flagError reports a flag-parse failure consistently.
func flagError(command string, jsonOut bool, err error) int {
	return emitError(jsonOut, command, &cmdError{class: "usage", message: err.Error(),
		hint: "run 'muxray help' for usage", exit: ExitUsage})
}

func printUsage(w io.Writer) {
	fmt.Fprint(w, `muxray — read tmux panes as deterministic JSON for LLM agents

Usage:
  muxray <command> [flags]

Commands:
  list        List tmux sessions/windows/panes (structured)
  snapshot    Capture a pane snapshot (stored locally; --out to also write a file)
  diff        Compare the current pane against a previous snapshot (--since)
  status      Classify the program state (Claude/Codex/Copilot, or unknown) of a pane
  scan        Classify EVERY pane in one call (the fleet view; --text for a glance)
  watch       Block until a pane reaches a target state (--until); the poll loop in one call
  inspect     Snapshot + diff + status in one call
  doctor      Report environment/tooling diagnostics
  telemetry   Inspect telemetry (telemetry show prints exactly what would be sent)
  bundle      Produce a sanitized diagnostic bundle for bug reports
  shim        Run a local credential-free fake LLM backend for a harness
  update      Update muxray in place from the latest GitHub release (--check, --version)
  usage       Print the agent-facing calling contract (same as USAGE.md)
  skill       Print the drop-in agent skill definition (SKILL.md, embedded)
  version     Print the muxray version

Common flags:
  --pane <target>   session | session:window.pane | pane-id | (empty = current pane)
  --json            emit JSON (default true)
  --text            emit a terse human summary instead
  --lines <n>       max lines of pane history to capture/keep (default 200)
  --debug           write a local diagnostic event to the debug log

Exit codes: 0 ok · 1 internal · 2 usage · 3 tmux/capture · 4 snapshot not found
            · 5 watch timed out (pane never reached the target state)

JSON is the default output. Program parsing degrades to status "unknown" rather
than failing. See 'muxray <command> -h' for command-specific flags.

Agents: 'muxray usage' is the full calling contract, and 'muxray skill' prints
this tool's agent skill definition — install it with
  muxray skill > ~/.claude/skills/muxray/SKILL.md

Examples:
  muxray scan --text                       # every pane and what it's doing
  muxray watch --pane work --until idle    # block until that pane is free
  muxray watch --pane work --timeout 5m    # ...or give up after 5m (exit 5)
`)
}
