package tool

import (
	"time"

	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

// Call captures a single tool invocation request.
//
// Sandbox related fields (Path, Host, Usage) are optional; when provided the
// executor will enforce the configured sandbox policies before running the
// tool.
type Call struct {
	Name   string
	Params map[string]any
	Path   string
	Host   string
	Usage  sandbox.ResourceUsage
	// SessionID optionally ties the invocation to a long-lived runtime session.
	// It is used for features like output persistence and is safe to leave empty.
	SessionID string
	// StreamSink optionally receives incremental output when the target tool
	// supports streaming via StreamingTool. It is ignored by non-streaming
	// tools to preserve backwards compatibility.
	StreamSink func(chunk string, isStderr bool)
}

// cloneParams performs a shallow copy to keep tool execution isolated from
// caller-provided maps. Nested maps are left untouched intentionally; tests
// rely on this to spot accidental sharing.
func (c Call) cloneParams() map[string]any {
	if c.Params == nil {
		return map[string]any{}
	}
	dup := make(map[string]any, len(c.Params))
	for k, v := range c.Params {
		dup[k] = cloneValue(v)
	}
	return dup
}

// CallResult holds the outcome of executing a Call.
type CallResult struct {
	Call        Call
	Result      *ToolResult
	Err         error
	StartedAt   time.Time
	CompletedAt time.Time
}

// Duration reports how long the execution took. Zero when timestamps are not
// populated.
func (r CallResult) Duration() time.Duration {
	if r.StartedAt.IsZero() || r.CompletedAt.IsZero() {
		return 0
	}
	return r.CompletedAt.Sub(r.StartedAt)
}

func cloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		copy := make(map[string]any, len(val))
		for k, inner := range val {
			copy[k] = cloneValue(inner)
		}
		return copy
	case []any:
		copy := make([]any, len(val))
		for i, inner := range val {
			copy[i] = cloneValue(inner)
		}
		return copy
	default:
		return v
	}
}
