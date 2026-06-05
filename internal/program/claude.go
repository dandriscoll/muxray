package program

// claudeAdapter recognizes Anthropic's Claude Code terminal harness.
//
// Signature and rules key off phrases that are stable across the Claude Code
// TUI rather than any single transcript: the "esc to interrupt" working
// indicator, the "? for shortcuts" ready prompt, approval dialogs, and error
// banners. Representative fixtures live under testdata/fixtures/claude; the
// nightly drift CI validates these against live Claude output.
func claudeAdapter() adapter {
	return adapter{
		name: "claude",
		signature: func(text, lower string) int {
			// Brand or Claude-distinctive whimsical spinner words.
			if containsAny(lower, "claude", "anthropic",
				"cogitating", "pondering", "herding", "ruminating", "schlepping", "noodling") {
				return 2
			}
			// Generic UI hints shared with other harnesses (weak).
			if containsAny(lower, "esc to interrupt", "? for shortcuts") {
				return 1
			}
			return 0
		},
		rules: []Rule{
			// Order matters: most urgent / most specific first.
			phraseRule("claude.error", StatusError, 0.85,
				"api error", "execution error", "request failed",
				"overloaded", "error:", "fatal:",
			),
			phraseRule("claude.blocked", StatusBlocked, 0.8,
				"blocked on", "blocked by", "cannot continue until",
				"waiting on lock", "merge conflict",
			),
			phraseRule("claude.needs_approval", StatusNeedsApproval, 0.9,
				"do you want to proceed", "do you want to make this edit",
				"do you want to create", "do you want to run",
				"❯ 1. yes", "1. yes", "approve this", "allow this tool",
			),
			phraseRule("claude.waiting_for_input", StatusWaitingForInput, 0.8,
				"press enter to continue", "waiting for your input",
				"enter your response", "(y/n)", "[y/n]",
			),
			phraseRule("claude.running", StatusRunning, 0.9,
				"esc to interrupt", "cogitating", "pondering", "herding",
				"thinking…", "working…", "tool use in progress",
			),
			phraseRule("claude.completed", StatusCompleted, 0.7,
				"completed in", "✓ done", "done!", "all set",
			),
			phraseRule("claude.idle", StatusIdle, 0.5,
				"? for shortcuts", "/help for help", "try \"",
			),
		},
	}
}
