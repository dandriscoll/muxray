// Package schema defines the stable, machine-readable output contract shared by
// every muxray command. The JSON these structs produce is the public API that
// LLM agents consume; field names and the schema version are a compatibility
// promise.
package schema

import "time"

// Version is the output-schema version. Bump it when the JSON contract changes
// in a way an agent could observe. It is emitted on every command result so an
// agent can detect format drift (the directive's "output format changed enough
// that status is unknown" condition, surfaced at the envelope level).
const Version = "1"

// Envelope holds the fields common to every command's JSON output. Each command
// response struct embeds it.
type Envelope struct {
	SchemaVersion string `json:"schema_version"`
	MuxrayVersion string `json:"muxray_version"`
	Command       string `json:"command"`
	GeneratedAt   string `json:"generated_at"`
}

// Error is the structured error payload emitted (to stderr) when a command
// fails. class is a stable, branchable identifier; hint names the next action.
type Error struct {
	Class   string `json:"class"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// ErrorResponse wraps an Error in an envelope for JSON error output.
type ErrorResponse struct {
	Envelope
	Error Error `json:"error"`
}

// NewEnvelope builds an envelope for the given command and muxray version. The
// caller supplies the version to avoid an import cycle with the version package.
func NewEnvelope(command, muxrayVersion string) Envelope {
	return Envelope{
		SchemaVersion: Version,
		MuxrayVersion: muxrayVersion,
		Command:       command,
		GeneratedAt:   time.Now().UTC().Format(time.RFC3339),
	}
}
