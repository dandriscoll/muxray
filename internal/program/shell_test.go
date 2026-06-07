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
		// Powerline themes (Starship/p10k) render U+E0B0 separators between the
		// user@host segment and the path (and before the branch). tmux preserves
		// the glyph bytes; recognition must see through them. Job 280.
		{"powerline_context", " dev@host  /src/project  main \n"},
		{"powerline_with_distro_icon", "  dev@host  /src/project  main\n"},
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
		` dev@host  /src `,
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

// pl is the powerline segment separator U+E0B0 (), rendered by Starship /
// powerlevel10k themes between prompt segments. tmux capture-pane preserves it.
const pl = ""

// TestShellPrompt_PowerlineOverScrolledCodex is the load-bearing regression for
// job 280: the exact reported pane. The footer ends at a powerline Starship
// prompt (U+E0B0 separators between user@host and the path), while scrollback —
// within the 12-line footer window — contains "codex" inside a filename
// ("bash: /usr/local/share/codex-completion.bash: No such file or directory").
// Pre-fix, contextPrompt could not see past the U+E0B0 glyph, detectShell
// declined, and the incidental "codex" substring fired codex.idle. The fix folds
// the decoration so the prompt is recognized → shell/idle, NOT a phantom Codex.
func TestShellPrompt_PowerlineOverScrolledCodex(t *testing.T) {
	frame := strings.Join([]string{
		"=> Attaching to 'project' as code...",
		"bash: /usr/local/share/completions/extra.bash: No such file or directory",
		"bash: /usr/local/share/codex-completion.bash: No such file or directory",
		"[exited]",
		" code@host " + pl + " /src/project " + pl + " main",
		"logout",
		" dev@host " + pl + " /src/project " + pl + " main devbox --stop project",
		"=> Stopping container 'project'...",
		"=> Container 'project' stopped.",
		" dev@host " + pl + " /src/project " + pl + " main devbox --delete project",
		"=> Container 'project' deleted.",
		" dev@host " + pl + " /src/project " + pl + " main ",
	}, "\n")
	res := program.Detect(frame, false)
	if res.Program == "codex" {
		t.Fatalf("regressed: powerline shell misread as codex (%s/%s) evidence=%q", res.Program, res.Status, res.Evidence)
	}
	if res.Program != "shell" || res.Status != program.StatusIdle {
		t.Fatalf("powerline shell prompt over scrolled 'codex' filename: got %s/%s, want shell/idle", res.Program, res.Status)
	}
}

// TestShellPrompt_DecorationDoesNotManufactureShell is the false-positive guard
// for the job-280 fix: folding Private Use Area glyphs to spaces must NOT turn a
// live agent frame into a shell. A decorated agent footer (a Nerd Font icon in
// the line) with no user@host/path shape must still classify as the agent.
func TestShellPrompt_DecorationDoesNotManufactureShell(t *testing.T) {
	// Codex running, footer carries a Nerd Font icon (U+F085 ) plus the hint.
	frame := "OpenAI Codex\n   working " + pl + " esc to interrupt\n"
	if res := program.Detect(frame, false); res.Program == "shell" {
		t.Fatalf("PUA folding manufactured a shell from an agent frame: %s/%s", res.Program, res.Status)
	}
}

// TestShellPrompt_StructuralGuard_ScrollbackOverShellShape implements job 275's
// filed-but-unimplemented I3 closure (008-I3), now mandatory on recurrence
// (job 280). It prepends an adversarial scrollback block — box-drawing, harness
// keywords (claude/codex), a harness hint, and a quoted status bar — above EACH
// known shell shape (plain AND powerline) and asserts shell/idle every time. The
// footer-bound principle says the last line is the present state regardless of
// what scrolled above it; this locks the whole class against future content
// shapes. (Scrollback stays within the 12-line footer window so the keywords are
// live candidates for the harness classifier — the hostile case.)
func TestShellPrompt_StructuralGuard_ScrollbackOverShellShape(t *testing.T) {
	scrollback := []string{
		"╭──────────────────────────────────────────╮",
		"│ > do the thing                            │",
		"╰──────────────────────────────────────────╯",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt",
		"bash: /usr/local/share/codex-completion.bash: No such file or directory",
		"OpenAI Codex and Claude Code were mentioned here",
		`    "[default] 0:claude*   \"devlogs\" 12:12 03-Jun-26"`,
	}
	shellShapes := []struct {
		name string
		last string
	}{
		{"plain_context", " dev@host  /src/project  main"},
		{"plain_branch", " dev@host  /src/project   main"},
		{"powerline_context", " dev@host " + pl + " /src/project " + pl + " main "},
		{"powerline_distro_icon", " dev@host " + pl + " /src/project " + pl + " main"},
		{"posix_dollar", "user@host:~/project$ "},
		{"posix_percent", "user@host ~ % "},
	}
	for _, s := range shellShapes {
		t.Run(s.name, func(t *testing.T) {
			frame := strings.Join(append(append([]string{}, scrollback...), s.last), "\n")
			res := program.Detect(frame, false)
			if res.Program != "shell" || res.Status != program.StatusIdle {
				t.Fatalf("scrollback defeated footer-bound shell detection: got %s/%s rule=%s evidence=%q, want shell/idle",
					res.Program, res.Status, res.RuleID, res.Evidence)
			}
		})
	}
}

