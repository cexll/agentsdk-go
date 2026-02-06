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

// Default timeouts per Claude Code spec.
const (
	defaultCommandTimeout = 600 * time.Second
	defaultPromptTimeout  = 30 * time.Second
	defaultAgentTimeout   = 60 * time.Second
	defaultHookTimeout    = defaultCommandTimeout
)

// Decision captures the permission outcome encoded in the hook exit code.
// Claude Code spec: 0=success(parse JSON), 2=blocking error(stderr),
// other=non-blocking(log stderr & continue).
type Decision int

const (
	DecisionAllow         Decision = iota // exit 0: success, parse JSON stdout
	DecisionBlockingError                 // exit 2: blocking error, stderr is message
	DecisionNonBlocking                   // exit 1,3+: non-blocking, log & continue
)

func (d Decision) String() string {
	switch d {
	case DecisionAllow:
		return "allow"
	case DecisionBlockingError:
		return "blocking_error"
	default:
		return "non_blocking"
	}
}

// HookOutput is the structured JSON output from hooks on exit 0.
type HookOutput struct {
	Continue    *bool  `json:"continue,omitempty"`
	StopReason  string `json:"stopReason,omitempty"`
	Decision    string `json:"decision,omitempty"`
	Reason      string `json:"reason,omitempty"`
	SystemMessage string `json:"systemMessage,omitempty"`

	HookSpecificOutput *HookSpecificOutput `json:"hookSpecificOutput,omitempty"`
}

// HookSpecificOutput carries event-specific fields from hook JSON output.
type HookSpecificOutput struct {
	HookEventName string `json:"hookEventName,omitempty"`

	// PreToolUse specific
	PermissionDecision       string         `json:"permissionDecision,omitempty"`
	PermissionDecisionReason string         `json:"permissionDecisionReason,omitempty"`
	UpdatedInput             map[string]any `json:"updatedInput,omitempty"`
	AdditionalContext        string         `json:"additionalContext,omitempty"`
}

// Result captures the full outcome of executing a shell hook.
type Result struct {
	Event    events.Event
	Decision Decision
	ExitCode int
	Output   *HookOutput // parsed JSON stdout on exit 0
	Stdout   string
	Stderr   string
}

// Selector filters hooks by matcher target and/or payload pattern.
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
		target := extractMatcherTarget(evt.Type, evt.Payload)
		if target == "" || !s.ToolName.MatchString(target) {
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
	Event         events.EventType
	Command       string
	Selector      Selector
	Timeout       time.Duration
	Env           map[string]string
	Name          string // optional label for debugging
	Async         bool   // fire-and-forget execution
	Once          bool   // execute only once per session
	StatusMessage string // status message shown during execution
}

