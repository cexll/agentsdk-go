package agent

import (
	"context"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/session"
)

func TestFindPendingToolCalls(t *testing.T) {
	now := time.Now()
	history := []session.Message{
		{
			Role: "assistant",
			ToolCalls: []session.ToolCall{{
				ID:        "tc-complete",
				Name:      "complete_tool",
				Arguments: map[string]any{"msg": "ok"},
				Timestamp: now,
			}},
		},
		{
			Role: "tool",
			ToolCalls: []session.ToolCall{{
				ID:        "tc-complete",
				Name:      "complete_tool",
				Output:    "done",
				Timestamp: now.Add(time.Second),
			}},
		},
		{
			Role: "assistant",
			ToolCalls: []session.ToolCall{{
				ID:        "tc-pending",
				Name:      "slow_tool",
				Arguments: map[string]any{"n": 1},
				Timestamp: now.Add(2 * time.Second),
			}},
		},
	}

	pending := findPendingToolCalls(history)
	if len(pending) != 1 {
		t.Fatalf("expected one pending call, got %d", len(pending))
	}
	if pending[0].ID != "tc-pending" {
		t.Fatalf("expected tc-pending, got %s", pending[0].ID)
	}
}

func TestRecoveryManagerAutoSeal(t *testing.T) {
	sess, err := session.NewMemorySession("auto-seal")
	if err != nil {
		t.Fatalf("NewMemorySession: %v", err)
	}
	pending := session.Message{
		Role: "assistant",
		ToolCalls: []session.ToolCall{{
			ID:        "pending-tool",
			Name:      "slow_tool",
			Arguments: map[string]any{"payload": "x"},
			Timestamp: time.Now(),
		}},
	}
	if err := sess.Append(pending); err != nil {
		t.Fatalf("append pending: %v", err)
	}

	mgr := NewRecoveryManager(sess)
	if err := mgr.AutoSeal(context.Background()); err != nil {
		t.Fatalf("autoseal: %v", err)
	}

	messages, err := sess.List(session.Filter{})
	if err != nil {
		t.Fatalf("list session: %v", err)
	}
	if len(messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(messages))
	}
	last := messages[1]
	if last.Role != "tool" {
		t.Fatalf("expected tool role, got %s", last.Role)
	}
	if len(last.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(last.ToolCalls))
	}
	tc := last.ToolCalls[0]
	if tc.Error == "" {
		t.Fatal("expected error message on sealed tool")
	}
	if auto, ok := tc.Metadata["auto_sealed"].(bool); !ok || !auto {
		t.Fatalf("auto sealed flag missing: %#v", tc.Metadata)
	}
}
