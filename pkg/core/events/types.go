package events

import (
	"fmt"
	"time"
)

// EventType enumerates all hookable lifecycle events supported by the SDK.
// Keeping the list small and explicit prevents accidental proliferation of
// loosely defined event names.
type EventType string

const (
	PreToolUse        EventType = "PreToolUse"
	PostToolUse       EventType = "PostToolUse"
	PreCompact        EventType = "PreCompact"
	ContextCompacted  EventType = "ContextCompacted"
	UserPromptSubmit  EventType = "UserPromptSubmit"
	SessionStart      EventType = "SessionStart"
	SessionEnd        EventType = "SessionEnd"
	Stop              EventType = "Stop"
	SubagentStart     EventType = "SubagentStart"
	SubagentStop      EventType = "SubagentStop"
	Notification      EventType = "Notification"
	TokenUsage        EventType = "TokenUsage"
	PermissionRequest EventType = "PermissionRequest"
	ModelSelected     EventType = "ModelSelected"
)

// Event represents a single occurrence in the system. It is intentionally
// lightweight; any structured payloads are stored in the Payload field.
type Event struct {
	ID        string      // optional explicit identifier; generated when empty
	Type      EventType   // required
	Timestamp time.Time   // auto-populated when zero
	SessionID string      // optional session identifier for hook payloads
	Payload   interface{} // optional, type asserted by hook executors
}

// Validate performs cheap sanity checks for callers that need stronger
// contracts than the zero-value guarantees.
func (e Event) Validate() error {
	if e.Type == "" {
		return fmt.Errorf("events: missing type")
	}
	return nil
}

// ToolUsePayload is emitted before tool execution.
type ToolUsePayload struct {
	Name   string
	Params map[string]any
}

// ToolResultPayload is emitted after tool execution.
type ToolResultPayload struct {
	Name     string
	Result   any
	Duration time.Duration
	Err      error
}

// PreCompactPayload is emitted before automatic context compaction.
type PreCompactPayload struct {
	EstimatedTokens int     `json:"estimated_tokens"`
	TokenLimit      int     `json:"token_limit"`
	Threshold       float64 `json:"threshold"`
	PreserveCount   int     `json:"preserve_count"`
}

// ContextCompactedPayload is emitted after context compaction completes.
type ContextCompactedPayload struct {
	Summary               string `json:"summary"`
	OriginalMessages      int    `json:"original_messages"`
	PreservedMessages     int    `json:"preserved_messages"`
	EstimatedTokensBefore int    `json:"estimated_tokens_before"`
	EstimatedTokensAfter  int    `json:"estimated_tokens_after"`
}

// UserPromptPayload captures a user supplied prompt.
type UserPromptPayload struct {
	Prompt string
}

// SessionPayload signals session lifecycle transitions.
type SessionPayload struct {
	SessionID string
	Metadata  map[string]any
}

// StopPayload indicates a stop notification for the main agent.
type StopPayload struct {
	Reason string
}

// SubagentStopPayload is emitted when a subagent stops independently.
type SubagentStopPayload struct {
	Name           string
	Reason         string
	AgentID        string // unique identifier for the subagent instance
	TranscriptPath string // path to the subagent transcript file
}

// SubagentStartPayload is emitted when a subagent starts.
type SubagentStartPayload struct {
	Name     string
	AgentID  string         // unique identifier for the subagent instance
	Metadata map[string]any // optional metadata
}

// PermissionRequestPayload is emitted when a tool requests permission.
type PermissionRequestPayload struct {
	ToolName   string
	ToolParams map[string]any
	Reason     string // optional reason for the permission request
}

// PermissionDecisionType represents the decision from a permission request hook.
type PermissionDecisionType string

const (
	PermissionAllow PermissionDecisionType = "allow"
	PermissionDeny  PermissionDecisionType = "deny"
	PermissionAsk   PermissionDecisionType = "ask"
)

// NotificationPayload transports informational messages.
type NotificationPayload struct {
	Message string
	Meta    map[string]any
}

// TokenUsagePayload reports model token usage for a completed run.
type TokenUsagePayload struct {
	InputTokens   int64  `json:"input_tokens"`
	OutputTokens  int64  `json:"output_tokens"`
	TotalTokens   int64  `json:"total_tokens"`
	CacheCreation int64  `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     int64  `json:"cache_read_input_tokens,omitempty"`
	Model         string `json:"model,omitempty"`
	SessionID     string `json:"session_id,omitempty"`
	RequestID     string `json:"request_id,omitempty"`
}

// ModelSelectedPayload is emitted when a model is selected for tool execution.
type ModelSelectedPayload struct {
	ToolName  string
	ModelTier string
	Reason    string
}
