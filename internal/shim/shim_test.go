package shim_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/dandriscoll/muxray/internal/shim"
)

// post hits a shim handler in-process via httptest (no port, no network) and
// returns the status code and body.
func post(t *testing.T, srv *shim.Server, path, body string) (int, string) {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Post(ts.URL+path, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func get(t *testing.T, srv *shim.Server, path string) (int, string) {
	t.Helper()
	ts := httptest.NewServer(srv.Handler())
	defer ts.Close()
	resp, err := http.Get(ts.URL + path)
	if err != nil {
		t.Fatalf("GET %s: %v", path, err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b)
}

func mustContain(t *testing.T, body string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(body, s) {
			t.Errorf("response missing %q in:\n%s", s, body)
		}
	}
}

// ---- Anthropic ----

func TestAnthropic_TextStream(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	code, body := post(t, s, "/v1/messages", `{"model":"x","max_tokens":16,"stream":true}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body,
		"event: message_start", `"type":"text_delta"`, "The task ", "is complete.",
		"event: content_block_stop", `"stop_reason":"end_turn"`, "event: message_stop")
}

func TestAnthropic_TextNonStream(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	code, body := post(t, s, "/v1/messages", `{"model":"x","max_tokens":16,"stream":false}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, `"type":"text"`, "The task is complete.", `"stop_reason":"end_turn"`)
}

func TestAnthropic_ApprovalStream(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioApproval)
	code, body := post(t, s, "/v1/messages", `{"stream":true}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, `"type":"tool_use"`, `"name":"Bash"`, `"stop_reason":"tool_use"`, "event: message_stop")
}

func TestAnthropic_Error(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioError)
	code, body := post(t, s, "/v1/messages", `{"stream":true}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
	mustContain(t, body, `"type":"error"`, "muxray shim injected error")
}

func TestAnthropic_CountTokens(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	code, body := post(t, s, "/v1/messages/count_tokens", `{}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, `"input_tokens"`)
}

// ---- OpenAI ----

func TestOpenAI_ChatStream(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioText)
	code, body := post(t, s, "/v1/chat/completions", `{"model":"x","stream":true}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, "chat.completion.chunk", `"content":"The task "`, `"finish_reason":"stop"`, "data: [DONE]")
}

func TestOpenAI_ChatNonStream(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioText)
	code, body := post(t, s, "/v1/chat/completions", `{"model":"x"}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, "chat.completion", "The task is complete.", `"finish_reason":"stop"`)
}

func TestOpenAI_ChatApprovalStream(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioApproval)
	code, body := post(t, s, "/v1/chat/completions", `{"stream":true}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, `"tool_calls"`, `"name":"shell"`, `"finish_reason":"tool_calls"`, "data: [DONE]")
}

func TestOpenAI_Responses(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioText)
	code, body := post(t, s, "/v1/responses", `{"stream":true}`)
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, "response.completed", "The task is complete.", "data: [DONE]")
}

func TestOpenAI_Error(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioError)
	code, _ := post(t, s, "/v1/chat/completions", `{"stream":true}`)
	if code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500", code)
	}
}

func TestOpenAI_Models(t *testing.T) {
	s := shim.New(shim.ProviderOpenAI, shim.ScenarioText)
	code, body := get(t, s, "/v1/models")
	if code != 200 {
		t.Fatalf("status %d", code)
	}
	mustContain(t, body, `"object":"list"`, "gpt-shim")
}

// ---- Misc ----

func TestUnhandledEndpoint404(t *testing.T) {
	s := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	code, _ := get(t, s, "/v1/whatever")
	if code != http.StatusNotFound {
		t.Errorf("status %d, want 404", code)
	}
}

func TestEnv(t *testing.T) {
	a := shim.New(shim.ProviderAnthropic, shim.ScenarioText)
	if err := a.Start(0); err != nil {
		t.Fatal(err)
	}
	defer a.Close()
	if a.Env()["ANTHROPIC_BASE_URL"] != a.BaseURL() {
		t.Errorf("anthropic base url env mismatch: %v vs %v", a.Env()["ANTHROPIC_BASE_URL"], a.BaseURL())
	}
	if a.Env()["ANTHROPIC_API_KEY"] == "" {
		t.Error("expected a placeholder API key")
	}

	o := shim.New(shim.ProviderOpenAI, shim.ScenarioText)
	if err := o.Start(0); err != nil {
		t.Fatal(err)
	}
	defer o.Close()
	if o.Env()["OPENAI_BASE_URL"] != o.BaseURL()+"/v1" {
		t.Errorf("openai base url env should end in /v1, got %q", o.Env()["OPENAI_BASE_URL"])
	}
}

func TestParse(t *testing.T) {
	if p, _ := shim.ParseProvider("claude"); p != shim.ProviderAnthropic {
		t.Error("claude should alias anthropic")
	}
	if p, _ := shim.ParseProvider("codex"); p != shim.ProviderOpenAI {
		t.Error("codex should alias openai")
	}
	if _, err := shim.ParseProvider("nope"); err == nil {
		t.Error("expected error for unknown provider")
	}
	if sc, _ := shim.ParseScenario(""); sc != shim.ScenarioText {
		t.Error("empty scenario should default to text")
	}
	if _, err := shim.ParseScenario("bogus"); err == nil {
		t.Error("expected error for unknown scenario")
	}
}
