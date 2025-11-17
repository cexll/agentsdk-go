package agent

import (
	"context"
	"time"
)

const (
	defaultMaxIterations = 10
	defaultTemperature   = 0.7
)

// RunContext carries execution level knobs for a single Run invocation.
type RunContext struct {
	SessionID     string        `json:"session_id" yaml:"session_id"`
	WorkDir       string        `json:"work_dir" yaml:"work_dir"`
	MaxIterations int           `json:"max_iterations" yaml:"max_iterations"`
	MaxTokens     int           `json:"max_tokens" yaml:"max_tokens"`
	Timeout       time.Duration `json:"timeout" yaml:"timeout"`
	ApprovalMode  ApprovalMode  `json:"approval_mode" yaml:"approval_mode"`
	Temperature   float64       `json:"temperature" yaml:"temperature"`
}

type runContextKey struct{}

// Merge applies non-zero override values on top of the receiver.
func (c RunContext) Merge(override RunContext) RunContext {
	merged := c
	if override.SessionID != "" {
		merged.SessionID = override.SessionID
	}
	if override.WorkDir != "" {
		merged.WorkDir = override.WorkDir
	}
	if override.MaxIterations > 0 {
		merged.MaxIterations = override.MaxIterations
	}
	if override.MaxTokens > 0 {
		merged.MaxTokens = override.MaxTokens
	}
	if override.Timeout > 0 {
		merged.Timeout = override.Timeout
	}
	if override.ApprovalMode != ApprovalMode(0) {
		merged.ApprovalMode = override.ApprovalMode
	}
	if override.Temperature != 0 {
		merged.Temperature = override.Temperature
	}
	return merged.Normalize()
}

// Normalize enforces sane defaults for unset fields.
func (c RunContext) Normalize() RunContext {
	if c.MaxIterations <= 0 {
		c.MaxIterations = defaultMaxIterations
	}
	if c.MaxTokens < 0 {
		c.MaxTokens = 0
	}
	if c.Timeout < 0 {
		c.Timeout = 0
	}
	if c.Temperature == 0 {
		c.Temperature = defaultTemperature
	}
	return c
}

// WithRunContext injects a RunContext into ctx for downstream retrieval.
func WithRunContext(ctx context.Context, rc RunContext) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, runContextKey{}, rc.Normalize())
}

// GetRunContext extracts a RunContext from ctx when present.
func GetRunContext(ctx context.Context) (RunContext, bool) {
	if ctx == nil {
		return RunContext{}, false
	}
	rc, ok := ctx.Value(runContextKey{}).(RunContext)
	return rc, ok
}

// ApprovalMode enumerates decision models for privileged operations.
type ApprovalMode int

const (
	// ApprovalNone disables approval checks.
	ApprovalNone ApprovalMode = iota
	// ApprovalRequired forces human approval before every privileged step.
	ApprovalRequired
	// ApprovalAuto allows the agent to auto-approve after gaining consent once.
	ApprovalAuto
)
