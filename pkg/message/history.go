package message

import "sync"

// History stores conversation messages purely in memory. It is concurrency
// safe and does not perform any persistence.
type History struct {
	mu         sync.RWMutex
	messages   []Message
	tokenCount int
	counter    TokenCounter
}

// NewHistory constructs an empty history.
func NewHistory() *History { return &History{counter: NaiveCounter{}} }

// Append stores a message at the end of the history. The message is cloned to
// avoid external mutation after insertion.
func (h *History) Append(msg Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	cloned := CloneMessage(msg)
	h.messages = append(h.messages, cloned)
	counter := h.counter
	if counter == nil {
		counter = NaiveCounter{}
	}
	h.tokenCount += counter.Count(cloned)
}

// Replace swaps the stored history with the provided slice, cloning entries to
// keep ownership local to the History.
func (h *History) Replace(msgs []Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = CloneMessages(msgs)
	counter := h.counter
	if counter == nil {
		counter = NaiveCounter{}
	}
	total := 0
	for _, msg := range h.messages {
		total += counter.Count(msg)
	}
	h.tokenCount = total
}

// All returns a cloned snapshot of the history in order from oldest to newest.
func (h *History) All() []Message {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return CloneMessages(h.messages)
}

// Last returns the newest message when present.
func (h *History) Last() (Message, bool) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	if len(h.messages) == 0 {
		return Message{}, false
	}
	return CloneMessage(h.messages[len(h.messages)-1]), true
}

// Len reports the number of stored messages.
func (h *History) Len() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return len(h.messages)
}

// TokenCount reports the estimated token cost of all stored messages.
func (h *History) TokenCount() int {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.tokenCount
}

// Reset clears the history contents.
func (h *History) Reset() {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.messages = nil
	h.tokenCount = 0
}
