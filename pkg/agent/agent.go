package agent

import (
	"context"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

// Agent exposes the minimal runtime surface required by callers.
type Agent interface {
	// Run executes a single turn interaction until completion.
	Run(ctx context.Context, input string) (*RunResult, error)

	// RunStream executes a turn and streams progress events.
	RunStream(ctx context.Context, input string) (<-chan event.Event, error)

	// Resume replays events from the configured store starting at bookmark.
	Resume(ctx context.Context, bookmark *event.Bookmark) (*RunResult, error)

	// RunWorkflow executes a StateGraph workflow with the agent's tool context.
	RunWorkflow(ctx context.Context, graph *workflow.Graph, opts ...workflow.ExecutorOption) error

	// AddTool registers a tool that can be invoked during execution.
	AddTool(tool tool.Tool) error

	// WithHook returns a shallow copy of the agent with an extra hook.
	WithHook(hook Hook) Agent

	// Fork clones the agent with shared configuration and optional constraints.
	Fork(opts ...ForkOption) (Agent, error)

	// Approve records a decision for a pending approval request.
	Approve(id string, approved bool) error

	// UseMiddleware registers a middleware onto the execution stack.
	UseMiddleware(mw middleware.Middleware)

	// RemoveMiddleware removes a middleware by name; returns true if found.
	RemoveMiddleware(name string) bool

	// ListMiddlewares lists middlewares in outer-to-inner execution order.
	ListMiddlewares() []middleware.Middleware
}

// Hook allows callers to intercept important lifecycle moments.
type Hook interface {
	PreRun(ctx context.Context, input string) error
	PostRun(ctx context.Context, result *RunResult) error
	PreToolCall(ctx context.Context, toolName string, params map[string]any) error
	PostToolCall(ctx context.Context, toolName string, call ToolCall) error
}

// NopHook offers a convenient zero-cost implementation for optional methods.
type NopHook struct{}

func (NopHook) PreRun(context.Context, string) error                      { return nil }
func (NopHook) PostRun(context.Context, *RunResult) error                 { return nil }
func (NopHook) PreToolCall(context.Context, string, map[string]any) error { return nil }
func (NopHook) PostToolCall(context.Context, string, ToolCall) error      { return nil }
