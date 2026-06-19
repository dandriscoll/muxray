// Package normalize converts raw terminal capture bytes into a stable, cleaned
// text representation suitable for hashing, diffing, and parsing. It is the
// single point where terminal escape sequences and in-line cursor movement are
// resolved, so that the rest of muxray operates on plain text.
//
// Design principle (see the design note): cleaned text is the unit of truth.
// Raw bytes are preserved elsewhere, but all logic — content hashing, diffing,
// program parsing — runs on the output of this package.
//
// Because cleaned text is the unit of truth, this package is also where a
// crucial distinction is preserved before color is discarded: an agent's input
// box renders the user's typed text at normal intensity but its ghost
// autosuggestion / placeholder text faint (dim). Stripping color would make the
// two identical, so a consuming agent would read the suggestion as a pending
// user prompt. We therefore drop the faint ghost text from the input line here,
// while leaving the (also-faint) recaps, hints, and other content untouched.
package normalize

import (
	"regexp"
	"strings"
	"unicode"
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
	// Suggestion is the harness's input-box ghost autosuggestion / placeholder
	// text, lifted out of the input line (where it would otherwise read as a
	// pending user prompt) into its own field. Empty when the box is empty or
	// holds real typed input. It is deliberately NOT part of Clean, so it never
	// enters the content hash and a rotating suggestion causes no spurious diff.
	Suggestion string
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

	// ansi_normalized reflects whether the raw carried any escape sequences,
	// computed before we remove them. (BEL alone does not count, matching the
	// original len-comparison semantics.)
	ansiFound := oscRe.MatchString(raw) || csiRe.MatchString(raw) ||
		charsetRe.MatchString(raw) || otherEscRe.MatchString(raw)

	// Strip OSC and BEL first: they carry no textual content and no faint state,
	// and removing them keeps the input-box ghost detector below dealing only
	// with the SGR/charset escapes it understands.
	stripped := oscRe.ReplaceAllString(raw, "")
	stripped = strings.ReplaceAll(stripped, "\x07", "")
	stripped = strings.ReplaceAll(stripped, "\r\n", "\n")

	// Lift the agent input-box ghost/placeholder text out of the input line
	// BEFORE stripping color. The faint (dim, SGR 2) styling is the ONLY signal
	// that distinguishes a harness's ghost autosuggestion from text the user
	// actually typed; once color is gone the two are identical bytes, and a
	// consuming agent reads the suggestion as a pending user prompt — the devlogs
	// misread this fixes. The text is returned separately (Result.Suggestion)
	// rather than discarded, so the signal is preserved under a name a consumer
	// won't mistake for input.
	var suggestion string
	stripped, suggestion = stripInputGhost(stripped)

	stripped = csiRe.ReplaceAllString(stripped, "")
	stripped = charsetRe.ReplaceAllString(stripped, "")
	stripped = otherEscRe.ReplaceAllString(stripped, "")

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
		Clean:      clean,
		ANSIFound:  ansiFound,
		Truncated:  truncated,
		LineCount:  len(lines),
		CharCount:  len(clean),
		Suggestion: suggestion,
	}
}

// promptGlyphs are the single-rune input-box prompt markers the agent harnesses
// draw at the start of their input line: Claude's "❯" (and the related shell
// glyphs the shell detector keys off in internal/program/shell.go), Codex's "›"
// (U+203A), and the ASCII ">" used by older/boxed input styles. The word-prefix
// prompt Copilot draws ("copilot>") is handled separately in findPrompt. Faint
// text following the marker is the harness's ghost autosuggestion or placeholder
// — never user input.
var promptGlyphs = map[rune]bool{'❯': true, '❮': true, '➜': true, '»': true, '›': true, '>': true}

