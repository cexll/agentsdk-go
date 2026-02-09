package message

// Message represents a single conversational turn used within the message
// package. It is purposefully minimal to keep the history layer independent
// from concrete model providers.
type Message struct {
	Role      string
	Content   string
	ToolCalls []ToolCall
	ReasoningContent string
}

// ToolCall mirrors the shape of a tool invocation produced by the assistant.
type ToolCall struct {
	ID        string
	Name      string
	Arguments map[string]any
	Result    string
}

// CloneMessage performs a deep clone of a model.Message, duplicating nested
// maps to avoid mutation leaks between callers.
func CloneMessage(msg Message) Message {
	clone := Message{Role: msg.Role, Content: msg.Content, ReasoningContent: msg.ReasoningContent}
	clone.ToolCalls = cloneToolCalls(msg.ToolCalls)
	return clone
}

// CloneMessages clones an entire slice of model messages.
func CloneMessages(msgs []Message) []Message {
	if len(msgs) == 0 {
		return []Message{}
	}
	out := make([]Message, len(msgs))
	for i, msg := range msgs {
		out[i] = CloneMessage(msg)
	}
	return out
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return []ToolCall{}
	}
	out := make([]ToolCall, len(calls))
	for i, call := range calls {
		out[i] = ToolCall{ID: call.ID, Name: call.Name, Arguments: cloneMap(call.Arguments), Result: call.Result}
	}
	return out
}

func cloneMap(input map[string]any) map[string]any {
	if input == nil {
		return nil
	}
	dup := make(map[string]any, len(input))
	for k, v := range input {
		dup[k] = v
	}
	return dup
}
