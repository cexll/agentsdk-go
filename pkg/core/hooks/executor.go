package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/core/middleware"
)

// defaultHookTimeout mirrors Claude Code's documented 30s hook budget.
const defaultHookTimeout = 30 * time.Second

// Decision captures the permission outcome encoded in the hook exit code.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionDeny
	DecisionAsk
	DecisionError
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionDeny:
		return "deny"
	case DecisionAsk:
		return "ask"
	default:
		return "error"
	}
}

// PermissionDecision holds the parsed stdout JSON emitted by PreToolUse hooks.
type PermissionDecision map[string]any

// Result captures the full outcome of executing a shell hook.
type Result struct {
	Event      events.Event
	Decision   Decision
	ExitCode   int
	Permission PermissionDecision
	Stdout     string
	Stderr     string
}

// Selector filters hooks by tool name and/or payload pattern.
type Selector struct {
	ToolName *regexp.Regexp
	Pattern  *regexp.Regexp
}

// NewSelector compiles optional regex patterns. Empty strings are treated as wildcards.
func NewSelector(toolPattern, payloadPattern string) (Selector, error) {
	sel := Selector{}
	if strings.TrimSpace(toolPattern) != "" {
		re, err := regexp.Compile(toolPattern)
		if err != nil {
			return sel, fmt.Errorf("hooks: compile tool matcher: %w", err)
		}
		sel.ToolName = re
	}
	if strings.TrimSpace(payloadPattern) != "" {
		re, err := regexp.Compile(payloadPattern)
		if err != nil {
			return sel, fmt.Errorf("hooks: compile payload matcher: %w", err)
		}
		sel.Pattern = re
	}
	return sel, nil
}

// Match returns true when the event satisfies all configured selectors.
func (s Selector) Match(evt events.Event) bool {
	if s.ToolName != nil {
		name := extractToolName(evt.Payload)
		if name == "" || !s.ToolName.MatchString(name) {
			return false
		}
	}
	if s.Pattern != nil {
		payload, err := json.Marshal(evt.Payload)
		if err != nil {
			return false
		}
		if !s.Pattern.Match(payload) {
			return false
		}
	}
	return true
}

// ShellHook describes a single shell command bound to an event type.
type ShellHook struct {
	Event    events.EventType
	Command  string
	Selector Selector
	Timeout  time.Duration
	Env      map[string]string
	Name     string // optional label for debugging
}

// Executor executes hooks by spawning shell commands with JSON stdin payloads.
type Executor struct {
	hooks   []ShellHook
	hooksMu sync.RWMutex

	mw      []middleware.Middleware
	timeout time.Duration
	errFn   func(events.EventType, error)

	defaultCommand string
}

// ExecutorOption configures optional behaviour.
type ExecutorOption func(*Executor)

// WithMiddleware wraps execution with the provided middleware chain.
func WithMiddleware(mw ...middleware.Middleware) ExecutorOption {
	return func(e *Executor) {
		e.mw = append(e.mw, mw...)
	}
}

// WithTimeout sets the default timeout per hook run. Zero uses the default budget.
func WithTimeout(d time.Duration) ExecutorOption {
	return func(e *Executor) {
		e.timeout = d
	}
}

// WithErrorHandler installs an async error sink. Errors are still returned to callers.
func WithErrorHandler(fn func(events.EventType, error)) ExecutorOption {
	return func(e *Executor) {
		e.errFn = fn
	}
}

// WithCommand defines the fallback shell command used when a hook omits Command.
func WithCommand(cmd string) ExecutorOption {
	return func(e *Executor) {
		e.defaultCommand = strings.TrimSpace(cmd)
	}
}

// NewExecutor constructs a shell-based hook executor.
func NewExecutor(opts ...ExecutorOption) *Executor {
	exe := &Executor{timeout: defaultHookTimeout, errFn: func(events.EventType, error) {}}
	for _, opt := range opts {
		opt(exe)
	}
	if exe.timeout <= 0 {
		exe.timeout = defaultHookTimeout
	}
	return exe
}

