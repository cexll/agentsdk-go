package message

import (
	"strings"
	"testing"
)

type fixedCounter struct{ costs []int }

func (f fixedCounter) Count(msg Message) int {
	if len(f.costs) == 0 {
		return 1
	}
	cost := f.costs[0]
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

func TestNaiveCounterEdgeCases(t *testing.T) {
	tests := []struct {
		name string
		msg  Message
		want int
	}{
		{
			name: "role and content contribute",
			msg:  Message{Role: "0123456789", Content: "abcdefgh"},
			want: 3, // 8/4 + 10/10
		},
		{
			name: "string tool call arguments",
			msg: Message{
				Role:    "r",
				Content: "abcd",
				ToolCalls: []ToolCall{{
					Name:      "calc",
					Arguments: map[string]any{"payload": "12345678"},
				}},
			},
			want: 14, // 4/4 + len("calc") + len("payload") + 8/4
		},
		{
			name: "non string argument fallback",
			msg: Message{
				ToolCalls: []ToolCall{{
					Name:      "x",
					Arguments: map[string]any{"n": 123},
				}},
			},
			want: 3, // len("x") + len("n") + default branch
		},
		{
			name: "enforces minimum token",
			msg:  Message{},
			want: 1,
		},
		{
			name: "multibyte counts bytes",
			msg:  Message{Role: "user", Content: strings.Repeat("汉", 8)},
			want: 6, // len([]byte("汉")) == 3, so 8*3/4
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := (NaiveCounter{}).Count(tt.msg)
			if got != tt.want {
				t.Fatalf("expected %d tokens, got %d", tt.want, got)
			}
		})
	}
}

func TestTrimmerTableScenarios(t *testing.T) {
	tests := []struct {
		name    string
		limit   int
		history []Message
		counter TokenCounter
		want    []string
	}{
		{
			name:    "empty history",
			limit:   5,
			history: nil,
			counter: nil,
			want:    []string{},
		},
		{
			name:    "single message",
			limit:   5,
			history: []Message{{Content: "solo"}},
			counter: tokenCounterFunc(func(Message) int { return 1 }),
			want:    []string{"solo"},
		},
		{
			name:    "all messages exceed limit",
			limit:   2,
			history: []Message{{Content: "old"}, {Content: "new"}},
			counter: tokenCounterFunc(func(Message) int { return 3 }),
			want:    []string{},
		},
		{
			name:    "zero token counter keeps all",
			limit:   1,
			history: []Message{{Content: "first"}, {Content: "second"}},
			counter: tokenCounterFunc(func(Message) int { return 0 }),
			want:    []string{"first", "second"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			trimmer := Trimmer{MaxTokens: tt.limit, Counter: tt.counter}
			got := trimmer.Trim(tt.history)
			if len(got) != len(tt.want) {
				t.Fatalf("expected %d messages, got %d", len(tt.want), len(got))
			}
			for i, content := range tt.want {
				if got[i].Content != content {
					t.Fatalf("message %d content mismatch, want %q got %q", i, content, got[i].Content)
				}
			}
		})
	}
}

type tokenCounterFunc func(Message) int

func (f tokenCounterFunc) Count(msg Message) int { return f(msg) }
