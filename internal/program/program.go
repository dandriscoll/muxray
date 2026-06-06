// Package program classifies terminal output from coding-agent harnesses
// (Claude, Codex, Copilot) into a shared, deterministic state model. Each
// program is a small adapter: a signature that recognizes the harness plus an
// ordered list of named rules that map characteristic output to a status. The
// classification is explainable — every result reports which program and which
// rule matched, and an optional trace records every rule that was considered.
//
// The parsers are deliberately driven by representative fixtures (testdata) and
// validated against live harness output by the nightly drift CI, rather than
// overfitting to a single transcript.
package program

import "strings"

// Status is the deterministic, program-independent state of an agent pane.
type Status string

const (
	StatusIdle            Status = "idle"
	StatusRunning         Status = "running"
	StatusBlocked         Status = "blocked"
	StatusWaitingForInput Status = "waiting_for_input"
	StatusNeedsApproval   Status = "needs_approval"
	StatusError           Status = "error"
	StatusCompleted       Status = "completed"
	StatusUnknown         Status = "unknown"
)

// Rule maps a set of characteristic phrases to a status. The first rule (in
// adapter order) whose Match succeeds wins for that program.
type Rule struct {
	ID         string
	Status     Status
	Confidence float64
	match      func(text, lower string) (bool, string)
}

// adapter recognizes one harness and classifies its output.
//
// signature returns a strength: 0 (no match), 1 (a weak/generic UI hint that
// could belong to several harnesses, e.g. "esc to interrupt"), or 2 (a
// distinctive brand match, e.g. "OpenAI Codex"). The program with the strongest
// signature wins, so a shared UI phrase never lets one harness steal another's
// branded pane.
type adapter struct {
	name      string
	signature func(text, lower string) int
	rules     []Rule
}

// TraceEntry records one rule (or signature) evaluation for explainability.
type TraceEntry struct {
	Program  string `json:"program"`
	RuleID   string `json:"rule_id"`
	Status   Status `json:"status,omitempty"`
	Matched  bool   `json:"matched"`
	Evidence string `json:"evidence,omitempty"`
}

// Result is the classification outcome.
type Result struct {
	Program     string       `json:"program"`
	Status      Status       `json:"status"`
	RuleID      string       `json:"rule_id,omitempty"`
	MatchSource string       `json:"match_source,omitempty"`
	Confidence  float64      `json:"confidence"`
	Evidence    string       `json:"evidence,omitempty"`
	Trace       []TraceEntry `json:"trace,omitempty"`
}

// tailLines is how many of the most recent lines the SIGNATURE considers when
// deciding which program a pane belongs to — a brand banner can sit a little way
// up the frame.
const tailLines = 60

// footerLines is how many of the most recent lines the STATE rules consider. A
// live harness shows its current state in its footer (the status/input region at
// the very bottom); state phrases that appear higher up are scrolled content
// (a past error, a quoted log, muxray's own output), NOT the current state.
// Bounding state to the footer is what stops a stale or incidental match from
// being reported as the present state (issues #2 and #4). It comfortably covers
// every representative fixture (all <= ~8 lines).
const footerLines = 12

