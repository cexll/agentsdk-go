package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"maps"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/cexll/agentsdk-go/pkg/approval"
	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/telemetry"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

// New constructs the default Agent implementation backed by basicAgent.
func New(cfg Config, opts ...Option) (Agent, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	queue := newApprovalQueue()
	agent := &basicAgent{
		cfg:             cfg,
		tools:           map[string]tool.Tool{},
		approval:        queue,
		middlewareStack: middleware.NewStack(),
		eventStore:      cfg.EventStore,
	}
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		if err := opt(agent); err != nil {
			return nil, err
		}
	}
	agent.configureRecoveryComponents()
	agent.initEventSequencer()
	return agent, nil
}

type basicAgent struct {
	cfg             Config
	hooks           []Hook
	tools           map[string]tool.Tool
	approval        *approval.Queue
	telemetry       *telemetry.Manager
	model           model.Model
	session         session.Session
	eventStore      event.EventStore
	toolMu          sync.RWMutex
	middlewareStack *middleware.Stack
	eventSeq        atomic.Int64
	recoveryMgr     *RecoveryManager
}

const (
	minStreamBufferSize = 2
	maxStreamBufferSize = 64
)

var errApprovalPending = errors.New("approval pending")

func (a *basicAgent) Run(ctx context.Context, input string) (_ *RunResult, err error) {
	tel := a.telemetryManager()
	started := time.Now()
	var runCtx RunContext
	var sanitized string

	var span trace.Span
	if tel != nil {
		attrs := tel.SanitizeAttributes(attribute.String("agent.name", a.cfg.Name))
		ctx, span = tel.StartSpan(ctx, "agent.run",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attrs...),
		)
	}
	defer func() {
		if span != nil {
			telemetry.EndSpan(span, err)
		}
		a.recordRequestMetrics(ctx, tel, "run", runCtx, sanitized, started, err)
	}()

	ctx, sanitized, runCtx, cancel, err := a.setupRun(ctx, input)
	if err != nil {
		return nil, err
	}
	if cancel != nil {
		defer cancel()
	}
	if span != nil {
		attrs := []attribute.KeyValue{attribute.String("agent.session_id", runCtx.SessionID)}
		if strings.TrimSpace(sanitized) != "" {
			attrs = append(attrs, attribute.String("agent.input", a.maskSensitive(sanitized)))
		}
		span.SetAttributes(a.sanitizeAttributes(attrs...)...)
	}
	return a.runWithEmitter(ctx, sanitized, runCtx, nil)
}

func (a *basicAgent) RunStream(ctx context.Context, input string) (<-chan event.Event, error) {
	tel := a.telemetryManager()
	started := time.Now()
	var span trace.Span
	if tel != nil {
		attrs := tel.SanitizeAttributes(attribute.String("agent.name", a.cfg.Name))
		ctx, span = tel.StartSpan(ctx, "agent.run_stream",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attrs...),
		)
	}

	ctx, sanitized, runCtx, cancel, err := a.setupRun(ctx, input)
	if err != nil {
		if span != nil {
			telemetry.EndSpan(span, err)
		}
		a.recordRequestMetrics(ctx, tel, "run_stream", runCtx, sanitized, started, err)
		return nil, err
	}
	if span != nil {
		attrs := []attribute.KeyValue{attribute.String("agent.session_id", runCtx.SessionID)}
		if strings.TrimSpace(sanitized) != "" {
			attrs = append(attrs, attribute.String("agent.input", a.maskSensitive(sanitized)))
		}
		span.SetAttributes(a.sanitizeAttributes(attrs...)...)
	}

	buffer := clampStreamBuffer(a.cfg.streamBuffer())
	ch := make(chan event.Event, buffer)
	dispatcher := newStreamDispatcher(ctx, ch, runCtx.SessionID, buffer)

	go func(ctx context.Context, rc RunContext, sanitized string) {
		defer close(ch)
		if cancel != nil {
			defer cancel()
		}
		var runErr error
		defer func() {
			a.recordRequestMetrics(ctx, tel, "run_stream", rc, sanitized, started, runErr)
			if span != nil {
				telemetry.EndSpan(span, runErr)
			}
		}()
		if emitErr := dispatcher.emit(progressEvent(rc.SessionID, "started", "stream started", nil)); emitErr != nil {
			runErr = emitErr
			return
		}
		if _, runErr = a.runWithEmitter(ctx, sanitized, rc, dispatcher.emit); runErr != nil {
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				dispatcher.pushTerminal(progressEvent(rc.SessionID, "stopped", runErr.Error(), nil))
			}
		}
	}(ctx, runCtx, sanitized)

	return ch, nil
}

func (a *basicAgent) Resume(ctx context.Context, bookmark *event.Bookmark) (*RunResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if a.eventStore == nil {
		return nil, errors.New("event store not configured")
	}
	events, err := a.eventStore.ReadSince(bookmark)
	if err != nil {
		return nil, err
	}
	res := &RunResult{
		Events:     append([]event.Event(nil), events...),
		StopReason: "resume_replay",
	}
	a.applyCompletionFromEvents(res)
	return res, nil
}

