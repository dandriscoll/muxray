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

// harnessHints are live-TUI footer phrases. Belt-and-suspenders veto for a
// footer that mixes a hint line with a prompt-shaped line.
var harnessHints = []string{"esc to interrupt", "? for shortcuts", "/help for help"}

// detectShell reports whether the cleaned footer ends at an interactive shell
// prompt, returning the matched rule id and the evidence line. The footer is the
// already-tail-bounded text the state rules use.
func detectShell(footer string) (ruleID, evidence string, ok bool) {
	footerLower := strings.ToLower(footer)
	// Vetoes first (cheap and decisive): a TUI box or a harness hint means the
	// pane is a live frame, not a shell.
	if strings.ContainsAny(footer, boxDrawing) {
		return "", "", false
	}
	for _, h := range harnessHints {
		if strings.Contains(footerLower, h) {
			return "", "", false
		}
	}

	last := lastNonEmptyLine(footer)
	if last == "" {
		return "", "", false
	}
	trimmed := strings.TrimLeft(last, " \t")

	// 1) Leading prompt glyph (Starship/p10k/fish), but not the "❯ 1." menu form
	//    that claude.needs_approval keys on.
	for _, g := range leadingGlyphs {
		if rest, found := strings.CutPrefix(trimmed, g); found {
			rest = strings.TrimLeft(rest, " ")
			if isMenuItem(rest) {
				break // an enumerated menu, not a shell prompt
			}
			return "shell.idle", last, true
		}
	}

	// 2) Starship / "user@host <path>" context line.
	if contextPrompt.MatchString(last) {
		return "shell.idle", last, true
	}

	// 3) Classic POSIX prompt ending in #/$/% with a user@host token.
	if posixPrompt.MatchString(last) {
		return "shell.idle", last, true
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
