package program

import (
	"regexp"
	"strings"
)

// Shell detection. A websocket / transport disconnect (commonly an incus VM
// dropping its connection) returns the user to an interactive shell. The pane
// then shows the harness's scrolled-up conversation, the closure error
// (e.g. "Error: websocket: close 1006 (abnormal closure): unexpected EOF"), and
// a fresh shell prompt at the bottom. That pane is NOT a live Claude/Codex/
// Copilot frame and must not be reported as an agent error: it is a shell at
// rest. We classify it program="shell", status=idle.
//
// This rides muxray's footer-bound principle (issues #2/#4): the footer is the
// present state. A shell prompt as the footer means the harness is not live, so
// shell detection runs as a pre-check in Detect, ahead of the harness signature
// step, and wins over any scrolled-up "error:" text.

// contextPrompt matches a Starship / classic "user@host <path>" context line:
// a user@host token, whitespace, then a path beginning with ~ or /. The required
// whitespace before the path rejects scp/git URLs ("git@github.com:org/repo" —
// colon, no space). Anchored at line start because a prompt is left-aligned.
//
// Verified against a live Starship capture (shape " dev@host  /tmp": leading
// space, user@host, spaces, path; a git branch appends when inside a repo).
var contextPrompt = regexp.MustCompile(`^\s*[\w.-]+@[\w.-]+\s+[~/]\S*`)

// posixPrompt matches a classic POSIX prompt ending in #/$/% that also carries a
// user@host token (the @ guards against a bare $/% appearing in displayed text,
// e.g. "cost: $12"). Matches "user@host:~/project$" and "user@host ~ $ ".
var posixPrompt = regexp.MustCompile(`@.*[#$%]\s*$`)

// leadingGlyphs are prompt characters used by Starship / powerlevel10k / modern
// fish as the FIRST glyph of the input line. They essentially never lead a TUI
// footer line, so as the first non-space rune they are a strong shell signal.
var leadingGlyphs = []string{"❯", "❮", "➜", "»"}

// boxDrawing are runes that appear in TUI input boxes / borders (Claude's
// "│ > │"). Their presence anywhere in the footer vetoes shell classification —
// a shell prompt never draws a box.
const boxDrawing = "│╭╮╰╯─┌┐└┘├┤┬┴┼━┃┏┓┗┛▏▕▎▌▐█"

// harnessHints are live-TUI footer phrases. Belt-and-suspenders veto for the
// ambiguous leading-glyph case (a footer that mixes a hint with a glyph line).
var harnessHints = []string{"esc to interrupt", "? for shortcuts", "/help for help"}

// tmuxActiveWin matches the ACTIVE window entry of a tmux status bar
// ("[session] 0:bash* …"): an index, a colon, the window's command/name, then the
// current-window marker '*'. The captured group is the active window's command.
// We key off the active window specifically (not the first listed) so a status
// bar like "0:vim 1:bash*" reports the foreground program, not the leftmost one.
var tmuxActiveWin = regexp.MustCompile(`\b\d+:([A-Za-z][\w.+-]*)\*`)

// shellCommands are window-command names that mean the active tmux window is at an
// interactive shell. Used only for the nested-tmux status-bar signal.
var shellCommands = map[string]bool{
	"bash": true, "zsh": true, "sh": true, "fish": true, "dash": true,
	"ksh": true, "mksh": true, "tcsh": true, "csh": true, "ash": true,
	"busybox": true, "nu": true, "pwsh": true, "xonsh": true,
}

// transportClose holds distinctive transport-layer closure signatures a coding
// agent prints when its backend connection drops (commonly an incus VM or proxy
// dropping the websocket), exiting the user to a shell. Unlike a generic
// "error:" — which a LIVE agent routinely prints in scrollback and which muxray
// must NOT treat as a disconnect (job 268's deliberate footer-bound choice) —
// these name the transport itself closing, so combined with a shell prompt in
// the footer they decisively mean the harness is gone.
var transportClose = []string{
	"websocket: close",            // gorilla/websocket: "websocket: close 1006 (abnormal closure)"
	"websocket connection closed", // alternate phrasing
}

// footerTransportClose reports whether the footer carries a transport-close
// signature, returning the original-case line that carried it as evidence.
func footerTransportClose(footer string) (evidence string, ok bool) {
	lower := strings.ToLower(footer)
	for _, s := range transportClose {
		if strings.Contains(lower, s) {
			return lineContaining(footer, s), true
		}
	}
	return "", false
}

// footerShellPrompt returns the lowest footer line that is a decisive shell
// prompt ("user@host <path>", or a classic "…[#$%]" prompt), terminal
// decoration folded first. Empty if none. Scanning the WHOLE footer rather than
// only the last line is safe ONLY because every caller gates it behind a
// transport-close signature (see detectShell step 4) — ungated, a quoted prompt
// in a live agent's scrollback would misfire.
func footerShellPrompt(footer string) string {
	lines := strings.Split(footer, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		norm := undecorate(lines[i])
		if contextPrompt.MatchString(norm) || posixPrompt.MatchString(norm) {
			return lines[i]
		}
	}
	return ""
}

func containsHarnessHint(footerLower string) bool {
	for _, h := range harnessHints {
		if strings.Contains(footerLower, h) {
			return true
		}
	}
	return false
}