func (a *basicAgent) RunWorkflow(ctx context.Context, graph *workflow.Graph, opts ...workflow.ExecutorOption) error {
	if ctx == nil {
		return errors.New("context is nil")
	}
	if graph == nil {
		return errors.New("graph is nil")
	}
	a.toolMu.RLock()
	toolsCopy := maps.Clone(a.tools)
	a.toolMu.RUnlock()

	// Tools are injected first so caller options can still override.
	opts = append([]workflow.ExecutorOption{workflow.WithTools(toolsCopy)}, opts...)
	executor := workflow.NewExecutor(graph, opts...)
	return executor.Run(ctx)
}

func (a *basicAgent) setupRun(ctx context.Context, input string) (context.Context, string, RunContext, context.CancelFunc, error) {
	if ctx == nil {
		return nil, "", RunContext{}, nil, errors.New("context is nil")
	}
	sanitized, err := sanitizeInput(input)
	if err != nil {
		return nil, "", RunContext{}, nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, "", RunContext{}, nil, err
	}
	override, _ := GetRunContext(ctx)
	runCtx := a.cfg.ResolveContext(override)
	if runCtx.Timeout > 0 {
		ctx, cancel := context.WithTimeout(ctx, runCtx.Timeout)
		return ctx, sanitized, runCtx, cancel, nil
	}
	return ctx, sanitized, runCtx, nil, nil
}

