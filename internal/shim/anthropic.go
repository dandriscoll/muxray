package shim

import (
	"encoding/json"
	"fmt"
	"net/http"
)

type anthropicRequest struct {
	Model     string `json:"model"`
	MaxTokens int    `json:"max_tokens"`
	Stream    bool   `json:"stream"`
}

// anthropicMessages emulates POST /v1/messages of the Anthropic Messages API.
func (s *Server) anthropicMessages(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	var req anthropicRequest
	_ = json.NewDecoder(r.Body).Decode(&req)
	model := req.Model
	if model == "" {
		model = "claude-shim"
	}

	if s.scenario == ScenarioError {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, `{"type":"error","error":{"type":"api_error","message":"muxray shim injected error"}}`)
		return
	}

	if req.Stream {
		s.anthropicStream(w, model)
		return
	}
	s.anthropicNonStream(w, model)
}

func (s *Server) anthropicStream(w http.ResponseWriter, model string) {
	flusher, _ := w.(http.Flusher)
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.WriteHeader(http.StatusOK)

	send := func(event string, data map[string]any) {
		b, _ := json.Marshal(data)
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event, b)
		if flusher != nil {
			flusher.Flush()
		}
	}

	send("message_start", map[string]any{
		"type": "message_start",
		"message": map[string]any{
			"id": "msg_shim_0001", "type": "message", "role": "assistant",
			"model": model, "content": []any{}, "stop_reason": nil, "stop_sequence": nil,
			"usage": map[string]any{"input_tokens": 10, "output_tokens": 1},
		},
	})

	if s.scenario == ScenarioApproval {
		send("content_block_start", map[string]any{
			"type": "content_block_start", "index": 0,
			"content_block": map[string]any{"type": "tool_use", "id": "toolu_shim_1", "name": "Bash", "input": map[string]any{}},
		})
		send("ping", map[string]any{"type": "ping"})
		send("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "input_json_delta", "partial_json": `{"command":"echo muxray-shim-approval"}`},
		})
		send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
		send("message_delta", map[string]any{
			"type":  "message_delta",
			"delta": map[string]any{"stop_reason": "tool_use", "stop_sequence": nil},
			"usage": map[string]any{"output_tokens": 12},
		})
		send("message_stop", map[string]any{"type": "message_stop"})
		return
	}

	send("content_block_start", map[string]any{
		"type": "content_block_start", "index": 0,
		"content_block": map[string]any{"type": "text", "text": ""},
	})
	send("ping", map[string]any{"type": "ping"})
	for _, chunk := range []string{"The task ", "is complete."} {
		send("content_block_delta", map[string]any{
			"type": "content_block_delta", "index": 0,
			"delta": map[string]any{"type": "text_delta", "text": chunk},
		})
	}
	send("content_block_stop", map[string]any{"type": "content_block_stop", "index": 0})
	send("message_delta", map[string]any{
		"type":  "message_delta",
		"delta": map[string]any{"stop_reason": "end_turn", "stop_sequence": nil},
		"usage": map[string]any{"output_tokens": 8},
	})
	send("message_stop", map[string]any{"type": "message_stop"})
}

func (s *Server) anthropicNonStream(w http.ResponseWriter, model string) {
	w.Header().Set("Content-Type", "application/json")
	var content []any
	stop := "end_turn"
	if s.scenario == ScenarioApproval {
		content = []any{map[string]any{
			"type": "tool_use", "id": "toolu_shim_1", "name": "Bash",
			"input": map[string]any{"command": "echo muxray-shim-approval"},
		}}
		stop = "tool_use"
	} else {
		content = []any{map[string]any{"type": "text", "text": "The task is complete."}}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{
		"id": "msg_shim_0001", "type": "message", "role": "assistant", "model": model,
		"content": content, "stop_reason": stop, "stop_sequence": nil,
		"usage": map[string]any{"input_tokens": 10, "output_tokens": 8},
	})
}

// anthropicCountTokens emulates POST /v1/messages/count_tokens.
func anthropicCountTokens(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	fmt.Fprint(w, `{"input_tokens":42}`)
}
