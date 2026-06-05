// Package tmux is the only place muxray shells out to the tmux binary. It owns
// pane-target parsing, capture, and listing. Every tmux failure is surfaced as a
// typed error carrying tmux's own stderr — errors are never silently swallowed.
package tmux

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
)

// Target is a resolved tmux pane target. Raw is what gets passed to `tmux -t`;
// the parsed components are metadata for the output. tmux itself resolves Raw,
// so muxray does not need to perfectly model session/window/pane resolution.
type Target struct {
	Raw     string `json:"raw"`
	Session string `json:"session,omitempty"`
	Window  string `json:"window,omitempty"`
	Pane    string `json:"pane,omitempty"`
}

// Error is a typed tmux failure. Class is a stable, branchable identifier.
type Error struct {
	Class  string
	Args   []string
	Stderr string
	Err    error
}

func (e *Error) Error() string {
	if e.Stderr != "" {
		return fmt.Sprintf("tmux %s: %s", strings.Join(e.Args, " "), e.Stderr)
	}
	return fmt.Sprintf("tmux %s: %v", strings.Join(e.Args, " "), e.Err)
}

func (e *Error) Unwrap() error { return e.Err }

// runCommand is the exec indirection, overridable in tests.
var runCommand = func(name string, args ...string) (stdout, stderr []byte, err error) {
	cmd := exec.Command(name, args...)
	var out, errb bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errb
	err = cmd.Run()
	return out.Bytes(), errb.Bytes(), err
}

// Available reports whether the tmux binary is on PATH.
func Available() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// InsideTmux reports whether muxray is running inside a tmux pane.
func InsideTmux() bool {
	return os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != ""
}

func run(args ...string) (string, error) {
	out, errb, err := runCommand("tmux", args...)
	if err != nil {
		msg := strings.TrimSpace(string(errb))
		class := classify(msg, err)
		if msg == "" {
			msg = err.Error()
		}
		return "", &Error{Class: class, Args: args, Stderr: msg, Err: err}
	}
	return string(out), nil
}

func classify(stderr string, err error) string {
	low := strings.ToLower(stderr)
	switch {
	case strings.Contains(low, "no server running"), strings.Contains(low, "no current session"):
		return "no_server"
	case strings.Contains(low, "can't find") || strings.Contains(low, "no such") || strings.Contains(low, "no pane") || strings.Contains(low, "session not found"):
		return "no_target"
	case errors.Is(err, exec.ErrNotFound):
		return "not_installed"
	default:
		return "tmux_error"
	}
}

// Version returns the tmux version string (e.g. "3.4"), without the "tmux "
// prefix.
func Version() (string, error) {
	out, err := run("-V")
	if err != nil {
		return "", err
	}
	v := strings.TrimSpace(out)
	v = strings.TrimPrefix(v, "tmux ")
	return v, nil
}

// ParseTarget parses the common tmux target forms:
//   - "" → the current pane (requires running inside tmux: $TMUX_PANE)
//   - "%N" → a pane id
//   - "$N" → a session id
//   - "session:window.pane" / "session:window" → session/window/pane
//   - "name" → a session name (tmux targets that session's active pane)
//
// An empty target outside tmux is a typed error, never a silent default.
func ParseTarget(s string) (Target, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		p := os.Getenv("TMUX_PANE")
		if p == "" {
			return Target{}, &Error{
				Class:  "no_target",
				Args:   []string{"(target)"},
				Stderr: "no --pane given and not running inside tmux (TMUX_PANE is unset)",
			}
		}
		return Target{Raw: p, Pane: p}, nil
	}
	if strings.HasPrefix(s, "%") {
		return Target{Raw: s, Pane: s}, nil
	}
	if strings.HasPrefix(s, "$") {
		return Target{Raw: s, Session: s}, nil
	}
	if i := strings.IndexByte(s, ':'); i >= 0 {
		t := Target{Raw: s, Session: s[:i]}
		rest := s[i+1:]
		if j := strings.IndexByte(rest, '.'); j >= 0 {
			t.Window = rest[:j]
			t.Pane = rest[j+1:]
		} else {
			t.Window = rest
		}
		return t, nil
	}
	return Target{Raw: s, Session: s}, nil
}

// CaptureOpts controls a capture-pane invocation.
type CaptureOpts struct {
	// Escapes preserves ANSI escape sequences (capture-pane -e). When false,
	// tmux emits plain text with no escapes.
	Escapes bool
	// JoinWrapped joins wrapped lines (capture-pane -J).
	JoinWrapped bool
	// Scrollback, when > 0, includes that many lines of history above the
	// visible pane (capture-pane -S -N). 0 captures the visible pane only.
	Scrollback int
}

// Capture returns the contents of the target pane.
func Capture(t Target, o CaptureOpts) (string, error) {
	args := []string{"capture-pane", "-p"}
	if o.JoinWrapped {
		args = append(args, "-J")
	}
	if o.Escapes {
		args = append(args, "-e")
	}
	if o.Scrollback > 0 {
		args = append(args, "-S", "-"+strconv.Itoa(o.Scrollback))
	}
	args = append(args, "-t", t.Raw)
	return run(args...)
}

// Pane describes one tmux pane for the list command.
type Pane struct {
	Session        string `json:"session"`
	WindowIndex    string `json:"window_index"`
	WindowName     string `json:"window_name"`
	WindowActive   bool   `json:"window_active"`
	PaneIndex      string `json:"pane_index"`
	PaneID         string `json:"pane_id"`
	PaneActive     bool   `json:"pane_active"`
	CurrentCommand string `json:"current_command"`
	Title          string `json:"title"`
	Width          int    `json:"width"`
	Height         int    `json:"height"`
}

const listFormat = "#{session_name}\t#{window_index}\t#{window_name}\t#{window_active}\t#{pane_index}\t#{pane_id}\t#{pane_active}\t#{pane_current_command}\t#{pane_title}\t#{pane_width}\t#{pane_height}"

// ListPanes returns every pane across all sessions. When no tmux server is
// running (a benign "zero sessions" state, not an error), it returns an empty
// slice and nil — this is interpretation of a known condition, not swallowing.
func ListPanes() ([]Pane, error) {
	out, err := run("list-panes", "-a", "-F", listFormat)
	if err != nil {
		var te *Error
		if errors.As(err, &te) && te.Class == "no_server" {
			return []Pane{}, nil
		}
		return nil, err
	}
	var panes []Pane
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		f := strings.Split(line, "\t")
		if len(f) < 11 {
			continue
		}
		w, _ := strconv.Atoi(f[9])
		h, _ := strconv.Atoi(f[10])
		panes = append(panes, Pane{
			Session:        f[0],
			WindowIndex:    f[1],
			WindowName:     f[2],
			WindowActive:   f[3] == "1",
			PaneIndex:      f[4],
			PaneID:         f[5],
			PaneActive:     f[6] == "1",
			CurrentCommand: f[7],
			Title:          f[8],
			Width:          w,
			Height:         h,
		})
	}
	return panes, nil
}
