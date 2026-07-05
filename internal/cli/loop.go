package cli

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/dandriscoll/muxray/internal/program"
	"github.com/dandriscoll/muxray/internal/schema"
	"github.com/dandriscoll/muxray/internal/telemetry"
	"github.com/dandriscoll/muxray/internal/tmux"
	"github.com/dandriscoll/muxray/internal/version"
)

// loop.go adds the two verbs the control loop is actually built from, so callers
// stop hand-rolling poll+compare+sleep (watch) and fleet fan-out (scan). Both sit
// on the shared classifyParsed helper and reuse the envelope / exit-code / error
// conventions verbatim. See jobs/273 for the usability review that motivated them.

// allStatuses maps every status name to its program.Status, for --until parsing
// and validation. Built from the program package's enum so the two never drift.
var allStatuses = map[string]program.Status{
	string(program.StatusIdle):            program.StatusIdle,
	string(program.StatusRunning):         program.StatusRunning,
	string(program.StatusBlocked):         program.StatusBlocked,
	string(program.StatusWaitingForInput): program.StatusWaitingForInput,
	string(program.StatusNeedsApproval):   program.StatusNeedsApproval,
	string(program.StatusError):           program.StatusError,
	string(program.StatusCompleted):       program.StatusCompleted,
	string(program.StatusUnknown):         program.StatusUnknown,
}

// defaultUntil is the "settled" set watch waits FOR by default: every state that
// means the pane is no longer actively working. It excludes `running` (still
// working — keep waiting) and `unknown` (a transient unclassified frame, e.g. a
// redraw with no footer match, must not end the wait prematurely — --timeout
// bounds a genuinely-stuck unknown). A disconnected pane surfaces as status=idle
// (program=shell) and so is caught by `idle`.
func defaultUntil() map[program.Status]bool {
	return map[program.Status]bool{
		program.StatusIdle:            true,
		program.StatusCompleted:       true,
		program.StatusNeedsApproval:   true,
		program.StatusWaitingForInput: true,
		program.StatusBlocked:         true,
		program.StatusError:           true,
	}
}

// parseUntil turns a comma-separated state list into a set, validating each name
// against the status enum. Empty → the settled default.
func parseUntil(s string) (map[program.Status]bool, *cmdError) {
	s = strings.TrimSpace(s)
	if s == "" {
		return defaultUntil(), nil
	}
	set := map[program.Status]bool{}
	for _, part := range strings.Split(s, ",") {
		name := strings.ToLower(strings.TrimSpace(part))
		if name == "" {
			continue
		}
		st, ok := allStatuses[name]
		if !ok {
			return nil, &cmdError{class: "usage",
				message: "unknown state " + part + " in --until",
				hint:    "valid states: " + strings.Join(statusNames(), ", "), exit: ExitUsage}
		}
		set[st] = true
	}
	if len(set) == 0 {
		return defaultUntil(), nil
	}
	return set, nil
}

func statusNames() []string {
	names := make([]string, 0, len(allStatuses))
	for n := range allStatuses {
		names = append(names, n)
	}
	sort.Strings(names)
	return names
}

func untilNames(set map[program.Status]bool) []string {
	names := make([]string, 0, len(set))
	for st := range set {
		names = append(names, string(st))
	}
	sort.Strings(names)
	return names
}

// ---- watch ----

// watchOutcome is how the wait ended.
type watchOutcome int

const (
	outcomeReached    watchOutcome = iota // a target state was observed
	outcomeTimeout                        // the deadline passed first
	outcomeCaptureErr                     // the pane/tmux could not be read
	outcomeCancelled                      // interrupted (SIGINT)
)

func (o watchOutcome) String() string {
	switch o {
	case outcomeReached:
		return "reached"
	case outcomeTimeout:
		return "timeout"
	case outcomeCancelled:
		return "cancelled"
	default:
		return "error"
	}
}

