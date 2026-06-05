package diag

import (
	"strings"
	"testing"
)

func TestRedact(t *testing.T) {
	cases := []string{
		"sk-ant-0123456789abcdefghij token",
		"export OPENAI_API_KEY=sk-abcdefghijklmnop1234",
		"ghp_0123456789abcdefghijklmnopqrstuvwxyz",
		"Authorization: Bearer abcdef0123456789ABCDEF",
		"AKIAIOSFODNN7EXAMPLE",
	}
	for _, in := range cases {
		got := Redact(in)
		if !strings.Contains(got, "[REDACTED]") {
			t.Errorf("Redact(%q) did not redact: %q", in, got)
		}
	}
}

func TestRedact_LeavesPlainTextAlone(t *testing.T) {
	in := "the build completed and all tests passed"
	if Redact(in) != in {
		t.Errorf("Redact altered benign text: %q", Redact(in))
	}
}

func TestDoctor(t *testing.T) {
	r := Doctor("1.2.3")
	if r.MuxrayVersion != "1.2.3" {
		t.Errorf("version=%q", r.MuxrayVersion)
	}
	if r.OS == "" || r.Arch == "" {
		t.Error("OS/Arch must be populated")
	}
	if len(r.Checks) == 0 {
		t.Error("expected diagnostic checks")
	}
	var sawTmuxCheck bool
	for _, c := range r.Checks {
		if c.Name == "tmux_installed" {
			sawTmuxCheck = true
		}
	}
	if !sawTmuxCheck {
		t.Error("expected a tmux_installed check")
	}
}

func TestBuildBundle_OmitsContentByDefault(t *testing.T) {
	b := BuildBundle("1.0.0", "2026-01-01T00:00:00Z", false, "a secret pane with sk-ant-0123456789abcdef")
	if b.ExcerptIncluded {
		t.Error("excerpt must be excluded by default")
	}
	if b.Excerpt != "" {
		t.Errorf("excerpt must be empty by default, got %q", b.Excerpt)
	}
}

func TestBuildBundle_RedactsExcerptWhenIncluded(t *testing.T) {
	b := BuildBundle("1.0.0", "2026-01-01T00:00:00Z", true, "key sk-ant-0123456789abcdefghij here")
	if !b.ExcerptIncluded {
		t.Fatal("excerpt should be included")
	}
	if strings.Contains(b.Excerpt, "sk-ant-0123456789") {
		t.Errorf("excerpt not redacted: %q", b.Excerpt)
	}
	if !strings.Contains(b.Excerpt, "[REDACTED]") {
		t.Errorf("expected redaction marker, got %q", b.Excerpt)
	}
}
