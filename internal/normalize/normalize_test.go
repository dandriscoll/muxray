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

func TestClean_DeterministicHashInput(t *testing.T) {
	// Two captures of the same content with different ANSI coloring must clean
	// to identical text (so the content hash is stable).
	a := Clean("\x1b[32mDeploy complete\x1b[0m", 0).Clean
	b := Clean("\x1b[1;34mDeploy complete\x1b[0m", 0).Clean
	if a != b {
		t.Errorf("cleaned outputs differ: %q vs %q", a, b)
	}
}
