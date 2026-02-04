package middleware

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

type requestPayload struct {
	Messages []string
	Tools    []string
	System   string
	MaxTokens int
	Model    string
	Temperature float64
}

type toolResultStruct struct {
	Name     string
	Output   string
	Metadata map[string]any
}

func TestTracePayloadBuilders(t *testing.T) {
	req := model.Request{
		Messages: []model.Message{{Role: "user", Content: "hi"}},
		Model:    "m",
		MaxTokens: 5,
	}
	if payload := modelRequestPayload(req); payload == nil || payload["model"] == nil {
		t.Fatalf("expected request payload")
	}

	rawReq := json.RawMessage(`{"messages":[{"role":"user","content":"hi"}]}`)
	if payload := modelRequestPayload(rawReq); payload == nil || payload["messages"] == nil {
		t.Fatalf("expected raw request payload")
	}

	if payload := modelRequestPayload([]byte("bad-json")); payload == nil || payload["raw"] == nil {
		t.Fatalf("expected raw fallback payload")
	}

	custom := requestPayload{Messages: []string{"a"}, System: "sys", Model: "m"}
	if payload := modelRequestPayload(custom); payload == nil || payload["system"] == nil {
		t.Fatalf("expected struct request payload")
	}

	resp := model.Response{Message: model.Message{Content: "ok"}, StopReason: "done"}
	if payload := modelResponsePayload(resp); payload == nil || payload["content"] == nil {
		t.Fatalf("expected response payload")
	}

	rawResp := json.RawMessage(`{"content":"ok"}`)
	if payload := modelResponsePayload(rawResp); payload == nil || payload["content"] == nil {
		t.Fatalf("expected raw response payload")
	}

	if payload := modelResponsePayload([]byte(`{"content":"ok"}`)); payload == nil || payload["content"] == nil {
		t.Fatalf("expected byte response payload")
	}

	call := tool.Call{Name: "Bash", Params: map[string]any{"command": "ls"}}
	if payload := toolCallPayload(call); payload == nil || payload["name"] != "Bash" {
		t.Fatalf("expected tool call payload")
	}
	if payload := toolCallPayload(map[string]any{"name": "x"}); payload == nil {
		t.Fatalf("expected map tool call payload")
	}
	if payload := toolCallPayload(json.RawMessage(`{"name":"x"}`)); payload == nil {
		t.Fatalf("expected raw tool call payload")
	}

	callRes := tool.CallResult{Call: call, Result: &tool.ToolResult{Output: "ok"}, Err: errors.New("boom")}
	if payload := toolResultPayload(callRes); payload == nil || payload["is_error"] != true {
		t.Fatalf("expected tool result payload")
	}

	tr := &tool.ToolResult{Output: "x", Error: errors.New("bad")}
	if payload := toolResultPayload(tr); payload == nil || payload["error"] == nil {
		t.Fatalf("expected tool result payload for ToolResult")
	}

	if payload := toolResultPayload(toolResultStruct{Name: "t", Output: "x"}); payload == nil || payload["content"] == nil {
		t.Fatalf("expected struct tool result payload")
	}
}

func TestStructToMapAndDecodeFallbacks(t *testing.T) {
	if got := structToMap(1, map[string]string{"A": "a"}); got != nil {
		t.Fatalf("expected nil for non-struct")
	}
	var ptr *requestPayload
	if got := structToMap(ptr, map[string]string{"System": "system"}); got != nil {
		t.Fatalf("expected nil for nil pointer")
	}

	raw := json.RawMessage("{bad}")
	decoded := decodeJSONMap(raw)
	if decoded == nil || decoded["raw"] == nil {
		t.Fatalf("expected raw fallback")
	}
}