// watchLoop is the pure poll loop, with the clock, sleep, and cancellation
// injected so it is unit-testable without a live tmux. It polls until the
// classified status is in `until`, the deadline passes (zero = none), a capture
// fails, or cancellation is signalled.
func watchLoop(
	poll func() (program.Result, *cmdError),
	until map[program.Status]bool,
	interval time.Duration,
	deadline time.Time,
	now func() time.Time,
	sleep func(time.Duration),
	cancelled func() bool,
) (res program.Result, cerr *cmdError, outcome watchOutcome, polls int) {
	timedOut := func() bool { return !deadline.IsZero() && !now().Before(deadline) }
	for {
		r, e := poll()
		polls++
		if e != nil {
			return program.Result{}, e, outcomeCaptureErr, polls
		}
		res = r
		if until[r.Status] {
			return res, nil, outcomeReached, polls
		}
		if timedOut() {
			return res, nil, outcomeTimeout, polls
		}
		if cancelled != nil && cancelled() {
			return res, nil, outcomeCancelled, polls
		}
		sleep(interval)
		if timedOut() {
			return res, nil, outcomeTimeout, polls
		}
	}
}

type watchResponse struct {
	schema.Envelope
	Target         tmux.Target    `json:"target"`
	Outcome        string         `json:"outcome"`
	Until          []string       `json:"until"`
	Classification program.Result `json:"classification"`
	Polls          int            `json:"polls"`
	WaitedMS       int64          `json:"waited_ms"`
}

func cmdWatch(args []string) int {
	fs := flag.NewFlagSet("watch", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs, true)
	untilFlag := fs.String("until", "", "comma-separated states to wait FOR (default: any non-running 'settled' state)")
	interval := fs.Duration("interval", time.Second, "poll interval (floored at 200ms)")
	timeout := fs.Duration("timeout", 0, "give up after this long (0 = wait forever)")
	timeoutExit := fs.Int("timeout-exit", ExitTimeout, "process exit code to return on timeout (0-255)")
	if err := fs.Parse(args); err != nil {
		return flagError("watch", true, err)
	}
	wantJSON := c.wantJSON()

	until, uerr := parseUntil(*untilFlag)
	if uerr != nil {
		return emitError(wantJSON, "watch", uerr)
	}
	if *interval < 200*time.Millisecond {
		*interval = 200 * time.Millisecond
	}
	// Validate the timeout exit code before touching tmux: an out-of-range code
	// would alias mod 256 at os.Exit (300 -> 44), silently mapping timeout onto
	// an unrelated code. Fail fast with a usage error instead.
	if verr := validateTimeoutExit(*timeoutExit); verr != nil {
		return emitError(wantJSON, "watch", verr)
	}

	spec, serr := c.targetSpec()
	if serr != nil {
		return emitError(wantJSON, "watch", serr)
	}
	target, perr := tmux.ParseTarget(spec)
	if perr != nil {
		return emitError(wantJSON, "watch", fromTmuxErr(perr))
	}

	// SIGINT/SIGTERM → cancel after the current poll so a Ctrl-C exits cleanly.
	stopped := false
	sigc := make(chan os.Signal, 1)
	signal.Notify(sigc, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigc)
	go func() {
		<-sigc
		stopped = true
	}()

	start := time.Now()
	var deadline time.Time
	if *timeout > 0 {
		deadline = start.Add(*timeout)
	}
	res, cerr, outcome, polls := watchLoop(
		func() (program.Result, *cmdError) { r, _, e := classifyParsed(target, c.lines, c.explain); return r, e },
		until, *interval, deadline, time.Now, time.Sleep,
		func() bool { return stopped },
	)
	waited := time.Since(start).Milliseconds()

	recordEvent(telemetry.Event{
		Command: "watch", Success: outcome != outcomeCaptureErr,
		Program: res.Program, Status: string(res.Status), RuleID: res.RuleID,
		Confidence: res.Confidence, Harness: res.Program,
	}, c.debug, start)

	if outcome == outcomeCaptureErr {
		return emitError(wantJSON, "watch", cerr)
	}

	resp := watchResponse{
		Envelope: schema.NewEnvelope("watch", version.Version), Target: target,
		Outcome: outcome.String(), Until: untilNames(until),
		Classification: res, Polls: polls, WaitedMS: waited,
	}
	if rc := emit(wantJSON, resp, func() string {
		return fmt.Sprintf("%s  %s/%s  (%d polls, %dms)",
			outcome.String(), res.Program, res.Status, polls, waited)
	}); rc != ExitOK {
		return rc // a render failure is an internal error, not a watch outcome
	}
	return resolveWatchExit(outcome, *timeoutExit)
}