// findPrompt returns the index of the last rune of the input-box prompt marker
// in a visible (escape-free) line — input/ghost text begins at the next rune —
// or -1 when the line is not an input line. Leading spaces (including the
// non-breaking spaces Claude pads with) and a box border ("│") are skipped
// first. The marker is then one of:
//
//   - a distinctive single glyph ("❯❮➜»›");
//   - a bare ">" ONLY inside a box border ("│ > │"), since a bare ">" at the
//     start of a line is otherwise a markdown blockquote;
//   - a run of ASCII letters ending in ">" (Copilot's "copilot>" prompt).
//
// Callers additionally require faint text after the marker before treating it as
// ghost, so this staying permissive does not cause spurious stripping.
func findPrompt(runes []rune) int {
	i := 0
	sawBorder := false
	for i < len(runes) {
		if runes[i] == '│' {
			sawBorder = true
			i++
			continue
		}
		if unicode.IsSpace(runes[i]) {
			i++
			continue
		}
		break
	}
	if i >= len(runes) {
		return -1
	}
	r := runes[i]
	switch {
	case promptGlyphs[r] && (r != '>' || sawBorder):
		return i
	case isASCIILetter(r):
		// A "word>" prompt such as Copilot's "copilot>".
		k := i
		for k < len(runes) && isASCIILetter(runes[k]) {
			k++
		}
		if k < len(runes) && runes[k] == '>' {
			return k
		}
	}
	return -1
}

