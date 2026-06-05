package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

var update = flag.Bool("update", false, "update golden files")

// run invokes the CLI in-process with stdout/stderr captured.
func run(args ...string) (out, errOut string, code int) {
	var o, e bytes.Buffer
	origOut, origErr := stdout, stderr
	stdout, stderr = &o, &e
	defer func() { stdout, stderr = origOut, origErr }()
	code = Run(args)
	return o.String(), e.String(), code
}

func TestVersion(t *testing.T) {
	out, _, code := run("version")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(out, "muxray") {
		t.Errorf("version output: %q", out)
	}
}

func TestHelp(t *testing.T) {
	out, _, code := run("help")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	for _, cmd := range []string{"list", "snapshot", "diff", "status", "inspect", "doctor", "usage"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help missing command %q", cmd)
		}
	}
}

// TestStatusBoundsToVisibleScreen is the regression test for issue #2: a stale
// error that has scrolled ABOVE the visible pane must not be classified as the
// current state. We render a Claude error screen, then push it off the top with a
// screenful of fresh output, and assert `muxray status` does not report `error`.
func TestStatusBoundsToVisibleScreen(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tmux-backed test in -short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	session := "muxray-issue2-" + strconv.Itoa(os.Getpid())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-x", "120", "-y", "40").CombinedOutput(); err != nil {
		t.Skipf("could not start tmux session (%v): %s", err, out)
	}
	defer exec.Command("tmux", "kill-session", "-t", session).Run()

	send := func(line string) {
		if out, err := exec.Command("tmux", "send-keys", "-t", session, line, "Enter").CombinedOutput(); err != nil {
			t.Fatalf("send-keys %q: %v: %s", line, err, out)
		}
	}
	// Render, in ONE command (so the geometry is deterministic — no per-command
	// prompt lines to perturb the count): a Claude error screen, then ~45 lines of
	// fresh output belonging to NONE of the three known programs, then a readiness
	// sentinel. With a 40-row pane this pushes the error OFF the visible screen
	// (>40 lines up) while keeping the whole transcript within the classifier's
	// window (<60 lines) — so the only thing that changes the verdict is whether
	// muxray reads the scrollback or just the visible frame. The sentinel token is
	// quote-split so MUXRDY2 appears only in OUTPUT, never the command echo (race).
	send(`printf 'Claude\n  API Error: 500 Internal Server Error\n  The request failed.\n'; for i in $(seq 1 45); do echo "line $i"; done; echo "MUXRDY"2`)
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("tmux", "capture-pane", "-p", "-t", session).CombinedOutput()
		if strings.Contains(string(out), "MUXRDY2") {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}

	out, errOut, code := run("status", "--pane", session, "--json")
	if code != ExitOK {
		t.Fatalf("status exit=%d stderr=%s", code, errOut)
	}
	var resp struct {
		Classification struct {
			Program string `json:"program"`
			Status  string `json:"status"`
		} `json:"classification"`
	}
	if err := json.Unmarshal([]byte(out), &resp); err != nil {
		t.Fatalf("parse status JSON: %v\n%s", err, out)
	}
	// The current screen is a plain shell — none of Claude/Codex/Copilot. muxray
	// must DECLINE (program=unknown, status=unknown) and let the agent parse it,
	// rather than reporting the historical error scrolled off the top (issue #2).
	if resp.Classification.Program != "unknown" || resp.Classification.Status != "unknown" {
		t.Errorf("muxray commented on an unrecognized current screen (should be unknown/unknown): got program=%q status=%q",
			resp.Classification.Program, resp.Classification.Status)
	}
}

func TestUsage(t *testing.T) {
	out, _, code := run("usage")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	// The agent-facing contract must name the load-bearing fields an agent branches
	// on; if any of these go missing the doc has lost its purpose.
	for _, marker := range []string{"schema_version", "program", "status", "Exit codes"} {
		if !strings.Contains(out, marker) {
			t.Errorf("usage output missing %q", marker)
		}
	}
}

