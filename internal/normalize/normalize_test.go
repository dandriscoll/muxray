package normalize

import (
	"strings"
	"testing"
)

func TestClean_StripsANSI(t *testing.T) {
	raw := "\x1b[31mred\x1b[0m and \x1b[1mbold\x1b[0m"
	r := Clean(raw, 0)
	if r.Clean != "red and bold" {
		t.Errorf("got %q", r.Clean)
	}
	if !r.ANSIFound {
		t.Error("expected ANSIFound=true")
	}
}

func TestClean_StripsOSCandBEL(t *testing.T) {
	raw := "\x1b]0;window title\x07hello\x07"
	r := Clean(raw, 0)
	if r.Clean != "hello" {
		t.Errorf("got %q", r.Clean)
	}
}

func TestClean_CarriageReturnOverwrite(t *testing.T) {
	// A spinner redraw: the later frame fully overwrites the earlier one.
	raw := "  Working 10%\r  Working 100% done"
	r := Clean(raw, 0)
	if r.Clean != "  Working 100% done" {
		t.Errorf("got %q", r.Clean)
	}
}

func TestClean_Backspace(t *testing.T) {
	raw := "abcd\b\bXY"
	r := Clean(raw, 0)
	if r.Clean != "abXY" {
		t.Errorf("got %q", r.Clean)
	}
}

func TestClean_NoANSI(t *testing.T) {
	r := Clean("plain text\nsecond line", 0)
	if r.ANSIFound {
		t.Error("expected ANSIFound=false for plain text")
	}
}

func TestClean_TrailingBlankLinesDropped(t *testing.T) {
	r := Clean("line1\nline2\n\n\n   \n", 0)
	if r.Clean != "line1\nline2" {
		t.Errorf("got %q", r.Clean)
	}
	if r.LineCount != 2 {
		t.Errorf("LineCount=%d, want 2", r.LineCount)
	}
}

func TestClean_LineLimitKeepsTail(t *testing.T) {
	var b strings.Builder
	for i := 0; i < 100; i++ {
		b.WriteString("line")
		b.WriteByte(byte('0' + i%10))
		b.WriteByte('\n')
	}
	r := Clean(b.String(), 10)
	if !r.Truncated {
		t.Error("expected Truncated=true")
	}
	if r.LineCount != 10 {
		t.Errorf("LineCount=%d, want 10", r.LineCount)
	}
	// The tail is kept: last line is line9 (index 99 -> 99%10 == 9).
	lines := strings.Split(r.Clean, "\n")
	if lines[len(lines)-1] != "line9" {
		t.Errorf("last line %q, want line9", lines[len(lines)-1])
	}
}

func TestClean_InvalidUTF8(t *testing.T) {
	raw := "ok \xff\xfe bytes"
	r := Clean(raw, 0)
	if !strings.Contains(r.Clean, "ok") || !strings.Contains(r.Clean, "bytes") {
		t.Errorf("got %q", r.Clean)
	}
	if strings.ContainsRune(r.Clean, 0xff) {
		t.Error("invalid bytes not replaced")
	}
}

func TestClean_StripsInputBoxGhostText(t *testing.T) {
	// The Claude input box at rest: a bright "❯ " prompt followed by faint
	// (SGR 2) ghost autosuggestion text. The ghost text must not survive into the
	// cleaned output, where it would read as a real pending user prompt.
	raw := "\x1b[39m❯ \x1b[2mWhy\x1b[0m \x1b[2mdidn't\x1b[0m \x1b[2mset_devlogs_url\x1b[0m \x1b[2mswitch\x1b[0m\n  ? for shortcuts"
	r := Clean(raw, 0)
	if strings.Contains(r.Clean, "Why") || strings.Contains(r.Clean, "set_devlogs_url") {
		t.Errorf("ghost suggestion leaked into clean output: %q", r.Clean)
	}
	lines := strings.Split(r.Clean, "\n")
	if lines[0] != "❯" {
		t.Errorf("prompt line = %q, want %q (empty box)", lines[0], "❯")
	}
	if r.Suggestion != "Why didn't set_devlogs_url switch" {
		t.Errorf("Suggestion = %q, want the lifted ghost text", r.Suggestion)
	}
}

func TestClean_GhostWordsSeparatedByNBSP(t *testing.T) {
	// Claude pads its ghost suggestion with non-breaking spaces (U+00A0) between
	// words. Those must read as blanks (not as real typed input) so the
	// suggestion is still lifted, and they fold to plain spaces in the result.
	raw := "\x1b[39m❯ \x1b[2mWhy\x1b[0m \x1b[2mdidn't\x1b[0m \x1b[2mit\x1b[0m\n  ? for shortcuts"
	r := Clean(raw, 0)
	if got := strings.Split(r.Clean, "\n")[0]; got != "❯" {
		t.Errorf("prompt line = %q, want empty box %q", got, "❯")
	}
	if r.Suggestion != "Why didn't it" {
		t.Errorf("Suggestion = %q, want %q", r.Suggestion, "Why didn't it")
	}
}

func TestClean_KeepsRealTypedInput(t *testing.T) {
	// Real user input is NOT faint and must be preserved verbatim.
	raw := "❯ deploy the azure collector"
	r := Clean(raw, 0)
	if r.Clean != "❯ deploy the azure collector" {
		t.Errorf("real input altered: %q", r.Clean)
	}
	if r.Suggestion != "" {
		t.Errorf("real input must not produce a suggestion, got %q", r.Suggestion)
	}
}

