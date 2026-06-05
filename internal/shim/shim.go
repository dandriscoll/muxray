// Package shim provides deterministic, local, credential-free fake LLM backends
// that the real Claude (Anthropic) and Codex (OpenAI) terminal harnesses can be
// pointed at via their base-URL environment variables. This lets muxray's
// mock-harness tests — and local development — drive the real harness CLIs and
// classify their real terminal output without any provider API key or network
// access.
//
// The shim is deliberately content-blind: it dispatches on a chosen Scenario,
// not on the prompt text, so responses are fully deterministic and never depend
// on real model behavior.
package shim

import (
	"fmt"
	"net"
	"net/http"
)

// Provider selects which provider API the shim emulates.
type Provider string

const (
	ProviderAnthropic Provider = "anthropic"
	ProviderOpenAI    Provider = "openai"
)

// Scenario selects the deterministic response the shim produces. The names
// describe the harness state each is intended to elicit.
type Scenario string

const (
	// ScenarioText returns a plain assistant message (the harness streams it,
	// shows a working indicator, then returns to idle/completed).
	ScenarioText Scenario = "text"
	// ScenarioApproval returns a tool/command-execution request, which the
	// harness surfaces as an approval/confirmation prompt.
	ScenarioApproval Scenario = "approval"
	// ScenarioError returns an API error, which the harness surfaces as an error.
	ScenarioError Scenario = "error"
)

// Server is a running shim backend.
type Server struct {
	provider Provider
	scenario Scenario
	srv      *http.Server
	ln       net.Listener
}

// New builds a shim for the given provider and scenario.
func New(p Provider, s Scenario) *Server {
	if s == "" {
		s = ScenarioText
	}
	return &Server{provider: p, scenario: s}
}

// Handler returns the HTTP handler, so tests can mount it with httptest without
// binding a port.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	switch s.provider {
	case ProviderAnthropic:
		mux.HandleFunc("/v1/messages", s.anthropicMessages)
		mux.HandleFunc("/v1/messages/count_tokens", anthropicCountTokens)
	case ProviderOpenAI:
		mux.HandleFunc("/v1/chat/completions", s.openaiChat)
		mux.HandleFunc("/v1/responses", s.openaiResponses)
		mux.HandleFunc("/v1/models", openaiModels)
	}
	// Any endpoint the harness probes that we don't emulate gets a benign 404
	// (never a hang), so the harness degrades rather than blocking.
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusNotFound)
		fmt.Fprint(w, `{"type":"error","error":{"type":"not_found_error","message":"muxray shim: unhandled endpoint"}}`)
	})
	return mux
}

// Start binds 127.0.0.1:<port> (port 0 = OS-chosen) and serves in the background.
func (s *Server) Start(port int) error {
	ln, err := net.Listen("tcp", fmt.Sprintf("127.0.0.1:%d", port))
	if err != nil {
		return fmt.Errorf("shim listen: %w", err)
	}
	s.ln = ln
	s.srv = &http.Server{Handler: s.Handler()}
	go func() { _ = s.srv.Serve(ln) }()
	return nil
}

// Addr returns the host:port the shim is bound to (empty until Start).
func (s *Server) Addr() string {
	if s.ln == nil {
		return ""
	}
	return s.ln.Addr().String()
}

// BaseURL returns the http://host:port base of the running shim.
func (s *Server) BaseURL() string {
	return "http://" + s.Addr()
}

// Close stops the server.
func (s *Server) Close() error {
	if s.srv != nil {
		return s.srv.Close()
	}
	return nil
}

// Env returns the environment variables that point the corresponding harness at
// this shim. The API key is a non-empty placeholder — no real credential.
func (s *Server) Env() map[string]string {
	switch s.provider {
	case ProviderAnthropic:
		return map[string]string{
			"ANTHROPIC_BASE_URL": s.BaseURL(),
			"ANTHROPIC_API_KEY":  "muxray-shim-no-key",
		}
	case ProviderOpenAI:
		return map[string]string{
			"OPENAI_BASE_URL": s.BaseURL() + "/v1",
			"OPENAI_API_KEY":  "muxray-shim-no-key",
		}
	}
	return map[string]string{}
}

// ParseProvider parses a provider name (accepts harness aliases).
func ParseProvider(s string) (Provider, error) {
	switch s {
	case "anthropic", "claude":
		return ProviderAnthropic, nil
	case "openai", "codex":
		return ProviderOpenAI, nil
	}
	return "", fmt.Errorf("unknown provider %q (want anthropic|openai)", s)
}

// ParseScenario parses a scenario name; empty defaults to text.
func ParseScenario(s string) (Scenario, error) {
	switch Scenario(s) {
	case ScenarioText, ScenarioApproval, ScenarioError:
		return Scenario(s), nil
	case "":
		return ScenarioText, nil
	}
	return "", fmt.Errorf("unknown scenario %q (want text|approval|error)", s)
}
