// Package snapshot models a captured pane plus metadata, and persists snapshots
// to a predictable local store. The content hash is computed over the cleaned
// text only (no timestamp or id), which is what makes unchanged-pane detection
// deterministic and reproducible across machines.
package snapshot

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/dandriscoll/muxray/internal/tmux"
)

// SchemaVersion is the snapshot on-disk format version. A change to
// normalization (which would shift content hashes) must bump this so a newer
// binary reading an older snapshot detects the mismatch instead of reporting a
// false change.
//
// v2: normalization removes agent input-box ghost/autosuggestion text, so a
// pane whose only change is a rotating ghost suggestion now hashes stably.
const SchemaVersion = "2"

// Snapshot is a captured pane and its metadata.
type Snapshot struct {
	SchemaVersion  string      `json:"schema_version"`
	ID             string      `json:"id"`
	CapturedAt     string      `json:"captured_at"`
	Target         tmux.Target `json:"target"`
	TmuxVersion    string      `json:"tmux_version"`
	Raw            string      `json:"raw,omitempty"`
	Clean          string      `json:"clean"`
	LineCount      int         `json:"line_count"`
	CharCount      int         `json:"char_count"`
	Truncated      bool        `json:"truncated"`
	ANSINormalized bool        `json:"ansi_normalized"`
	ContentHash    string      `json:"content_hash"`
	// ProgramSuggestion is the harness's input-box ghost autosuggestion /
	// placeholder text, lifted out of Clean so it is not mistaken for a pending
	// user prompt. It is intentionally excluded from ContentHash (which covers
	// Clean only), so a rotating suggestion never registers as a content change.
	ProgramSuggestion string `json:"program_suggestion,omitempty"`
}

// HashContent returns the deterministic content hash for cleaned pane text.
func HashContent(clean string) string {
	sum := sha256.Sum256([]byte(clean))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// New builds a snapshot, computing its content hash and id.
func New(target tmux.Target, tmuxVersion, raw, clean string, lineCount, charCount int, truncated, ansi bool, capturedAt time.Time) *Snapshot {
	s := &Snapshot{
		SchemaVersion:  SchemaVersion,
		CapturedAt:     capturedAt.UTC().Format(time.RFC3339Nano),
		Target:         target,
		TmuxVersion:    tmuxVersion,
		Raw:            raw,
		Clean:          clean,
		LineCount:      lineCount,
		CharCount:      charCount,
		Truncated:      truncated,
		ANSINormalized: ansi,
		ContentHash:    HashContent(clean),
	}
	s.ID = computeID(target.Raw, s.CapturedAt, s.ContentHash)
	return s
}

func computeID(rawTarget, capturedAt, contentHash string) string {
	sum := sha256.Sum256([]byte(rawTarget + "|" + capturedAt + "|" + contentHash))
	return hex.EncodeToString(sum[:])[:12]
}

// DefaultDir returns the snapshot store root, honoring XDG_STATE_HOME.
func DefaultDir() string {
	base := os.Getenv("XDG_STATE_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return filepath.Join(os.TempDir(), "muxray", "snapshots")
		}
		base = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(base, "muxray", "snapshots")
}

// TargetKey returns a filesystem-safe per-target subdirectory name.
func TargetKey(t tmux.Target) string {
	key := t.Session
	if key == "" {
		key = t.Raw
	}
	return sanitize(key)
}

func sanitize(s string) string {
	if s == "" {
		return "_"
	}
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '%', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// Snapshots persist raw captured pane content, which can contain anything on the
// user's screen (API keys, tokens, source, PII). They are therefore written
// user-private so another local user on a shared host cannot read them (issue
// #5). On Windows these Unix mode bits are largely a no-op; the guarantee is for
// POSIX hosts.
const (
	fileMode fs.FileMode = 0o600
	dirMode  fs.FileMode = 0o700
)

// Save writes the snapshot under dir (DefaultDir when empty) and returns the
// path written.
func Save(s *Snapshot, dir string) (string, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	d := filepath.Join(dir, TargetKey(s.Target))
	if err := os.MkdirAll(d, dirMode); err != nil {
		return "", fmt.Errorf("create snapshot directory %s: %w", d, err)
	}
	path := filepath.Join(d, s.ID+".json")
	if err := WriteFile(s, path); err != nil {
		return "", err
	}
	return path, nil
}

// WriteFile serializes a snapshot to an explicit path (the --out destination),
// written user-private (0600).
func WriteFile(s *Snapshot, path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal snapshot: %w", err)
	}
	if err := writeFile0600(path, b); err != nil {
		return fmt.Errorf("write snapshot %s: %w", path, err)
	}
	return nil
}

// writeFile0600 writes b to path with mode 0600, FORCING the mode even when path
// already exists — os.WriteFile's perm argument is ignored for an existing file,
// so a pre-existing world-readable destination (a re-used --out path, or a
// re-saved snapshot id) would otherwise keep its looser mode. Chmod runs before
// the content is written so the file is never readable with content at a looser
// mode.
func writeFile0600(path string, b []byte) error {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fileMode)
	if err != nil {
		return err
	}
	if err := f.Chmod(fileMode); err != nil {
		_ = f.Close()
		return err
	}
	if _, err := f.Write(b); err != nil {
		_ = f.Close()
		return err
	}
	return f.Close()
}

// Load reads and parses a snapshot file.
func Load(path string) (*Snapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read snapshot %s: %w", path, err)
	}
	var s Snapshot
	if err := json.Unmarshal(b, &s); err != nil {
		return nil, fmt.Errorf("parse snapshot %s: %w", path, err)
	}
	return &s, nil
}

// Latest returns the most recent snapshot (by CapturedAt) for a target, or
// os.ErrNotExist if none exist.
func Latest(dir string, t tmux.Target) (*Snapshot, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	d := filepath.Join(dir, TargetKey(t))
	entries, err := os.ReadDir(d)
	if err != nil {
		return nil, os.ErrNotExist
	}
	var newest *Snapshot
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		s, err := Load(filepath.Join(d, e.Name()))
		if err != nil {
			continue
		}
		if newest == nil || s.CapturedAt > newest.CapturedAt {
			newest = s
		}
	}
	if newest == nil {
		return nil, os.ErrNotExist
	}
	return newest, nil
}

// FindByID searches the store for a snapshot with the given id.
func FindByID(dir, id string) (*Snapshot, error) {
	if dir == "" {
		dir = DefaultDir()
	}
	want := id + ".json"
	var found *Snapshot
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if d.Name() == want {
			if s, e := Load(p); e == nil {
				found = s
				return fs.SkipAll
			}
		}
		return nil
	})
	if found == nil {
		return nil, os.ErrNotExist
	}
	return found, nil
}
