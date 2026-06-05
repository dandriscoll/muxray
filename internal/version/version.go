// Package version carries build identification, injected at link time via
// -ldflags "-X github.com/dandriscoll/muxray/internal/version.Version=...".
package version

import "fmt"

// These are overridden at build time by the release/build tooling. The defaults
// are what a `go install` from source reports.
var (
	Version = "dev"
	Commit  = "none"
	Date    = "unknown"
)

// String returns a human-readable one-line version identifier.
func String() string {
	return fmt.Sprintf("muxray %s (commit %s, built %s)", Version, Commit, Date)
}
