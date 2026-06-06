package snapshot

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/dandriscoll/muxray/internal/tmux"
)

// TestSnapshotsAreUserPrivate pins the issue-#5 security control: snapshots hold
// raw pane content and must be written 0600 under 0700 dirs so another local user
// cannot read them. If a future edit reverts to 0644/0755 this test goes red.
func TestSnapshotsAreUserPrivate(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits are not the confidentiality mechanism on Windows")
	}
	dir := t.TempDir()
	s := mkSnap(tmux.Target{Raw: "%0", Session: "dev"}, "echo secret-token", time.Now())
	path, err := Save(s, dir)
	if err != nil {
		t.Fatalf("Save: %v", err)
	}

	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != fileMode {
		t.Errorf("snapshot file mode = %o, want %o (world/group must not read pane content)", got, fileMode)
	}

	di, err := os.Stat(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	if got := di.Mode().Perm(); got != dirMode {
		t.Errorf("snapshot dir mode = %o, want %o", got, dirMode)
	}
}

// TestWriteFileForcesPrivateOverExisting guards the overwrite hole that a plain
// "change 0o644 to 0o600" fix would miss: os.WriteFile's perm is ignored for an
// existing file, so a re-used --out path (or re-saved id) that pre-exists
// world-readable must be forced back to 0600.
func TestWriteFileForcesPrivateOverExisting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix mode bits are not the confidentiality mechanism on Windows")
	}
	path := filepath.Join(t.TempDir(), "out.json")
	// Pre-create the destination world-readable, as an older binary or the user
	// might have.
	if err := os.WriteFile(path, []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	s := mkSnap(tmux.Target{Raw: "%1"}, "content", time.Now())
	if err := WriteFile(s, path); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	fi, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if got := fi.Mode().Perm(); got != fileMode {
		t.Errorf("overwritten file mode = %o, want %o (must force-downgrade a pre-existing 0644)", got, fileMode)
	}
}
