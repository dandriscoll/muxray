package diff

import (
	"reflect"
	"testing"
)

func TestDiff_DefaultUnchanged(t *testing.T) {
	// Default-state invariant: identical cleaned text is changed:false,
	// deterministically, with no diff work.
	const s = "line a\nline b\nline c"
	r := Compute("prev1", "cur1", s, s, Options{MaxItems: 40})
	if r.Changed {
		t.Fatal("expected Changed=false for identical content")
	}
	if r.Summary != "no changes" {
		t.Errorf("summary=%q", r.Summary)
	}
	if len(r.Added) != 0 || len(r.Removed) != 0 {
		t.Errorf("added=%v removed=%v, want empty", r.Added, r.Removed)
	}
}

func TestDiff_Changed_NonTrivialMapping(t *testing.T) {
	// The fixture forces a non-trivial pre->post mapping: a line is removed from
	// the middle and two are appended, so the diff logic is actually exercised.
	prev := "alpha\nbeta\ngamma\ndelta"
	cur := "alpha\ngamma\ndelta\nepsilon\nzeta"
	r := Compute("p", "c", prev, cur, Options{MaxItems: 40})
	if !r.Changed {
		t.Fatal("expected Changed=true")
	}
	if !contains(r.Removed, "beta") {
		t.Errorf("removed=%v, want it to contain beta", r.Removed)
	}
	if !contains(r.Added, "epsilon") || !contains(r.Added, "zeta") {
		t.Errorf("added=%v, want epsilon and zeta", r.Added)
	}
	if r.AddedCount != 2 || r.RemovedCount != 1 {
		t.Errorf("added=%d removed=%d, want 2/1", r.AddedCount, r.RemovedCount)
	}
}

func TestDiff_Context(t *testing.T) {
	prev := "a\nb\nc\nd\ne"
	cur := "a\nb\nX\nd\ne"
	r := Compute("p", "c", prev, cur, Options{Context: 1, MaxItems: 40})
	if !contains(r.Context, "b") || !contains(r.Context, "d") {
		t.Errorf("context=%v, want b and d around the change", r.Context)
	}
}

func TestDiff_CompactTruncation(t *testing.T) {
	prev := ""
	cur := "1\n2\n3\n4\n5"
	r := Compute("p", "c", prev, cur, Options{MaxItems: 2})
	if !r.Truncated {
		t.Error("expected Truncated=true when added exceeds MaxItems")
	}
	if len(r.Added) != 2 {
		t.Errorf("added len=%d, want 2 (capped)", len(r.Added))
	}
	r2 := Compute("p", "c", prev, cur, Options{MaxItems: 2, Full: true})
	if r2.Truncated || len(r2.Added) != 5 {
		t.Errorf("full mode: truncated=%v len=%d, want false/5", r2.Truncated, len(r2.Added))
	}
}

func TestDiff_TailExcerpt(t *testing.T) {
	cur := "1\n2\n3\n4\n5"
	r := Compute("p", "c", "x", cur, Options{TailLines: 2})
	if !reflect.DeepEqual(r.CurrentTailExcerpt, []string{"4", "5"}) {
		t.Errorf("tail=%v, want [4 5]", r.CurrentTailExcerpt)
	}
}

func TestDiff_AllAdded_AllRemoved(t *testing.T) {
	r := Compute("p", "c", "", "only", Options{MaxItems: 40})
	if r.RemovedCount != 0 || r.AddedCount != 1 {
		t.Errorf("empty->one: added=%d removed=%d", r.AddedCount, r.RemovedCount)
	}
	r = Compute("p", "c", "only", "", Options{MaxItems: 40})
	if r.RemovedCount != 1 || r.AddedCount != 0 {
		t.Errorf("one->empty: added=%d removed=%d", r.AddedCount, r.RemovedCount)
	}
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}
