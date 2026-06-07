// Command muxray reads tmux sessions/panes as deterministic JSON for LLM
// agents: structured listing, snapshotting, change diffing, and deterministic
// program-state classification for Claude, Codex, and Copilot.
package main

import (
	"os"

	"github.com/dandriscoll/muxray/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
