package middleware

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTraceMiddlewareRecords(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	tm := NewTraceMiddleware(dir)
	defer tm.Close()
	ctx := context.WithValue(context.Background(), SessionIDContextKey, "sess")
	st := &State{Iteration: 1, Values: map[string]any{}}
	if err := tm.BeforeAgent(ctx, st); err != nil {
		t.Fatalf("before agent failed: %v", err)
	}
	if err := tm.AfterAgent(ctx, st); err != nil {
		t.Fatalf("after agent failed: %v", err)
	}

	if len(tm.sessions) == 0 {
		t.Fatalf("expected session recorded")
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir failed: %v", err)
	}
	found := false
	for _, entry := range entries {
		if filepath.Ext(entry.Name()) == ".jsonl" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected jsonl trace output")
	}
}