// Register adds shell hooks to the executor. Hooks are matched by event type and selector.
func (e *Executor) Register(hooks ...ShellHook) {
	e.hooksMu.Lock()
	defer e.hooksMu.Unlock()
	e.hooks = append(e.hooks, hooks...)
}

// Publish executes matching hooks for the event using a background context.
// It preserves the previous API while delegating to Execute.
func (e *Executor) Publish(evt events.Event) error {
	_, err := e.Execute(context.Background(), evt)
	return err
}

// Execute runs all matching hooks for the provided event and returns their results.
func (e *Executor) Execute(ctx context.Context, evt events.Event) ([]Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := validateEvent(evt.Type); err != nil {
		return nil, err
	}

	var results []Result
	handler := middleware.Chain(func(c context.Context, ev events.Event) error {
		var err error
		results, err = e.runHooks(c, ev)
		return err
	}, e.mw...)

	if err := handler(ctx, evt); err != nil {
		e.report(evt.Type, err)
		return nil, err
	}
	return results, nil
}

// Close is present for API parity; no resources are held.
func (e *Executor) Close() {}

func (e *Executor) runHooks(ctx context.Context, evt events.Event) ([]Result, error) {
	hooks := e.matchingHooks(evt)
	if len(hooks) == 0 {
		return nil, nil
	}

	payload, err := buildPayload(evt)
	if err != nil {
		return nil, err
	}

	results := make([]Result, 0, len(hooks))
	for _, hook := range hooks {
		res, err := e.executeHook(ctx, hook, payload, evt)
		if err != nil {
			e.report(evt.Type, err)
			return nil, err
		}
		results = append(results, res)
	}
	return results, nil
}

func (e *Executor) matchingHooks(evt events.Event) []ShellHook {
	e.hooksMu.RLock()
	defer e.hooksMu.RUnlock()

	var matches []ShellHook
	for _, hook := range e.hooks {
		if hook.Event != evt.Type {
			continue
		}
		if hook.Selector.Match(evt) {
			matches = append(matches, hook)
		}
	}

	// Fallback: single default command bound to all events.
	if len(matches) == 0 && strings.TrimSpace(e.defaultCommand) != "" {
		matches = append(matches, ShellHook{Event: evt.Type, Command: e.defaultCommand})
	}
	return matches
}

func (e *Executor) executeHook(ctx context.Context, hook ShellHook, payload []byte, evt events.Event) (Result, error) {
	var res Result
	res.Event = evt

	cmdStr := strings.TrimSpace(hook.Command)
	if cmdStr == "" {
		cmdStr = e.defaultCommand
	}
	if cmdStr == "" {
		return res, errors.New("hooks: missing command")
	}

	deadline := effectiveTimeout(hook.Timeout, e.timeout)
	runCtx, cancel := context.WithTimeout(ctx, deadline)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "/bin/sh", "-c", cmdStr)
	cmd.Env = mergeEnv(os.Environ(), hook.Env)

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.Stdin = bytes.NewReader(payload)

	err := cmd.Run()
	outStr := stdout.String()
	errStr := stderr.String()

	// Handle context timeout explicitly.
	if errors.Is(runCtx.Err(), context.DeadlineExceeded) {
		if cmd.Process != nil {
			// nolint:errcheck // Process cleanup, error not actionable
			cmd.Process.Kill()
		}
		return res, fmt.Errorf("hooks: command timed out after %s: %s", deadline, errStr)
	}

	decision, exitCode, failure := classifyExit(err)
	res.Decision = decision
	res.ExitCode = exitCode
	res.Stdout = outStr
	res.Stderr = errStr

	if failure != nil {
		return res, fmt.Errorf("hooks: %w; stderr: %s", failure, errStr)
	}

	if evt.Type == events.PreToolUse && strings.TrimSpace(outStr) != "" {
		permission, err := decodePermission(outStr)
		if err != nil {
			return res, err
		}
		res.Permission = permission
	}

	return res, nil
}

func effectiveTimeout(hookTimeout, defaultTimeout time.Duration) time.Duration {
	if hookTimeout > 0 {
		return hookTimeout
	}
	if defaultTimeout > 0 {
		return defaultTimeout
	}
	return defaultHookTimeout
}