// validateTimeoutExit rejects a --timeout-exit code outside the Unix exit-status
// range. A process exit status is 8-bit, so os.Exit truncates mod 256; accepting
// >255 (or a negative) would silently alias the caller's chosen code onto an
// unrelated one, which is exactly what "validated" is meant to prevent.
func validateTimeoutExit(code int) *cmdError {
	if code < 0 || code > 255 {
		return &cmdError{class: "usage",
			message: fmt.Sprintf("invalid --timeout-exit %d", code),
			hint:    "exit code must be between 0 and 255", exit: ExitUsage}
	}
	return nil
}

// resolveWatchExit maps a completed watch outcome to the process exit code.
// Only a timeout uses the caller-chosen code (default ExitTimeout); every other
// outcome that reaches here (reached/cancelled) is a successful wait and returns
// ExitOK. Capture and render errors return earlier in cmdWatch and never arrive.
func resolveWatchExit(outcome watchOutcome, timeoutExit int) int {
	if outcome == outcomeTimeout {
		return timeoutExit
	}
	return ExitOK
}

// ---- scan ----

type scanPaneView struct {
	Target         string         `json:"target"`
	Session        string         `json:"session"`
	Window         string         `json:"window"`
	Pane           string         `json:"pane"`
	Active         bool           `json:"active"`
	Classification program.Result `json:"classification"`
	Error          string         `json:"error,omitempty"`
}

type scanResponse struct {
	schema.Envelope
	Panes     []scanPaneView `json:"panes"`
	PaneCount int            `json:"pane_count"`
}

// paneClass pairs a pane with its classification (or capture error). Splitting
// assembly (buildScanResponse) from the tmux-touching loop keeps the shaping
// unit-testable without a live tmux.
type paneClass struct {
	pane tmux.Pane
	res  program.Result
	cerr *cmdError
}

func buildScanResponse(classes []paneClass) scanResponse {
	resp := scanResponse{Envelope: schema.NewEnvelope("scan", version.Version), Panes: []scanPaneView{}}
	for _, pc := range classes {
		v := scanPaneView{
			Target:  pc.pane.PaneID,
			Session: pc.pane.Session, Window: pc.pane.WindowIndex, Pane: pc.pane.PaneIndex,
			Active:         pc.pane.PaneActive,
			Classification: pc.res,
		}
		if pc.cerr != nil {
			v.Error = pc.cerr.class
			// A pane that could not be read is reported unknown rather than guessed.
			v.Classification = program.Result{Program: "unknown", Status: program.StatusUnknown}
		}
		resp.Panes = append(resp.Panes, v)
	}
	resp.PaneCount = len(resp.Panes)
	return resp
}

func scanText(r scanResponse) string {
	if len(r.Panes) == 0 {
		return "no tmux panes"
	}
	var b strings.Builder
	for _, p := range r.Panes {
		marker := " "
		if p.Active {
			marker = "*"
		}
		line := fmt.Sprintf("%s %s:%s.%s %s  %s/%s",
			marker, p.Session, p.Window, p.Pane, p.Target, p.Classification.Program, p.Classification.Status)
		if p.Error != "" {
			line += "  (error: " + p.Error + ")"
		}
		fmt.Fprintln(&b, line)
	}
	return strings.TrimRight(b.String(), "\n")
}

func cmdScan(args []string) int {
	fs := flag.NewFlagSet("scan", flag.ContinueOnError)
	fs.SetOutput(stderr)
	lines := fs.Int("lines", 200, "max lines of pane history to capture per pane")
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary (one line per pane) instead")
	explain := fs.Bool("explain", false, "include the parser trace for each pane")
	debug := fs.Bool("debug", false, "write a local diagnostic event to the debug log")
	if err := fs.Parse(args); err != nil {
		return flagError("scan", true, err)
	}
	wantJSON := !*text && *jsonOut

	start := time.Now()
	panes, err := tmux.ListPanes()
	if err != nil {
		return emitError(wantJSON, "scan", fromTmuxErr(err))
	}
	classes := make([]paneClass, 0, len(panes))
	for _, p := range panes {
		target, perr := tmux.ParseTarget(p.PaneID)
		if perr != nil {
			classes = append(classes, paneClass{pane: p, cerr: fromTmuxErr(perr)})
			continue
		}
		res, _, cerr := classifyParsed(target, *lines, *explain)
		classes = append(classes, paneClass{pane: p, res: res, cerr: cerr})
	}
	resp := buildScanResponse(classes)

	recordEvent(telemetry.Event{Command: "scan", Success: true}, *debug, start)

	return emit(wantJSON, resp, func() string { return scanText(resp) })
}
