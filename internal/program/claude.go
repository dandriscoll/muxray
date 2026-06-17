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
			// Claude-distinctive ready-prompt footer: the permission-mode line
			// ("⏵⏵ bypass permissions on (shift+tab to cycle)") that Claude shows
			// whenever it is idle and waiting for input. The "shift+tab to cycle"
			// mode-switch hint is specific to Claude Code, so it identifies the
			// harness even when the capture omits the word "claude" (e.g. the pane
			// is captured without tmux's window-name status line).
			if containsAny(lower, "shift+tab to cycle") {
				return 2
			}
			// Generic UI hints shared with other harnesses (weak).
			if containsAny(lower, "esc to interrupt", "? for shortcuts") {
				return 1
			}
			return 0
		},
		rules: []Rule{
			// Order matters: first match wins. The ordering is by RELIABILITY of
			// the signal, not by a notion of urgency.
			//
			//   1. needs_approval / waiting_for_input — real dialog chrome; the
			//      agent is halted needing the human. These never co-occur with the
			//      live spinner.
			//   2. running — the live-working indicator ("esc to interrupt"). This
			//      is the single most reliable Claude chrome: a pane showing it is
			//      actively executing a turn and therefore CANNOT be in a stopped
			//      state. It must beat the content-phrase rules below.
			//   3. error / blocked — these key off free-text content substrings
			//      ("error:", "blocked by", "merge conflict", …), not chrome, so
			//      they also match incidental footer content: the agent's todo
			//      panel, a quoted log line, narration. Bounding state to the footer
			//      (issues #2/#4) is not enough — Claude renders its todo panel
			//      INSIDE the footer, directly above the input box. So these run
			//      AFTER running: a phrase like "(… blocked by CSP)" in a todo row
			//      must not override a pane that is provably still working
			//      (job 305 — the rw session reported running-as-blocked).
			//
			// error/blocked still fire correctly for a genuinely stopped pane: when
			// the agent has errored or declared itself blocked, the spinner is gone,
			// "esc to interrupt" is absent, running does not match, and these win.
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
			phraseRule("claude.error", StatusError, 0.85,
				"api error", "execution error", "request failed",
				"overloaded", "error:", "fatal:",
			),
			phraseRule("claude.blocked", StatusBlocked, 0.8,
				"blocked on", "blocked by", "cannot continue until",
				"waiting on lock", "merge conflict",
			),
			phraseRule("claude.completed", StatusCompleted, 0.7,
				"completed in", "✓ done", "done!", "all set",
			),
			phraseRule("claude.idle", StatusIdle, 0.5,
				"? for shortcuts", "/help for help", "try \"",
				// The current Claude Code ready prompt no longer shows
				// "? for shortcuts" at rest; it shows the permission-mode footer
				// ("⏵⏵ <mode> on (shift+tab to cycle)") above the input box while
				// it waits for the next instruction. A running frame carries
				// "esc to interrupt" and is caught by claude.running above, which
				// is checked first, so this only fires when the pane is at rest.
				"shift+tab to cycle",
			),
		},
	}
}
