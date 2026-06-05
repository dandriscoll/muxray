package cli

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/dandriscoll/muxray/internal/diag"
	"github.com/dandriscoll/muxray/internal/diff"
	"github.com/dandriscoll/muxray/internal/program"
	"github.com/dandriscoll/muxray/internal/schema"
	"github.com/dandriscoll/muxray/internal/snapshot"
	"github.com/dandriscoll/muxray/internal/telemetry"
	"github.com/dandriscoll/muxray/internal/tmux"
	"github.com/dandriscoll/muxray/internal/version"
)

// ---- list ----

type paneView struct {
	PaneIndex      string `json:"pane_index"`
	PaneID         string `json:"pane_id"`
	Active         bool   `json:"active"`
	CurrentCommand string `json:"current_command"`
	Title          string `json:"title"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
}

type windowView struct {
	Index  string     `json:"index"`
	Name   string     `json:"name"`
	Active bool       `json:"active"`
	Panes  []paneView `json:"panes"`
}

type sessionView struct {
	Name    string       `json:"name"`
	Windows []windowView `json:"windows"`
}

type listResponse struct {
	schema.Envelope
	Sessions  []sessionView `json:"sessions"`
	PaneCount int           `json:"pane_count"`
}

func cmdList(args []string) int {
	fs := flag.NewFlagSet("list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary instead")
	if err := fs.Parse(args); err != nil {
		return flagError("list", true, err)
	}
	wantJSON := !*text && *jsonOut

	panes, err := tmux.ListPanes()
	if err != nil {
		return emitError(wantJSON, "list", fromTmuxErr(err))
	}
	resp := listResponse{
		Envelope:  schema.NewEnvelope("list", version.Version),
		Sessions:  buildSessions(panes),
		PaneCount: len(panes),
	}
	return emit(wantJSON, resp, func() string { return listText(resp) })
}

// buildSessions assembles the flat pane list into an ordered session/window/pane
// tree, preserving tmux's ordering.
func buildSessions(panes []tmux.Pane) []sessionView {
	var sessions []sessionView
	sIdx := map[string]int{}
	wIdx := map[string]int{} // key: session+"\x00"+windowIndex
	for _, p := range panes {
		si, ok := sIdx[p.Session]
		if !ok {
			si = len(sessions)
			sIdx[p.Session] = si
			sessions = append(sessions, sessionView{Name: p.Session})
		}
		wkey := p.Session + "\x00" + p.WindowIndex
		wi, ok := wIdx[wkey]
		if !ok {
			wi = len(sessions[si].Windows)
			wIdx[wkey] = wi
			sessions[si].Windows = append(sessions[si].Windows, windowView{
				Index: p.WindowIndex, Name: p.WindowName, Active: p.WindowActive,
			})
		}
		sessions[si].Windows[wi].Panes = append(sessions[si].Windows[wi].Panes, paneView{
			PaneIndex: p.PaneIndex, PaneID: p.PaneID, Active: p.PaneActive,
			CurrentCommand: p.CurrentCommand, Title: p.Title, Width: p.Width, Height: p.Height,
		})
	}
	if sessions == nil {
		sessions = []sessionView{}
	}
	return sessions
}

func listText(r listResponse) string {
	var b strings.Builder
	if len(r.Sessions) == 0 {
		return "no tmux sessions"
	}
	for _, s := range r.Sessions {
		for _, w := range s.Windows {
			for _, p := range w.Panes {
				marker := " "
				if p.Active {
					marker = "*"
				}
				fmt.Fprintf(&b, "%s %s:%s.%s %s [%s] %dx%d\n",
					marker, s.Name, w.Index, p.PaneIndex, p.PaneID, p.CurrentCommand, p.Width, p.Height)
			}
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- snapshot ----

type snapshotResponse struct {
	schema.Envelope
	Snapshot  *snapshot.Snapshot `json:"snapshot"`
	StorePath string             `json:"store_path,omitempty"`
	OutPath   string             `json:"out_path,omitempty"`
}

func cmdSnapshot(args []string) int {
	fs := flag.NewFlagSet("snapshot", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs, false)
	out := fs.String("out", "", "also write the snapshot to this file path")
	noStore := fs.Bool("no-store", false, "do not write to the local snapshot store")
	noRaw := fs.Bool("no-raw", false, "omit the raw (escape-preserving) capture from the snapshot")
	if err := fs.Parse(args); err != nil {
		return flagError("snapshot", true, err)
	}
	wantJSON := c.wantJSON()

	start := time.Now()
	spec, serr := c.targetSpec()
	if serr != nil {
		return emitError(wantJSON, "snapshot", serr)
	}
	target, perr := tmux.ParseTarget(spec)
	if perr != nil {
		return emitError(wantJSON, "snapshot", fromTmuxErr(perr))
	}
	snap, cerr := capturePane(target, c.lines, c.lines)
	if cerr != nil {
		recordEvent(telemetry.Event{Command: "snapshot", Success: false, ErrorClass: cerr.class}, c.debug, start)
		return emitError(wantJSON, "snapshot", cerr)
	}
	if *noRaw {
		snap.Raw = ""
	}

	resp := snapshotResponse{Envelope: schema.NewEnvelope("snapshot", version.Version), Snapshot: snap}
	writeOK := true
	if !*noStore {
		path, err := snapshot.Save(snap, "")
		if err != nil {
			writeOK = false
			recordEvent(telemetry.Event{Command: "snapshot", Success: false, ErrorClass: "snapshot_write", SnapshotWriteOK: &writeOK}, c.debug, start)
			return emitError(wantJSON, "snapshot", &cmdError{class: "snapshot_write", message: err.Error(),
				hint: "check that the snapshot store is writable (muxray doctor)", exit: ExitInternal})
		}
		resp.StorePath = path
	}
	if *out != "" {
		if err := snapshot.WriteFile(snap, *out); err != nil {
			return emitError(wantJSON, "snapshot", &cmdError{class: "snapshot_write", message: err.Error(),
				hint: "check that --out path is writable", exit: ExitInternal})
		}
		resp.OutPath = *out
	}

	recordEvent(telemetry.Event{
		Command: "snapshot", Success: true, TmuxVersion: snap.TmuxVersion,
		ANSINormalized: snap.ANSINormalized, LineCount: snap.LineCount, CharCount: snap.CharCount,
		Truncated: snap.Truncated, SnapshotWriteOK: &writeOK,
		ContentFingerprint: telemetry.Fingerprint(snap.Clean),
	}, c.debug, start)

	return emit(wantJSON, resp, func() string {
		return fmt.Sprintf("snapshot %s  (%d lines, %d chars)\n  hash: %s\n  store: %s",
			snap.ID, snap.LineCount, snap.CharCount, snap.ContentHash, resp.StorePath)
	})
}

// ---- diff ----

type diffResponse struct {
	schema.Envelope
	Target tmux.Target `json:"target"`
	diff.Result
}

func cmdDiff(args []string) int {
	fs := flag.NewFlagSet("diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs, false)
	since := fs.String("since", "", "previous snapshot: a file path, a snapshot id, or empty/'latest' for the most recent stored snapshot of this pane")
	ctx := fs.Int("context", 0, "lines of unchanged context to include around each change")
	full := fs.Bool("full", false, "do not cap added/removed lines (compact by default)")
	maxItems := fs.Int("max-items", 40, "max added/removed lines per side in compact mode")
	save := fs.Bool("save", false, "also store the freshly captured current snapshot")
	if err := fs.Parse(args); err != nil {
		return flagError("diff", true, err)
	}
	wantJSON := c.wantJSON()

	start := time.Now()
	spec, serr := c.targetSpec()
	if serr != nil {
		return emitError(wantJSON, "diff", serr)
	}
	target, perr := tmux.ParseTarget(spec)
	if perr != nil {
		return emitError(wantJSON, "diff", fromTmuxErr(perr))
	}
	cur, cerr := capturePane(target, c.lines, c.lines)
	if cerr != nil {
		return emitError(wantJSON, "diff", cerr)
	}
	prev, derr := resolveSince(*since, target)
	if derr != nil {
		return emitError(wantJSON, "diff", derr)
	}
	if *save {
		// --save is a convenience; a store failure must not fail the diff, but it
		// is surfaced (not silently swallowed) as a non-fatal note on stderr.
		if _, err := snapshot.Save(cur, ""); err != nil {
			fmt.Fprintf(stderr, "muxray: warning: --save failed: %v\n", err)
		}
	}

	res := diff.Compute(prev.ID, cur.ID, prev.Clean, cur.Clean, diff.Options{
		Context: *ctx, Full: *full, MaxItems: *maxItems, TailLines: 10,
	})
	resp := diffResponse{
		Envelope: schema.NewEnvelope("diff", version.Version),
		Target:   target,
		Result:   res,
	}
	readOK := true
	recordEvent(telemetry.Event{
		Command: "diff", Success: true, DiffChanged: &res.Changed, DiffHunks: res.Hunks,
		LineCount: cur.LineCount, CharCount: cur.CharCount, SnapshotReadOK: &readOK,
		ContentFingerprint: telemetry.Fingerprint(cur.Clean),
	}, c.debug, start)

	return emit(wantJSON, resp, func() string {
		if !res.Changed {
			return "changed: false"
		}
		return fmt.Sprintf("changed: true  %s", res.Summary)
	})
}

// resolveSince resolves the --since reference to a stored or on-disk snapshot.
func resolveSince(since string, target tmux.Target) (*snapshot.Snapshot, *cmdError) {
	switch {
	case since == "" || since == "latest":
		s, err := snapshot.Latest("", target)
		if err != nil {
			return nil, &cmdError{class: "snapshot_not_found",
				message: "no previous snapshot found for this pane",
				hint:    "run 'muxray snapshot --pane " + target.Raw + "' first", exit: ExitNotFound}
		}
		return s, nil
	default:
		if _, err := os.Stat(since); err == nil {
			s, err := snapshot.Load(since)
			if err != nil {
				return nil, &cmdError{class: "snapshot_corrupt", message: err.Error(),
					hint: "the --since file is not a valid muxray snapshot", exit: ExitNotFound}
			}
			return s, nil
		}
		s, err := snapshot.FindByID("", since)
		if err != nil {
			return nil, &cmdError{class: "snapshot_not_found",
				message: "no snapshot with id " + since,
				hint:    "pass a snapshot file path, a valid id, or omit --since for the latest", exit: ExitNotFound}
		}
		return s, nil
	}
}

// ---- status ----

type statusResponse struct {
	schema.Envelope
	Target         tmux.Target    `json:"target"`
	Classification program.Result `json:"classification"`
	Tail           []string       `json:"tail_excerpt"`
}

func cmdStatus(args []string) int {
	fs := flag.NewFlagSet("status", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs, true)
	if err := fs.Parse(args); err != nil {
		return flagError("status", true, err)
	}
	wantJSON := c.wantJSON()

	start := time.Now()
	spec, serr := c.targetSpec()
	if serr != nil {
		return emitError(wantJSON, "status", serr)
	}
	target, perr := tmux.ParseTarget(spec)
	if perr != nil {
		return emitError(wantJSON, "status", fromTmuxErr(perr))
	}
	// Classify the VISIBLE screen only (no scrollback): an agent TUI's current
	// state is the current frame. Reading scrollback would let a stale error that
	// has scrolled off the top of the pane be classified as the present state
	// (issue #2 — historical error misclassified).
	snap, cerr := capturePane(target, c.lines, 0)
	if cerr != nil {
		return emitError(wantJSON, "status", cerr)
	}
	result := program.Detect(snap.Clean, c.explain)
	resp := statusResponse{
		Envelope:       schema.NewEnvelope("status", version.Version),
		Target:         target,
		Classification: result,
		Tail:           tailExcerpt(snap.Clean, 10),
	}
	recordEvent(telemetry.Event{
		Command: "status", Success: true, Program: result.Program, Status: string(result.Status),
		RuleID: result.RuleID, Confidence: result.Confidence, Harness: result.Program,
		ANSINormalized: snap.ANSINormalized, LineCount: snap.LineCount, CharCount: snap.CharCount,
		Truncated: snap.Truncated, ContentFingerprint: telemetry.Fingerprint(snap.Clean),
	}, c.debug, start)

	return emit(wantJSON, resp, func() string {
		return fmt.Sprintf("%s/%s  (rule=%s confidence=%.2f)",
			result.Program, result.Status, result.RuleID, result.Confidence)
	})
}

// ---- inspect ----

type inspectResponse struct {
	schema.Envelope
	Target         tmux.Target        `json:"target"`
	Snapshot       *snapshot.Snapshot `json:"snapshot"`
	Diff           *diff.Result       `json:"diff,omitempty"`
	Classification program.Result     `json:"classification"`
}

func cmdInspect(args []string) int {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var c commonFlags
	c.register(fs, true)
	since := fs.String("since", "", "previous snapshot for the diff portion (file, id, or empty/'latest')")
	noRaw := fs.Bool("no-raw", true, "omit raw capture from the embedded snapshot (default true to keep inspect compact)")
	if err := fs.Parse(args); err != nil {
		return flagError("inspect", true, err)
	}
	wantJSON := c.wantJSON()

	start := time.Now()
	spec, serr := c.targetSpec()
	if serr != nil {
		return emitError(wantJSON, "inspect", serr)
	}
	target, perr := tmux.ParseTarget(spec)
	if perr != nil {
		return emitError(wantJSON, "inspect", fromTmuxErr(perr))
	}
	// Snapshot keeps scrollback (it feeds the diff); classification runs on the
	// VISIBLE screen only, so a stale error scrolled off the top is not reported
	// as the current state (issue #2). Two captures, one consistent target.
	snap, cerr := capturePane(target, c.lines, c.lines)
	if cerr != nil {
		return emitError(wantJSON, "inspect", cerr)
	}
	screen, scerr := capturePane(target, c.lines, 0)
	if scerr != nil {
		return emitError(wantJSON, "inspect", scerr)
	}
	result := program.Detect(screen.Clean, c.explain)

	resp := inspectResponse{
		Envelope:       schema.NewEnvelope("inspect", version.Version),
		Target:         target,
		Snapshot:       snap,
		Classification: result,
	}
	// Diff against the previous snapshot when one exists; absence is not an error.
	if prev, derr := resolveSince(*since, target); derr == nil {
		res := diff.Compute(prev.ID, snap.ID, prev.Clean, snap.Clean, diff.Options{MaxItems: 40, TailLines: 10})
		resp.Diff = &res
	}
	if *noRaw {
		snap.Raw = ""
	}

	var changed *bool
	hunks := 0
	if resp.Diff != nil {
		changed = &resp.Diff.Changed
		hunks = resp.Diff.Hunks
	}
	recordEvent(telemetry.Event{
		Command: "inspect", Success: true, Program: result.Program, Status: string(result.Status),
		RuleID: result.RuleID, Confidence: result.Confidence, Harness: result.Program,
		ANSINormalized: snap.ANSINormalized, LineCount: snap.LineCount, CharCount: snap.CharCount,
		Truncated: snap.Truncated, DiffChanged: changed, DiffHunks: hunks,
		ContentFingerprint: telemetry.Fingerprint(snap.Clean),
	}, c.debug, start)

	return emit(wantJSON, resp, func() string {
		ch := "n/a"
		if resp.Diff != nil {
			ch = fmt.Sprintf("%v", resp.Diff.Changed)
		}
		return fmt.Sprintf("%s/%s  changed=%s  (%d lines)",
			result.Program, result.Status, ch, snap.LineCount)
	})
}

// ---- doctor ----

type doctorResponse struct {
	schema.Envelope
	Report diag.Report `json:"diagnostics"`
}

func cmdDoctor(args []string) int {
	fs := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary instead")
	if err := fs.Parse(args); err != nil {
		return flagError("doctor", true, err)
	}
	wantJSON := !*text && *jsonOut
	resp := doctorResponse{
		Envelope: schema.NewEnvelope("doctor", version.Version),
		Report:   diag.Doctor(version.Version),
	}
	return emit(wantJSON, resp, func() string {
		var b strings.Builder
		fmt.Fprintf(&b, "muxray %s  %s/%s\n", resp.Report.MuxrayVersion, resp.Report.OS, resp.Report.Arch)
		for _, ck := range resp.Report.Checks {
			mark := "ok "
			if !ck.OK {
				mark = "FAIL"
			}
			fmt.Fprintf(&b, "[%s] %s: %s\n", mark, ck.Name, ck.Detail)
		}
		return strings.TrimRight(b.String(), "\n")
	})
}

// ---- telemetry ----

type telemetryShowResponse struct {
	schema.Envelope
	KillSwitch      bool            `json:"kill_switch_active"`
	ExternalEnabled bool            `json:"external_enabled"`
	NetworkSink     bool            `json:"network_sink"`
	Note            string          `json:"note"`
	ExampleEvent    telemetry.Event `json:"example_event"`
}

func cmdTelemetry(args []string) int {
	sub := "show"
	if len(args) > 0 && !strings.HasPrefix(args[0], "-") {
		sub, args = args[0], args[1:]
	}
	fs := flag.NewFlagSet("telemetry", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary instead")
	if err := fs.Parse(args); err != nil {
		return flagError("telemetry", true, err)
	}
	wantJSON := !*text && *jsonOut

	switch sub {
	case "show":
		// Illustrative, host-independent example of the event shape. Host fields
		// are fixed sample values so the shape is stable across platforms.
		example := telemetry.Event{
			MuxrayVersion: version.Version, OS: "linux", Arch: "amd64", TmuxVersion: "3.4",
			Command: "status", DurationMS: 12, Success: true,
			Program: "claude", Status: "running", RuleID: "claude.running", Confidence: 0.9,
			ANSINormalized: true, LineCount: 42, CharCount: 1337, Truncated: false,
			ContentFingerprint: "0123456789abcdef", InvocationID: "example",
		}
		resp := telemetryShowResponse{
			Envelope:        schema.NewEnvelope("telemetry.show", version.Version),
			KillSwitch:      telemetry.KillSwitch(),
			ExternalEnabled: telemetry.ExternalEnabled(),
			NetworkSink:     false,
			Note:            "This version has NO network telemetry sink. Events are content-free by construction (only counts, classes, booleans, and hashes — never pane text, prompts, secrets, or environment). Local diagnostic logging is written only under --debug. Disable everything with MUXRAY_NO_TELEMETRY=1 or DO_NOT_TRACK=1.",
			ExampleEvent:    example,
		}
		return emit(wantJSON, resp, func() string {
			return "telemetry: no network sink in this version; kill_switch=" +
				fmt.Sprintf("%v", resp.KillSwitch) + "\nexample event:\n" + telemetry.Show(example)
		})
	default:
		return emitError(wantJSON, "telemetry", &cmdError{class: "usage",
			message: "unknown telemetry subcommand " + sub,
			hint:    "supported: 'muxray telemetry show'", exit: ExitUsage})
	}
}

// ---- bundle ----

type bundleResponse struct {
	schema.Envelope
	Bundle diag.Bundle `json:"bundle"`
}

func cmdBundle(args []string) int {
	fs := flag.NewFlagSet("bundle", flag.ContinueOnError)
	fs.SetOutput(stderr)
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit a terse human summary instead")
	out := fs.String("out", "", "write the bundle to this file path instead of stdout")
	includeExcerpt := fs.Bool("include-excerpt", false, "include a secret-redacted pane excerpt from --pane (off by default)")
	pane := fs.String("pane", "", "pane to excerpt when --include-excerpt is set")
	lines := fs.Int("lines", 40, "lines for the optional excerpt")
	if err := fs.Parse(args); err != nil {
		return flagError("bundle", true, err)
	}
	wantJSON := !*text && *jsonOut

	excerpt := ""
	if *includeExcerpt {
		if target, perr := tmux.ParseTarget(*pane); perr == nil {
			if snap, cerr := capturePane(target, *lines, *lines); cerr == nil {
				excerpt = snap.Clean
			}
		}
	}
	b := diag.BuildBundle(version.Version, schema.NewEnvelope("bundle", version.Version).GeneratedAt, *includeExcerpt, excerpt)
	resp := bundleResponse{Envelope: schema.NewEnvelope("bundle", version.Version), Bundle: b}

	if *out != "" {
		data, _ := jsonMarshalIndent(resp)
		if err := os.WriteFile(*out, data, 0o644); err != nil {
			return emitError(wantJSON, "bundle", &cmdError{class: "write_error", message: err.Error(),
				hint: "check that --out is writable", exit: ExitInternal})
		}
		fmt.Fprintf(stdout, "wrote sanitized bundle to %s\n", *out)
		return ExitOK
	}
	return emit(wantJSON, resp, func() string {
		return fmt.Sprintf("bundle: tmux_found=%v inside_tmux=%v events=%d excerpt=%v",
			b.Doctor.TmuxFound, b.Doctor.InsideTmux, len(b.RecentEvents), b.ExcerptIncluded)
	})
}

// ---- shared small helpers ----

func recordEvent(e telemetry.Event, debug bool, start time.Time) {
	e.MuxrayVersion = version.Version
	e.OS = goos()
	e.Arch = goarch()
	e.DurationMS = time.Since(start).Milliseconds()
	if e.InvocationID == "" {
		e.InvocationID = invocationID()
	}
	telemetry.Record(e, debug)
}

func tailExcerpt(clean string, n int) []string {
	if clean == "" {
		return []string{}
	}
	ls := strings.Split(clean, "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return ls
}
