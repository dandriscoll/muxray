package telemetry

import (
	"bufio"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKillSwitch(t *testing.T) {
	cases := []struct {
		env  map[string]string
		want bool
	}{
		{map[string]string{"MUXRAY_NO_TELEMETRY": "1"}, true},
		{map[string]string{"MUXRAY_NO_TELEMETRY": "true"}, true},
		{map[string]string{"MUXRAY_NO_TELEMETRY": "0"}, false},
		{map[string]string{"MUXRAY_NO_TELEMETRY": "false"}, false},
		{map[string]string{"DO_NOT_TRACK": "1"}, true},
		{map[string]string{"DO_NOT_TRACK": "true"}, true},
		{map[string]string{"DO_NOT_TRACK": "0"}, false},
		{map[string]string{}, false},
	}
	for _, c := range cases {
		t.Setenv("MUXRAY_NO_TELEMETRY", "")
		t.Setenv("DO_NOT_TRACK", "")
		for k, v := range c.env {
			t.Setenv(k, v)
		}
		if got := KillSwitch(); got != c.want {
			t.Errorf("env %v: KillSwitch=%v, want %v", c.env, got, c.want)
		}
	}
}

func TestFingerprint(t *testing.T) {
	if Fingerprint("") != "" {
		t.Error("empty content should produce empty fingerprint")
	}
	a := Fingerprint("the same screen")
	b := Fingerprint("the same screen")
	if a != b {
		t.Error("fingerprint must be deterministic")
	}
	if a == Fingerprint("a different screen") {
		t.Error("distinct content must produce distinct fingerprints")
	}
	if len(a) != 16 {
		t.Errorf("fingerprint length=%d, want 16", len(a))
	}
}

// TestEventIsContentFree asserts the Event JSON keys are a closed set of safe,
// content-free fields. This is the encoded form of the type-level redaction
// boundary: if someone adds a raw-content field, this test fails loudly.
func TestEventIsContentFree(t *testing.T) {
	e := Event{MuxrayVersion: "x", Command: "status", InvocationID: "i"}
	b, _ := json.Marshal(e)
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatal(err)
	}
	allowed := map[string]bool{
		"muxray_version": true, "os": true, "arch": true, "tmux_version": true,
		"command": true, "duration_ms": true, "success": true, "error_class": true,
		"program": true, "status": true, "rule_id": true, "confidence": true,
		"ansi_normalized": true, "line_count": true, "char_count": true,
		"truncated": true, "diff_changed": true, "diff_hunks": true,
		"snapshot_read_ok": true, "snapshot_write_ok": true, "harness": true,
		"content_fingerprint": true, "invocation_id": true,
	}
	for k := range m {
		if !allowed[k] {
			t.Errorf("Event carries unexpected field %q — telemetry must stay content-free", k)
		}
	}
}

func TestRecord_DebugWritesLocalLog(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("MUXRAY_NO_TELEMETRY", "")
	t.Setenv("DO_NOT_TRACK", "")

	Record(Event{Command: "status", Success: true, InvocationID: "abc"}, true)

	logPath := filepath.Join(dir, "muxray", "debug.log")
	f, err := os.Open(logPath)
	if err != nil {
		t.Fatalf("debug log not written: %v", err)
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if !sc.Scan() {
		t.Fatal("debug log empty")
	}
	if !strings.Contains(sc.Text(), `"command":"status"`) {
		t.Errorf("unexpected log line: %s", sc.Text())
	}
}

func TestRecord_NoDebugNoWrite(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	Record(Event{Command: "status"}, false)
	if _, err := os.Stat(filepath.Join(dir, "muxray", "debug.log")); !os.IsNotExist(err) {
		t.Error("debug log should not exist without --debug")
	}
}

func TestRecord_KillSwitchSuppresses(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("XDG_STATE_HOME", dir)
	t.Setenv("MUXRAY_NO_TELEMETRY", "1")
	Record(Event{Command: "status"}, true)
	if _, err := os.Stat(filepath.Join(dir, "muxray", "debug.log")); !os.IsNotExist(err) {
		t.Error("kill switch must suppress even the local debug log")
	}
}
