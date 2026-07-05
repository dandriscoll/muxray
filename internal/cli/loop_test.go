package cli

import (
	"strings"
	"testing"
	"time"

	"github.com/dandriscoll/muxray/internal/program"
	"github.com/dandriscoll/muxray/internal/tmux"
)

func tmuxPane(session, window, pane, id string, active bool) tmux.Pane {
	return tmux.Pane{Session: session, WindowIndex: window, PaneIndex: pane, PaneID: id, PaneActive: active}
}

// scriptPoll returns a poll func that yields the given results in order, repeating
// the last one forever. A nil cmdError per step means a successful classify.
func scriptPoll(results ...program.Result) func() (program.Result, *cmdError) {
	i := 0
	return func() (program.Result, *cmdError) {
		r := results[i]
		if i < len(results)-1 {
			i++
		}
		return r, nil
	}
}

func res(status program.Status) program.Result {
	return program.Result{Program: "claude", Status: status}
}

// fakeClock advances only when sleep is called, so watchLoop's timing is
// deterministic and the test never actually waits.
type fakeClock struct{ cur time.Time }

func (f *fakeClock) now() time.Time        { return f.cur }
func (f *fakeClock) sleep(d time.Duration) { f.cur = f.cur.Add(d) }

func TestWatchLoop_ReachesTargetState(t *testing.T) {
	fc := &fakeClock{cur: time.Unix(0, 0)}
	poll := scriptPoll(res(program.StatusRunning), res(program.StatusRunning), res(program.StatusIdle))
	r, cerr, outcome, polls := watchLoop(poll, defaultUntil(), time.Second, time.Time{}, fc.now, fc.sleep, nil)
	if cerr != nil || outcome != outcomeReached {
		t.Fatalf("got outcome=%v cerr=%v, want reached", outcome, cerr)
	}
	if r.Status != program.StatusIdle {
		t.Errorf("final status=%s, want idle", r.Status)
	}
	if polls != 3 {
		t.Errorf("polls=%d, want 3", polls)
	}
}

func TestWatchLoop_WaitsThroughRunningAndUnknown(t *testing.T) {
	// running and unknown are NOT in the default until set: the loop must keep
	// waiting through both, then settle on needs_approval.
	fc := &fakeClock{cur: time.Unix(0, 0)}
	poll := scriptPoll(res(program.StatusRunning), res(program.StatusUnknown), res(program.StatusNeedsApproval))
	r, _, outcome, polls := watchLoop(poll, defaultUntil(), time.Second, time.Time{}, fc.now, fc.sleep, nil)
	if outcome != outcomeReached || r.Status != program.StatusNeedsApproval {
		t.Fatalf("got %s/%s, want reached/needs_approval", outcome, r.Status)
	}
	if polls != 3 {
		t.Errorf("polls=%d, want 3 (waited through running+unknown)", polls)
	}
}

func TestWatchLoop_TimesOut(t *testing.T) {
	fc := &fakeClock{cur: time.Unix(0, 0)}
	// Never settles: always running. Deadline is 5 ticks of 1s.
	poll := scriptPoll(res(program.StatusRunning))
	deadline := fc.cur.Add(5 * time.Second)
	r, cerr, outcome, polls := watchLoop(poll, defaultUntil(), time.Second, deadline, fc.now, fc.sleep, nil)
	if cerr != nil || outcome != outcomeTimeout {
		t.Fatalf("got outcome=%v cerr=%v, want timeout", outcome, cerr)
	}
	if r.Status != program.StatusRunning {
		t.Errorf("timeout should still report the last-seen state; got %s", r.Status)
	}
	if polls < 1 {
		t.Errorf("polls=%d, want >=1", polls)
	}
}

func TestWatchLoop_CaptureError(t *testing.T) {
	fc := &fakeClock{cur: time.Unix(0, 0)}
	poll := func() (program.Result, *cmdError) {
		return program.Result{}, &cmdError{class: "tmux_no_target", exit: ExitTmux}
	}
	_, cerr, outcome, _ := watchLoop(poll, defaultUntil(), time.Second, time.Time{}, fc.now, fc.sleep, nil)
	if outcome != outcomeCaptureErr || cerr == nil || cerr.class != "tmux_no_target" {
		t.Fatalf("got outcome=%v cerr=%v, want captureErr/tmux_no_target", outcome, cerr)
	}
}

func TestWatchLoop_Cancelled(t *testing.T) {
	fc := &fakeClock{cur: time.Unix(0, 0)}
	poll := scriptPoll(res(program.StatusRunning))
	cancelled := true
	_, _, outcome, polls := watchLoop(poll, defaultUntil(), time.Second, time.Time{}, fc.now, fc.sleep, func() bool { return cancelled })
	if outcome != outcomeCancelled {
		t.Fatalf("got outcome=%v, want cancelled", outcome)
	}
	if polls != 1 {
		t.Errorf("polls=%d, want 1 (cancelled after first poll)", polls)
	}
}