func (a *basicAgent) runWithEmitter(ctx context.Context, input string, runCtx RunContext, emit func(event.Event) error) (*RunResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	var watchdog *Watchdog
	if timeout := a.cfg.watchdogTimeout(); timeout > 0 {
		var wdCtx context.Context
		var cancel context.CancelFunc
		wdCtx, cancel = context.WithCancel(ctx)
		handler := a.watchdogHandler(runCtx, cancel, timeout)
		wd := NewWatchdog(timeout, handler)
		if wd != nil {
			ctx = wdCtx
			watchdog = wd
			watchdog.Start(ctx)
			defer func() {
				watchdog.Stop()
				cancel()
			}()
			watchdog.Heartbeat()
		} else {
			cancel()
		}
	}
	result := &RunResult{StopReason: StopReasonComplete}
	appendAndEmit := func(evt event.Event) error {
		recorded := a.prepareEventForStore(evt)
		result.Events = append(result.Events, recorded)
		a.persistEvent(recorded)
		if emit == nil {
			return nil
		}
		return emit(recorded)
	}
	if err := runHooks(a.hooks, false, func(h Hook) error {
		return h.PreRun(ctx, input)
	}); err != nil {
		return nil, err
	}
	a.autoSealPending(ctx)
	if err := appendAndEmit(progressEvent(runCtx.SessionID, "accepted", "input accepted", nil)); err != nil {
		return result, err
	}

	// Append user input to session (once at the start)
	if a.session != nil {
		msg := session.Message{
			Role:      "user",
			Content:   input,
			Timestamp: time.Now().UTC(),
		}
		if err := a.session.Append(msg); err != nil {
			result.StopReason = "session_error"
			sessionErr := fmt.Errorf("session append user: %w", err)
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "session", sessionErr, false)); emitErr != nil {
				return result, emitErr
			}
			if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
				sessionErr = errors.Join(sessionErr, hookErr)
			}
			return result, sessionErr
		}
	}

	// Agentic loop: continue until no tool calls
	iteration := 0
	maxIterations := runCtx.MaxIterations
	if maxIterations <= 0 {
		maxIterations = defaultMaxIterations
	}
	for {
		if watchdog != nil {
			watchdog.Heartbeat()
		}
		// Check context cancellation
		if err := ctx.Err(); err != nil {
			result.StopReason = "context_cancelled"
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "context", err, false)); emitErr != nil {
				return result, emitErr
			}
			if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
				err = errors.Join(err, hookErr)
			}
			return result, err
		}

		// Emit iteration start event
		iteration++
		if err := appendAndEmit(progressEvent(runCtx.SessionID, "iteration_start", fmt.Sprintf("starting iteration %d/%d", iteration, maxIterations), map[string]any{
			"iteration":     iteration,
			"maxIterations": maxIterations,
		})); err != nil {
			return result, err
		}

		// Build messages from session history
		messages, err := a.buildModelMessagesFromHistory()
		if err != nil {
			result.StopReason = "session_error"
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "session", err, false)); emitErr != nil {
				return result, emitErr
			}
			if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
				err = errors.Join(err, hookErr)
			}
			return result, err
		}

		// Call LLM with tool schemas
		if a.model == nil {
			// No model configured, return default response
			result.Output = a.defaultResponse(input, runCtx)
			result.StopReason = "no_model"
			break
		}

		var (
			resp      model.Message
			modelResp *middleware.ModelResponse
		)

		modelReq := &middleware.ModelRequest{
			Messages:  messages,
			Tools:     a.buildToolSchemas(),
			SessionID: runCtx.SessionID,
			Metadata:  map[string]any{},
		}

		finalHandler := func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelResponse, error) {
			var r model.Message
			var err error
			if modelWithTools, ok := a.model.(model.ModelWithTools); ok && len(req.Tools) > 0 {
				r, err = modelWithTools.GenerateWithTools(ctx, req.Messages, req.Tools)
			} else {
				r, err = a.model.Generate(ctx, req.Messages)
			}
			if err != nil {
				return nil, err
			}
			return &middleware.ModelResponse{
				Message:  r,
				Usage:    model.TokenUsage{},
				Metadata: map[string]any{},
			}, nil
		}

		if stack := a.middlewareStack; stack != nil {
			modelResp, err = stack.ExecuteModelCall(ctx, modelReq, finalHandler)
		} else {
			modelResp, err = finalHandler(ctx, modelReq)
		}

		if err != nil {
			result.StopReason = "model_error"
			modelErr := fmt.Errorf("model generate: %w", err)
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "model", modelErr, false)); emitErr != nil {
				return result, emitErr
			}
			if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
				modelErr = errors.Join(modelErr, hookErr)
			}
			return result, modelErr
		}

		if modelResp == nil {
			result.StopReason = "model_error"
			modelErr := errors.New("model generate: empty response")
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "model", modelErr, false)); emitErr != nil {
				return result, emitErr
			}
			if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
				modelErr = errors.Join(modelErr, hookErr)
			}
			return result, modelErr
		}

		resp = modelResp.Message

		// Append assistant response to session
		if a.session != nil {
			msg := modelToSessionMessage(resp)
			if err := a.session.Append(msg); err != nil {
				result.StopReason = "session_error"
				sessionErr := fmt.Errorf("session append assistant: %w", err)
				if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "session", sessionErr, false)); emitErr != nil {
					return result, emitErr
				}
				if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
					sessionErr = errors.Join(sessionErr, hookErr)
				}
				return result, sessionErr
			}
		}

		// Check stop condition: no tool calls
		if len(resp.ToolCalls) == 0 {
			result.Output = strings.TrimSpace(resp.Content)
			result.StopReason = StopReasonComplete
			break
		}

		if iteration >= maxIterations {
			result.Output = strings.TrimSpace(resp.Content)
			result.StopReason = StopReasonMaxIterations
			warnMsg := fmt.Errorf("max iterations %d reached; stopping", maxIterations)
			if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "max_iterations", warnMsg, false)); emitErr != nil {
				return result, emitErr
			}
			break
		}

		// Execute all tool calls
		for _, tc := range resp.ToolCalls {
			// Emit tool call event
			toolCallEvt := event.NewEvent(
				event.EventToolCall,
				runCtx.SessionID,
				event.ToolCallData{
					Name:   tc.Name,
					Params: maps.Clone(tc.Arguments),
				},
			)
			if err := appendAndEmit(toolCallEvt); err != nil {
				return result, err
			}

			// Check approval
			approvedRec, approved, approvalErr := a.requireApproval(runCtx, tc.Name, tc.Arguments, appendAndEmit)
			if approvalErr != nil {
				result.StopReason = "approval_pending"
				if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
					approvalErr = errors.Join(approvalErr, hookErr)
				}
				return result, approvalErr
			}
			if !approved {
				result.StopReason = "approval_pending"
				return result, errApprovalPending
			}

			// Emit tool progress start
			if err := appendAndEmit(toolProgressEvent(runCtx.SessionID, tc.Name, "started", map[string]any{
				"params": maps.Clone(tc.Arguments),
			})); err != nil {
				return result, err
			}

			// Execute tool
			if watchdog != nil {
				watchdog.Heartbeat()
			}
			call, toolErr := a.executeTool(ctx, runCtx, tc.Name, tc.Arguments)
			call.ID = tc.ID // Preserve tool call ID from LLM response
			if approvedRec.ID != "" {
				if call.Metadata == nil {
					call.Metadata = map[string]any{}
				}
				call.Metadata["approval_id"] = approvedRec.ID
				call.Metadata["approval_auto"] = approvedRec.Auto
			}
			result.ToolCalls = append(result.ToolCalls, call)

			// Append tool result to session
			if a.session != nil {
				if err := a.session.Append(toolCallToSessionMessage(call)); err != nil {
					result.StopReason = "session_error"
					sessionErr := fmt.Errorf("session append tool: %w", err)
					if emitErr := appendAndEmit(errorEvent(runCtx.SessionID, "session", sessionErr, false)); emitErr != nil {
						return result, emitErr
					}
					if hookErr := a.runPostHooks(ctx, result); hookErr != nil {
						sessionErr = errors.Join(sessionErr, hookErr)
					}
					return result, sessionErr
				}
			}

			// Emit tool result event
			toolResultEvt := event.NewEvent(
				event.EventToolResult,
				runCtx.SessionID,
				event.ToolResultData{
					Name:     call.Name,
					Output:   call.Output,
					Error:    call.Error,
					Duration: call.Duration,
				},
			)
			if err := appendAndEmit(toolResultEvt); err != nil {
				return result, err
			}

			// Emit tool progress finished
			details := map[string]any{
				"duration_ms": call.Duration.Milliseconds(),
			}
			if call.Error != "" {
				details["error"] = call.Error
			}
			if err := appendAndEmit(toolProgressEvent(runCtx.SessionID, tc.Name, "finished", details)); err != nil {
				return result, err
			}

			// Handle tool error (continue loop, let LLM handle it)
			if toolErr != nil {
				// Do not exit immediately; let LLM see the error and decide next step
				// Error is already recorded in call.Error and session
			}
			if watchdog != nil {
				watchdog.Heartbeat()
			}
		}

		// Continue to next iteration
	}

	result.Usage = estimateUsage(input, result.Output)
	completed := progressEvent(runCtx.SessionID, "completed", "run completed", map[string]any{
		"stop_reason": result.StopReason,
		"iterations":  iteration,
	})
	if err := appendAndEmit(completed); err != nil {
		return result, err
	}
	if err := appendAndEmit(event.NewEvent(event.EventCompletion, runCtx.SessionID, completionSummary(result))); err != nil {
		return result, err
	}
	if err := a.runPostHooks(ctx, result); err != nil {
		return result, err
	}
	return result, nil
}

