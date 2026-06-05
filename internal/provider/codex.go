package provider

// codexAdapter recognizes OpenAI's Codex CLI terminal harness.
//
// Representative fixtures live under testdata/fixtures/codex; the nightly drift
// CI validates these against live Codex output.
func codexAdapter() adapter {
	return adapter{
		name: "codex",
		signature: func(text, lower string) bool {
			return containsAny(lower, "codex", "openai codex")
		},
		rules: []Rule{
			phraseRule("codex.error", StatusError, 0.8,
				"error:", "failed to", "stream error", "rate limit",
				"request failed", "exception:",
			),
			phraseRule("codex.needs_approval", StatusNeedsApproval, 0.9,
				"allow command", "run command?", "do you want to run",
				"apply patch?", "allow this command", "approve",
				"[y/n]", "(y/n)",
			),
			phraseRule("codex.waiting_for_input", StatusWaitingForInput, 0.8,
				"press enter", "waiting for input", "enter to send",
				"type a message",
			),
			phraseRule("codex.running", StatusRunning, 0.88,
				"esc to interrupt", "working", "thinking", "generating",
				"running command", "applying patch",
			),
			phraseRule("codex.completed", StatusCompleted, 0.7,
				"completed", "finished", "✔", "done",
			),
			phraseRule("codex.idle", StatusIdle, 0.4,
				"openai codex", "codex",
			),
		},
	}
}
