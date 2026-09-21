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

// TestDetect_StateIsFooterBound is the regression for issue #4 (and the within-frame
// case of #2): the current state must come from the harness FOOTER, not from
// keywords that merely appear in scrolled content or displayed output.
func TestDetect_StateIsFooterBound(t *testing.T) {
	pad := func(s string, n int) string { return strings.Repeat(s+"\n", n) }

	// A) A genuinely RUNNING Claude frame that has an "Error:" mention in its
	//    scrolled-up conversation. Pre-fix, the error rule (checked before running,
	//    and matched anywhere in the tail) won → claude/error. Now the footer's
	//    "esc to interrupt" governs → claude/running.
	running := "Claude Code\n  Error: an earlier tool call failed\n" +
		pad("  (scrolled conversation line)", 14) +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt\n"
	if res := program.Detect(running, false); res.Program != "claude" || res.Status != program.StatusRunning {
		t.Errorf("A: running harness with a scrolled error: got %s/%s, want claude/running", res.Program, res.Status)
	}

	// B) A shell that merely DISPLAYS muxray's output (mentions Claude/Codex/Copilot
	//    and "Error:") with a shell prompt as its footer. muxray must NOT report the
	//    harness error from the displayed JSON (the cap.json class from issue #4);
	//    the live footer is a shell prompt, so the pane is a shell at rest →
	//    shell/idle (it used to decline to unknown/unknown; a recognized shell is a
	//    first-class idle state now). The load-bearing guarantee is unchanged: the
	//    scrolled claude/error is not surfaced.
	shell := "$ muxray inspect dan\n" +
		"  \"classification\": { \"program\": \"claude\", \"status\": \"error\" },\n" +
		"  \"evidence\": \"Error: something failed\",\n" +
		"  status   Classify the program state (Claude/Codex/Copilot, or unknown)\n" +
		pad("  \"key\": \"value\",", 14) +
		"user@host ~ $ \n"
	if res := program.Detect(shell, false); res.Program != "shell" || res.Status != program.StatusIdle {
		t.Errorf("B: shell displaying harness keywords: got %s/%s, want shell/idle", res.Program, res.Status)
	}
}

// TestDetect_RunningBeatsInFooterStoppedPhrase is the regression for job 305: a
// live, actively-running Claude frame was classified "blocked" because its todo
// panel — which renders INSIDE the footer, right above the input box — carried a
// row reading "… blocked by …". The content-phrase rule claude.blocked matched
// that incidental row and, being ordered before claude.running, won first-match.
// Footer-bounding (issue #4, TestDetect_StateIsFooterBound case A) does NOT cover
// this: the phrase is within the footer, not scrolled above it; only the live
// "esc to interrupt" chrome winning over the content-phrase stopped-state rules
// fixes it. Frame is synthetic (muxray is public) but structurally identical to the
// real rw capture that triggered the report.
func TestDetect_RunningBeatsInFooterStoppedPhrase(t *testing.T) {
	frame := "" +
		"● Refactoring the payments module…\n" +
		"\n" +
		"● Update Todos\n" +
		"  ⎿  ✔ #41 Fix typecheck error on main (build break)\n" +
		"     ◼ #42 Extract the retry helper into its own package\n" +
		"     ◻ #43 Cache the QR decoder locally (web scanner blocked by CSP)\n" +
		"     ◻ #44 Start the call harness with the mic muted\n" +
		"\n" +
		"────────────────────────────────────────────────────────────────────\n" +
		"❯\n" +
		"────────────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · esc to interrupt · ctrl+t to hide tasks\n"
	clean := normalize.Clean(frame, 200).Clean
	res := program.Detect(clean, false)
	if res.Program != "claude" || res.Status != program.StatusRunning {
		t.Fatalf("running frame with in-footer 'blocked by' todo: got %s/%s, want claude/running", res.Program, res.Status)
	}
	if res.RuleID != "claude.running" {
		t.Errorf("got rule %q, want claude.running (the live indicator must win over content-phrase stopped states)", res.RuleID)
	}
}

