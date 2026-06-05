package provider

// copilotAdapter recognizes the GitHub Copilot CLI terminal harness.
//
// Representative fixtures live under testdata/fixtures/copilot; the nightly
// drift CI validates these against live Copilot output.
func copilotAdapter() adapter {
	return adapter{
		name: "copilot",
		signature: func(text, lower string) bool {
			return containsAny(lower, "copilot", "github copilot")
		},
		rules: []Rule{
			phraseRule("copilot.error", StatusError, 0.8,
				"error:", "failed", "could not", "request failed",
			),
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
			phraseRule("copilot.completed", StatusCompleted, 0.7,
				"completed", "done", "✓", "suggestion accepted",
			),
			phraseRule("copilot.idle", StatusIdle, 0.4,
				"github copilot", "how can i help", "copilot>",
			),
		},
	}
}