func (a *basicAgent) AddTool(t tool.Tool) error {
	if t == nil {
		return errors.New("tool is nil")
	}
	name := strings.TrimSpace(t.Name())
	if name == "" {
		return errors.New("tool name is empty")
	}
	a.toolMu.Lock()
	defer a.toolMu.Unlock()
	if a.tools == nil {
		a.tools = map[string]tool.Tool{}
	}
	if _, exists := a.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}
	a.tools[name] = t
	return nil
}

func (a *basicAgent) UseMiddleware(mw middleware.Middleware) {
	if mw == nil {
		return
	}
	if a.middlewareStack == nil {
		a.middlewareStack = middleware.NewStack()
	}
	a.middlewareStack.Use(mw)
}

func (a *basicAgent) RemoveMiddleware(name string) bool {
	if a.middlewareStack == nil {
		return false
	}
	return a.middlewareStack.Remove(name)
}

func (a *basicAgent) ListMiddlewares() []middleware.Middleware {
	if a.middlewareStack == nil {
		return nil
	}
	return a.middlewareStack.List()
}

func (a *basicAgent) cloneMiddlewareStack() *middleware.Stack {
	if a == nil || a.middlewareStack == nil {
		return middleware.NewStack()
	}
	clone := middleware.NewStack()
	for _, mw := range a.middlewareStack.List() {
		clone.Use(mw)
	}
	return clone
}

func (a *basicAgent) WithHook(h Hook) Agent {
	if h == nil {
		return a
	}
	clone := *a
	clone.middlewareStack = a.cloneMiddlewareStack()
	clone.hooks = append(append([]Hook(nil), a.hooks...), h)
	return &clone
}

func (a *basicAgent) Fork(opts ...ForkOption) (Agent, error) {
	if a == nil {
		return nil, errors.New("agent is nil")
	}
	cfg := forkConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}
	child := &basicAgent{
		cfg:       a.cfg,
		hooks:     append([]Hook(nil), a.hooks...),
		tools:     map[string]tool.Tool{},
		approval:  a.approval,
		telemetry: a.telemetry,
		model:     a.model,
		session:   a.session,
	}
	child.middlewareStack = a.cloneMiddlewareStack()
	a.toolMu.RLock()
	for name, impl := range a.tools {
		if len(cfg.toolWhitelist) > 0 {
			if _, ok := cfg.toolWhitelist[name]; !ok {
				continue
			}
		}
		child.tools[name] = impl
	}
	a.toolMu.RUnlock()
	return child, nil
}

func (a *basicAgent) Approve(id string, approved bool) error {
	if a.approval == nil {
		return errors.New("approval queue not configured")
	}
	if approved {
		_, err := a.approval.Approve(id, "approved")
		return err
	}
	_, err := a.approval.Reject(id, "rejected")
	return err
}

