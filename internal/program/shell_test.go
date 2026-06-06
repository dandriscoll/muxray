package program_test

import (
	"strings"
	"testing"

	"github.com/dandriscoll/muxray/internal/program"
)

// TestShellPrompt_Variants asserts that a pane whose footer ends at an
// interactive shell prompt classifies as shell/idle across common prompt
// shapes — the re-coding of a websocket / incus-VM disconnect that dropped the
// user back to the shell (the harness is no longer live).
func TestShellPrompt_Variants(t *testing.T) {
	cases := []struct {
		name string
		in   string
	}{
		{"starship_context_verified_shape", " dev@host  /tmp\n"},
		{"starship_with_branch", " dev@host  /src/project   main\n"},
		{"bash_userhost_path", "user@host:~/project$\n"},
		{"zsh_userhost_percent", "user@host ~ %\n"},
		{"leading_glyph_starship", "❯ \n"},
		{"leading_glyph_arrow", "➜  ~/src git:(main)\n"},
		// The exact shape from the bug report: a Starship context line with the
		// transport-closure error appended after the path on the same line.
		{"disconnect_oneliner", "dev@host  /src/project   main Error: websocket: close 1006 (abnormal closure): unexpected EOF\n"},
		// Realistic post-disconnect pane: harness scrollback + closure error, then
		// a fresh shell prompt as the footer. The scrolled claude/error must lose
		// to the footer prompt.
		{"disconnect_full_pane", "✻ Claude Code\n  I'll start now.\n  Error: websocket: close 1006 (abnormal closure): unexpected EOF\n dev@host  /src/project   main\n"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := program.Detect(c.in, false)
			if res.Program != "shell" || res.Status != program.StatusIdle || res.RuleID != "shell.idle" {
				t.Errorf("got %s/%s rule=%s, want shell/idle rule=shell.idle", res.Program, res.Status, res.RuleID)
			}
		})
	}
}

// TestShellPrompt_NegativesAndVetoes asserts the detector does NOT steal a live
// harness frame or misfire on displayed text that merely contains prompt-shaped
// characters.
func TestShellPrompt_NegativesAndVetoes(t *testing.T) {
	cases := []struct {
		name        string
		in          string
		wantProgram string
		wantStatus  program.Status
	}{
		// Live Claude running: footer carries the "esc to interrupt" hint → veto.
		{"claude_running_not_shell", "Claude Code\n  esc to interrupt\n", "claude", program.StatusRunning},
		// Claude approval menu uses "❯ 1." after the glyph — the glyph branch must
		// not treat an enumerated menu as a shell prompt.
		{"claude_menu_not_shell", "Claude Code\n  Do you want to proceed?\n  ❯ 1. Yes\n", "claude", program.StatusNeedsApproval},
		// Claude's bordered input box (box-drawing) → veto even with a ">" inside.
		{"claude_box_not_shell", "Claude Code\n╭──────────╮\n│ > run it │\n╰──────────╯\n? for shortcuts\n", "claude", program.StatusIdle},
		// A git remote URL is host:path (colon, no space) → not a context prompt.
		{"git_url_not_shell", "origin git@github.com:org/repo.git (fetch)\n", "unknown", program.StatusUnknown},
		// A displayed dollar amount must not look like a POSIX prompt (no @host).
		{"cost_line_not_shell", "Refund processed: $42.00 to the customer\n", "unknown", program.StatusUnknown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			res := program.Detect(c.in, false)
			if res.Program == "shell" {
				t.Fatalf("misfired as shell: %s/%s evidence=%q", res.Program, res.Status, res.Evidence)
			}
			if res.Program != c.wantProgram || res.Status != c.wantStatus {
				t.Errorf("got %s/%s, want %s/%s", res.Program, res.Status, c.wantProgram, c.wantStatus)
			}
		})
	}
}

// TestShellPrompt_LastLineWinsOverScrolledContent is the regression for job 275
// case 1: a pane displaying muxray's own JSON output (whose embedded capture
// carries box-drawing and harness phrases) with a real shell prompt on the last
// line. Pre-fix, the whole-footer box/hint veto fired on the scrolled content and
// suppressed the prompt → unknown. The footer-bound principle says the last line
// (the prompt) is the present state → shell/idle.
func TestShellPrompt_LastLineWinsOverScrolledContent(t *testing.T) {
	frame := strings.Join([]string{
		`{`,
		`  "tail_excerpt": [`,
		`    "────────────────────────────────────────────",`,
		`    "  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt",`,
		`    "[default] 0:bash*                          \"devlogs\" 12:12 03-Jun-26"`,
		`  ]`,
		`}`,
		` dan@mataf  /src `,
	}, "\n")
	res := program.Detect(frame, false)
	if res.Program != "shell" || res.Status != program.StatusIdle {
		t.Fatalf("scrolled box-drawing must not suppress a real shell prompt: got %s/%s, want shell/idle", res.Program, res.Status)
	}
}

// TestShellPrompt_NestedTmuxStatusBar is the regression for job 275 case 2: a
// nested-tmux pane whose visible footer is just the inner tmux status bar (active
// window running bash) over blank lines, with a harness phrase far up that trips
// the signature. The status bar's active window is the present state → shell/idle.
// The companion guard: a status bar whose active window runs an AGENT
// ("0:claude*") must NOT be called shell — it flows to the harness classifier.
func TestShellPrompt_NestedTmuxStatusBar(t *testing.T) {
	build := func(active string) string {
		ls := []string{`  "evidence": "  ⏵⏵ bypass permissions on (shift+tab to cycle)",`}
		for i := 0; i < 16; i++ {
			ls = append(ls, ``)
		}
		ls = append(ls, `[default] 0:`+active+`*                                       "devlogs" 12:12 03-Jun-26`)
		return strings.Join(ls, "\n")
	}
	if res := program.Detect(build("bash"), false); res.Program != "shell" || res.Status != program.StatusIdle {
		t.Errorf("nested tmux active window=bash: got %s/%s, want shell/idle", res.Program, res.Status)
	}
	// Active window is an agent → not a shell (must reach the harness classifier,
	// not be hijacked as shell by the status-bar heuristic).
	if res := program.Detect(build("claude"), false); res.Program == "shell" {
		t.Errorf("nested tmux active window=claude must NOT be shell; got %s/%s", res.Program, res.Status)
	}
}

// TestShellPrompt_ExplainTrace verifies the shell pre-check is recorded in the
// explain trace (explainability contract: a reader can see why a pane was
// called shell).
func TestShellPrompt_ExplainTrace(t *testing.T) {
	res := program.Detect(" dev@host  /tmp\n", true)
	if res.Program != "shell" {
		t.Fatalf("want shell, got %s", res.Program)
	}
	var saw bool
	for _, e := range res.Trace {
		if e.Program == "shell" && e.RuleID == "shell.idle" && e.Matched {
			saw = true
		}
	}
	if !saw {
		t.Errorf("explain trace missing a matched shell.idle entry: %+v", res.Trace)
	}
}

// guard against an accidental dependency on trailing newlines.
func TestShellPrompt_NoTrailingNewline(t *testing.T) {
	if res := program.Detect(strings.TrimRight(" dev@host  /tmp\n", "\n"), false); res.Program != "shell" {
		t.Errorf("want shell without trailing newline, got %s/%s", res.Program, res.Status)
	}
}
