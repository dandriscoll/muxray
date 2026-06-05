// Package diag provides local diagnostics: the `doctor` environment report, a
// sanitized diagnostic bundle for bug reports, and the redaction used to keep
// any optional excerpt free of obvious secrets. Pane content can contain
// secrets, so the bundle omits content by default and redacts it when explicitly
// included.
package diag

import (
	"os"
	"regexp"
	"runtime"

	"github.com/dandriscoll/muxray/internal/config"
	"github.com/dandriscoll/muxray/internal/snapshot"
	"github.com/dandriscoll/muxray/internal/telemetry"
	"github.com/dandriscoll/muxray/internal/tmux"
)

// Check is a single named diagnostic with a pass/fail and a human detail.
type Check struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
}

// Report is the doctor output.
type Report struct {
	MuxrayVersion       string  `json:"muxray_version"`
	OS                  string  `json:"os"`
	Arch                string  `json:"arch"`
	TmuxFound           bool    `json:"tmux_found"`
	TmuxVersion         string  `json:"tmux_version,omitempty"`
	InsideTmux          bool    `json:"inside_tmux"`
	SnapshotDir         string  `json:"snapshot_dir"`
	SnapshotDirWritable bool    `json:"snapshot_dir_writable"`
	ConfigPath          string  `json:"config_path"`
	ConfigExists        bool    `json:"config_exists"`
	TelemetryKillSwitch bool    `json:"telemetry_kill_switch"`
	TelemetryExternal   bool    `json:"telemetry_external_enabled"`
	Checks              []Check `json:"checks"`
}

// Doctor collects local environment diagnostics. version is passed in to avoid
// an import cycle with the version package.
func Doctor(muxrayVersion string) Report {
	r := Report{
		MuxrayVersion:       muxrayVersion,
		OS:                  runtime.GOOS,
		Arch:                runtime.GOARCH,
		TmuxFound:           tmux.Available(),
		InsideTmux:          tmux.InsideTmux(),
		SnapshotDir:         snapshot.DefaultDir(),
		ConfigPath:          config.Path(),
		ConfigExists:        config.Exists(),
		TelemetryKillSwitch: telemetry.KillSwitch(),
		TelemetryExternal:   telemetry.ExternalEnabled(),
	}
	if r.TmuxFound {
		if v, err := tmux.Version(); err == nil {
			r.TmuxVersion = v
		}
	}
	r.SnapshotDirWritable = dirWritable(r.SnapshotDir)

	r.Checks = []Check{
		boolCheck("tmux_installed", r.TmuxFound,
			"tmux found on PATH",
			"tmux is NOT on PATH — muxray needs tmux to read panes; install tmux"),
		boolCheck("snapshot_dir_writable", r.SnapshotDirWritable,
			"snapshot store is writable: "+r.SnapshotDir,
			"snapshot store is NOT writable: "+r.SnapshotDir),
		boolCheck("inside_tmux", r.InsideTmux,
			"running inside a tmux pane ($TMUX set)",
			"not inside tmux — pass an explicit --pane target"),
	}
	return r
}

func boolCheck(name string, ok bool, okDetail, failDetail string) Check {
	if ok {
		return Check{Name: name, OK: true, Detail: okDetail}
	}
	return Check{Name: name, OK: false, Detail: failDetail}
}

// dirWritable reports whether dir (created if needed) accepts a write.
func dirWritable(dir string) bool {
	if dir == "" {
		return false
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return false
	}
	f, err := os.CreateTemp(dir, ".muxray-writecheck-*")
	if err != nil {
		return false
	}
	name := f.Name()
	_ = f.Close()
	_ = os.Remove(name)
	return true
}

// Secret-shaped patterns redacted from any included excerpt. This is a
// best-effort defense for the explicit-excerpt path, not a substitute for the
// default of omitting content entirely.
var secretPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-[A-Za-z0-9_-]{16,}`),                                         // OpenAI-style keys
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{16,}`),                                     // Anthropic keys
	regexp.MustCompile(`gh[pousr]_[A-Za-z0-9]{20,}`),                                    // GitHub tokens
	regexp.MustCompile(`AKIA[0-9A-Z]{16}`),                                              // AWS access key id
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._-]{16,}`),                              // bearer tokens
	regexp.MustCompile(`xox[baprs]-[A-Za-z0-9-]{10,}`),                                  // Slack tokens
	regexp.MustCompile(`eyJ[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}\.[A-Za-z0-9_-]{10,}`), // JWTs
	regexp.MustCompile(`-----BEGIN [A-Z ]*PRIVATE KEY-----`),                            // PEM private keys
}

// Redact replaces secret-shaped substrings with a placeholder.
func Redact(s string) string {
	for _, re := range secretPatterns {
		s = re.ReplaceAllString(s, "[REDACTED]")
	}
	return s
}
