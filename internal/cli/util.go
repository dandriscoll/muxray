package cli

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"runtime"
)

func goos() string   { return runtime.GOOS }
func goarch() string { return runtime.GOARCH }

// invocationID returns a random per-invocation identifier for telemetry
// correlation. It carries no machine or user information.
func invocationID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}

func jsonMarshalIndent(v any) ([]byte, error) {
	return json.MarshalIndent(v, "", "  ")
}
