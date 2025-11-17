package agent

import (
	"encoding/json"
	"fmt"
	"maps"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

// sessionToModelMessages normalizes persisted session history so it can be sent to a model.
func sessionToModelMessages(messages []session.Message) []model.Message {
	if len(messages) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(messages))
	for _, msg := range messages {
		out = append(out, model.Message{
			Role:      normalizeRole(msg.Role),
			Content:   msg.Content,
			ToolCalls: sessionToolCallsToModel(msg.ToolCalls),
		})
	}
	return out
}

// modelToSessionMessage converts a model response into a session-friendly message.
func modelToSessionMessage(msg model.Message) session.Message {
	return session.Message{
		Role:      normalizeRole(msg.Role),
		Content:   msg.Content,
		ToolCalls: modelToolCallsToSession(msg.ToolCalls),
		Timestamp: time.Now().UTC(),
	}
}

// toolCallToSessionMessage records a tool invocation result as a session message.
func toolCallToSessionMessage(call ToolCall) session.Message {
	now := time.Now().UTC()
	name := normalizeToolName(call.Name)
	args := maps.Clone(call.Params)
	metadata := cloneMetadata(call.Metadata)
	if call.Duration > 0 {
		if metadata == nil {
			metadata = map[string]any{}
		}
		metadata["duration_ms"] = call.Duration.Milliseconds()
	}
	errText := strings.TrimSpace(call.Error)
	toolCall := session.ToolCall{
		ID:        call.ID,
		Name:      name,
		Arguments: args,
		Output:    call.Output,
		Error:     errText,
		Metadata:  metadata,
		Timestamp: now,
	}
	return session.Message{
		Role:      "tool",
		Content:   formatToolCallContent(name, args, call.Output, errText),
		ToolCalls: []session.ToolCall{toolCall},
		Timestamp: now,
	}
}

func sessionToolCallsToModel(calls []session.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, model.ToolCall{
			ID:        call.ID,
			Name:      normalizeToolName(call.Name),
			Arguments: maps.Clone(call.Arguments),
		})
	}
	return out
}

func modelToolCallsToSession(calls []model.ToolCall) []session.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]session.ToolCall, 0, len(calls))
	now := time.Now().UTC()
	for _, call := range calls {
		args := maps.Clone(call.Arguments)
		out = append(out, session.ToolCall{
			ID:        call.ID,
			Name:      normalizeToolName(call.Name),
			Arguments: args,
			Timestamp: now,
		})
	}
	return out
}

func normalizeRole(role string) string {
	switch strings.ToLower(strings.TrimSpace(role)) {
	case "assistant", "model":
		return "assistant"
	case "system":
		return "system"
	case "tool":
		return "tool"
	case "user", "":
		return "user"
	default:
		return "user"
	}
}

func normalizeToolName(name string) string {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return "unknown_tool"
	}
	return trimmed
}

func formatToolCallContent(name string, params map[string]any, output any, errText string) string {
	payload := map[string]any{
		"tool":   name,
		"params": params,
		"output": output,
		"error":  errText,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Sprintf("tool=%s output=%v error=%s", name, output, errText)
	}
	return string(data)
}