func isASCIILetter(r rune) bool {
	return (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')
}

// stripInputGhost lifts ghost autosuggestion / placeholder text out of an agent
// input-box prompt line, operating on text that still carries SGR escapes. It
// returns the rewritten text and the extracted suggestion (empty if none).
//
// A prompt line is "<leading spaces/box-border> <prompt-marker> <text>", where
// the marker is recognized by findPrompt (Claude's "❯", Codex's "›", Copilot's
// "copilot>", …). When the text after the marker is rendered faint (SGR 2), it
// is the harness's ghost suggestion, not anything the user typed — so it is
// removed from the line (leaving an empty input box) and returned separately.
// Real typed input is never faint and is preserved; the "❯ 1. yes" approval
// menu is not faint either, so it is untouched. Non-prompt lines (and the many
// legitimately-dim lines elsewhere — recaps, footer hints) are returned verbatim.
func stripInputGhost(s string) (text, suggestion string) {
	// Cheap bail-out: a ghost line needs both an escape sequence and a prompt
	// glyph. Most captures of most panes have one but not both on the same pass.
	if !strings.Contains(s, "\x1b[") {
		return s, ""
	}
	lines := strings.Split(s, "\n")
	changed := false
	for i, ln := range lines {
		if nl, sg, ok := stripGhostLine(ln); ok {
			lines[i] = nl
			changed = true
			if sg != "" {
				// The real input line is at the bottom; if several lines match,
				// the lowest one's suggestion is the live one.
				suggestion = sg
			}
		}
	}
	if !changed {
		return s, suggestion
	}
	return strings.Join(lines, "\n"), suggestion
}

// stripGhostLine returns the line with input-box ghost text removed, the
// extracted suggestion, and whether it changed. A line changes only when it is a
// prompt line AND carries faint (non-blank) text after the prompt glyph. The
// suggestion is populated only for a pure ghost line (no real typed input mixed
// in). The returned line is plain text (its escapes resolved away); the
// remaining escapes elsewhere are stripped by the caller's normal pass.
func stripGhostLine(line string) (string, string, bool) {
	type cell struct {
		r     rune
		faint bool
	}
	rs := []rune(line)
	cells := make([]cell, 0, len(rs))
	faint := false
	for i := 0; i < len(rs); {
		if rs[i] == '\x1b' {
			adv, isSGR, params := consumeEscape(rs, i)
			if isSGR {
				faint = applySGRFaint(faint, params)
			}
			i += adv
			continue
		}
		cells = append(cells, cell{rs[i], faint})
		i++
	}

	// Locate the prompt marker. Input begins immediately after it.
	runes := make([]rune, len(cells))
	for i := range cells {
		runes[i] = cells[i].r
	}
	glyph := findPrompt(runes)
	if glyph < 0 {
		return line, "", false
	}

	// Classify the cells after the glyph (ignoring spaces and a box border):
	// faint text is the ghost suggestion; non-faint text is something the user
	// actually typed.
	hasGhost, hasReal := false, false
	for j := glyph + 1; j < len(cells); j++ {
		if cells[j].r == '│' || unicode.IsSpace(cells[j].r) {
			continue
		}
		if cells[j].faint {
			hasGhost = true
		} else {
			hasReal = true
		}
	}
	if !hasGhost {
		return line, "", false
	}

	// Rebuild: keep everything up to and including the glyph, then keep only the
	// non-faint cells after it (a trailing box border, or — defensively — any
	// real text). Trailing whitespace (including the non-breaking spaces Claude
	// pads the input line with) is trimmed so an empty box cleans to just "❯".
	var b strings.Builder
	for j := 0; j <= glyph; j++ {
		b.WriteRune(cells[j].r)
	}
	for j := glyph + 1; j < len(cells); j++ {
		if cells[j].faint {
			continue
		}
		b.WriteRune(cells[j].r)
	}
	clean := strings.TrimRightFunc(b.String(), unicode.IsSpace)

	// Extract the suggestion only for a pure ghost line — when real typed input
	// is mixed in, the faint span is an inline completion whose boundaries we
	// can't safely reconstruct, so we keep the typed text in clean and surface no
	// suggestion rather than guess. For a pure ghost line the whole run after the
	// glyph (minus a trailing box border) is the suggestion.
	suggestion := ""
	if !hasReal {
		end := len(cells)
		for end > glyph+1 {
			r := cells[end-1].r
			if r == '│' || unicode.IsSpace(r) {
				end--
				continue
			}
			break
		}
		var sb strings.Builder
		for j := glyph + 1; j < end; j++ {
			// Claude separates suggestion words with non-breaking spaces; fold any
			// Unicode space to a plain space so the lifted text reads naturally.
			if unicode.IsSpace(cells[j].r) {
				sb.WriteRune(' ')
				continue
			}
			sb.WriteRune(cells[j].r)
		}
		suggestion = strings.TrimSpace(sb.String())
	}
	return clean, suggestion, true
}

// consumeEscape reports how many runes to advance past the escape sequence
// starting at rs[i] (which must be ESC). For an SGR sequence (CSI ... 'm') it
// also returns the parameter string so the caller can track faint state.
func consumeEscape(rs []rune, i int) (adv int, isSGR bool, params string) {
	if i+1 >= len(rs) {
		return 1, false, ""
	}
	switch rs[i+1] {
	case '[': // CSI: ESC [ params intermediates final
		j := i + 2
		start := j
		for j < len(rs) && !(rs[j] >= '@' && rs[j] <= '~') {
			j++
		}
		if j >= len(rs) { // unterminated — consume the rest
			return len(rs) - i, false, ""
		}
		return (j - i) + 1, rs[j] == 'm', string(rs[start:j])
	case '(', ')', '*', '+': // charset selection: ESC X <byte>
		if i+2 < len(rs) {
			return 3, false, ""
		}
		return 2, false, ""
	default: // other two-byte escapes
		return 2, false, ""
	}
}

// applySGRFaint updates the faint (dim) intensity flag from one SGR parameter
// string. Faint is set by code 2 and cleared by 0 (reset all), an empty
// parameter (== 0), or 22 (normal intensity). Colon-separated sub-parameters are
// reduced to their leading code; all other codes leave faint unchanged.
func applySGRFaint(faint bool, params string) bool {
	if params == "" {
		return false
	}
	for _, p := range strings.Split(params, ";") {
		if idx := strings.IndexByte(p, ':'); idx >= 0 {
			p = p[:idx]
		}
		switch p {
		case "", "0", "22":
			faint = false
		case "2":
			faint = true
		}
	}
	return faint
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
