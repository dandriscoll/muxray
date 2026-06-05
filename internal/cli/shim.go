package cli

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"sort"
	"syscall"

	"github.com/dandriscoll/muxray/internal/schema"
	"github.com/dandriscoll/muxray/internal/shim"
	"github.com/dandriscoll/muxray/internal/version"
)

type shimResponse struct {
	schema.Envelope
	Provider string            `json:"provider"`
	Scenario string            `json:"scenario"`
	BaseURL  string            `json:"base_url"`
	Env      map[string]string `json:"env"`
}

// cmdShim launches a local, credential-free fake LLM backend that a real Claude
// or Codex harness can be pointed at. It prints the env vars to export, then
// serves until interrupted.
func cmdShim(args []string) int {
	fs := flag.NewFlagSet("shim", flag.ContinueOnError)
	fs.SetOutput(stderr)
	provider := fs.String("provider", "anthropic", "provider to emulate: anthropic|openai")
	scenario := fs.String("scenario", "text", "scenario: text|approval|error")
	port := fs.Int("port", 0, "TCP port (0 = OS-chosen); binds loopback only")
	jsonOut := fs.Bool("json", true, "emit JSON (default)")
	text := fs.Bool("text", false, "emit shell 'export' lines instead of JSON")
	once := fs.Bool("once", false, "start, print connection info, then exit (for scripting/tests)")
	if err := fs.Parse(args); err != nil {
		return flagError("shim", true, err)
	}
	wantJSON := !*text && *jsonOut

	p, err := shim.ParseProvider(*provider)
	if err != nil {
		return emitError(wantJSON, "shim", &cmdError{class: "usage", message: err.Error(),
			hint: "use --provider anthropic or --provider openai", exit: ExitUsage})
	}
	sc, err := shim.ParseScenario(*scenario)
	if err != nil {
		return emitError(wantJSON, "shim", &cmdError{class: "usage", message: err.Error(),
			hint: "use --scenario text, approval, or error", exit: ExitUsage})
	}

	srv := shim.New(p, sc)
	if err := srv.Start(*port); err != nil {
		return emitError(wantJSON, "shim", &cmdError{class: "shim_listen", message: err.Error(),
			hint: "choose a free --port, or 0 for an OS-chosen port", exit: ExitInternal})
	}
	defer srv.Close()

	resp := shimResponse{
		Envelope: schema.NewEnvelope("shim", version.Version),
		Provider: string(p), Scenario: string(sc),
		BaseURL: srv.BaseURL(), Env: srv.Env(),
	}
	if wantJSON {
		b, _ := jsonMarshalIndent(resp)
		fmt.Fprintln(stdout, string(b))
	} else {
		keys := make([]string, 0, len(resp.Env))
		for k := range resp.Env {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			fmt.Fprintf(stdout, "export %s=%s\n", k, resp.Env[k])
		}
		fmt.Fprintf(stderr, "muxray shim: %s (scenario=%s) listening on %s — Ctrl-C to stop\n", p, sc, srv.BaseURL())
	}

	if *once {
		return ExitOK
	}

	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	<-sig
	return ExitOK
}
