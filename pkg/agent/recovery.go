package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/session"
)

// RecoveryManager seals unfinished tool invocations left behind by abrupt crashes.
type RecoveryManager struct {
	session session.Session
}

// NewRecoveryManager constructs a RecoveryManager bound to the provided session.
func NewRecoveryManager(sess session.Session) *RecoveryManager {
	if sess == nil {
		return nil
	}
	return &RecoveryManager{session: sess}
}

// AutoSeal scans the session history and appends failure records for unfinished tool calls.
func (r *RecoveryManager) AutoSeal(ctx context.Context) error {
	if r == nil || r.session == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
	}

	messages, err := r.session.List(session.Filter{})
	if err != nil {
		return fmt.Errorf("recovery: list session: %w", err)
	}
	pending := findPendingToolCalls(messages)
	if len(pending) == 0 {
		return nil
	}

	for _, tc := range pending {
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}
		call := ToolCall{
			ID:     tc.ID,
			Name:   tc.Name,
			Params: maps.Clone(tc.Arguments),
			Output: "ERROR: Process interrupted",
			Error:  "process interrupted before tool completion",
			Metadata: map[string]any{
				"auto_sealed": true,
			},
		}
		msg := toolCallToSessionMessage(call)
		if err := r.session.Append(msg); err != nil {
			return fmt.Errorf("recovery: append auto seal for %s: %w", tc.Name, err)
		}
	}
	return nil
}

func findPendingToolCalls(messages []session.Message) []session.ToolCall {
	if len(messages) == 0 {
		return nil
	}
	pending := make(map[string]session.ToolCall)
	for _, msg := range messages {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		for _, tc := range msg.ToolCalls {
			key := makeToolCallKey(tc)
			if key == "" {
				continue
			}
			if isCompletionEntry(role, tc) {
				delete(pending, key)
				continue
			}
			if role == "assistant" {
				pending[key] = tc
			}
		}
	}
	if len(pending) == 0 {
		return nil
	}
	result := make([]session.ToolCall, 0, len(pending))
	for _, tc := range pending {
		result = append(result, tc)
	}
	sort.SliceStable(result, func(i, j int) bool {
		return result[i].Timestamp.Before(result[j].Timestamp)
	})
	return result
}

func isCompletionEntry(role string, tc session.ToolCall) bool {
	if tc.Output != nil || strings.TrimSpace(tc.Error) != "" {
		return true
	}
	return role == "tool"
}

func makeToolCallKey(tc session.ToolCall) string {
	if tc.ID != "" {
		return tc.ID
	}
	payload := map[string]any{
		"name": strings.TrimSpace(tc.Name),
		"args": tc.Arguments,
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return strings.TrimSpace(tc.Name)
	}
	return string(data)
}
