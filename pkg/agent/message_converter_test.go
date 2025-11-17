package agent

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

func TestSessionToModelMessages(t *testing.T) {
	args := map[string]any{"city": "SF"}
	history := []session.Message{
		{Role: "USER", Content: "hi"},
		{
			Role:    "assistant",
			Content: "result",
			ToolCalls: []session.ToolCall{{
				ID:        "tc1",
				Name:      "calc",
				Arguments: map[string]any{"x": 1},
			}},
		},
		{Role: "SYSTEM", Content: "rules"},
		{Role: "", Content: "fallback", ToolCalls: []session.ToolCall{{Name: "noop", Arguments: args}}},
	}
	result := sessionToModelMessages(history)
	if len(result) != len(history) {
		t.Fatalf("messages len: want %d got %d", len(history), len(result))
	}
	wantRoles := []string{"user", "assistant", "system", "user"}
	for i, want := range wantRoles {
		if result[i].Role != want {
			t.Fatalf("message %d role: want %s got %s", i, want, result[i].Role)
		}
	}
	history[1].ToolCalls[0].Arguments["x"] = 9
	if val := result[1].ToolCalls[0].Arguments["x"]; val.(int) != 1 {
		t.Fatalf("arguments not cloned: %+v", result[1].ToolCalls[0].Arguments)
	}
	history[3].ToolCalls[0].Arguments["city"] = "NY"
	if val := result[3].ToolCalls[0].Arguments["city"]; val.(string) != "SF" {
		t.Fatalf("arguments mutated: %+v", result[3].ToolCalls[0].Arguments)
	}
}

func TestModelToSessionMessage(t *testing.T) {
	args := map[string]any{"city": "SF"}
	msg := model.Message{
		Role:    "MODEL",
		Content: "done",
		ToolCalls: []model.ToolCall{{
			ID:        "call-1",
			Name:      "lookup",
			Arguments: args,
		}},
	}
	converted := modelToSessionMessage(msg)
	if converted.Role != "assistant" {
		t.Fatalf("role mismatch: %s", converted.Role)
	}
	if converted.Content != msg.Content {
		t.Fatalf("content mismatch: %s", converted.Content)
	}
	if converted.Timestamp.IsZero() {
		t.Fatal("timestamp not set")
	}
	args["city"] = "NY"
	if val := converted.ToolCalls[0].Arguments["city"]; val.(string) != "SF" {
		t.Fatalf("tool call arguments not cloned: %+v", converted.ToolCalls[0].Arguments)
	}
}

func TestToolCallToSessionMessage(t *testing.T) {
	params := map[string]any{"city": "SF"}
	metadata := map[string]any{"source": "unit"}
	output := map[string]any{"temp_c": 21}
	call := ToolCall{
		Name:     " weather ",
		Params:   params,
		Output:   output,
		Duration: 1500 * time.Millisecond,
		Metadata: metadata,
	}
	msg := toolCallToSessionMessage(call)
	if msg.Role != "tool" {
		t.Fatalf("role: want tool got %s", msg.Role)
	}
	if msg.Timestamp.IsZero() {
		t.Fatal("timestamp not set")
	}
	if len(msg.ToolCalls) != 1 {
		t.Fatalf("tool calls len: %d", len(msg.ToolCalls))
	}
	tc := msg.ToolCalls[0]
	if tc.Name != "weather" {
		t.Fatalf("tool name: %s", tc.Name)
	}
	if tc.Metadata["source"].(string) != "unit" {
		t.Fatalf("metadata not preserved: %+v", tc.Metadata)
	}
	if tc.Metadata["duration_ms"].(int64) != 1500 {
		t.Fatalf("duration metadata missing: %+v", tc.Metadata)
	}
	params["city"] = "NY"
	metadata["source"] = "mutated"
	if tc.Arguments["city"].(string) != "SF" {
		t.Fatalf("arguments mutated: %+v", tc.Arguments)
	}
	if tc.Metadata["source"].(string) != "unit" {
		t.Fatalf("metadata mutated: %+v", tc.Metadata)
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(msg.Content), &payload); err != nil {
		t.Fatalf("content json: %v", err)
	}
	if payload["tool"].(string) != "weather" {
		t.Fatalf("payload tool mismatch: %+v", payload)
	}
	if payload["params"].(map[string]any)["city"].(string) != "SF" {
		t.Fatalf("payload params mismatch: %+v", payload)
	}
	if payload["error"].(string) != "" {
		t.Fatalf("payload error mismatch: %+v", payload)
	}
}

func TestToolCallToSessionMessageJSONFallback(t *testing.T) {
	ch := make(chan int)
	call := ToolCall{
		Name:   "",
		Params: map[string]any{"bad": ch},
		Output: ch,
		Error:  "boom",
	}
	msg := toolCallToSessionMessage(call)
	if msg.ToolCalls[0].Name != "unknown_tool" {
		t.Fatalf("fallback tool name mismatch: %s", msg.ToolCalls[0].Name)
	}
	if !strings.HasPrefix(msg.Content, "tool=unknown_tool") {
		t.Fatalf("unexpected fallback content: %s", msg.Content)
	}
}
