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
	PreToolUse         EventType = "PreToolUse"
	PostToolUse        EventType = "PostToolUse"
	PostToolUseFailure EventType = "PostToolUseFailure"
	PreCompact         EventType = "PreCompact"
	ContextCompacted   EventType = "ContextCompacted"
	UserPromptSubmit   EventType = "UserPromptSubmit"
	SessionStart       EventType = "SessionStart"
	SessionEnd         EventType = "SessionEnd"
	Stop               EventType = "Stop"
	SubagentStart      EventType = "SubagentStart"
	SubagentStop       EventType = "SubagentStop"
	Notification       EventType = "Notification"
	TokenUsage         EventType = "TokenUsage"
	PermissionRequest  EventType = "PermissionRequest"
	ModelSelected      EventType = "ModelSelected"
	MCPToolsChanged    EventType = "MCPToolsChanged"
)

// Event represents a single occurrence in the system. It is intentionally
// lightweight; any structured payloads are stored in the Payload field.
type Event struct {
	ID        string      // optional explicit identifier; generated when empty
	Type      EventType   // required
	Timestamp time.Time   // auto-populated when zero
	SessionID string      // optional session identifier for hook payloads
	RequestID string      // optional request identifier for distributed tracing
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
	Name      string
	Params    map[string]any
	ToolUseID string // unique identifier for this tool use
}

// ToolResultPayload is emitted after tool execution.
type ToolResultPayload struct {
	Name      string
	Params    map[string]any // original tool input params
	ToolUseID string         // matches the ToolUsePayload.ToolUseID
	Result    any
	Duration  time.Duration
	Err       error
}

// PreCompactPayload is emitted before automatic context compaction.
type PreCompactPayload struct {
	Trigger           string  `json:"trigger,omitempty"`
	CustomInstructions string `json:"custom_instructions,omitempty"`
	EstimatedTokens   int     `json:"estimated_tokens"`
	TokenLimit        int     `json:"token_limit"`
	Threshold         float64 `json:"threshold"`
	PreserveCount     int     `json:"preserve_count"`
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

// SessionPayload signals session lifecycle transitions (legacy, kept for backward compat).
type SessionPayload struct {
	SessionID string
	Metadata  map[string]any
}

// SessionStartPayload is emitted when a session starts.
type SessionStartPayload struct {
	SessionID string
	Source    string         // entry point source (e.g., "cli", "api")
	Model     string         // model being used
	AgentType string         // agent type (e.g., "main", "subagent")
	Metadata  map[string]any
}

// SessionEndPayload is emitted when a session ends.
type SessionEndPayload struct {
	SessionID string
	Reason    string         // reason for ending (e.g., "user_exit", "error")
	Metadata  map[string]any
}

// StopPayload indicates a stop notification for the main agent.
type StopPayload struct {
	Reason         string
	StopHookActive bool // whether a stop hook is currently active
}

// SubagentStopPayload is emitted when a subagent stops independently.
type SubagentStopPayload struct {
	Name           string
	Reason         string
	AgentID        string // unique identifier for the subagent instance
	AgentType      string // type of the subagent
	TranscriptPath string // path to the subagent transcript file
	StopHookActive bool   // whether a stop hook is currently active
}

// SubagentStartPayload is emitted when a subagent starts.
type SubagentStartPayload struct {
	Name      string
	AgentID   string         // unique identifier for the subagent instance
	AgentType string         // type of the subagent
	Metadata  map[string]any // optional metadata
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
	Title            string
	Message          string
	NotificationType string         // type of notification for matcher filtering
	Meta             map[string]any
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

// MCPToolsChangedPayload is emitted when an MCP server notifies the client that
// its tool list changed (notifications/tools/list_changed) and the client has
// refreshed its tool snapshot.
type MCPToolsChangedPayload struct {
	// Server identifies the MCP server that triggered the update. Callers should
	// treat this as an opaque identifier (often the connection spec/URL).
	Server string
	// SessionID is the MCP session identifier when available.
	SessionID string
	// Tools is the refreshed tool snapshot.
	Tools []MCPToolDescriptor
	// Error is set when refreshing tools failed.
	Error string
}

// MCPToolDescriptor is a minimal copy of MCP tool metadata suitable for
// reacting to list-changed notifications without depending on the MCP SDK.
type MCPToolDescriptor struct {
	Name         string
	Description  string
	InputSchema  any
	OutputSchema any
	Title        string
}
