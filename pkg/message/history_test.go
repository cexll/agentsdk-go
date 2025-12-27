package message

import (
	"sync"
	"testing"
)

func TestHistoryAppendAndSnapshotIsolation(t *testing.T) {
	h := NewHistory()
	msg := Message{Role: "user", Content: "hi", ToolCalls: []ToolCall{{ID: "1", Name: "ping", Arguments: map[string]any{"x": 1}}}}
	h.Append(msg)

	// Mutate original; stored copy must remain stable.
	msg.ToolCalls[0].Arguments["x"] = 9
	snapshot := h.All()
	if len(snapshot) != 1 {
		t.Fatalf("expected 1 message, got %d", len(snapshot))
	}
	got, ok := snapshot[0].ToolCalls[0].Arguments["x"].(int)
	if !ok {
		t.Fatalf("expected int argument, got %T", snapshot[0].ToolCalls[0].Arguments["x"])
	}
	if got != 1 {
		t.Fatalf("arguments mutated: %v", got)
	}
}

func TestHistoryReplaceLastAndReset(t *testing.T) {
	h := NewHistory()
	msgs := []Message{{Role: "system"}, {Role: "assistant", Content: "done"}}
	h.Replace(msgs)

	last, ok := h.Last()
	if !ok || last.Role != "assistant" {
		t.Fatalf("last message mismatch: %+v", last)
	}
	if h.Len() != 2 {
		t.Fatalf("len = %d", h.Len())
	}

	h.Reset()
	if h.Len() != 0 {
		t.Fatalf("reset failed, len=%d", h.Len())
	}
}

func TestHistoryConcurrentReaders(t *testing.T) {
	h := NewHistory()
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			h.Append(Message{Role: "user", Content: "msg"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = h.All()
			_, _ = h.Last()
		}
	}()
	wg.Wait()
}

type countingCounter struct {
	calls int
	cost  int
}

func (c *countingCounter) Count(Message) int {
	c.calls++
	return c.cost
}

func TestHistoryTokenCountTracksAppendReplaceAndReset(t *testing.T) {
	h := NewHistory()
	counter := &countingCounter{cost: 2}
	h.counter = counter

	h.Append(Message{Role: "user", Content: "a"})
	if counter.calls != 1 {
		t.Fatalf("expected 1 counter call after append, got %d", counter.calls)
	}
	if got := h.TokenCount(); got != 2 {
		t.Fatalf("TokenCount=%d, want %d", got, 2)
	}

	callsBefore := counter.calls
	for i := 0; i < 10; i++ {
		_ = h.TokenCount()
	}
	if counter.calls != callsBefore {
		t.Fatalf("TokenCount should be O(1): counter calls changed from %d to %d", callsBefore, counter.calls)
	}

	h.Replace([]Message{{Role: "user"}, {Role: "assistant"}})
	if counter.calls != callsBefore+2 {
		t.Fatalf("expected Replace to call counter %d times, got %d", 2, counter.calls-callsBefore)
	}
	if got := h.TokenCount(); got != 4 {
		t.Fatalf("TokenCount=%d, want %d", got, 4)
	}

	h.Reset()
	if got := h.TokenCount(); got != 0 {
		t.Fatalf("TokenCount=%d after Reset, want 0", got)
	}
}
