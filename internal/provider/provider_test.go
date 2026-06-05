package provider_test

import (
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/dandriscoll/muxray/internal/normalize"
	"github.com/dandriscoll/muxray/internal/provider"
)

var update = flag.Bool("update", false, "update golden files")

// goldenClass is the deterministic classification recorded per fixture.
type goldenClass struct {
	Provider string `json:"provider"`
	Status   string `json:"status"`
	RuleID   string `json:"rule_id"`
}

// TestFixtures runs every committed fixture transcript through the full
// normalize -> Detect pipeline and compares the classification to its golden.
// Run `go test ./internal/provider -update` to regenerate goldens intentionally.
func TestFixtures(t *testing.T) {
	base := filepath.Join("testdata", "fixtures")
	providers, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}
	for _, prov := range providers {
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
				res := provider.Detect(clean, false)
				got := goldenClass{Provider: res.Provider, Status: string(res.Status), RuleID: res.RuleID}

				// Self-check: the fixture's directory names the expected provider
				// ("generic" means no provider), and the file names the expected
				// status. This keeps fixtures from being tautological — a fixture
				// that does not actually exhibit its labeled state fails here even
				// after -update.
				wantProvider := provName
				if provName == "generic" {
					wantProvider = "unknown"
				}
				if got.Provider != wantProvider {
					t.Errorf("fixture provider mismatch: detected %q, fixture dir says %q", got.Provider, wantProvider)
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
	want := []provider.Status{
		provider.StatusIdle, provider.StatusRunning, provider.StatusBlocked,
		provider.StatusWaitingForInput, provider.StatusNeedsApproval,
		provider.StatusError, provider.StatusCompleted, provider.StatusUnknown,
	}
	seen := map[string]bool{}
	base := filepath.Join("testdata", "fixtures")
	providers, _ := os.ReadDir(base)
	for _, prov := range providers {
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
			seen[string(provider.Detect(clean, false).Status)] = true
		}
	}
	for _, w := range want {
		if !seen[string(w)] {
			t.Errorf("no fixture produces status %q — add one", w)
		}
	}
}

// TestProvider_DefaultUnknown is the zero/garbage-input default-state invariant:
// unrecognized output classifies as provider=unknown, status=unknown, never a
// crash.
func TestProvider_DefaultUnknown(t *testing.T) {
	for _, in := range []string{"", "   \n  \n", "just some random terminal text\n$ ls\n"} {
		res := provider.Detect(in, false)
		if res.Provider != "unknown" || res.Status != provider.StatusUnknown {
			t.Errorf("input %q: got %s/%s, want unknown/unknown", in, res.Provider, res.Status)
		}
	}
}

// TestExplainTrace verifies the parser trace is populated when explain is on and
// records both matched and unmatched rules (explainability contract).
func TestExplainTrace(t *testing.T) {
	res := provider.Detect("Claude Code\n  esc to interrupt\n", true)
	if len(res.Trace) == 0 {
		t.Fatal("expected a non-empty trace with explain=true")
	}
	if res.Status != provider.StatusRunning || res.RuleID != "claude.running" {
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
		status provider.Status
		ruleID string
	}{
		{"Claude Code\n  Do you want to proceed?\n  1. Yes\n", provider.StatusNeedsApproval, "claude.needs_approval"},
		{"OpenAI Codex\n  stream error: rate limit\n", provider.StatusError, "codex.error"},
		{"GitHub Copilot\n  Generating a suggestion\n", provider.StatusRunning, "copilot.running"},
	}
	for _, c := range cases {
		res := provider.Detect(c.in, false)
		if res.Status != c.status || res.RuleID != c.ruleID {
			t.Errorf("input %q: got %s/%s, want %s/%s", c.in, res.Status, res.RuleID, c.status, c.ruleID)
		}
	}
}
