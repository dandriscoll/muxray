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

// tailLines is how many of the most recent lines a parser considers — status is
// a property of the current screen, which lives at the bottom of the pane.
const tailLines = 60

// Detect classifies cleaned pane text. When no program signature is
// recognized, it returns Program="unknown", Status=unknown — a first-class
// result, never a crash. When explain is true, the full evaluation trace is
// attached (powers `--explain` and the diagnosis of unknown classifications).
func Detect(clean string, explain bool) Result {
	text := lastLines(clean, tailLines)
	lower := strings.ToLower(text)
	var trace []TraceEntry

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
		res := Result{Program: "unknown", Status: StatusUnknown, MatchSource: "no_program_signature", Confidence: 0}
		if explain {
			res.Trace = trace
		}
		return res
	}

	// Step 2: classify with the chosen program's ordered rules; first match
	// wins. A recognized program with no matching rule reports its name with an
	// unknown status (signature_only) rather than guessing a state.
	a := adapters[chosen]
	best := Result{Program: a.name, Status: StatusUnknown, MatchSource: "signature_only", Confidence: 0.3}
	fired := false
	for _, rule := range a.rules {
		ok, ev := rule.match(text, lower)
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
