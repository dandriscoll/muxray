// Package normalize converts raw terminal capture bytes into a stable, cleaned
// text representation suitable for hashing, diffing, and parsing. It is the
// single point where terminal escape sequences and in-line cursor movement are
// resolved, so that the rest of muxray operates on plain text.
//
// Design principle (see the design note): cleaned text is the unit of truth.
// Raw bytes are preserved elsewhere, but all logic — content hashing, diffing,
// program parsing — runs on the output of this package.
package normalize

import (
	"regexp"
	"strings"
	"unicode/utf8"
)

// Escape-sequence matchers. These are intentionally explicit and small rather
// than pulling in a dependency: tmux capture-pane emits a bounded set of
// sequences (SGR colors with -e, plus the occasional OSC/charset), and a tiny
// audited regex set keeps muxray dependency-free and easy to reason about.
var (
	// CSI: ESC [ ... final byte. Covers SGR color, cursor moves, erases.
	csiRe = regexp.MustCompile("\x1b\\[[0-9;?:]*[ -/]*[@-~]")
	// OSC: ESC ] ... (BEL | ST). Window titles, hyperlinks, etc.
	oscRe = regexp.MustCompile("\x1b\\][^\x07\x1b]*(?:\x07|\x1b\\\\)")
	// Charset selection: ESC ( B and friends.
	charsetRe = regexp.MustCompile("\x1b[()*+][0-9A-Za-z]")
	// Other two-byte escapes (ESC = > 7 8 c etc.).
	otherEscRe = regexp.MustCompile("\x1b[@-Z\\\\-_=>78c]")
)

// Result is the outcome of normalizing a raw capture.
type Result struct {
	// Clean is the normalized text: no escape sequences, cursor movement
	// resolved, trailing whitespace and trailing blank lines trimmed.
	Clean string
	// ANSIFound reports whether any escape sequence was stripped (feeds the
	// telemetry "ansi_normalized" field).
	ANSIFound bool
	// Truncated reports whether the output was line-limited.
	Truncated bool
	// LineCount is the number of lines in Clean.
	LineCount int
	// CharCount is the byte length of Clean.
	CharCount int
}

// Clean normalizes raw terminal bytes. maxLines <= 0 means no line limit;
// otherwise the most recent maxLines lines are kept (the tail of a pane is the
// current screen state an agent cares about) and Truncated is set.
func Clean(raw string, maxLines int) Result {
	// Resolve invalid UTF-8 to the replacement rune so downstream string ops
	// and JSON encoding never choke (lossy but non-crashing — a dependability
	// requirement).
	if !utf8.ValidString(raw) {
		raw = strings.ToValidUTF8(raw, "�")
	}

	stripped := oscRe.ReplaceAllString(raw, "")
	stripped = csiRe.ReplaceAllString(stripped, "")
	stripped = charsetRe.ReplaceAllString(stripped, "")
	stripped = otherEscRe.ReplaceAllString(stripped, "")
	ansiFound := len(stripped) != len(raw)

	// Strip BEL (audible bell) which carries no textual content.
	stripped = strings.ReplaceAll(stripped, "\x07", "")

	// Normalize newlines, then resolve carriage-return / backspace overwrites
	// per physical line so spinners and progress redraws collapse to their
	// final visible state.
	stripped = strings.ReplaceAll(stripped, "\r\n", "\n")
	lines := strings.Split(stripped, "\n")
	for i, ln := range lines {
		lines[i] = strings.TrimRight(applyOverwrite(ln), " \t")
	}

	// Drop trailing blank lines (panes are usually padded with blanks below the
	// content); keep interior blanks.
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}

	truncated := false
	if maxLines > 0 && len(lines) > maxLines {
		lines = lines[len(lines)-maxLines:]
		truncated = true
	}

	clean := strings.Join(lines, "\n")
	return Result{
		Clean:     clean,
		ANSIFound: ansiFound,
		Truncated: truncated,
		LineCount: len(lines),
		CharCount: len(clean),
	}
}

// applyOverwrite simulates terminal overwrite semantics within a single line:
// '\r' returns the cursor to column 0 and '\b' moves it back one column, so
// later characters overwrite earlier ones. Content past the overwrite that is
// not replaced is preserved, matching real terminal behavior.
func applyOverwrite(line string) string {
	if !strings.ContainsAny(line, "\r\b") {
		return line
	}
	var buf []rune
	col := 0
	for _, r := range line {
		switch r {
		case '\r':
			col = 0
		case '\b':
			if col > 0 {
				col--
			}
		default:
			if col < len(buf) {
				buf[col] = r
			} else {
				buf = append(buf, r)
			}
			col++
		}
	}
	return string(buf)
}
