// Package config loads optional user configuration from an XDG-predictable
// location. Configuration is entirely optional; a missing or malformed file
// yields zero values (telemetry off, defaults applied), never an error — muxray
// must work out of the box with no config.
package config

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// Config is the on-disk configuration document. Telemetry is opt-in, so the
// zero value (Enabled=false) means telemetry is disabled by default.
type Config struct {
	Telemetry struct {
		// Enabled gates any external telemetry emission. Default false (opt-in).
		Enabled bool `json:"enabled"`
	} `json:"telemetry"`
	// DefaultLines overrides the default pane line limit when > 0.
	DefaultLines int `json:"default_lines"`
}

// Path returns the configuration file path, honoring XDG_CONFIG_HOME.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "muxray", "config.json")
}

// Load reads the config file if present. Any error (missing, unreadable,
// malformed) returns the zero Config; configuration never blocks operation.
func Load() Config {
	var c Config
	p := Path()
	if p == "" {
		return c
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return c
	}
	_ = json.Unmarshal(b, &c) // malformed config is ignored, not fatal
	return c
}

// Exists reports whether a config file is present and readable.
func Exists() bool {
	p := Path()
	if p == "" {
		return false
	}
	_, err := os.Stat(p)
	return err == nil
}