func TestClean_KeepsApprovalMenu(t *testing.T) {
	// The "❯ 1. Yes" approval selection is bright, not faint — leave it intact so
	// the needs_approval rule still fires.
	raw := "\x1b[1m❯ 1. Yes\x1b[0m\n  2. No"
	r := Clean(raw, 0)
	if !strings.Contains(r.Clean, "❯ 1. Yes") {
		t.Errorf("approval menu altered: %q", r.Clean)
	}
}

func TestClean_KeepsDimContentOutsideInputBox(t *testing.T) {
	// Dim text that is NOT on a prompt line (recaps, "+N lines" hints) is real
	// content and must be preserved.
	raw := "\x1b[2m… +2 lines (ctrl+o to expand)\x1b[0m\n\x1b[2m※ recap: it works\x1b[0m"
	r := Clean(raw, 0)
	if !strings.Contains(r.Clean, "+2 lines") || !strings.Contains(r.Clean, "recap: it works") {
		t.Errorf("legitimate dim content dropped: %q", r.Clean)
	}
}

func TestClean_StripsCodexGhostText(t *testing.T) {
	// Codex's input prompt is "›" (U+203A); its ghost suggestion renders faint.
	raw := "› \x1b[2mask Codex something\x1b[0m\n  OpenAI Codex"
	r := Clean(raw, 0)
	if strings.Contains(r.Clean, "ask Codex something") {
		t.Errorf("codex ghost leaked: %q", r.Clean)
	}
	if got := strings.Split(r.Clean, "\n")[0]; got != "›" {
		t.Errorf("prompt line = %q, want empty box %q", got, "›")
	}
	if r.Suggestion != "ask Codex something" {
		t.Errorf("Suggestion = %q, want %q", r.Suggestion, "ask Codex something")
	}
}

func TestClean_StripsCopilotGhostText(t *testing.T) {
	// Copilot's prompt is the word-prefixed "copilot>"; ghost text follows faint.
	raw := "copilot> \x1b[2mwhat would you like to do?\x1b[0m"
	r := Clean(raw, 0)
	if strings.Contains(r.Clean, "what would you like") {
		t.Errorf("copilot ghost leaked: %q", r.Clean)
	}
	if r.Clean != "copilot>" {
		t.Errorf("prompt line = %q, want %q (prompt kept, ghost gone)", r.Clean, "copilot>")
	}
	if r.Suggestion != "what would you like to do?" {
		t.Errorf("Suggestion = %q, want %q", r.Suggestion, "what would you like to do?")
	}
}

func TestClean_KeepsBareBlockquoteWithLeadingGreater(t *testing.T) {
	// A bare ">" at line start is a markdown blockquote, not a word-prefixed
	// prompt, so dim quoted text must survive.
	raw := "\x1b[2m> dimly quoted\x1b[0m"
	r := Clean(raw, 0)
	if !strings.Contains(r.Clean, "dimly quoted") {
		t.Errorf("blockquote wrongly stripped: %q", r.Clean)
	}
}

func TestClean_KeepsDimBlockquote(t *testing.T) {
	// A bare ">" begins a markdown blockquote, not an input box. Dim quoted text
	// must survive — only a ">" inside a box border counts as a prompt.
	raw := "\x1b[2m> a dimly quoted line\x1b[0m"
	r := Clean(raw, 0)
	if !strings.Contains(r.Clean, "a dimly quoted line") {
		t.Errorf("dim blockquote wrongly stripped: %q", r.Clean)
	}
}

func TestClean_StripsBoxedGhostText(t *testing.T) {
	// The older boxed input style: "│ > ghost │". A ">" inside a border IS a
	// prompt, so its faint ghost text is removed while the border is kept.
	raw := "│ > \x1b[2mtry asking a question\x1b[0m │"
	r := Clean(raw, 0)
	if strings.Contains(r.Clean, "try asking") {
		t.Errorf("boxed ghost text leaked: %q", r.Clean)
	}
	if !strings.Contains(r.Clean, "│") {
		t.Errorf("box border dropped: %q", r.Clean)
	}
	if r.Suggestion != "try asking a question" {
		t.Errorf("Suggestion = %q, want box ghost text without the border", r.Suggestion)
	}
}

func TestClean_GhostTextHashStableAcrossSuggestions(t *testing.T) {
	// Two captures differing only in the rotating ghost suggestion must clean to
	// identical text, so an idle pane does not report spurious changes.
	a := Clean("\x1b[39m❯ \x1b[2mtry this thing\x1b[0m\n  ? for shortcuts", 0).Clean
	b := Clean("\x1b[39m❯ \x1b[2mor that other thing instead\x1b[0m\n  ? for shortcuts", 0).Clean
	if a != b {
		t.Errorf("ghost suggestions produced different clean text:\n a=%q\n b=%q", a, b)
	}
}

func TestClean_DeterministicHashInput(t *testing.T) {
	// Two captures of the same content with different ANSI coloring must clean
	// to identical text (so the content hash is stable).
	a := Clean("\x1b[32mDeploy complete\x1b[0m", 0).Clean
	b := Clean("\x1b[1;34mDeploy complete\x1b[0m", 0).Clean
	if a != b {
		t.Errorf("cleaned outputs differ: %q vs %q", a, b)
	}
}
