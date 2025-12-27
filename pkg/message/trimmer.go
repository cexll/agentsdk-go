package message

// TokenCounter returns an estimated token cost for a message.
type TokenCounter interface {
	Count(msg Message) int
}

// NaiveCounter approximates tokens using byte length. It intentionally
// errs on the side of overestimation to avoid exceeding upstream context
// limits.
type NaiveCounter struct{}

// Count implements TokenCounter.
func (NaiveCounter) Count(msg Message) int {
	tokens := len(msg.Content)/4 + len(msg.Role)/10
	for _, call := range msg.ToolCalls {
		tokens += len(call.Name)
		for k, v := range call.Arguments {
			tokens += len(k)
			switch val := v.(type) {
			case string:
				tokens += len(val) / 4
			default:
				_ = val
				tokens += 1
			}
		}
	}
	if tokens == 0 {
		tokens = 1
	}
	return tokens
}

// Trimmer removes the oldest messages when the estimated token budget exceeds
// MaxTokens. The newest messages are preserved.
type Trimmer struct {
	MaxTokens int
	Counter   TokenCounter
}

// NewTrimmer constructs a Trimmer with the provided token limit. When counter
// is nil a NaiveCounter is used.
func NewTrimmer(limit int, counter TokenCounter) *Trimmer {
	if counter == nil {
		counter = NaiveCounter{}
	}
	return &Trimmer{MaxTokens: limit, Counter: counter}
}

// Trim returns a trimmed copy of messages that fits within the token limit. If
// the limit is zero or negative an empty slice is returned.
func (t *Trimmer) Trim(history []Message) []Message {
	if t == nil || t.MaxTokens <= 0 {
		return []Message{}
	}

	counter := t.Counter
	if counter == nil {
		counter = NaiveCounter{}
	}

	tokens := 0
	kept := make([]Message, 0, len(history))
	for i := len(history) - 1; i >= 0; i-- {
		candidate := history[i]
		cost := counter.Count(candidate)
		if tokens+cost > t.MaxTokens {
			break
		}
		kept = append(kept, CloneMessage(candidate))
		tokens += cost
	}

	// Reverse to restore chronological order.
	for i, j := 0, len(kept)-1; i < j; i, j = i+1, j-1 {
		kept[i], kept[j] = kept[j], kept[i]
	}
	return kept
}
