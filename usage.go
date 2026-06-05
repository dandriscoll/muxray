// Package muxray exposes the embedded agent-facing usage contract (USAGE.md) so
// the binary can print the exact same document a user points an agent at —
// `muxray usage` emits this, and it can never drift from the committed file
// because it IS the committed file, embedded at build time.
package muxray

import _ "embed"

// Usage is the contents of USAGE.md, the calling contract for an agent driving
// muxray as a tool.
//
//go:embed USAGE.md
var Usage string
