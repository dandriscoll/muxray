package tmux_test

// Mock-harness tests: a deterministic "dummy backend" emits program-
// characteristic terminal output into a real tmux pane, which muxray then reads
// through its real capture -> normalize -> classify pipeline. This validates the
// end-to-end classification of state transitions (startup, running, approval,
// error, completion) without any real model call or program credential.
//
// The screens are rendered with printf (no temp files), so the test is hermetic
// and does not depend on the surrounding TMPDIR path. It runs on a PRIVATE tmux
// server (a dedicated socket under a fresh TMUX_TMPDIR), never the default
// server: driving the default server from inside a live tmux+agent session
// pokes the operator's panes and can destabilize their loop. Because the
// private server is addressed through TMUX_TMPDIR, muxray's own Capture path
// (which shells out to bare `tmux`) exercises the same private server — the
// test still covers muxray's real capture code, just pointed at an isolated
// socket. The test strips TMUX/TMUX_PANE from every tmux invocation so a client
// launched from inside tmux can never resolve a target against the outer
// server. Skips cleanly when tmux is unavailable or in -short mode.

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

// harnessEnv owns the private tmux server the mock harness renders into.
type harnessEnv struct {
	t      *testing.T
	tmpdir string
}

// newHarnessEnv starts a private tmux server under a fresh TMUX_TMPDIR and
// returns the env every tmux invocation must run with. Isolation is by
// TMUX_TMPDIR alone (the server keeps the default socket NAME inside the
// private dir): muxray's own Capture path shells out to bare `tmux`, so the
// only way to point it at this server without code changes is the env var —
// a named -L socket would leave bare `tmux` reaching the real default server.
func newHarnessEnv(t *testing.T) *harnessEnv {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping mock-harness test in -short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping mock-harness test")
	}
	tmpdir, err := os.MkdirTemp("", "muxray-harness-tmp-")
	if err != nil {
		t.Fatalf("MkdirTemp: %v", err)
	}
	e := &harnessEnv{t: t, tmpdir: tmpdir}
	t.Cleanup(func() {
		_, _ = e.tmux("kill-server")
		_ = os.RemoveAll(tmpdir)
	})
	return e
}

// env returns the environment for a tmux client: the private server's
// TMUX_TMPDIR, with TMUX/TMUX_PANE removed so no target ever resolves against
// an outer (possibly default) server.
func (e *harnessEnv) env() []string {
	env := append([]string{}, os.Environ()...)
	filtered := env[:0]
	for _, kv := range env {
		if strings.HasPrefix(kv, "TMUX=") || strings.HasPrefix(kv, "TMUX_PANE=") {
			continue
		}
		filtered = append(filtered, kv)
	}
	return append(filtered, "TMUX_TMPDIR="+e.tmpdir)
}

// tmux runs a tmux client against the private server only.
func (e *harnessEnv) tmux(args ...string) (string, error) {
	e.t.Helper()
	cmd := exec.Command("tmux", args...)
	cmd.Env = e.env()
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestMockHarness_StateTransitions(t *testing.T) {
	e := newHarnessEnv(t)

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
			// Each step gets its own session (isolation, no cross-step bleed).
			session := "muxray-harness-" + strconv.Itoa(os.Getpid()) + "-" + strconv.Itoa(i)
			if out, err := e.tmux("new-session", "-d", "-s", session, "-x", "120", "-y", "40"); err != nil {
				t.Skipf("could not start tmux session (%v): %s", err, out)
			}
			defer e.tmux("kill-session", "-t", session)

			// Render the harness screen, then HOLD it in the foreground with
			// `sleep` so the shell never draws a prompt below the content. This
			// makes the captured pane a genuine LIVE harness frame whose footer is
			// the harness's own output — not a returned-to-shell prompt, which
			// muxray now (correctly) classifies as shell/idle. Without the hold the
			// trailing shell prompt is captured nondeterministically (it raced
			// green locally and red in CI). A readiness sentinel is echoed last;
			// it is a unique, word-free token (MUXRDY<i>) that matches no program
			// rule and is not a shell-prompt shape, so it neither perturbs the
			// classification nor trips shell detection. It is quote-split
			// (echo "MUXRDY"<i>) so the contiguous token appears only in OUTPUT,
			// never in the typed command echo.
			payload := strings.Join(s.lines, `\n`) + `\n`
			ready := "MUXRDY" + strconv.Itoa(i)
			sendKeys(e, session, "clear")
			sendKeys(e, session, "printf '"+payload+"'; echo \"MUXRDY\""+strconv.Itoa(i)+"; sleep 30")
			if !waitFor(e, session, ready) {
				t.Fatalf("pane never showed readiness sentinel %q", ready)
			}

			// muxray's own Capture path, pointed at the private server through
			// TMUX_TMPDIR (its run() shells out to bare `tmux`).
			captureCmd := func(name string, args ...string) (stdout, stderr []byte, err error) {
				cmd := exec.Command(name, args...)
				cmd.Env = e.env()
				var outb, errb strings.Builder
				cmd.Stdout = &outb
				cmd.Stderr = &errb
				err = cmd.Run()
				return []byte(outb.String()), []byte(errb.String()), err
			}
			origRun := tmux.SetRunCommand(captureCmd)
			defer origRun()
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

func sendKeys(e *harnessEnv, target, line string) {
	e.t.Helper()
	if out, err := e.tmux("send-keys", "-t", target, line, "Enter"); err != nil {
		e.t.Fatalf("send-keys %q: %v: %s", line, err, out)
	}
}

func waitFor(e *harnessEnv, target, want string) bool {
	e.t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, err := e.tmux("capture-pane", "-p", "-t", target)
		if err == nil && strings.Contains(out, want) {
			return true
		}
		time.Sleep(50 * time.Millisecond)
	}
	return false
}