// privateUse reports whether r is in a Unicode Private Use Area. Powerline
// themes (Starship, powerlevel10k) and Nerd Fonts render prompt SEPARATORS and
// icons from these ranges — e.g. the powerline arrow U+E0B0 between the
// user@host segment and the path, a distro icon as the leading glyph, the
// git-branch glyph U+E0A0 before the branch name. tmux capture-pane preserves
// these bytes, so a real shell prompt arrives decorated with runes the
// prompt-shape matchers treat as opaque non-separators, defeating recognition.
// Nothing standard lives in these ranges — box-drawing is U+2500-block and the
// ❯/➜/»/❮ prompt glyphs are U+276F/279C/00BB/276E — so folding PUA runes to
// spaces is safe: it cannot eat a box border, eat a leading glyph, or
// manufacture a user@host/path shape out of an agent frame.
func privateUse(r rune) bool {
	return (r >= 0xE000 && r <= 0xF8FF) || // BMP PUA
		(r >= 0xF0000 && r <= 0xFFFFD) || // Supplementary PUA-A
		(r >= 0x100000 && r <= 0x10FFFD) // Supplementary PUA-B
}

// undecorate folds Private Use Area glyphs (powerline / Nerd Font separators and
// icons) to spaces so the prompt-shape matchers see the underlying
// "user@host <path>" structure regardless of terminal theme decoration.
func undecorate(s string) string {
	return strings.Map(func(r rune) rune {
		if privateUse(r) {
			return ' '
		}
		return r
	}, s)
}

// detectShell reports whether the cleaned footer ends at an interactive shell,
// returning the matched rule id and the evidence line. The footer is the
// already-tail-bounded text the state rules use.
//
// The footer-bound principle (issue #4) governs: the LAST line is the present
// state. So a definitive shell shape on the last non-empty line wins regardless
// of box-drawing or harness hints in the SCROLLED content above it (e.g. a pane
// displaying muxray's own JSON output, whose embedded captures carry box-drawing
// and harness phrases, then a fresh shell prompt). A live agent TUI never ends
// its frame with a "user@host /path" prompt, a "…$"/"…#" prompt, or a tmux status
// bar whose active window runs a shell — so those three are decisive. Only the
// leading-glyph prompt ("❯") is genuinely ambiguous with Claude's boxed input
// line, so it alone stays guarded by the box/hint veto.
func detectShell(footer string) (ruleID, evidence string, ok bool) {
	last := lastNonEmptyLine(footer)
	if last == "" {
		return "", "", false
	}
	// Fold powerline / Nerd Font decoration glyphs to spaces so a themed prompt
	// (Starship/p10k separators, distro icons) is recognized by its structure
	// rather than defeated by the glyphs tmux capture-pane preserves. The
	// matchers below run on the folded line; evidence reports the ORIGINAL line.
	norm := undecorate(last)
	trimmed := strings.TrimLeft(norm, " \t")

	// 1) Starship / "user@host <path>" context line — decisive on the last line.
	if contextPrompt.MatchString(norm) {
		return "shell.idle", last, true
	}

	// 2) Classic POSIX prompt ending in #/$/% with a user@host token — decisive.
	if posixPrompt.MatchString(norm) {
		return "shell.idle", last, true
	}

	// 3) Nested-tmux status bar whose ACTIVE window runs a shell. Require the line
	//    to actually start with the "[session]" bracket so a displayed/quoted
	//    status bar in scrolled output (leading '"') is not mistaken for the live
	//    bar. A non-shell active command (e.g. "0:claude*") does NOT match, so a
	//    nested tmux running an agent still flows to the harness classifier.
	if strings.HasPrefix(trimmed, "[") {
		if m := tmuxActiveWin.FindStringSubmatch(last); m != nil && shellCommands[strings.ToLower(m[1])] {
			return "shell.idle", last, true
		}
	}

	// 4) Transport disconnect hidden under trailing chrome. A distinctive
	//    transport-close signature (e.g. "websocket: close 1006") TOGETHER WITH a
	//    shell prompt anywhere in the footer means the agent's connection dropped
	//    and the user is back at a shell — even when the prompt is NOT the last
	//    line because a nested-tmux status bar and/or a stale agent footer remnant
	//    trail it. (An abnormal websocket close in a nested-tmux pane leaves the
	//    inner status bar's window name stale at "0:claude*" and may leave the
	//    agent's last footer frame painted below the new prompt — so checks 1–3,
	//    which read only the last line, miss the prompt and the pane is wrongly
	//    read as claude/error.) The transport-close signature GATES the whole-
	//    footer prompt scan: a live agent merely displaying a "user@host /path"
	//    line or the words "websocket: close" in scrollback satisfies only one of
	//    the two conditions, so it is never mistaken for a shell.
	if ev, ok := footerTransportClose(footer); ok && footerShellPrompt(footer) != "" {
		return "shell.idle", ev, true
	}

	// 5) Leading prompt glyph (Starship/p10k/fish) — AMBIGUOUS with Claude's boxed
	//    "❯" input line, so only a shell when no live-frame marker (a TUI box or a
	//    harness hint) is present in the footer, and not the "❯ 1." menu form.
	if !strings.ContainsAny(footer, boxDrawing) && !containsHarnessHint(strings.ToLower(footer)) {
		for _, g := range leadingGlyphs {
			if rest, found := strings.CutPrefix(trimmed, g); found {
				rest = strings.TrimLeft(rest, " ")
				if isMenuItem(rest) {
					break // an enumerated menu, not a shell prompt
				}
				return "shell.idle", last, true
			}
		}
	}

	return "", "", false
}

// isMenuItem reports whether s looks like an enumerated menu entry ("1.", "2)"),
// which a TUI (e.g. Claude's "❯ 1. Yes") draws after a glyph — not a shell.
func isMenuItem(s string) bool {
	i := 0
	for i < len(s) && s[i] >= '0' && s[i] <= '9' {
		i++
	}
	return i > 0 && i < len(s) && (s[i] == '.' || s[i] == ')')
}

func lastNonEmptyLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}
