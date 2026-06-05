package tmux_test

// Mock-harness tests: a deterministic "dummy backend" emits program-
// characteristic terminal output into a real tmux pane, which muxray then reads
// through its real capture -> normalize -> classify pipeline. This validates the
// end-to-end classification of state transitions (startup, running, approval,
// error, completion) without any real model call or program credential.
//
// The screens are rendered with printf (no temp files), so the test is hermetic
// and does not depend on the surrounding TMPDIR path. These run on the default
// tmux server (exercising muxray's own Capture path) but create and destroy only
// their own uniquely-named session, and skip cleanly when tmux is unavailable or
// in -short mode.

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dandriscoll/muxray/internal/normalize"
	"github.com/dandriscoll/muxray/internal/program"
	"github.com/dandriscoll/muxray/internal/tmux"
)

func TestMockHarness_StateTransitions(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping mock-harness test in -short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping mock-harness test")
	}

	session := "muxray-harness-" + strconv.Itoa(os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "120", "-y", "40").CombinedOutput(); err != nil {
		t.Skipf("could not start tmux session (%v): %s", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", session).Run()

	steps := []struct {
		name        string
		lines       []string
		wantProgram string
		wantStatus  program.Status
	}{
		{"startup", []string{"GitHub Copilot", "  How can I help you today?", "  copilot>"}, "copilot", program.StatusIdle},
		{"running", []string{"OpenAI Codex", "  Working (esc to interrupt)", "  Running command: go test"}, "codex", program.StatusRunning},
		{"approval", []string{"Claude Code", "  Claude wants to run a command.", "  Do you want to proceed?", "  1. Yes"}, "claude", program.StatusNeedsApproval},
		{"error", []string{"Claude", "  API Error: 500 Internal Server Error", "  The request failed."}, "claude", program.StatusError},
		{"completion", []string{"Claude", "  Done! Completed in 1m 24s.", "", "  ? for shortcuts"}, "claude", program.StatusCompleted},
	}

	for i, s := range steps {
		t.Run(s.name, func(t *testing.T) {
			// Render the screen, then print a readiness sentinel from a SEPARATE
			// command. Two properties make this race-free and side-effect-free:
			//   1. The sentinel is split with a quote (echo "MUXRDY"<i>) so the
			//      contiguous token appears only in the command's OUTPUT, never in
			//      the typed command echo — so waitFor fires after the screen is
			//      actually drawn and the prior step cleared, not while the next
			//      command is merely echoed.
			//   2. The token is a unique, word-free string (MUXRDY<i>) so it never
			//      collides with a program rule phrase (e.g. "done").
			payload := strings.Join(s.lines, `\n`) + `\n`
			ready := "MUXRDY" + strconv.Itoa(i)
			sendKeys(t, session, "clear")
			sendKeys(t, session, "printf '"+payload+"'")
			sendKeys(t, session, `echo "MUXRDY"`+strconv.Itoa(i))
			if !waitFor(t, session, ready) {
				t.Fatalf("pane never showed readiness sentinel %q", ready)
			}

			raw, err := tmux.Capture(tmux.Target{Raw: session}, tmux.CaptureOpts{JoinWrapped: true})
			if err != nil {
				t.Fatalf("muxray Capture: %v", err)
			}
			clean := normalize.Clean(raw, 200).Clean
			res := program.Detect(clean, false)
			if res.Program != s.wantProgram || res.Status != s.wantStatus {
				t.Errorf("step %s: got %s/%s, want %s/%s\npane:\n%s",
					s.name, res.Program, res.Status, s.wantProgram, s.wantStatus, clean)
			}
		})
	}
}

func sendKeys(t *testing.T, target, line string) {
	t.Helper()
	if out, err := exec.Command("tmux", "send-keys", "-t", target, line, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send-keys %q: %v: %s", line, err, out)
	}
}

func waitFor(t *testing.T, target, want string) bool {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := exec.Command("tmux", "capture-pane", "-p", "-t", target).CombinedOutput()
		if err == nil && strings.Contains(string(out), want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
