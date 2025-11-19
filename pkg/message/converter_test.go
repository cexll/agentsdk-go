package message

import "testing"

func TestCloneMessageDeepCopiesToolCallArguments(t *testing.T) {
	msg := Message{Role: "assistant", Content: "call", ToolCalls: []ToolCall{{Name: "sum", Arguments: map[string]any{"a": 1}}}}
	cloned := CloneMessage(msg)

	cloned.ToolCalls[0].Arguments["a"] = 42
	if v, ok := msg.ToolCalls[0].Arguments["a"].(int); !ok || v != 1 {
		t.Fatalf("original mutated: %+v", msg.ToolCalls[0].Arguments)
	}
}

func TestCloneMessagesEmpty(t *testing.T) {
	msgs := CloneMessages(nil)
	if len(msgs) != 0 {
		t.Fatalf("expected empty slice, got %d", len(msgs))
	}
}