// TestShellPrompt_DisconnectUnderTmuxChrome is the load-bearing regression for
// job 281 and a recurrence of job 268's I1 (transport disconnect read as an
// agent error). A nested-tmux pane: the inner tmux [default] window 0 is still
// named "claude", but claude hit an abnormal websocket close and dropped to a
// shell. The capture (the inner client's grid) ends at the inner status bar
// "[default] 0:claude*"; above it sit a stale claude footer-hint, the real shell
// prompt, and the websocket-close error. Pre-fix, detectShell's last-line checks
// saw only "0:claude*" (not a shell command) and declined, so the harness
// classifier reported the websocket-close line as claude/error. The fix: a
// transport-close signature plus a shell prompt anywhere in the footer →
// shell/idle — the connection dropped; the pane is at a shell, not erroring.
func TestShellPrompt_DisconnectUnderTmuxChrome(t *testing.T) {
	frame := strings.Join([]string{
		"  - One commit, not two. The repo convention is one-job-per-commit.",
		"────────────────────────────────────────────────────────────",
		"❯ Error: websocket: close 1006 (abnormal closure): unexpected EOF",
		" dev@host " + pl + " /src/project " + pl + " ppe ",
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents",
		"",
		"",
		`[default] 0:claude*                          "✳ Review edited files" 12:12 03-Jun-26`,
	}, "\n")
	res := program.Detect(frame, false)
	if res.Program == "claude" {
		t.Fatalf("regressed (job 268/281): disconnect-to-shell read as %s/%s evidence=%q", res.Program, res.Status, res.Evidence)
	}
	if res.Program != "shell" || res.Status != program.StatusIdle {
		t.Fatalf("disconnect under tmux chrome: got %s/%s, want shell/idle", res.Program, res.Status)
	}
}

// TestShellPrompt_TransportCloseGate proves the two-condition gate on the
// transport-disconnect path, so it cannot be satisfied by either signal alone:
//   - a LIVE agent that merely MENTIONS "websocket: close" in scrollback but has
//     no shell prompt must stay the agent (not shell);
//   - a quoted "user@host /path" line in scrollback with NO transport-close
//     signature must not flip a live agent to shell.
func TestShellPrompt_TransportCloseGate(t *testing.T) {
	// Condition A only (close signature, no prompt): live Claude discussing a
	// websocket close, its live footer hint present, no shell prompt anywhere.
	closeNoPrompt := strings.Join([]string{
		"Claude Code",
		"  The server logged: websocket: close 1006 (abnormal closure)",
		"  Let me retry the request.",
		"  esc to interrupt",
	}, "\n")
	if res := program.Detect(closeNoPrompt, false); res.Program == "shell" {
		t.Fatalf("close signature without a shell prompt must NOT be shell: got %s/%s", res.Program, res.Status)
	}
	// Condition B only (prompt in scrollback, no close signature): a live Claude
	// showing a transcript that quotes a shell prompt, with its live footer hint.
	promptNoClose := strings.Join([]string{
		"Claude Code",
		"  Here is the transcript you asked about:",
		"   dev@host  /src/project   main",
		"  esc to interrupt",
	}, "\n")
	if res := program.Detect(promptNoClose, false); res.Program == "shell" {
		t.Fatalf("scrollback prompt without a close signature must NOT be shell: got %s/%s", res.Program, res.Status)
	}
}

// TestShellPrompt_StructuralGuard_ChromeBelowDisconnect extends job 275's I3
// structural guard to the trailing-chrome dimension: an abnormal-close error and
// a shell prompt, then assorted chrome BELOW the prompt (a stale agent hint, a
// nested-tmux status bar with a stale agent window name). The present state is
// the disconnect-to-shell regardless of what trails the prompt → shell/idle.
func TestShellPrompt_StructuralGuard_ChromeBelowDisconnect(t *testing.T) {
	prefix := []string{
		"❯ Error: websocket: close 1006 (abnormal closure): unexpected EOF",
		" dev@host  /src/project  main ",
	}
	trailers := [][]string{
		{`[default] 0:claude*                 "✳ Review edited files" 12:12 03-Jun-26`},
		{"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt"},
		{"  ⏵⏵ bypass permissions on (shift+tab to cycle)", "", `[default] 0:codex*    "x" 12:12`},
		{"", "", ""},
	}
	for i, tr := range trailers {
		t.Run("trailer"+string(rune('A'+i)), func(t *testing.T) {
			frame := strings.Join(append(append([]string{}, prefix...), tr...), "\n")
			res := program.Detect(frame, false)
			if res.Program != "shell" || res.Status != program.StatusIdle {
				t.Fatalf("chrome below a disconnect prompt defeated detection: got %s/%s rule=%s evidence=%q, want shell/idle",
					res.Program, res.Status, res.RuleID, res.Evidence)
			}
		})
	}
}