func (a *basicAgent) executeTool(ctx context.Context, runCtx RunContext, name string, params map[string]any) (call ToolCall, err error) {
	call = ToolCall{Name: name, Params: maps.Clone(params)}
	if call.Params == nil {
		call.Params = map[string]any{}
	}
	a.toolMu.RLock()
	impl := a.tools[name]
	a.toolMu.RUnlock()
	if impl == nil {
		err = fmt.Errorf("tool %s not registered", name)
		call.Error = err.Error()
		return call, err
	}

	tel := a.telemetryManager()
	var span trace.Span
	if tel != nil {
		attrs := tel.SanitizeAttributes(attribute.String("tool.name", name))
		ctx, span = tel.StartSpan(ctx, "agent.tool",
			trace.WithSpanKind(trace.SpanKindInternal),
			trace.WithAttributes(attrs...),
		)
	}
	defer func() {
		if span != nil {
			telemetry.EndSpan(span, err)
		}
	}()

	if hookErr := runHooks(a.hooks, false, func(h Hook) error {
		return h.PreToolCall(ctx, name, call.Params)
	}); hookErr != nil {
		err = hookErr
		return call, err
	}
	req := &middleware.ToolCallRequest{
		Name:      name,
		Arguments: maps.Clone(call.Params),
		SessionID: runCtx.SessionID,
		Metadata:  map[string]any{},
	}

	finalHandler := func(ctx context.Context, req *middleware.ToolCallRequest) (*middleware.ToolCallResponse, error) {
		started := time.Now()
		result, execErr := impl.Execute(ctx, req.Arguments)
		var output string
		if result != nil {
			output = result.Output
		}
		resp := &middleware.ToolCallResponse{
			Output:   output,
			Data:     result,
			Error:    execErr,
			Metadata: map[string]any{"duration": time.Since(started)},
		}
		return resp, nil
	}

	timeout := a.cfg.toolTimeout()
	toolCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	type toolResult struct {
		resp *middleware.ToolCallResponse
		err  error
	}

	resultCh := make(chan toolResult, 1)
	go func() {
		var (
			resp    *middleware.ToolCallResponse
			execErr error
		)
		if stack := a.middlewareStack; stack != nil {
			resp, execErr = stack.ExecuteToolCall(toolCtx, req, finalHandler)
		} else {
			resp, execErr = finalHandler(toolCtx, req)
		}
		resultCh <- toolResult{resp: resp, err: execErr}
	}()

	started := time.Now()
	var toolResp *middleware.ToolCallResponse
	select {
	case res := <-resultCh:
		toolResp = res.resp
		err = res.err
	case <-toolCtx.Done():
		call.Duration = time.Since(started)
		err = toolCtx.Err()
		if errors.Is(err, context.DeadlineExceeded) {
			err = fmt.Errorf("tool %s timed out after %s", name, timeout)
		}
		call.Error = err.Error()
		if call.Metadata == nil {
			call.Metadata = map[string]any{}
		}
		call.Metadata["timeout"] = errors.Is(toolCtx.Err(), context.DeadlineExceeded)
		if hookErr := a.invokePostToolHooks(ctx, name, call); hookErr != nil {
			err = errors.Join(err, hookErr)
			if call.Error == "" {
				call.Error = hookErr.Error()
			}
		}
		a.recordToolMetrics(ctx, tel, name, err)
		return call, err
	}

	if err != nil {
		return call, err
	}
	if toolResp == nil {
		return call, errors.New("tool: empty response from middleware")
	}

	call.Duration = time.Since(started)
	if len(toolResp.Metadata) > 0 {
		call.Metadata = maps.Clone(toolResp.Metadata)
	}
	if toolResp.Data != nil {
		call.Output = toolResp.Data
	} else {
		call.Output = toolResp.Output
	}
	if toolResp.Error != nil {
		call.Error = toolResp.Error.Error()
		err = toolResp.Error
	} else {
		err = nil
	}
	if hookErr := a.invokePostToolHooks(ctx, name, call); hookErr != nil {
		err = errors.Join(err, hookErr)
		if call.Error == "" {
			call.Error = hookErr.Error()
		}
	}
	a.recordToolMetrics(ctx, tel, name, err)
	return call, err
}

func (a *basicAgent) configureRecoveryComponents() {
	if !a.cfg.EnableRecovery {
		return
	}
	if a.recoveryMgr == nil && a.session != nil {
		a.recoveryMgr = NewRecoveryManager(a.session)
	}
}

func (a *basicAgent) autoSealPending(ctx context.Context) {
	if !a.cfg.EnableRecovery || a.recoveryMgr == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := a.recoveryMgr.AutoSeal(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Printf("agent: auto seal failed: %v", err)
	}
}

func (a *basicAgent) watchdogHandler(runCtx RunContext, cancel context.CancelFunc, timeout time.Duration) func() {
	if !a.cfg.EnableRecovery {
		return nil
	}
	return func() {
		log.Printf("agent: watchdog timeout after %s (session=%s)", timeout, runCtx.SessionID)
		if cancel != nil {
			cancel()
		}
		a.autoSealPending(context.Background())
	}
}

func (a *basicAgent) requireApproval(runCtx RunContext, toolName string, params map[string]any, emit func(event.Event) error) (approval.Record, bool, error) {
	if a.approval == nil {
		return approval.Record{}, true, nil
	}
	if runCtx.ApprovalMode == ApprovalNone || runCtx.SessionID == "" {
		return approval.Record{}, true, nil
	}
	rec, _, err := a.approval.Request(runCtx.SessionID, toolName, params)
	if err != nil {
		return rec, false, err
	}
	if rec.Decision == approval.DecisionApproved {
		if emit != nil {
			_ = emit(event.NewEvent(event.EventApprovalDecided, runCtx.SessionID, event.ApprovalResponse{ID: rec.ID, Approved: true, Comment: rec.Comment}))
		}
		return rec, true, nil
	}
	if emit != nil {
		_ = emit(event.NewEvent(event.EventApprovalRequested, runCtx.SessionID, event.ApprovalRequest{
			ID:       rec.ID,
			ToolName: toolName,
			Params:   maps.Clone(rec.Params),
		}))
	}
	return rec, false, fmt.Errorf("%w: %s", errApprovalPending, rec.ID)
}

