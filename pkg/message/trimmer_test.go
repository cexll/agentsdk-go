package message

import "testing"

type fixedCounter struct{ costs []int }

func (f fixedCounter) Count(msg Message) int {
	if len(f.costs) == 0 {
		return 1
	}
	cost := f.costs[0]
	f.costs = f.costs[1:]
	return cost
}

func TestTrimmerKeepsNewestWithinLimit(t *testing.T) {
	history := []Message{
		{Role: "system", Content: "boot"},
		{Role: "user", Content: "hi"},
		{Role: "assistant", Content: "hello"},
		{Role: "user", Content: "next"},
	}

	counter := fixedCounter{costs: []int{1, 1, 1, 1}}
	trimmer := NewTrimmer(3, counter)
	trimmed := trimmer.Trim(history)

	if len(trimmed) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(trimmed))
	}
	if trimmed[0].Content != "hi" || trimmed[2].Content != "next" {
		t.Fatalf("unexpected order: %+v", trimmed)
	}
}

func TestTrimmerZeroLimitReturnsEmpty(t *testing.T) {
	trimmer := NewTrimmer(0, nil)
	trimmed := trimmer.Trim([]Message{{Role: "user"}})
	if len(trimmed) != 0 {
		t.Fatalf("expected empty result, got %d", len(trimmed))
	}
}

func TestTrimmerUsesNaiveCounterWhenNil(t *testing.T) {
	trimmer := NewTrimmer(10, nil)
	history := []Message{{Role: "user", Content: "abcd"}}
	trimmed := trimmer.Trim(history)
	if len(trimmed) != 1 {
		t.Fatalf("expected message kept")
	}
}

func TestNaiveCounterCountsNonZero(t *testing.T) {
	if (NaiveCounter{}).Count(Message{}) == 0 {
		t.Fatalf("naive counter should return at least 1")
	}
}

func testCounter(costs []int) TokenCounter {
	return tokenCounterFunc(func(msg Message) int {
		cost := costs[len(costs)-1]
		costs = costs[:len(costs)-1]
		return cost
	})
}

type tokenCounterFunc func(Message) int

func (f tokenCounterFunc) Count(msg Message) int { return f(msg) }