// TestDetect_RunningBeatsInFooterStoppedPhrase_Siblings is the job-305 sweep
// for the sibling adapters: the same inversion (a live, working pane carrying
// an incidental "blocked by" / "failed to" row in its in-footer status panel)
// must classify as running for Codex and Copilot too, not error/blocked.
// Before the reorder these frames classified codex/error and copilot/error
// respectively (reproduced red against the pre-fix rule order).
func TestDetect_RunningBeatsInFooterStoppedPhrase_Siblings(t *testing.T) {
	frames := []struct {
		name        string
		frame       string
		wantProgram string
		wantRule    string
	}{
		{
			name: "codex",
			frame: "OpenAI Codex\n" +
				"  • Working (esc to interrupt)\n" +
				"  Running command: go test ./...\n" +
				"  ◻ #43 Cache the QR decoder locally (web scanner blocked by CSP)\n" +
				"  ⎿  previous attempt failed to reach the service\n",
			wantProgram: "codex",
			wantRule:    "codex.running",
		},
		{
			name: "copilot",
			frame: "GitHub Copilot\n" +
				"  Thinking…\n" +
				"  Generating a suggestion (esc to interrupt)\n" +
				"  ◻ #12 Retry policy (merge conflict in retry.go)\n" +
				"  note: last build failed on two packages\n",
			wantProgram: "copilot",
			wantRule:    "copilot.running",
		},
	}
	for _, f := range frames {
		t.Run(f.name, func(t *testing.T) {
			clean := normalize.Clean(f.frame, 200).Clean
			res := program.Detect(clean, false)
			if res.Program != f.wantProgram || res.Status != program.StatusRunning {
				t.Fatalf("%s running frame with in-footer stopped phrases: got %s/%s, want %s/running\npane:\n%s",
					f.name, res.Program, res.Status, f.wantProgram, clean)
			}
			if res.RuleID != f.wantRule {
				t.Errorf("%s: got rule %q, want %s (live indicator must win over content-phrase stopped states)",
					f.name, res.RuleID, f.wantRule)
			}
		})
	}
}

// TestDetect_StoppedStatesStillDetectedAfterReorder guards the other side of
// the job-305 sibling sweep: reordering running ahead of error/blocked must not
// shadow a genuinely stopped pane. A stopped frame has no live spinner, so
// running cannot match and the content-phrase rules still fire.
func TestDetect_StoppedStatesStillDetectedAfterReorder(t *testing.T) {
	cases := []struct {
		name   string
		frame  string
		status program.Status
		rule   string
	}{
		{"codex_error", "OpenAI Codex\n  stream error: rate limit exceeded (429)\n  failed to complete the request\n",
			program.StatusError, "codex.error"},
		{"codex_blocked", "OpenAI Codex\n  I can't continue: there is a merge conflict in internal/auth/user.go.\n  Resolve it, then tell me to proceed.\n",
			program.StatusBlocked, "codex.blocked"},
		{"copilot_error", "GitHub Copilot\n  error: could not reach the Copilot service\n  Request failed.\n",
			program.StatusError, "copilot.error"},
		{"copilot_blocked", "GitHub Copilot\n  Cannot continue until the merge conflict in retry.go is resolved.\n",
			program.StatusBlocked, "copilot.blocked"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			clean := normalize.Clean(c.frame, 200).Clean
			res := program.Detect(clean, false)
			if res.Status != c.status || res.RuleID != c.rule {
				t.Fatalf("stopped frame: got %s/%s (%s), want %s (%s)\npane:\n%s",
					res.Program, res.Status, res.RuleID, c.status, c.rule, clean)
			}
		})
	}
}

// TestDetect_ClaudeReadyPromptIsIdle is the regression for the chat-reported bug
// (job 272): the CURRENT Claude Code ready prompt — a "✻ Cooked for …" rested
// spinner, the "❯" input box, and the permission-mode footer
// "⏵⏵ <mode> on (shift+tab to cycle)" — was classified program=unknown because
// the idle rule only knew the older "? for shortcuts" footer. The frame below is
// the reported transcript, sanitized to generic content (muxray is public). It
// includes tmux's window-name status line, which is why the signature matched on
// the word "claude" while no state rule fired → the misleading unknown.
func TestDetect_ClaudeReadyPromptIsIdle(t *testing.T) {
	frame := "\n" +
		"✻ Cooked for 13m 24s\n" +
		"\n" +
		"────────────────────────────────────────────────────────────────────────────\n" +
		"❯ refactor the auth module and add tests\n" +
		"────────────────────────────────────────────────────────────────────────────\n" +
		"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ← for agents\n" +
		"\n\n" +
		"[default] 0:claude*                                            \"✳ edit config\" 18:06 06-Jun-26\n"
	clean := normalize.Clean(frame, 200).Clean
	res := program.Detect(clean, false)
	if res.Program != "claude" || res.Status != program.StatusIdle {
		t.Fatalf("ready prompt: got %s/%s, want claude/idle", res.Program, res.Status)
	}
	if res.RuleID != "claude.idle" {
		t.Errorf("ready prompt: got rule %q, want claude.idle", res.RuleID)
	}
}