func (a *basicAgent) runPostHooks(ctx context.Context, result *RunResult) error {
	return runHooks(a.hooks, true, func(h Hook) error {
		return h.PostRun(ctx, result)
	})
}

func (a *basicAgent) invokePostToolHooks(ctx context.Context, name string, call ToolCall) error {
	return runHooks(a.hooks, true, func(h Hook) error {
		return h.PostToolCall(ctx, name, call)
	})
}

func (a *basicAgent) generateResponse(ctx context.Context, input string, rc RunContext) (string, error) {
	if a == nil {
		return "", errors.New("agent is nil")
	}
	if a.model == nil {
		return a.defaultResponse(input, rc), nil
	}
	if ctx == nil {
		return "", errors.New("context is nil")
	}
	messages, err := a.buildModelMessages(input)
	if err != nil {
		return "", err
	}
	resp, err := a.model.Generate(ctx, messages)
	if err != nil {
		return "", fmt.Errorf("model generate: %w", err)
	}
	content := strings.TrimSpace(resp.Content)
	if content == "" {
		return "", errors.New("model returned empty response")
	}
	role := strings.TrimSpace(resp.Role)
	if role == "" {
		role = "assistant"
	}
	if sess := a.session; sess != nil {
		msg := modelToSessionMessage(model.Message{
			Role:      role,
			Content:   content,
			ToolCalls: resp.ToolCalls,
		})
		if err := sess.Append(msg); err != nil {
			return "", fmt.Errorf("session append assistant: %w", err)
		}
	}
	return content, nil
}

func (a *basicAgent) buildModelMessages(input string) ([]model.Message, error) {
	var messages []model.Message
	sess := a.session
	if sess != nil {
		history, err := sess.List(session.Filter{})
		if err != nil {
			return nil, fmt.Errorf("session list: %w", err)
		}
		messages = append(messages, sessionToModelMessages(history)...)
	}
	messages = append(messages, model.Message{Role: "user", Content: input})
	if sess != nil {
		msg := session.Message{
			Role:      "user",
			Content:   input,
			Timestamp: time.Now().UTC(),
		}
		if err := sess.Append(msg); err != nil {
			return nil, fmt.Errorf("session append user: %w", err)
		}
	}
	return messages, nil
}

// buildModelMessagesFromHistory builds messages from session history without appending new input.
// Used in the agentic loop where user input is already appended.
func (a *basicAgent) buildModelMessagesFromHistory() ([]model.Message, error) {
	var messages []model.Message
	sess := a.session
	if sess != nil {
		history, err := sess.List(session.Filter{})
		if err != nil {
			return nil, fmt.Errorf("session list: %w", err)
		}
		messages = append(messages, sessionToModelMessages(history)...)
	}
	return messages, nil
}

// buildToolSchemas converts registered tools to LLM-compatible tool schemas.
func (a *basicAgent) buildToolSchemas() []map[string]any {
	a.toolMu.RLock()
	defer a.toolMu.RUnlock()

	schemas := make([]map[string]any, 0, len(a.tools))
	for _, t := range a.tools {
		jsonSchema := t.Schema()
		if jsonSchema == nil {
			// Tool has no parameters
			schemas = append(schemas, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.Name(),
					"description": t.Description(),
				},
			})
			continue
		}

		// Convert JSONSchema to LLM tool schema format
		toolSchema := map[string]any{
			"type": "function",
			"function": map[string]any{
				"name":        t.Name(),
				"description": t.Description(),
				"parameters":  jsonSchema, // JSONSchema is already map[string]interface{}
			},
		}
		schemas = append(schemas, toolSchema)
	}
	return schemas
}

func (a *basicAgent) defaultResponse(input string, rc RunContext) string {
	if rc.SessionID != "" {
		return fmt.Sprintf("session %s: %s", rc.SessionID, input)
	}
	return fmt.Sprintf("processed: %s", input)
}

func (a *basicAgent) telemetryManager() *telemetry.Manager {
	if a != nil && a.telemetry != nil {
		return a.telemetry
	}
	return telemetry.Default()
}

func (a *basicAgent) sanitizeAttributes(attrs ...attribute.KeyValue) []attribute.KeyValue {
	if a != nil && a.telemetry != nil {
		return a.telemetry.SanitizeAttributes(attrs...)
	}
	return telemetry.SanitizeAttributes(attrs...)
}

func (a *basicAgent) maskSensitive(value string) string {
	if a != nil && a.telemetry != nil {
		return a.telemetry.MaskText(value)
	}
	return telemetry.MaskText(value)
}

