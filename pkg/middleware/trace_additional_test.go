package middleware

import (
	"context"
	"testing"
)

func TestTraceMiddlewareStages(t *testing.T) {
	dir := t.TempDir()
	mw := NewTraceMiddleware(dir)
	state := &State{Values: map[string]any{}, Iteration: 1}
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sess")

	if mw.Name() == "" {
		t.Fatalf("expected name")
	}
	if err := mw.BeforeAgent(ctx, state); err != nil {
		t.Fatalf("before agent: %v", err)
	}
	if err := mw.BeforeModel(ctx, state); err != nil {
		t.Fatalf("before model: %v", err)
	}
	if err := mw.AfterModel(ctx, state); err != nil {
		t.Fatalf("after model: %v", err)
	}
	if err := mw.BeforeTool(ctx, state); err != nil {
		t.Fatalf("before tool: %v", err)
	}
	if err := mw.AfterTool(ctx, state); err != nil {
		t.Fatalf("after tool: %v", err)
	}
	if err := mw.AfterAgent(ctx, state); err != nil {
		t.Fatalf("after agent: %v", err)
	}
}
