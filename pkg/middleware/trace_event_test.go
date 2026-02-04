package middleware

import (
	"errors"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestTraceEventCapture(t *testing.T) {
	t.Parallel()

	st := &State{
		ModelInput:  model.Request{Model: "test"},
		ModelOutput: model.Response{Message: model.Message{Content: "ok"}},
		ToolCall:    tool.Call{Name: "Demo"},
		ToolResult:  &tool.ToolResult{Success: true, Output: "ok"},
		Values: map[string]any{
			"model.stop_reason": "end",
			"trace.error":       errors.New("boom"),
		},
	}

	if captureModelRequest(StageBeforeModel, st) == nil {
		t.Fatalf("expected model request payload")
	}
	if captureModelResponse(StageAfterModel, st) == nil {
		t.Fatalf("expected model response payload")
	}
	if captureToolCall(StageBeforeTool, st) == nil {
		t.Fatalf("expected tool call payload")
	}
	toolRes := captureToolResult(StageAfterTool, st, map[string]any{"id": "1", "name": "Demo"})
	if toolRes["id"] != "1" {
		t.Fatalf("unexpected tool result id")
	}
	if err := captureTraceError(StageAfterTool, st, map[string]any{"is_error": true}); err == "" {
		t.Fatalf("expected trace error")
	}
}
