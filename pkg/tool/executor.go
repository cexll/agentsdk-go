package tool

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

// Executor wires tool registry lookup with sandbox enforcement.
// A nil sandbox manager disables enforcement.
type Executor struct {
	registry *Registry
	sandbox  *sandbox.Manager
}

// NewExecutor constructs an executor backed by the provided registry. When
// registry is nil a fresh Registry is created so callers never receive a nil
// executor by accident.
func NewExecutor(registry *Registry, sb *sandbox.Manager) *Executor {
	if registry == nil {
		registry = NewRegistry()
	}
	return &Executor{registry: registry, sandbox: sb}
}

// Registry exposes the underlying registry primarily for tests.
func (e *Executor) Registry() *Registry { return e.registry }

// Execute runs a single tool call. Parameters are shallow-cloned before being
// handed over to the tool to avoid concurrent callers mutating shared maps.
func (e *Executor) Execute(ctx context.Context, call Call) (*CallResult, error) {
	if e == nil || e.registry == nil {
		return nil, errors.New("executor is not initialised")
	}
	if strings.TrimSpace(call.Name) == "" {
		return nil, errors.New("tool name is empty")
	}

	if e.sandbox != nil {
		if err := e.sandbox.Enforce(call.Path, call.Host, call.Usage); err != nil {
			return nil, err
		}
	}

	tool, err := e.registry.Get(call.Name)
	if err != nil {
		return nil, err
	}

	started := time.Now()
	res, execErr := tool.Execute(ctx, call.cloneParams())
	cr := &CallResult{
		Call:        call,
		Result:      res,
		Err:         execErr,
		StartedAt:   started,
		CompletedAt: time.Now(),
	}
	return cr, execErr
}

// ExecuteAll runs the provided calls concurrently and preserves ordering in the
// returned slice. Each call is isolated with its own parameter copy. Execution
// stops early when the context is cancelled; tools observe ctx directly.
func (e *Executor) ExecuteAll(ctx context.Context, calls []Call) []CallResult {
	results := make([]CallResult, len(calls))
	var wg sync.WaitGroup
	wg.Add(len(calls))

	for i := range calls {
		call := calls[i]
		go func(idx int) {
			defer wg.Done()
			if ctx != nil && ctx.Err() != nil {
				results[idx] = CallResult{Call: call, Err: ctx.Err()}
				return
			}
			cr, err := e.Execute(ctx, call)
			if cr != nil {
				results[idx] = *cr
				return
			}
			// When executor is nil, propagate error without result payload.
			results[idx] = CallResult{Call: call, Err: err}
		}(i)
	}

	wg.Wait()
	return results
}

// WithSandbox returns a shallow copy using the provided sandbox manager.
func (e *Executor) WithSandbox(sb *sandbox.Manager) *Executor {
	if e == nil {
		return NewExecutor(nil, sb)
	}
	clone := *e
	clone.sandbox = sb
	return &clone
}
