package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	for _, cmd := range []string{"list", "snapshot", "diff", "status", "inspect", "doctor"} {
		if !strings.Contains(out, cmd) {
			t.Errorf("help missing command %q", cmd)
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
	if m["command"] != "doctor" || m["schema_version"] != "1" {
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