// TestDetect_ClaudeRecognizedWithoutBrandWord guards the signature addition: the
// permission-mode footer alone ("shift+tab to cycle") identifies Claude even when
// the capture omits the word "claude" entirely (a pane captured without tmux's
// status line). Without the signature change this declines to no_program_signature.
func TestDetect_ClaudeRecognizedWithoutBrandWord(t *testing.T) {
	frame := "" +
		"╭──────────────────────────────────────────────╮\n" +
		"│ > write the migration                          │\n" +
		"╰──────────────────────────────────────────────╯\n" +
		"  ⏵⏵ accept edits on (shift+tab to cycle)\n"
	if strings.Contains(strings.ToLower(frame), "claude") {
		t.Fatal("test premise broken: frame must not contain the word claude")
	}
	clean := normalize.Clean(frame, 200).Clean
	res := program.Detect(clean, false)
	if res.Program != "claude" || res.Status != program.StatusIdle {
		t.Fatalf("brandless ready prompt: got %s/%s, want claude/idle", res.Program, res.Status)
	}
}

// TestDetect_DeclineReasonIsDistinct guards the match_source diagnosability fix:
// the two decline paths must report different reasons so an unknown is debuggable
// from the result alone, without --explain. A frame that mentions a harness but
// shows no live state declines with no_state_match; truly unrecognized text
// declines with no_program_signature.
func TestDetect_DeclineReasonIsDistinct(t *testing.T) {
	// Claude named in scrolled content, but the footer shows no live state.
	noState := "Claude Code session log\n" + strings.Repeat("  some scrolled prose line\n", 14)
	if res := program.Detect(noState, false); res.Program != "unknown" || res.MatchSource != "no_state_match" {
		t.Errorf("harness-named, no live state: got program=%s match_source=%q, want unknown/no_state_match",
			res.Program, res.MatchSource)
	}
	// No harness recognized at all.
	noSig := "just some unrelated terminal output\nnothing to see here\n"
	if res := program.Detect(noSig, false); res.Program != "unknown" || res.MatchSource != "no_program_signature" {
		t.Errorf("unrecognized text: got program=%s match_source=%q, want unknown/no_program_signature",
			res.Program, res.MatchSource)
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

// TestDetect_IdleTodoRowIsNotState is issue #10 item 1: an AT-REST pane whose
// todo/plan panel carries a row with a stopped-state phrase ("blocked by",
// "error:", "could not") must classify as idle. The todo panel sits inside the
// footer, so footer-bounding alone does not exclude it, and with no live
// spinner the running rule cannot save it the way it does for a working pane.
// A todo row is a line led by a checkbox glyph (◻ ◼ ✔ ☐ ☒), optionally after
// Claude's ⎿ continuation mark. A ⎿ line WITHOUT a checkbox is not a todo row:
// Claude prints its API errors that way, which the second half pins.
func TestDetect_IdleTodoRowIsNotState(t *testing.T) {
	frames := []struct {
		name        string
		frame       string
		wantProgram string
		wantRule    string
	}{
		{
			name: "claude",
			frame: "● Update Todos\n" +
				"  ⎿  ✔ #41 Fix typecheck error: missing return (build break)\n" +
				"     ◻ #43 Cache the QR decoder locally (web scanner blocked by CSP)\n" +
				"     ☐ #44 Start the call harness with the mic muted\n" +
				"\n" +
				"────────────────────────────────────────────────────────────────────\n" +
				"❯\n" +
				"────────────────────────────────────────────────────────────────────\n" +
				"  ⏵⏵ bypass permissions on (shift+tab to cycle) · ctrl+t to hide tasks\n",
			wantProgram: "claude",
			wantRule:    "claude.idle",
		},
		{
			name: "codex",
			frame: "OpenAI Codex  v1.0\n" +
				"  ◻ #43 Cache the QR decoder locally (web scanner blocked by CSP)\n" +
				"  ◼ #45 Handle error: timeout on retry\n" +
				"\n" +
				"  ›\n",
			wantProgram: "codex",
			wantRule:    "codex.idle",
		},
		{
			name: "copilot",
			frame: "GitHub Copilot\n" +
				"  ☒ #12 Retry policy (merge conflict in retry.go)\n" +
				"  ◻ #13 Report when the client could not reach the registry\n" +
				"\n" +
				"  copilot>\n",
			wantProgram: "copilot",
			wantRule:    "copilot.idle",
		},
	}
	for _, f := range frames {
		t.Run(f.name, func(t *testing.T) {
			clean := normalize.Clean(f.frame, 200).Clean
			res := program.Detect(clean, false)
			if res.Program != f.wantProgram || res.Status != program.StatusIdle || res.RuleID != f.wantRule {
				t.Fatalf("%s at-rest frame with a stopped-phrase todo row: got %s/%s (%s), want %s/idle (%s)\npane:\n%s",
					f.name, res.Program, res.Status, res.RuleID, f.wantProgram, f.wantRule, clean)
			}
		})
	}

	// The same phrases OUTSIDE a todo row still mean what they say.
	stopped := []struct {
		name   string
		frame  string
		status program.Status
		rule   string
	}{
		{"claude API error on a ⎿ line", "  ⎿ API Error: 500 Internal Server Error\n\n  ? for shortcuts\n",
			program.StatusError, "claude.error"},
		{"claude blocked in prose", "  ⏺ Claude\n  I'm blocked by a merge conflict in auth.go.\n\n  ? for shortcuts\n",
			program.StatusBlocked, "claude.blocked"},
		{"codex error line", "OpenAI Codex\n  error: failed to apply patch\n",
			program.StatusError, "codex.error"},
		{"copilot blocked line", "GitHub Copilot\n  Cannot continue until the lock is released.\n",
			program.StatusBlocked, "copilot.blocked"},
	}
	for _, s := range stopped {
		t.Run(s.name, func(t *testing.T) {
			res := program.Detect(normalize.Clean(s.frame, 200).Clean, false)
			if res.Status != s.status || res.RuleID != s.rule {
				t.Fatalf("got %s/%s (%s), want %s (%s)", res.Program, res.Status, res.RuleID, s.status, s.rule)
			}
		})
	}
}

// TestNoConflictingStateRules is issue #10 item 2, a design-time tripwire: for
// every committed fixture, the live-working rule and a content-phrase stopped
// rule (error, blocked) of the detected program must not BOTH match the
// footer. Rule order decides which one is reported, but a co-fire means the
// fixture carries text that makes the classification depend on that order,
// which is the fixture-design smell this repo has been bitten by.
func TestNoConflictingStateRules(t *testing.T) {
	base := filepath.Join("testdata", "fixtures")
	programs, err := os.ReadDir(base)
	if err != nil {
		t.Fatalf("read fixtures dir: %v", err)
	}
	checked, sawRunning := 0, 0
	for _, prov := range programs {
		if !prov.IsDir() {
			continue
		}
		files, _ := os.ReadDir(filepath.Join(base, prov.Name()))
		for _, f := range files {
			if !strings.HasSuffix(f.Name(), ".txt") {
				continue
			}
			raw, err := os.ReadFile(filepath.Join(base, prov.Name(), f.Name()))
			if err != nil {
				t.Fatal(err)
			}
			res := program.Detect(normalize.Clean(string(raw), 200).Clean, true)
			checked++
			matched := map[program.Status][]string{}
			for _, e := range res.Trace {
				if e.Program == res.Program && e.Matched && e.RuleID != "signature" {
					matched[e.Status] = append(matched[e.Status], e.RuleID)
				}
			}
			if len(matched[program.StatusRunning]) == 0 {
				continue
			}
			sawRunning++
			for _, stop := range []program.Status{program.StatusError, program.StatusBlocked} {
				if ids := matched[stop]; len(ids) > 0 {
					t.Errorf("%s/%s: %v co-fires with %v on the same footer",
						prov.Name(), f.Name(), matched[program.StatusRunning], ids)
				}
			}
		}
	}
	// the sweep must have reached real frames, including live-working ones
	if checked < 20 || sawRunning < 3 {
		t.Fatalf("tripwire swept %d fixtures, %d with a running match; the corpus was not reached", checked, sawRunning)
	}
}
