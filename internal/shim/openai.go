package shim

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// created is a fixed, deterministic timestamp used in OpenAI responses (real
// time is intentionally avoided so output is reproducible).
const created = 1700000000

type openaiRequest struct {
	Model  string `json:"model"`
	Stream bool   `json:"stream"`
}

// openaiChat emulates POST /v1/chat/completions of the OpenAI API (the universal
// endpoint most OpenAI-compatible harnesses, including Codex, can target).
func (s *Server) openaiChat(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openaiRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	model := req.Model
	if model == "" {
		model = "gpt-shim"
	}

	if s.scenario == ScenarioError {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"muxray shim injected error","type":"server_error"}}`)
		return
	}

	if req.Stream {
		s.openaiChatStream(w, model)
		return
	}
	s.openaiChatNonStream(w, model)
}

func (s *Server) openaiChatStream(w http.ResponseWriter, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	send := func(choice map[string]any) {
		chunk := map[string]any{
			"id": "chatcmpl-shim", "object": "chat.completion.chunk",
			"created": created, "model": model, "choices": []any{choice},
		}
		b, _ := json.Marshal(chunk)
		fmt.Fprintf(w, "data: %s\n\n", b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	send(map[string]any{"index": 0, "delta": map[string]any{"role": "assistant"}, "finish_reason": nil})

	if s.scenario == ScenarioApproval {
		send(map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "id": "call_shim_1", "type": "function",
			"function": map[string]any{"name": "shell", "arguments": ""},
		}}}, "finish_reason": nil})
		send(map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{
			"index": 0, "function": map[string]any{"arguments": `{"command":"echo muxray-shim-approval"}`},
		}}}, "finish_reason": nil})
		send(map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "tool_calls"})
	} else {
		for _, chunk := range []string{"The task ", "is complete."} {
			send(map[string]any{"index": 0, "delta": map[string]any{"content": chunk}, "finish_reason": nil})
		}
		send(map[string]any{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"})
	}
	fmt.Fprint(w, "data: [DONE]\n\n")
	if flusher != nil {
		flusher.Flush()
	}
}

func (s *Server) openaiChatNonStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	var message map[string]any
	finish := "stop"
	if s.scenario == ScenarioApproval {
		message = map[string]any{"role": "assistant", "content": nil, "tool_calls": []any{map[string]any{
			"id": "call_shim_1", "type": "function",
			"function": map[string]any{"name": "shell", "arguments": `{"command":"echo muxray-shim-approval"}`},
		}}}
		finish = "tool_calls"
	} else {
		message = map[string]any{"role": "assistant", "content": "The task is complete."}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "chatcmpl-shim", "object": "chat.completion", "created": created, "model": model,
		"choices": []any{map[string]any{"index": 0, "message": message, "finish_reason": finish}},
		"usage":   map[string]any{"prompt_tokens": 10, "completion_tokens": 8, "total_tokens": 18},
	})
}

// openaiResponses emulates a minimal POST /v1/responses (the newer Responses
// API). It returns a single completed text response; streaming requests get the
// streamed event form terminated by response.completed + [DONE].
func (s *Server) openaiResponses(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req openaiRequest
	_ = json.NewDecoder(r.Body).Decode(&req)

	if s.scenario == ScenarioError {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"error":{"message":"muxray shim injected error","type":"server_error"}}`)
		return
	}

	text := "The task is complete."
	if req.Stream {
		flusher, _ := w.(http.Flusher)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		emit := func(event string, data map[string]any) {
			b, _ := json.Marshal(data)
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
			if flusher != nil {
				flusher.Flush()
			}
		}
		emit("response.output_text.delta", map[string]any{"type": "response.output_text.delta", "delta": text})
		emit("response.completed", map[string]any{"type": "response.completed", "response": responsesBody(text)})
		fmt.Fprint(w, "data: [DONE]\n\n")
		if flusher != nil {
			flusher.Flush()
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(responsesBody(text))
}

func responsesBody(text string) map[string]any {
	return map[string]any{
		"id": "resp_shim", "object": "response", "created_at": created, "status": "completed",
		"output": []any{map[string]any{
			"type": "message", "role": "assistant",
			"content": []any{map[string]any{"type": "output_text", "text": text}},
		}},
	}
}

// openaiModels emulates GET /v1/models, which some clients probe at startup.
func openaiModels(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"object":"list","data":[{"id":"gpt-shim","object":"model","created":1700000000,"owned_by":"muxray-shim"}]}`)
}