func classifyExit(runErr error) (Decision, int, error) {
	if runErr == nil {
		return DecisionAllow, 0, nil
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code := exitErr.ExitCode()
		switch code {
		case 0:
			return DecisionAllow, code, nil
		case 1:
			return DecisionDeny, code, nil
		case 2:
			return DecisionAsk, code, nil
		default:
			return DecisionError, code, fmt.Errorf("command exited with code %d", code)
		}
	}
	return DecisionError, -1, runErr
}

func decodePermission(out string) (PermissionDecision, error) {
	var parsed PermissionDecision
	trimmed := strings.TrimSpace(out)
	if err := json.Unmarshal([]byte(trimmed), &parsed); err != nil {
		return nil, fmt.Errorf("hooks: decode permissionDecision: %w", err)
	}
	return parsed, nil
}

func buildPayload(evt events.Event) ([]byte, error) {
	envelope := map[string]any{
		"hook_event_name": evt.Type,
	}
	if evt.SessionID != "" {
		envelope["session_id"] = evt.SessionID
	}

	switch p := evt.Payload.(type) {
	case events.ToolUsePayload:
		envelope["tool_input"] = sanitizedToolInput(p)
	case events.ToolResultPayload:
		envelope["tool_response"] = sanitizedToolResult(p)
	case events.PreCompactPayload:
		envelope["pre_compact"] = p
	case events.ContextCompactedPayload:
		envelope["context_compacted"] = p
	case events.SubagentStartPayload:
		envelope["subagent_start"] = p
	case events.SubagentStopPayload:
		envelope["subagent_stop"] = p
	case events.PermissionRequestPayload:
		envelope["permission_request"] = p
	case events.SessionPayload:
		envelope["session"] = p
	case events.NotificationPayload:
		envelope["notification"] = p
	case events.UserPromptPayload:
		envelope["user_prompt"] = p
	case events.StopPayload:
		envelope["stop"] = p
	case events.ModelSelectedPayload:
		envelope["model_selected"] = p
	case nil:
		// allowed
	default:
		return nil, fmt.Errorf("hooks: unsupported payload type %T", evt.Payload)
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("hooks: marshal payload: %w", err)
	}
	return data, nil
}

func sanitizedToolResult(p events.ToolResultPayload) map[string]any {
	out := map[string]any{
		"name": p.Name,
	}
	if p.Result != nil {
		out["result"] = p.Result
	}
	if p.Duration > 0 {
		out["duration_ms"] = p.Duration.Milliseconds()
	}
	if p.Err != nil {
		out["error"] = p.Err.Error()
	}
	return out
}

func sanitizedToolInput(p events.ToolUsePayload) map[string]any {
	return map[string]any{
		"name":   p.Name,
		"params": p.Params,
	}
}

func mergeEnv(base []string, extra map[string]string) []string {
	if len(extra) == 0 {
		return base
	}
	env := append([]string(nil), base...)
	for k, v := range extra {
		env = append(env, fmt.Sprintf("%s=%s", k, v))
	}
	return env
}

func extractToolName(payload any) string {
	switch p := payload.(type) {
	case events.ToolUsePayload:
		return p.Name
	case events.ToolResultPayload:
		return p.Name
	case events.SubagentStartPayload:
		return p.Name
	case events.SubagentStopPayload:
		return p.Name
	case events.PermissionRequestPayload:
		return p.ToolName
	default:
		return ""
	}
}

func validateEvent(t events.EventType) error {
	switch t {
	case events.PreToolUse, events.PostToolUse, events.PreCompact, events.ContextCompacted,
		events.Notification, events.UserPromptSubmit,
		events.SessionStart, events.SessionEnd, events.Stop, events.TokenUsage,
		events.SubagentStart, events.SubagentStop,
		events.PermissionRequest, events.ModelSelected:
		return nil
	default:
		return fmt.Errorf("hooks: unsupported event %s", t)
	}
}

func (e *Executor) report(t events.EventType, err error) {
	if e.errFn != nil && err != nil {
		e.errFn(t, err)
	}
}
