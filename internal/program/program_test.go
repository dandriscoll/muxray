package program_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dandriscoll/muxray/internal/normalize"
	"github.com/dandriscoll/muxray/internal/program"
)

var update = flag.Bool("update", false, "update golden files")

// goldenClass is the deterministic classification recorded per fixture.
type goldenClass struct {
	Program string `json:"program"`
	Status  string `json:"status"`
	RuleID  string `json:"rule_id"`
}

// TestFixtures runs every committed fixture transcript through the full
// normalize -> Detect pipeline and compares the classification to its golden.
// Run `go test ./internal/program -update` to regenerate goldens intentionally.
func TestFixtures(t *testing.T) {
	base := filepath.Join("testdata", "fixtures")
	programs, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}
	for _, prov := range programs {
		if !prov.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(base, prov.Name()))
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".txt") {
				continue
			}
			provName, fileName := prov.Name(), f.Name()
			name := strings.TrimSuffix(fileName, ".txt")
			t.Run(provName+"/"+name, func(t *testing.T) {
				raw, err := os.ReadFile(filepath.Join(base, provName, fileName))
				if err != nil {
					t.Fatal(err)
				}
				clean := normalize.Clean(string(raw), 200).Clean
				res := program.Detect(clean, false)
				got := goldenClass{Program: res.Program, Status: string(res.Status), RuleID: res.RuleID}

				// Self-check: the fixture's directory names the expected program
				// ("generic" means no program), and the file names the expected
				// status. This keeps fixtures from being tautological — a fixture
				// that does not actually exhibit its labeled state fails here even
				// after -update.
				wantProgram := provName
				if provName == "generic" {
					wantProgram = "unknown"
				}
				if got.Program != wantProgram {
					t.Errorf("fixture program mismatch: detected %q, fixture dir says %q", got.Program, wantProgram)
				}
				if got.Status != name {
					t.Errorf("fixture status mismatch: detected %q, fixture file says %q", got.Status, name)
				}

				goldenPath := filepath.Join("testdata", "golden", provName, name+".json")
				if *update {
					if err := os.MkdirAll(filepath.Dir(goldenPath), 0o755); err != nil {
						t.Fatal(err)
					}
					b, _ := json.MarshalIndent(got, "", "  ")
					if err := os.WriteFile(goldenPath, append(b, '\n'), 0o644); err != nil {
						t.Fatal(err)
					}
				}
				wantB, err := os.ReadFile(goldenPath)
				if err != nil {
					t.Fatalf("missing golden %s (run with -update to create): %v", goldenPath, err)
				}
				var want goldenClass
				if err := json.Unmarshal(wantB, &want); err != nil {
					t.Fatal(err)
				}
				if got != want {
					t.Errorf("classification mismatch\n got: %+v\nwant: %+v", got, want)
				}
			})
		}
	}
}

// TestStatusCoverage asserts the fixture corpus exercises every status enum
// value — a coverage floor so no status ships without at least one fixture.
func TestStatusCoverage(t *testing.T) {
	want := []program.Status{
		program.StatusIdle, program.StatusRunning, program.StatusBlocked,
		program.StatusWaitingForInput, program.StatusNeedsApproval,
		program.StatusError, program.StatusCompleted, program.StatusUnknown,
	}
	seen := map[string]bool{}
	base := filepath.Join("testdata", "fixtures")
	programs, _ := os.ReadDir(base)
	for _, prov := range programs {
		if !prov.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(base, prov.Name()))
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".txt") {
				continue
			}
			raw, _ := os.ReadFile(filepath.Join(base, prov.Name(), f.Name()))
			clean := normalize.Clean(string(raw), 200).Clean
			seen[string(program.Detect(clean, false).Status)] = true
		}
	}
	for _, w := range want {
		if !seen[string(w)] {
			t.Errorf("no fixture produces status %q — add one", w)
		}
	}
}

// TestProgram_DefaultUnknown is the zero/garbage-input default-state invariant:
// unrecognized output classifies as program=unknown, status=unknown, never a
// crash.
func TestProgram_DefaultUnknown(t *testing.T) {
	for _, in := range []string{"", "   \n  \n", "just some random terminal text\n$ ls\n"} {
		res := program.Detect(in, false)
		if res.Program != "unknown" || res.Status != program.StatusUnknown {
			t.Errorf("input %q: got %s/%s, want unknown/unknown", in, res.Program, res.Status)
		}
	}
}

// TestExplainTrace verifies the parser trace is populated when explain is on and
// records both matched and unmatched rules (explainability contract).
func TestExplainTrace(t *testing.T) {
	res := program.Detect("Claude Code\n  esc to interrupt\n", true)
	if len(res.Trace) == 0 {
		t.Fatal("expected a non-empty trace with explain=true")
	}
	if res.Status != program.StatusRunning || res.RuleID != "claude.running" {
		t.Fatalf("got %s/%s, want running/claude.running", res.Status, res.RuleID)
	}
	var sawSignature, sawMatched bool
	for _, e := range res.Trace {
		if e.RuleID == "signature" {
			sawSignature = true
		}
		if e.Matched && e.RuleID == "claude.running" {
			sawMatched = true
		}
	}
	if !sawSignature || !sawMatched {
		t.Errorf("trace incomplete: signature=%v matchedRule=%v", sawSignature, sawMatched)
	}
}

// TestRuleSpecificity guards against a parser that hard-codes a status: each of
// these must resolve to its specific rule, so removing that rule would fail.
func TestRuleSpecificity(t *testing.T) {
	cases := []struct {
		in     string
		status program.Status
		ruleID string
	}{
		{"Claude Code\n  Do you want to proceed?\n  1. Yes\n", program.StatusNeedsApproval, "claude.needs_approval"},
		{"OpenAI Codex\n  stream error: rate limit\n", program.StatusError, "codex.error"},
		{"GitHub Copilot\n  Generating a suggestion\n", program.StatusRunning, "copilot.running"},
	}
	for _, c := range cases {
		res := program.Detect(c.in, false)
		if res.Status != c.status || res.RuleID != c.ruleID {
			t.Errorf("input %q: got %s/%s, want %s/%s", c.in, res.Status, res.RuleID, c.status, c.ruleID)
		}
	}
}