func TestParseUntil(t *testing.T) {
	// Default (empty) excludes running and unknown.
	d, err := parseUntil("")
	if err != nil {
		t.Fatalf("default: %v", err)
	}
	if d[program.StatusRunning] || d[program.StatusUnknown] {
		t.Errorf("default until must exclude running and unknown: %v", untilNames(d))
	}
	if !d[program.StatusIdle] || !d[program.StatusError] {
		t.Errorf("default until must include idle and error: %v", untilNames(d))
	}
	// Explicit set.
	got, err := parseUntil("idle, needs_approval")
	if err != nil {
		t.Fatalf("explicit: %v", err)
	}
	if len(got) != 2 || !got[program.StatusIdle] || !got[program.StatusNeedsApproval] {
		t.Errorf("explicit until wrong: %v", untilNames(got))
	}
	// Bad name → usage error naming valid states.
	_, berr := parseUntil("idle,bogus")
	if berr == nil || berr.exit != ExitUsage || !strings.Contains(berr.hint, "running") {
		t.Errorf("bad state should be a usage error listing valid states; got %v", berr)
	}
}

func TestResolveWatchExit(t *testing.T) {
	// Default: timeout maps to ExitTimeout (5) when the caller passes the default.
	if got := resolveWatchExit(outcomeTimeout, ExitTimeout); got != ExitTimeout {
		t.Errorf("timeout default: got %d, want %d", got, ExitTimeout)
	}
	// Custom code such as 0 is honored on timeout — the headline use case.
	if got := resolveWatchExit(outcomeTimeout, 0); got != 0 {
		t.Errorf("timeout custom 0: got %d, want 0", got)
	}
	if got := resolveWatchExit(outcomeTimeout, 42); got != 42 {
		t.Errorf("timeout custom 42: got %d, want 42", got)
	}
	// Only timeout uses the custom code; reached and cancelled are successful waits.
	if got := resolveWatchExit(outcomeReached, 42); got != ExitOK {
		t.Errorf("reached: got %d, want %d (custom code must not leak to non-timeout)", got, ExitOK)
	}
	if got := resolveWatchExit(outcomeCancelled, 42); got != ExitOK {
		t.Errorf("cancelled: got %d, want %d", got, ExitOK)
	}
}

func TestValidateTimeoutExit(t *testing.T) {
	for _, code := range []int{0, 5, 255} {
		if err := validateTimeoutExit(code); err != nil {
			t.Errorf("code %d should be valid, got %v", code, err)
		}
	}
	for _, code := range []int{-1, 256, 300} {
		err := validateTimeoutExit(code)
		if err == nil || err.exit != ExitUsage {
			t.Errorf("code %d should be a usage error, got %v", code, err)
		}
		if err != nil && !strings.Contains(err.hint, "255") {
			t.Errorf("code %d: hint should name the 0-255 bound, got %q", code, err.hint)
		}
	}
}

// The flag is registered and validated before any tmux work, so a bad code is a
// clean usage error with no live tmux required.
func TestWatchTimeoutExitFlagValidation(t *testing.T) {
	_, errOut, code := run("watch", "--timeout-exit", "300")
	if code != ExitUsage {
		t.Errorf("bad --timeout-exit: exit=%d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "timeout-exit") {
		t.Errorf("stderr should name the flag: %q", errOut)
	}
	// Negative uses the =form so flag parsing doesn't read -1 as the next flag.
	if _, _, code := run("watch", "--timeout-exit=-1"); code != ExitUsage {
		t.Errorf("negative --timeout-exit: exit=%d, want %d", code, ExitUsage)
	}
}

func TestBuildScanResponse(t *testing.T) {
	classes := []paneClass{
		{pane: tmuxPane("work", "1", "0", "%1", true), res: res(program.StatusRunning)},
		{pane: tmuxPane("work", "2", "0", "%2", false), cerr: &cmdError{class: "tmux_no_target"}},
	}
	r := buildScanResponse(classes)
	if r.PaneCount != 2 || len(r.Panes) != 2 {
		t.Fatalf("pane count=%d", r.PaneCount)
	}
	if r.Panes[0].Target != "%1" || r.Panes[0].Classification.Status != program.StatusRunning {
		t.Errorf("pane0 wrong: %+v", r.Panes[0])
	}
	// The unreadable pane is reported unknown with its error class, not guessed.
	if r.Panes[1].Error != "tmux_no_target" || r.Panes[1].Classification.Program != "unknown" {
		t.Errorf("errored pane should be unknown+error: %+v", r.Panes[1])
	}
	if r.Command != "scan" {
		t.Errorf("envelope command=%q, want scan", r.Command)
	}
}

func TestScanText(t *testing.T) {
	r := buildScanResponse([]paneClass{
		{pane: tmuxPane("work", "1", "0", "%1", true), res: res(program.StatusIdle)},
	})
	txt := scanText(r)
	if !strings.Contains(txt, "%1") || !strings.Contains(txt, "claude/idle") || !strings.HasPrefix(txt, "*") {
		t.Errorf("scan text line malformed: %q", txt)
	}
	// Empty pane set is a clean message, not a crash.
	if got := scanText(buildScanResponse(nil)); got != "no tmux panes" {
		t.Errorf("empty scan text=%q", got)
	}
}
