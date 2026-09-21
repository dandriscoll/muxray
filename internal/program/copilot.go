package program

// copilotAdapter recognizes the GitHub Copilot CLI terminal harness.
//
// Representative fixtures live under testdata/fixtures/copilot; the nightly
// drift CI validates these against live Copilot output.
func copilotAdapter() adapter {
	return adapter{
		name: "copilot",
		signature: func(text, lower string) int {
			if containsAny(lower, "github copilot", "copilot") {
				return 2
			}
			return 0
		},
		// Rule order is load-bearing (job 305, swept to siblings here):
		// the live-working indicator (copilot.running) must precede the
		// content-phrase stopped-state rules (error/blocked), because Copilot
		// can carry incidental "failed …" / "blocked by …" text in its status
		// area while actively generating. Interactive-pause rules stay first:
		// a pane asking for approval or input has no live spinner, so they
		// cannot shadow a running frame.
		rules: []Rule{
			phraseRule("copilot.needs_approval", StatusNeedsApproval, 0.9,
				"allow copilot to run", "run this command?",
				"do you want to allow", "confirm", "[y/n]", "(y/n)",
			),
			phraseRule("copilot.waiting_for_input", StatusWaitingForInput, 0.8,
				"press enter", "type your", "select an option",
				"what would you like",
			),
			phraseRule("copilot.running", StatusRunning, 0.85,
				"esc to interrupt", "thinking", "generating", "working",
				"loading suggestion",
			),
			contentPhraseRule("copilot.error", StatusError, 0.8,
				"error:", "failed", "could not", "request failed",
			),
			contentPhraseRule("copilot.blocked", StatusBlocked, 0.8,
				"blocked on", "blocked by", "cannot continue until",
				"merge conflict",
			),
			phraseRule("copilot.completed", StatusCompleted, 0.7,
				"completed", "done", "✓", "suggestion accepted",
			),
			phraseRule("copilot.idle", StatusIdle, 0.4,
				"github copilot", "how can i help", "copilot>",
			),
		},
	}
}
