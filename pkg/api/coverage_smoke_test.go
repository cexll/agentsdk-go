package api

import (
	"context"
	"testing"
	"time"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func TestRuntimeAccessorsAndHooksSmoke(t *testing.T) {
	t.Run("runtime accessors", func(t *testing.T) {
		var rt *Runtime
		if got := rt.GetSessionStats("session"); got != nil {
			t.Fatalf("expected nil session stats, got %+v", got)
		}
		if got := rt.GetTotalStats(); got != nil {
			t.Fatalf("expected nil total stats, got %+v", got)
		}

		rt = &Runtime{}
		if got := rt.Config(); got != nil {
			t.Fatalf("expected nil config, got %+v", got)
		}

		tracker := newTokenTracker(true, nil)
		tracker.Record(TokenStats{
			InputTokens:  1,
			OutputTokens: 2,
			TotalTokens:  3,
			Model:        "test",
			SessionID:    "session",
			RequestID:    "request",
			Timestamp:    time.Now(),
		})

		rt.tokens = tracker
		if got := rt.GetSessionStats("session"); got == nil {
			t.Fatal("expected session stats, got nil")
		}
		if got := rt.GetTotalStats(); got == nil {
			t.Fatal("expected total stats, got nil")
		}
	})

	t.Run("hook adapter publishes", func(t *testing.T) {
		rec := defaultHookRecorder()
		adapter := &runtimeHookAdapter{
			executor: corehooks.NewExecutor(),
			recorder: rec,
		}

		ctx := context.Background()
		if err := adapter.SessionStart(ctx, coreevents.SessionPayload{SessionID: "session"}); err != nil {
			t.Fatalf("SessionStart: %v", err)
		}
		if err := adapter.SessionEnd(ctx, coreevents.SessionPayload{SessionID: "session"}); err != nil {
			t.Fatalf("SessionEnd: %v", err)
		}
		if err := adapter.SubagentStart(ctx, coreevents.SubagentStartPayload{Name: "subagent"}); err != nil {
			t.Fatalf("SubagentStart: %v", err)
		}
		if err := adapter.SubagentStop(ctx, coreevents.SubagentStopPayload{Name: "subagent"}); err != nil {
			t.Fatalf("SubagentStop: %v", err)
		}
		if err := adapter.ModelSelected(ctx, coreevents.ModelSelectedPayload{ToolName: "tool"}); err != nil {
			t.Fatalf("ModelSelected: %v", err)
		}

		if events := rec.Drain(); len(events) == 0 {
			t.Fatal("expected hook adapter to record events")
		}

		var nilAdapter *runtimeHookAdapter
		if err := nilAdapter.SessionStart(ctx, coreevents.SessionPayload{}); err != nil {
			t.Fatalf("nil SessionStart: %v", err)
		}
		if err := nilAdapter.SessionEnd(ctx, coreevents.SessionPayload{}); err != nil {
			t.Fatalf("nil SessionEnd: %v", err)
		}
		if err := nilAdapter.SubagentStart(ctx, coreevents.SubagentStartPayload{}); err != nil {
			t.Fatalf("nil SubagentStart: %v", err)
		}
		if err := nilAdapter.SubagentStop(ctx, coreevents.SubagentStopPayload{}); err != nil {
			t.Fatalf("nil SubagentStop: %v", err)
		}
		if err := nilAdapter.ModelSelected(ctx, coreevents.ModelSelectedPayload{}); err != nil {
			t.Fatalf("nil ModelSelected: %v", err)
		}
	})

	t.Run("noop tracer end span", func(t *testing.T) {
		tracer, err := NewTracer(OTELConfig{})
		if err != nil {
			t.Fatalf("NewTracer: %v", err)
		}
		span := tracer.StartAgentSpan("session", "request", 0)
		tracer.EndSpan(span, map[string]any{"key": "value"}, nil)
	})
}