func (a *basicAgent) recordRequestMetrics(ctx context.Context, tel *telemetry.Manager, kind string, runCtx RunContext, input string, started time.Time, err error) {
	req := telemetry.RequestData{
		Kind:      kind,
		AgentName: a.cfg.Name,
		SessionID: runCtx.SessionID,
		Input:     input,
		Duration:  time.Since(started),
		Error:     err,
	}
	if tel != nil {
		tel.RecordRequest(ctx, req)
		return
	}
	telemetry.RecordRequest(ctx, req)
}

func (a *basicAgent) recordToolMetrics(ctx context.Context, tel *telemetry.Manager, name string, err error) {
	data := telemetry.ToolData{
		AgentName: a.cfg.Name,
		Name:      name,
		Error:     err,
	}
	if tel != nil {
		tel.RecordToolCall(ctx, data)
		return
	}
	telemetry.RecordToolCall(ctx, data)
}

func sanitizeInput(input string) (string, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", errors.New("input is empty")
	}
	return trimmed, nil
}

func estimateUsage(input, output string) TokenUsage {
	in := utf8.RuneCountInString(input)
	out := utf8.RuneCountInString(output)
	return TokenUsage{
		InputTokens:  in,
		OutputTokens: out,
		TotalTokens:  in + out,
	}
}

func parseToolInstruction(input string) (string, map[string]any, bool, error) {
	trimmed := strings.TrimSpace(input)
	if !strings.HasPrefix(trimmed, "tool:") {
		return "", nil, false, nil
	}
	payload := strings.TrimSpace(strings.TrimPrefix(trimmed, "tool:"))
	if payload == "" {
		return "", nil, false, errors.New("missing tool name")
	}
	parts := strings.SplitN(payload, " ", 2)
	name := strings.TrimSpace(parts[0])
	if name == "" {
		return "", nil, false, errors.New("tool name is empty")
	}
	params := map[string]any{}
	if len(parts) == 2 {
		raw := strings.TrimSpace(parts[1])
		if raw != "" {
			if err := json.Unmarshal([]byte(raw), &params); err != nil {
				return "", nil, false, fmt.Errorf("parse tool params: %w", err)
			}
		}
	}
	return name, params, true, nil
}

func progressEvent(sessionID, stage, message string, details map[string]any) event.Event {
	return event.NewEvent(event.EventProgress, sessionID, event.ProgressData{
		Stage:   stage,
		Message: message,
		Details: maps.Clone(details),
	})
}

func errorEvent(sessionID, kind string, err error, recoverable bool) event.Event {
	if err == nil {
		err = errors.New("unknown error")
	}
	return event.NewEvent(event.EventError, sessionID, event.ErrorData{
		Message:     err.Error(),
		Kind:        kind,
		Recoverable: recoverable,
	})
}

func completionSummary(res *RunResult) event.CompletionData {
	summary := event.CompletionData{
		Output:     res.Output,
		StopReason: res.StopReason,
	}
	if usage := convertUsage(res.Usage); usage != nil {
		summary.Usage = usage
	}
	if len(res.ToolCalls) > 0 {
		summary.ToolCalls = convertToolCalls(res.ToolCalls)
	}
	return summary
}

func completionDataFromPayload(payload any) *event.CompletionData {
	switch data := payload.(type) {
	case event.CompletionData:
		copy := data
		return &copy
	case *event.CompletionData:
		if data == nil {
			return nil
		}
		copy := *data
		return &copy
	case map[string]any:
		raw, err := json.Marshal(data)
		if err != nil {
			return nil
		}
		var summary event.CompletionData
		if err := json.Unmarshal(raw, &summary); err != nil {
			return nil
		}
		return &summary
	default:
		return nil
	}
}

func convertToolCalls(calls []ToolCall) []event.ToolCallData {
	if len(calls) == 0 {
		return nil
	}
	data := make([]event.ToolCallData, 0, len(calls))
	for _, call := range calls {
		data = append(data, event.ToolCallData{
			Name:   call.Name,
			Params: maps.Clone(call.Params),
		})
	}
	return data
}

func convertUsage(u TokenUsage) *event.UsageData {
	if u == (TokenUsage{}) {
		return nil
	}
	return &event.UsageData{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
		CacheTokens:  u.CacheTokens,
	}
}

func tokenUsageFromEvent(data *event.UsageData) TokenUsage {
	if data == nil {
		return TokenUsage{}
	}
	return TokenUsage{
		InputTokens:  data.InputTokens,
		OutputTokens: data.OutputTokens,
		TotalTokens:  data.TotalTokens,
		CacheTokens:  data.CacheTokens,
	}
}

func newApprovalQueue() *approval.Queue {
	dir := filepath.Join(os.TempDir(), "agentsdk-approvals")
	store, err := approval.NewRecordLog(dir)
	if err != nil {
		return approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	}
	return approval.NewQueue(store, approval.NewWhitelist())
}

func stringify(v any) string {
	switch val := v.(type) {
	case string:
		return val
	case fmt.Stringer:
		return val.String()
	case []byte:
		return string(val)
	default:
		data, err := json.Marshal(val)
		if err != nil {
			return fmt.Sprint(val)
		}
		return string(data)
	}
}

