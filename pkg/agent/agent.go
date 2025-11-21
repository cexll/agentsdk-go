package agent

import (
	"context"
	"errors"
	"fmt"

	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
)

type contextKey string

const middlewareStateKey contextKey = "agentsdk.middleware.state"

var (
	ErrMaxIterations = errors.New("max iterations reached")
	ErrNilModel      = errors.New("agent: model is nil")
)

// Model produces the next output for the agent given the current context.
type Model interface {
	Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}

// ToolExecutor performs a tool call emitted by the model.
type ToolExecutor interface {
	Execute(ctx context.Context, call ToolCall, c *Context) (ToolResult, error)
}

// ToolCall describes a discrete tool invocation request.
type ToolCall struct {
	ID    string
	Name  string
	Input map[string]any
}

// ToolResult holds the outcome of a tool invocation.
type ToolResult struct {
	Name     string
	Output   string
	Metadata map[string]any
}

// ModelOutput is the result returned by a Model.Generate call.
type ModelOutput struct {
	Content   string
	ToolCalls []ToolCall
	Done      bool
}

// Agent drives the core loop, invoking middleware, model, and tools.
type Agent struct {
	model Model
	tools ToolExecutor
	opts  Options
	mw    *middleware.Chain
}

// New constructs an Agent with the provided collaborators.
func New(model Model, tools ToolExecutor, opts Options) (*Agent, error) {
	if model == nil {
		return nil, ErrNilModel
	}
	applied := opts.withDefaults()
	return &Agent{
		model: model,
		tools: tools,
		opts:  applied,
		mw:    applied.Middleware,
	}, nil
}

// Run executes the agent loop. It terminates when the model returns a final
// output (Done or no tool calls), the context is canceled, or an error occurs.
func (a *Agent) Run(ctx context.Context, c *Context) (*ModelOutput, error) {
	if a == nil {
		return nil, errors.New("agent is nil")
	}
	if c == nil {
		c = NewContext()
	}
	if ctx == nil {
		ctx = context.Background()
	}

	if a.opts.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, a.opts.Timeout)
		defer cancel()
	}

	stateValues := map[string]any{}
	if len(c.Values) > 0 {
		for k, v := range c.Values {
			stateValues[k] = v
		}
	}
	state := &middleware.State{
		Agent:  c,
		Values: stateValues,
	}
	ctx = context.WithValue(ctx, model.MiddlewareStateKey, state)

	if err := a.mw.Execute(ctx, middleware.StageBeforeAgent, state); err != nil {
		return nil, err
	}

	var last *ModelOutput
	iteration := 0

	for {
		if err := ctx.Err(); err != nil {
			return last, err
		}
		if a.opts.MaxIterations > 0 && iteration >= a.opts.MaxIterations {
			return last, ErrMaxIterations
		}

		c.Iteration = iteration
		state.Iteration = iteration

		if err := a.mw.Execute(ctx, middleware.StageBeforeModel, state); err != nil {
			return last, err
		}

		// Inject middleware state into context so model can populate ModelInput/ModelOutput
		modelCtx := context.WithValue(ctx, middlewareStateKey, state)
		out, err := a.model.Generate(modelCtx, c)
		if err != nil {
			return last, err
		}
		if out == nil {
			return last, errors.New("model returned nil output")
		}

		last = out
		c.LastModelOutput = out
		state.ModelOutput = out

		if err := a.mw.Execute(ctx, middleware.StageAfterModel, state); err != nil {
			return last, err
		}

		if out.Done || len(out.ToolCalls) == 0 {
			if err := a.mw.Execute(ctx, middleware.StageAfterAgent, state); err != nil {
				return last, err
			}
			return out, nil
		}

		var firstMiddlewareErr error

		for _, call := range out.ToolCalls {
			state.ToolCall = call
			if err := a.mw.Execute(ctx, middleware.StageBeforeTool, state); err != nil && firstMiddlewareErr == nil {
				firstMiddlewareErr = err
			}

			if a.tools == nil {
				return last, fmt.Errorf("tool executor is nil for call %s", call.Name)
			}

			res, err := a.tools.Execute(ctx, call, c)
			if err != nil {
				res = ToolResult{
					Name:   call.Name,
					Output: fmt.Sprintf("Tool execution failed: %v", err),
					Metadata: map[string]any{
						"is_error": true,
					},
				}
			}

			c.ToolResults = append(c.ToolResults, res)
			state.ToolResult = res

			if err := a.mw.Execute(ctx, middleware.StageAfterTool, state); err != nil && firstMiddlewareErr == nil {
				firstMiddlewareErr = err
			}
		}

		if firstMiddlewareErr != nil {
			return last, firstMiddlewareErr
		}

		iteration++
	}
}
