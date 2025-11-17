package agent

import (
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
)

// RunResult captures the final outcome for a single agent turn.
type RunResult struct {
	Output     string        `json:"output"`
	ToolCalls  []ToolCall    `json:"tool_calls"`
	Usage      TokenUsage    `json:"usage"`
	StopReason string        `json:"stop_reason"`
	Events     []event.Event `json:"events"`
}

const (
	StopReasonComplete      = "complete"
	StopReasonMaxIterations = "max_iterations"
)

// TokenUsage holds lightweight token accounting numbers.
type TokenUsage struct {
	InputTokens  int `json:"input_tokens"`
	OutputTokens int `json:"output_tokens"`
	TotalTokens  int `json:"total_tokens"`
	CacheTokens  int `json:"cache_tokens"`
}

// ToolCall records a single invocation of a registered tool.
type ToolCall struct {
	ID       string         `json:"id,omitempty"`
	Name     string         `json:"name"`
	Params   map[string]any `json:"params"`
	Output   any            `json:"output"`
	Error    string         `json:"error"`
	Duration time.Duration  `json:"duration"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Failed reports whether the tool invocation returned an error.
func (c ToolCall) Failed() bool {
	return c.Error != ""
}