func runHooks(hooks []Hook, collect bool, fn func(Hook) error) error {
	var joined error
	for _, hook := range hooks {
		if err := fn(hook); err != nil {
			if !collect {
				return err
			}
			joined = errors.Join(joined, err)
		}
	}
	return joined
}

func clampStreamBuffer(size int) int {
	if size < minStreamBufferSize {
		return minStreamBufferSize
	}
	if size > maxStreamBufferSize {
		return maxStreamBufferSize
	}
	return size
}

type streamDispatcher struct {
	ctx        context.Context
	out        chan<- event.Event
	sessionID  string
	bufferSize int
	throttled  atomic.Bool
}

func newStreamDispatcher(ctx context.Context, out chan<- event.Event, sessionID string, buffer int) *streamDispatcher {
	if buffer < minStreamBufferSize {
		buffer = minStreamBufferSize
	}
	return &streamDispatcher{
		ctx:        ctx,
		out:        out,
		sessionID:  sessionID,
		bufferSize: buffer,
	}
}

func (d *streamDispatcher) emit(evt event.Event) error {
	select {
	case <-d.ctx.Done():
		return d.ctx.Err()
	case d.out <- evt:
		return nil
	default:
	}
	if d.throttled.CompareAndSwap(false, true) {
		if !d.blockingSend(d.backpressureEvent("throttled")) {
			return d.ctx.Err()
		}
	}
	if !d.blockingSend(evt) {
		return d.ctx.Err()
	}
	if d.throttled.CompareAndSwap(true, false) {
		d.tryEmit(d.backpressureEvent("recovered"))
	}
	return nil
}

func (d *streamDispatcher) blockingSend(evt event.Event) bool {
	for {
		select {
		case <-d.ctx.Done():
			return false
		case d.out <- evt:
			return true
		}
	}
}

func (d *streamDispatcher) tryEmit(evt event.Event) {
	select {
	case <-d.ctx.Done():
	case d.out <- evt:
	default:
	}
}

func (d *streamDispatcher) backpressureEvent(state string) event.Event {
	return progressEvent(d.sessionID, "backpressure", state, map[string]any{
		"buffer_size": d.bufferSize,
	})
}

func (d *streamDispatcher) pushTerminal(evt event.Event) {
	if evt.Type == "" {
		return
	}
	select {
	case d.out <- evt:
		return
	default:
	}
	timer := time.NewTimer(50 * time.Millisecond)
	defer timer.Stop()
	select {
	case d.out <- evt:
	case <-timer.C:
	}
}

func toolProgressEvent(sessionID, name, state string, extra map[string]any) event.Event {
	data := map[string]any{
		"tool":  name,
		"state": state,
	}
	for k, v := range extra {
		if v == nil {
			continue
		}
		data[k] = v
	}
	return event.NewEvent(event.EventProgress, sessionID, event.ProgressData{
		Stage:   fmt.Sprintf("tool:%s", name),
		Message: state,
		Details: data,
	})
}

func (a *basicAgent) prepareEventForStore(evt event.Event) event.Event {
	if a == nil || a.eventStore == nil {
		return evt
	}
	if evt.Bookmark != nil {
		a.observeEventSeq(evt.Bookmark.Seq)
		return evt
	}
	seq := a.eventSeq.Add(1)
	evt.Bookmark = &event.Bookmark{
		Seq:       seq,
		Timestamp: time.Now().UTC(),
	}
	return evt
}

func (a *basicAgent) persistEvent(evt event.Event) {
	if a == nil || a.eventStore == nil || evt.Bookmark == nil {
		return
	}
	if err := a.eventStore.Append(evt); err != nil {
		log.Printf("agent: event store append failed: %v", err)
	}
}

func (a *basicAgent) initEventSequencer() {
	if a == nil || a.eventStore == nil {
		return
	}
	last, err := a.eventStore.LastBookmark()
	if err != nil {
		log.Printf("agent: query last bookmark: %v", err)
		return
	}
	if last != nil {
		a.eventSeq.Store(last.Seq)
	}
}

func (a *basicAgent) observeEventSeq(seq int64) {
	if a == nil || seq <= 0 {
		return
	}
	for {
		current := a.eventSeq.Load()
		if seq <= current {
			return
		}
		if a.eventSeq.CompareAndSwap(current, seq) {
			return
		}
	}
}

func (a *basicAgent) applyCompletionFromEvents(res *RunResult) {
	if res == nil {
		return
	}
	for i := len(res.Events) - 1; i >= 0; i-- {
		evt := res.Events[i]
		if evt.Type != event.EventCompletion {
			continue
		}
		if summary := completionDataFromPayload(evt.Data); summary != nil {
			if summary.StopReason != "" {
				res.StopReason = summary.StopReason
			}
			res.Output = summary.Output
			if summary.Usage != nil {
				res.Usage = tokenUsageFromEvent(summary.Usage)
			}
		}
		return
	}
}
