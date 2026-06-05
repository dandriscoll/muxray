package snapshot

import (
	"os"
	"testing"
	"time"

	"github.com/dandriscoll/muxray/internal/tmux"
)

func mkSnap(target tmux.Target, clean string, at time.Time) *Snapshot {
	return New(target, "3.4", "raw", clean, 1, len(clean), false, false, at)
}

func TestHashContent_Deterministic(t *testing.T) {
	a := HashContent("same content")
	b := HashContent("same content")
	if a != b {
		t.Fatalf("hash not deterministic: %s vs %s", a, b)
	}
	if HashContent("different") == a {
		t.Fatal("distinct content produced same hash")
	}
}

func TestNew_HashIsContentOnly(t *testing.T) {
	// Same cleaned content captured at different times / different ids must
	// share the content hash (the basis of cross-machine changed:false).
	t1 := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	t2 := time.Date(2026, 2, 2, 0, 0, 0, 0, time.UTC)
	s1 := mkSnap(tmux.Target{Raw: "%1"}, "hello world", t1)
	s2 := mkSnap(tmux.Target{Raw: "%2"}, "hello world", t2)
	if s1.ContentHash != s2.ContentHash {
		t.Errorf("content hashes differ for identical content: %s vs %s", s1.ContentHash, s2.ContentHash)
	}
	if s1.ID == s2.ID {
		t.Error("ids should differ for different target/time")
	}
}

func TestSaveLoadRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s := mkSnap(tmux.Target{Raw: "%0", Session: "dev"}, "content here", time.Now())
	path, err := Save(s, dir)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if got.ContentHash != s.ContentHash || got.Clean != s.Clean || got.ID != s.ID {
		t.Errorf("round-trip mismatch: %+v vs %+v", got, s)
	}
}

func TestLatest(t *testing.T) {
	dir := t.TempDir()
	target := tmux.Target{Raw: "%0", Session: "dev"}
	older := mkSnap(target, "old", time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	newer := mkSnap(target, "new", time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC))
	if _, err := Save(older, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := Save(newer, dir); err != nil {
		t.Fatal(err)
	}
	got, err := Latest(dir, target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clean != "new" {
		t.Errorf("Latest returned %q, want new", got.Clean)
	}
}

func TestLatest_NoneFound(t *testing.T) {
	dir := t.TempDir()
	_, err := Latest(dir, tmux.Target{Raw: "%9", Session: "nope"})
	if err != os.ErrNotExist {
		t.Errorf("got %v, want ErrNotExist", err)
	}
}

func TestFindByID(t *testing.T) {
	dir := t.TempDir()
	s := mkSnap(tmux.Target{Raw: "%0", Session: "dev"}, "find me", time.Now())
	if _, err := Save(s, dir); err != nil {
		t.Fatal(err)
	}
	got, err := FindByID(dir, s.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Clean != "find me" {
		t.Errorf("got %q", got.Clean)
	}
	if _, err := FindByID(dir, "deadbeef0000"); err != os.ErrNotExist {
		t.Errorf("missing id: got %v, want ErrNotExist", err)
	}
}

func TestTargetKey_Sanitizes(t *testing.T) {
	// Path separators must not survive into the store key; '.' is allowed.
	got := TargetKey(tmux.Target{Raw: "weird/../name", Session: "weird/../name"})
	if got != "weird_.._name" {
		t.Errorf("TargetKey = %q, want weird_.._name", got)
	}
	if TargetKey(tmux.Target{Raw: ""}) == "" {
		t.Error("empty target must map to a non-empty key")
	}
}