// Detect classifies cleaned pane text. It only speaks for a genuine live
// Claude/Codex/Copilot frame: it picks a program from the signature, then reads
// the current state from the FOOTER. If no footer state rule matches, it returns
// Program="unknown", Status=unknown and DECLINES — muxray does not comment on a
// pane that merely mentions a harness (a shell showing logs, a transcript, or
// muxray's own output) and leaves it for the agent to parse. Unknown is a
// first-class result, never a crash. When explain is true, the full evaluation
// trace is attached (powers `--explain` and the diagnosis of unknown results).
func Detect(clean string, explain bool) Result {
	text := lastLines(clean, tailLines)
	lower := strings.ToLower(text)
	footer := lastLines(clean, footerLines)
	footerLower := strings.ToLower(footer)
	var trace []TraceEntry

	declined := func() Result {
		res := Result{Program: "unknown", Status: StatusUnknown, MatchSource: "no_program_signature", Confidence: 0}
		if explain {
			res.Trace = trace
		}
		return res
	}

	// Step 0: shell pre-check. If the footer ends at an interactive shell prompt,
	// the harness is not live — the user has been returned to a shell (commonly a
	// websocket / incus-VM disconnect that dumped them out of Claude/Codex). This
	// is the footer-bound principle (issues #2/#4) applied to transport drops: a
	// shell prompt at the bottom wins over any scrolled-up "error:" text, so a
	// disconnect reads as shell/idle rather than an agent error.
	if _, ev, ok := detectShell(footer); ok {
		if explain {
			trace = append(trace, TraceEntry{Program: "shell", RuleID: "shell.idle", Status: StatusIdle, Matched: true, Evidence: ev})
		}
		res := Result{
			Program:     "shell",
			Status:      StatusIdle,
			RuleID:      "shell.idle",
			MatchSource: "rule:shell.idle",
			Confidence:  0.6,
			Evidence:    ev,
		}
		if explain {
			res.Trace = trace
		}
		return res
	}
	if explain {
		trace = append(trace, TraceEntry{Program: "shell", RuleID: "shell.idle", Status: StatusIdle, Matched: false})
	}

	// Step 1: choose the program with the strongest signature. Brand matches
	// (strength 2) always beat generic UI hints (strength 1); ties fall to
	// adapter order.
	chosen := -1
	bestSig := 0
	for i, a := range adapters {
		s := a.signature(text, lower)
		if explain {
			trace = append(trace, TraceEntry{Program: a.name, RuleID: "signature", Matched: s > 0, Evidence: sigName(s)})
		}
		if s > bestSig {
			bestSig = s
			chosen = i
		}
	}
	if chosen < 0 {
		return declined()
	}

	// Step 2: read the current state from the FOOTER using the chosen program's
	// ordered rules; first match wins. A program signature WITHOUT a live footer
	// state is not a confidently-live frame — most often the harness's name just
	// appears in displayed content — so muxray declines (unknown/unknown) rather
	// than guessing or reporting the program off an incidental keyword.
	a := adapters[chosen]
	var best Result
	fired := false
	for _, rule := range a.rules {
		ok, ev := rule.match(footer, footerLower)
		if explain {
			trace = append(trace, TraceEntry{Program: a.name, RuleID: rule.ID, Status: rule.Status, Matched: ok, Evidence: ev})
		}
		if ok && !fired {
			best = Result{
				Program:     a.name,
				Status:      rule.Status,
				RuleID:      rule.ID,
				MatchSource: "rule:" + rule.ID,
				Confidence:  rule.Confidence,
				Evidence:    ev,
			}
			fired = true
			if !explain {
				break
			}
		}
	}
	if !fired {
		return declined()
	}
	if explain {
		best.Trace = trace
	}
	return best
}

func sigName(strength int) string {
	switch strength {
	case 2:
		return "brand"
	case 1:
		return "weak_hint"
	default:
		return ""
	}
}

// Programs returns the registered program names (deterministic order).
func Programs() []string {
	names := make([]string, len(adapters))
	for i, a := range adapters {
		names[i] = a.name
	}
	return names
}

func lastLines(s string, n int) string {
	ls := strings.Split(s, "\n")
	if len(ls) > n {
		ls = ls[len(ls)-n:]
	}
	return strings.Join(ls, "\n")
}

// phraseRule builds a rule that fires when any of the given phrases appears
// (case-insensitively) in the text. The evidence is the original-case line that
// contained the match, so results stay explainable.
func phraseRule(id string, status Status, conf float64, phrases ...string) Rule {
	lowered := make([]string, len(phrases))
	for i, p := range phrases {
		lowered[i] = strings.ToLower(p)
	}
	return Rule{
		ID:         id,
		Status:     status,
		Confidence: conf,
		match: func(text, lower string) (bool, string) {
			for _, p := range lowered {
				if strings.Contains(lower, p) {
					return true, lineContaining(text, p)
				}
			}
			return false, ""
		},
	}
}

func lineContaining(text, needleLower string) string {
	for _, ln := range strings.Split(text, "\n") {
		if strings.Contains(strings.ToLower(ln), needleLower) {
			return strings.TrimSpace(ln)
		}
	}
	return ""
}

func containsAny(lower string, phrases ...string) bool {
	for _, p := range phrases {
		if strings.Contains(lower, strings.ToLower(p)) {
			return true
		}
	}
	return false
}
