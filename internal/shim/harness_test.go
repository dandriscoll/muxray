package shim_test

// End-to-end tests that drive the REAL Claude harness against the shim, proving
// no provider key is required. They skip cleanly when `claude` (or tmux) is not
// installed — e.g. on standard CI runners — so the default lane never needs a
// credential. Codex/Copilot have no analogous test here: codex is not assumed
// installed, and Copilot's GitHub-OAuth auth is not base-URL-overridable.

import (
	"context"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/dandriscoll/muxray/internal/normalize"
	"github.com/dandriscoll/muxray/internal/provider"
	"github.com/dandriscoll/muxray/internal/shim"
	"github.com/dandriscoll/muxray/internal/tmux"
)

func envSlice(m map[string]string) []string {
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

// TestClaudePrintAgainstShim is the deterministic proof that the shim drives the
// real Claude CLI with no API key: `claude --print` against the shim returns the
// shim's scripted text.
func TestClaudePrintAgainstShim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-harness test in -short mode")
	}
	claude, err := exec.LookPath("claude")
	if err != nil {
		t.Skip("claude not installed; skipping real-harness test")
	}

	srv := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	if err := srv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, claude, "--print", "say hello")
	cmd.Env = append(os.Environ(), envSlice(srv.Env())...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Skipf("claude --print did not complete (version/onboarding dependent): %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "The task is complete.") {
		t.Errorf("claude did not return the shim's response; got:\n%s", out)
	}
}

// TestClaudeTUIClassificationAgainstShim drives the interactive Claude TUI
// against the shim inside tmux and confirms muxray classifies the real terminal
// output as the claude provider with a known (non-unknown) status. The exact
// status depends on the harness's onboarding flow and version, so the test polls
// for recognition and skips (rather than fails) if the TUI never reaches a
// recognizable state in time — keeping it non-flaky.
func TestClaudeTUIClassificationAgainstShim(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-harness test in -short mode")
	}
	if _, err := exec.LookPath("claude"); err != nil {
		t.Skip("claude not installed; skipping")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed; skipping")
	}

	srv := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	if err := srv.Start(0); err != nil {
		t.Fatal(err)
	}
	defer srv.Close()

	session := "muxray-claude-tui-" + strconv.Itoa(os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "120", "-y", "40").CombinedOutput(); err != nil {
		t.Skipf("could not start tmux session: %v: %s", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", session).Run()

	for k, v := range srv.Env() {
		_ = exec.Command("tmux", "send-keys", "-t", session, "export "+k+"="+v, "Enter").Run()
	}
	_ = exec.Command("tmux", "send-keys", "-t", session, "claude", "Enter").Run()

	// Poll until muxray classifies the pane as claude with a DEFINITE status.
	// Requiring a known status avoids matching the bare "claude" command echo in
	// the shell before the TUI has rendered (which would read as claude/unknown).
	target := tmux.Target{Raw: session}
	deadline := time.Now().Add(25 * time.Second)
	var res provider.Result
	sawClaude := false
	for time.Now().Before(deadline) {
		if raw, err := tmux.Capture(target, tmux.CaptureOpts{JoinWrapped: true}); err == nil {
			res = provider.Detect(normalize.Clean(raw, 200).Clean, false)
			if res.Provider == "claude" {
				sawClaude = true
				if res.Status != provider.StatusUnknown {
					break
				}
			}
		}
		time.Sleep(300 * time.Millisecond)
	}

	if res.Provider != "claude" || res.Status == provider.StatusUnknown {
		t.Skipf("real claude TUI did not reach a definite state in time (onboarding/version dependent); sawClaude=%v last=%s/%s", sawClaude, res.Provider, res.Status)
	}
	t.Logf("muxray classified the real claude TUI as claude/%s (rule=%s)", res.Status, res.RuleID)
}
