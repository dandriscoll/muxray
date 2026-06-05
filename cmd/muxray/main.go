// Command muxray reads tmux sessions/panes and adapts their output for LLM
// agents: structured listing, snapshotting, change diffing, and deterministic
// provider-state classification for Claude, Codex, and Copilot.
package main

import (
	"os"

	"github.com/dandriscoll/muxray/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