// Executor executes hooks by spawning shell commands with JSON stdin payloads.
type Executor struct {
	hooks   []ShellHook
	hooksMu sync.RWMutex

	mw      []middleware.Middleware
	timeout time.Duration
	errFn   func(events.EventType, error)
	workDir string

	defaultCommand string

	// onceTracker tracks which Once hooks have already executed per session.
	onceTracker sync.Map // key: "sessionID:hookName" -> struct{}
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

// WithWorkDir sets the working directory for hook command execution.
func WithWorkDir(dir string) ExecutorOption {
	return func(e *Executor) {
		e.workDir = dir
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
func (e *Executor) Close() {
	_ = e
}

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
		// Check Once constraint
		if hook.Once && hook.Name != "" {
			key := evt.SessionID + ":" + hook.Name
			if _, loaded := e.onceTracker.LoadOrStore(key, struct{}{}); loaded {
				continue
			}
		}

		// Async hooks: fire-and-forget
		if hook.Async {
			go func(h ShellHook, p []byte, ev events.Event) {
				_, err := e.executeHook(context.Background(), h, p, ev)
				if err != nil {
					e.report(ev.Type, err)
				}
			}(hook, payload, evt)
			continue
		}

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
	if e.workDir != "" {
		cmd.Dir = e.workDir
	}

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

	decision, exitCode := classifyExit(err)
	res.Decision = decision
	res.ExitCode = exitCode
	res.Stdout = outStr
	res.Stderr = errStr

	switch decision {
	case DecisionAllow:
		// Exit 0: parse JSON stdout if present
		if trimmed := strings.TrimSpace(outStr); trimmed != "" {
			output, parseErr := decodeHookOutput(trimmed)
			if parseErr != nil {
				return res, parseErr
			}
			res.Output = output
		}
	case DecisionBlockingError:
		// Exit 2: blocking error, stderr is the error message
		return res, fmt.Errorf("hooks: blocking error: %s", errStr)
	case DecisionNonBlocking:
		// Exit 1, 3+: non-blocking, log stderr and continue
		if errStr != "" {
			e.report(evt.Type, fmt.Errorf("hooks: non-blocking (exit %d): %s", exitCode, errStr))
		}
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

// classifyExit maps process exit codes to Decision per Claude Code spec.
// 0 = success (parse JSON), 2 = blocking error, other = non-blocking.
func classifyExit(runErr error) (Decision, int) {
	if runErr == nil {
		return DecisionAllow, 0
	}
	var exitErr *exec.ExitError
	if errors.As(runErr, &exitErr) {
		code := exitErr.ExitCode()
		switch code {
		case 0:
			return DecisionAllow, code
		case 2:
			return DecisionBlockingError, code
		default:
			return DecisionNonBlocking, code
		}
	}
	// Non-exit errors (e.g., command not found) are blocking.
	return DecisionBlockingError, -1
}

func decodeHookOutput(out string) (*HookOutput, error) {
	var parsed HookOutput
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		return nil, fmt.Errorf("hooks: decode hook output: %w", err)
	}
	return &parsed, nil
}

func buildPayload(evt events.Event) ([]byte, error) {
	envelope := map[string]any{
		"hook_event_name": evt.Type,
	}
	if evt.SessionID != "" {
		envelope["session_id"] = evt.SessionID
	}

	// Flatten payload fields into envelope per Claude Code spec.
	switch p := evt.Payload.(type) {
	case events.ToolUsePayload:
		envelope["tool_name"] = p.Name
		envelope["tool_input"] = p.Params
		if p.ToolUseID != "" {
			envelope["tool_use_id"] = p.ToolUseID
		}
	case events.ToolResultPayload:
		envelope["tool_name"] = p.Name
		if p.Params != nil {
			envelope["tool_input"] = p.Params
		}
		if p.ToolUseID != "" {
			envelope["tool_use_id"] = p.ToolUseID
		}
		if p.Result != nil {
			envelope["tool_result"] = p.Result
		}
		if p.Duration > 0 {
			envelope["duration_ms"] = p.Duration.Milliseconds()
		}
		if p.Err != nil {
			envelope["error"] = p.Err.Error()
			envelope["is_error"] = true
		}
	case events.PreCompactPayload:
		envelope["trigger"] = p.Trigger
		if p.CustomInstructions != "" {
			envelope["custom_instructions"] = p.CustomInstructions
		}
		envelope["estimated_tokens"] = p.EstimatedTokens
		envelope["token_limit"] = p.TokenLimit
		envelope["threshold"] = p.Threshold
		envelope["preserve_count"] = p.PreserveCount
	case events.ContextCompactedPayload:
		envelope["context_compacted"] = p
	case events.SubagentStartPayload:
		envelope["agent_name"] = p.Name
		if p.AgentID != "" {
			envelope["agent_id"] = p.AgentID
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SubagentStopPayload:
		envelope["agent_name"] = p.Name
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		if p.AgentID != "" {
			envelope["agent_id"] = p.AgentID
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.TranscriptPath != "" {
			envelope["transcript_path"] = p.TranscriptPath
		}
		envelope["stop_hook_active"] = p.StopHookActive
	case events.PermissionRequestPayload:
		envelope["tool_name"] = p.ToolName
		if p.ToolParams != nil {
			envelope["tool_input"] = p.ToolParams
		}
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
	case events.SessionStartPayload:
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Source != "" {
			envelope["source"] = p.Source
		}
		if p.Model != "" {
			envelope["model"] = p.Model
		}
		if p.AgentType != "" {
			envelope["agent_type"] = p.AgentType
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SessionEndPayload:
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.SessionPayload:
		// Legacy compat: flatten session payload
		if p.SessionID != "" {
			envelope["session_id"] = p.SessionID
		}
		if p.Metadata != nil {
			envelope["metadata"] = p.Metadata
		}
	case events.NotificationPayload:
		if p.Title != "" {
			envelope["title"] = p.Title
		}
		envelope["message"] = p.Message
		if p.NotificationType != "" {
			envelope["notification_type"] = p.NotificationType
		}
		if p.Meta != nil {
			envelope["metadata"] = p.Meta
		}
	case events.UserPromptPayload:
		envelope["user_prompt"] = p.Prompt
	case events.StopPayload:
		if p.Reason != "" {
			envelope["reason"] = p.Reason
		}
		envelope["stop_hook_active"] = p.StopHookActive
	case events.ModelSelectedPayload:
		envelope["model_selected"] = p
	case nil:
		// allowed
	default:
		return nil, fmt.Errorf("hooks: unsupported payload type %T", evt.Payload)
	}

	// Add cwd to all payloads
	if cwd, err := os.Getwd(); err == nil {
		envelope["cwd"] = cwd
	}

	data, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("hooks: marshal payload: %w", err)
	}
	return data, nil
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

// extractMatcherTarget returns the string to match against the hook's selector
// based on event type and payload, per Claude Code spec:
// - PreToolUse/PostToolUse/PostToolUseFailure/PermissionRequest → tool name
// - SessionStart → source; SessionEnd → reason
// - Notification → notification_type; PreCompact → trigger
// - SubagentStart/SubagentStop → agent_type (fallback to name)
// - UserPromptSubmit/Stop → always match (return empty to skip matcher)
func extractMatcherTarget(eventType events.EventType, payload any) string {
	switch eventType {
	case events.PreToolUse:
		if p, ok := payload.(events.ToolUsePayload); ok {
			return p.Name
		}
	case events.PostToolUse, events.PostToolUseFailure:
		if p, ok := payload.(events.ToolResultPayload); ok {
			return p.Name
		}
	case events.PermissionRequest:
		if p, ok := payload.(events.PermissionRequestPayload); ok {
			return p.ToolName
		}
	case events.SessionStart:
		if p, ok := payload.(events.SessionStartPayload); ok {
			return p.Source
		}
		if p, ok := payload.(events.SessionPayload); ok {
			if src, ok := p.Metadata["source"].(string); ok {
				return src
			}
		}
	case events.SessionEnd:
		if p, ok := payload.(events.SessionEndPayload); ok {
			return p.Reason
		}
		if p, ok := payload.(events.SessionPayload); ok {
			if reason, ok := p.Metadata["reason"].(string); ok {
				return reason
			}
		}
	case events.Notification:
		if p, ok := payload.(events.NotificationPayload); ok {
			return p.NotificationType
		}
	case events.PreCompact:
		if p, ok := payload.(events.PreCompactPayload); ok {
			return p.Trigger
		}
	case events.SubagentStart:
		if p, ok := payload.(events.SubagentStartPayload); ok {
			if p.AgentType != "" {
				return p.AgentType
			}
			return p.Name
		}
	case events.SubagentStop:
		if p, ok := payload.(events.SubagentStopPayload); ok {
			if p.AgentType != "" {
				return p.AgentType
			}
			return p.Name
		}
	case events.UserPromptSubmit, events.Stop:
		// These events always match (no matcher support)
		return ""
	}
	return ""
}

func validateEvent(t events.EventType) error {
	switch t {
	case events.PreToolUse, events.PostToolUse, events.PostToolUseFailure, events.PreCompact, events.ContextCompacted,
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
