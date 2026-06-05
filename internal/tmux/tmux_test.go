package tmux

import (
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"
)

// withStub replaces runCommand for the duration of fn and restores it after, so
// stubbed unit tests never leak into the real-tmux integration tests.
func withStub(t *testing.T, stub func(name string, args ...string) ([]byte, []byte, error), fn func()) {
	t.Helper()
	orig := runCommand
	runCommand = stub
	defer func() { runCommand = orig }()
	fn()
}

func TestParseTarget(t *testing.T) {
	cases := []struct {
		in   string
		want Target
	}{
		{"%3", Target{Raw: "%3", Pane: "%3"}},
		{"$1", Target{Raw: "$1", Session: "$1"}},
		{"work", Target{Raw: "work", Session: "work"}},
		{"work:2", Target{Raw: "work:2", Session: "work", Window: "2"}},
		{"work:2.1", Target{Raw: "work:2.1", Session: "work", Window: "2", Pane: "1"}},
	}
	for _, c := range cases {
		got, err := ParseTarget(c.in)
		if err != nil {
			t.Errorf("%q: unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("%q: got %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestParseTarget_EmptyOutsideTmux(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	_, err := ParseTarget("")
	if err == nil {
		t.Fatal("expected error for empty target outside tmux")
	}
	var te *Error
	if !errors.As(err, &te) || te.Class != "no_target" {
		t.Errorf("got %v, want a no_target tmux error", err)
	}
}

func TestParseTarget_EmptyInsideTmux(t *testing.T) {
	t.Setenv("TMUX_PANE", "%7")
	got, err := ParseTarget("")
	if err != nil {
		t.Fatal(err)
	}
	if got.Pane != "%7" || got.Raw != "%7" {
		t.Errorf("got %+v, want pane %%7", got)
	}
}

func TestCapture_BuildsArgs(t *testing.T) {
	var gotArgs []string
	withStub(t, func(name string, args ...string) ([]byte, []byte, error) {
		gotArgs = args
		return []byte("pane contents"), nil, nil
	}, func() {
		out, err := Capture(Target{Raw: "%2"}, CaptureOpts{Escapes: true, JoinWrapped: true, Scrollback: 50})
		if err != nil {
			t.Fatal(err)
		}
		if out != "pane contents" {
			t.Errorf("out=%q", out)
		}
	})
	joined := strings.Join(gotArgs, " ")
	for _, want := range []string{"capture-pane", "-p", "-J", "-e", "-S -50", "-t %2"} {
		if !strings.Contains(joined, want) {
			t.Errorf("args %q missing %q", joined, want)
		}
	}
}

func TestCapture_TmuxErrorSurfaced(t *testing.T) {
	withStub(t, func(name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("can't find pane: %9"), errors.New("exit 1")
	}, func() {
		_, err := Capture(Target{Raw: "%9"}, CaptureOpts{})
		if err == nil {
			t.Fatal("expected error")
		}
		var te *Error
		if !errors.As(err, &te) || te.Class != "no_target" {
			t.Errorf("got %v (class?), want no_target", err)
		}
		if !strings.Contains(err.Error(), "can't find pane") {
			t.Errorf("error should carry tmux stderr, got %v", err)
		}
	})
}

func TestListPanes_Parse(t *testing.T) {
	line := strings.Join([]string{"dev", "0", "main", "1", "0", "%0", "1", "claude", "title", "120", "40"}, "\t")
	withStub(t, func(name string, args ...string) ([]byte, []byte, error) {
		return []byte(line + "\n"), nil, nil
	}, func() {
		panes, err := ListPanes()
		if err != nil {
			t.Fatal(err)
		}
		if len(panes) != 1 {
			t.Fatalf("got %d panes, want 1", len(panes))
		}
		p := panes[0]
		if p.Session != "dev" || p.PaneID != "%0" || p.CurrentCommand != "claude" || p.Width != 120 || !p.PaneActive {
			t.Errorf("parsed pane wrong: %+v", p)
		}
	})
}

func TestListPanes_NoServerIsEmpty(t *testing.T) {
	withStub(t, func(name string, args ...string) ([]byte, []byte, error) {
		return nil, []byte("no server running on /tmp/tmux-1000/default"), errors.New("exit 1")
	}, func() {
		panes, err := ListPanes()
		if err != nil {
			t.Fatalf("no-server should be benign, got %v", err)
		}
		if len(panes) != 0 {
			t.Errorf("want empty, got %d", len(panes))
		}
	})
}

func TestClassify(t *testing.T) {
	if got := classify("no server running on x", errors.New("x")); got != "no_server" {
		t.Errorf("got %q", got)
	}
	if got := classify("", exec.ErrNotFound); got != "not_installed" {
		t.Errorf("got %q", got)
	}
	if got := classify("can't find session: foo", errors.New("x")); got != "no_target" {
		t.Errorf("got %q", got)
	}
}

func TestInsideTmux(t *testing.T) {
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	if InsideTmux() {
		t.Error("should be false with TMUX and TMUX_PANE unset")
	}
	t.Setenv("TMUX_PANE", "%0")
	if !InsideTmux() {
		t.Error("should be true with TMUX_PANE set")
	}
	_ = os.Getenv
}