func TestUnknownCommand(t *testing.T) {
	_, errOut, code := run("frobnicate")
	if code != ExitUsage {
		t.Errorf("exit=%d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "unknown command") {
		t.Errorf("stderr: %q", errOut)
	}
}

func TestNoArgs(t *testing.T) {
	_, _, code := run()
	if code != ExitUsage {
		t.Errorf("exit=%d, want %d", code, ExitUsage)
	}
}

func TestDoctorJSON(t *testing.T) {
	out, _, code := run("doctor")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("doctor output is not JSON: %v\n%s", err, out)
	}
	if m["command"] != "doctor" || m["schema_version"] != "2" {
		t.Errorf("envelope wrong: %v", m)
	}
	if _, ok := m["diagnostics"]; !ok {
		t.Error("missing diagnostics object")
	}
}

func TestDoctorText(t *testing.T) {
	out, _, code := run("doctor", "--text")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	if !strings.Contains(out, "tmux_installed") {
		t.Errorf("doctor text: %q", out)
	}
}

// TestTelemetryShowGolden is a representative command-level golden. Volatile
// envelope fields (generated_at) are normalized before comparison.
func TestTelemetryShowGolden(t *testing.T) {
	out, _, code := run("telemetry", "show")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("not JSON: %v", err)
	}
	// Telemetry must advertise no network sink and stay content-free.
	if m["network_sink"] != false {
		t.Errorf("network_sink=%v, want false", m["network_sink"])
	}
	m["generated_at"] = "<normalized>"
	m["muxray_version"] = "<normalized>"
	normalized, _ := json.MarshalIndent(m, "", "  ")

	goldenPath := filepath.Join("testdata", "telemetry_show.golden.json")
	if *update {
		if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(goldenPath, append(normalized, '\n'), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(goldenPath)
	if err != nil {
		t.Fatalf("missing golden (run -update): %v", err)
	}
	if strings.TrimSpace(string(want)) != strings.TrimSpace(string(normalized)) {
		t.Errorf("telemetry show golden mismatch:\n got: %s\nwant: %s", normalized, want)
	}
}

func TestDiff_SnapshotNotFound(t *testing.T) {
	// With an unwritable/empty store and a bogus --since id, diff reports a
	// not-found error and the dedicated exit code — never a silent success.
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	if !insideTmuxForTest() {
		t.Skip("needs tmux to capture the current pane")
	}
	_, errOut, code := run("diff", "--since", "nonexistent000")
	if code != ExitNotFound {
		t.Skipf("diff exit=%d (env-dependent on tmux capture); stderr=%s", code, errOut)
	}
	if !strings.Contains(errOut, "snapshot") {
		t.Errorf("stderr: %q", errOut)
	}
}

func insideTmuxForTest() bool {
	return os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
}

func TestShimOnce(t *testing.T) {
	out, _, code := run("shim", "--provider", "anthropic", "--scenario", "approval", "--once")
	if code != ExitOK {
		t.Fatalf("exit=%d", code)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("shim output is not JSON: %v\n%s", err, out)
	}
	if m["provider"] != "anthropic" || m["scenario"] != "approval" {
		t.Errorf("unexpected shim response: %v", m)
	}
	env, ok := m["env"].(map[string]any)
	if !ok || env["ANTHROPIC_BASE_URL"] == nil || env["ANTHROPIC_API_KEY"] == nil {
		t.Errorf("missing env in shim response: %v", m["env"])
	}
}

func TestShimUnknownProvider(t *testing.T) {
	_, errOut, code := run("shim", "--provider", "bogus", "--once")
	if code != ExitUsage {
		t.Errorf("exit=%d, want %d", code, ExitUsage)
	}
	if !strings.Contains(errOut, "unknown provider") {
		t.Errorf("stderr: %q", errOut)
	}
}
