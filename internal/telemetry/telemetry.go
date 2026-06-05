// Package telemetry defines structured diagnostic events and the redaction
// boundary that keeps them private. The boundary is the type system itself: the
// Event struct has no field that can hold raw pane content, prompts, secrets, or
// environment — only counts, classes, booleans, and hashes. Accidental content
// capture is therefore a compile error, not a runtime hope.
//
// Privacy posture (per the directive):
//   - Telemetry is opt-in. This version ships NO network sink at all, so there
//     is no egress to leak; the event struct, redaction boundary, previewer, and
//     kill switches all exist so a future sink is a small, auditable addition.
//   - Strictly local diagnostic logging (under --debug) is the only thing that
//     writes anything, and only to a local file.
//   - MUXRAY_NO_TELEMETRY / DO_NOT_TRACK / config hard-disable everything.
//   - All telemetry operations are non-fatal.
package telemetry

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/dandriscoll/muxray/internal/config"
)

// Event is a single invocation's diagnostic record. Every field is a safe,
// low-cardinality, content-free signal. There is intentionally no field for raw
// text, prompts, completions, file contents, or environment variables.
type Event struct {
	MuxrayVersion      string  `json:"muxray_version"`
	OS                 string  `json:"os"`
	Arch               string  `json:"arch"`
	TmuxVersion        string  `json:"tmux_version,omitempty"`
	Command            string  `json:"command"`
	DurationMS         int64   `json:"duration_ms"`
	Success            bool    `json:"success"`
	ErrorClass         string  `json:"error_class,omitempty"`
	Program            string  `json:"program,omitempty"`
	Status             string  `json:"status,omitempty"`
	RuleID             string  `json:"rule_id,omitempty"`
	Confidence         float64 `json:"confidence,omitempty"`
	ANSINormalized     bool    `json:"ansi_normalized"`
	LineCount          int     `json:"line_count"`
	CharCount          int     `json:"char_count"`
	Truncated          bool    `json:"truncated"`
	DiffChanged        *bool   `json:"diff_changed,omitempty"`
	DiffHunks          int     `json:"diff_hunks,omitempty"`
	SnapshotReadOK     *bool   `json:"snapshot_read_ok,omitempty"`
	SnapshotWriteOK    *bool   `json:"snapshot_write_ok,omitempty"`
	Harness            string  `json:"harness,omitempty"`
	ContentFingerprint string  `json:"content_fingerprint,omitempty"`
	InvocationID       string  `json:"invocation_id"`
}

// KillSwitch reports whether telemetry is hard-disabled by environment. A hard
// disable suppresses even the local debug log.
func KillSwitch() bool {
	if v := os.Getenv("MUXRAY_NO_TELEMETRY"); v != "" && v != "0" && !strings.EqualFold(v, "false") {
		return true
	}
	if v := os.Getenv("DO_NOT_TRACK"); v == "1" || strings.EqualFold(v, "true") {
		return true
	}
	return false
}

// ExternalEnabled reports whether external (network) telemetry would be emitted
// if a sink existed. It requires explicit opt-in via config and is overridden by
// the kill switch. This version has no sink, so this only informs `telemetry
// show`.
func ExternalEnabled() bool {
	if KillSwitch() {
		return false
	}
	return config.Load().Telemetry.Enabled
}

// Fingerprint returns a short, irreversible hash of cleaned text — a stable
// identifier for "the same screen" that carries none of the content.
func Fingerprint(clean string) string {
	if clean == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(sum[:])[:16]
}

// Show renders the event exactly as it would be recorded/sent. Because the sink
// (future) reuses this same Event, the preview equals the payload by
// construction.
func Show(e Event) string {
	b, _ := json.MarshalIndent(e, "", "  ")
	return string(b)
}

// Record writes the event to a local debug log when debug logging is on. It
// never performs network I/O in this version and never panics — telemetry must
// not affect the critical path.
func Record(e Event, debug bool) {
	defer func() { _ = recover() }() // belt-and-suspenders: never fatal
	if KillSwitch() {
		return
	}
	if !debug {
		return // local-only diagnostic logging is gated on --debug
	}
	dir := stateDir()
	if dir == "" {
		return
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, "debug.log"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	_, _ = f.Write(append(b, '\n'))
}

// stateDir is the local muxray state directory (honors XDG_STATE_HOME).
func stateDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "muxray")
}

// DebugLogPath returns the path of the local debug log.
func DebugLogPath() string {
	d := stateDir()
	if d == "" {
		return ""
	}
	return filepath.Join(d, "debug.log")
}
