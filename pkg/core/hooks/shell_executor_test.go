package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/core/middleware"
	"github.com/stretchr/testify/require"
)

func TestExecuteSerializesPayloadAndParsesOutput(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.json")

	script := writeScript(t, dir, "dump_and_allow.sh", fmt.Sprintf(`#!/bin/sh
cat > "%s"
printf '{"decision":"allow","reason":"ok","hookSpecificOutput":{"permissionDecision":"allow","updatedInput":{"path":"/tmp/new"}}}'
`, payloadPath))

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script})

	evt := events.Event{
		Type:      events.PreToolUse,
		SessionID: "sess-42",
		Payload: events.ToolUsePayload{
			Name:   "Write",
			Params: map[string]any{"path": "/tmp/demo"},
		},
	}

	results, err := exec.Execute(context.Background(), evt)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(results) != 1 {
		t.Fatalf("expected 1 result, got %d", len(results))
	}

	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}

	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if got["hook_event_name"] != string(events.PreToolUse) {
		t.Fatalf("unexpected event name %v", got["hook_event_name"])
	}
	if got["session_id"] != "sess-42" {
		t.Fatalf("missing session id: %v", got["session_id"])
	}
	// Flat format: tool_name and tool_input at top level
	if got["tool_name"] != "Write" {
		t.Fatalf("tool_name mismatch: %v", got["tool_name"])
	}
	toolInput, ok := got["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input type mismatch: %T", got["tool_input"])
	}
	if toolInput["path"] != "/tmp/demo" {
		t.Fatalf("tool_input.path mismatch: %v", toolInput["path"])
	}

	output := results[0].Output
	if output == nil {
		t.Fatalf("expected non-nil Output")
	}
	if output.Decision != "allow" || output.Reason != "ok" {
		t.Fatalf("unexpected output: %+v", output)
	}
	if output.HookSpecificOutput == nil || output.HookSpecificOutput.PermissionDecision != "allow" {
		t.Fatalf("unexpected hookSpecificOutput: %+v", output.HookSpecificOutput)
	}
	if results[0].Decision != DecisionAllow || results[0].ExitCode != 0 {
		t.Fatalf("unexpected decision %s code %d", results[0].Decision, results[0].ExitCode)
	}
}

func TestExitCodeMapping(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	cases := []struct {
		code      int
		decision  Decision
		wantError bool
	}{
		{0, DecisionAllow, false},
		{1, DecisionNonBlocking, false},
		{2, DecisionBlockingError, true},
		{5, DecisionNonBlocking, false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(fmt.Sprintf("exit_%d", tc.code), func(t *testing.T) {
			t.Parallel()
			script := writeScript(t, dir, fmt.Sprintf("exit_%d.sh", tc.code), fmt.Sprintf("#!/bin/sh\nexit %d\n", tc.code))
			exec := NewExecutor()
			exec.Register(ShellHook{Event: events.Notification, Command: script})
			evt := events.Event{Type: events.Notification, Payload: events.NotificationPayload{Message: "hi"}}

			results, err := exec.Execute(context.Background(), evt)
			if tc.wantError {
				if err == nil {
					t.Fatalf("expected error for code %d", tc.code)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if len(results) != 1 || results[0].Decision != tc.decision || results[0].ExitCode != tc.code {
				t.Fatalf("unexpected result %+v", results)
			}
		})
	}
}

func TestTimeoutIsHonored(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 1\n")

	exec := NewExecutor(WithTimeout(100 * time.Millisecond))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err == nil || !strings.Contains(err.Error(), "timed out") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestStderrCapturedOnBlockingError(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 2 = blocking error
	script := writeScript(t, dir, "stderr.sh", "#!/bin/sh\necho boom >&2\nexit 2\n")

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestNonBlockingExitContinues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 3 = non-blocking, should not error
	script := writeScript(t, dir, "nonblock.sh", "#!/bin/sh\necho warning >&2\nexit 3\n")

	var reportedErr error
	exec := NewExecutor(WithErrorHandler(func(_ events.EventType, err error) {
		reportedErr = err
	}))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err != nil {
		t.Fatalf("non-blocking exit should not return error, got %v", err)
	}
	if len(results) != 1 || results[0].Decision != DecisionNonBlocking {
		t.Fatalf("unexpected result: %+v", results)
	}
	if reportedErr == nil || !strings.Contains(reportedErr.Error(), "warning") {
		t.Fatalf("expected non-blocking error to be reported, got %v", reportedErr)
	}
}

func TestSelectorFiltersMatcherTarget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "ok.sh", "#!/bin/sh\nexit 0\n")

	sel, err := NewSelector("^Write$", "")
	if err != nil {
		t.Fatal(err)
	}
	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script, Selector: sel})

	// Should match Write
	results, err := exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "Write", Params: map[string]any{}},
	})
	if err != nil || len(results) != 1 {
		t.Fatalf("expected match for Write, got %d results, err=%v", len(results), err)
	}

	// Should NOT match Read
	results, err = exec.Execute(context.Background(), events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "Read", Params: map[string]any{}},
	})
	if err != nil || len(results) != 0 {
		t.Fatalf("expected no match for Read, got %d results", len(results))
	}
}

func TestConcurrentCallsAreIsolated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 0.05\n")

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	var wg sync.WaitGroup
	errs := make([]error, 5)
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			_, errs[idx] = exec.Execute(context.Background(), events.Event{Type: events.Notification})
		}(i)
	}
	wg.Wait()
	for i, e := range errs {
		if e != nil {
			t.Fatalf("goroutine %d: %v", i, e)
		}
	}
}

func TestDefaultCommandFallbackAndPublishWrapper(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "default.sh", "#!/bin/sh\necho '{\"decision\":\"allow\"}'\n")

	exec := NewExecutor(WithCommand(script))
	// No hooks registered — should use default command
	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err != nil || len(results) != 1 {
		t.Fatalf("expected default command result, got %d, err=%v", len(results), err)
	}
	if results[0].Output == nil || results[0].Output.Decision != "allow" {
		t.Fatalf("expected allow decision from default command, got %+v", results[0])
	}

	// Publish wrapper
	if err := exec.Publish(events.Event{Type: events.Notification}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
}

func TestMiddlewareAndErrorHandler(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "ok.sh", "#!/bin/sh\nexit 0\n")

	var called bool
	mw := func(next middleware.Handler) middleware.Handler {
		return func(ctx context.Context, evt events.Event) error {
			called = true
			return next(ctx, evt)
		}
	}

	exec := NewExecutor(WithMiddleware(mw))
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if !called {
		t.Fatal("middleware was not called")
	}
}

func TestEnvIsMergedIntoCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "env.sh", "#!/bin/sh\necho $MY_HOOK_VAR >&2\n")

	exec := NewExecutor()
	exec.Register(ShellHook{
		Event:   events.Notification,
		Command: script,
		Env:     map[string]string{"MY_HOOK_VAR": "hello_hooks"},
	})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err != nil || len(results) != 1 {
		t.Fatalf("execute: %v", err)
	}
	if !strings.Contains(results[0].Stderr, "hello_hooks") {
		t.Fatalf("expected env var in stderr, got %q", results[0].Stderr)
	}
}

func TestBuildPayloadFlatFormat(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		evt    events.Event
		checks map[string]any
	}{
		{
			name: "ToolUsePayload",
			evt: events.Event{
				Type: events.PreToolUse, SessionID: "s1",
				Payload: events.ToolUsePayload{Name: "Bash", Params: map[string]any{"command": "ls"}, ToolUseID: "tu1"},
			},
			checks: map[string]any{"tool_name": "Bash", "tool_use_id": "tu1", "hook_event_name": "PreToolUse"},
		},
		{
			name: "ToolResultPayload",
			evt: events.Event{
				Type:    events.PostToolUse,
				Payload: events.ToolResultPayload{Name: "Bash", Result: "ok", ToolUseID: "tu2"},
			},
			checks: map[string]any{"tool_name": "Bash", "tool_use_id": "tu2"},
		},
		{
			name: "NotificationPayload",
			evt: events.Event{
				Type:    events.Notification,
				Payload: events.NotificationPayload{Title: "t", Message: "m", NotificationType: "info"},
			},
			checks: map[string]any{"title": "t", "message": "m", "notification_type": "info"},
		},
		{
			name: "StopPayload",
			evt: events.Event{
				Type:    events.Stop,
				Payload: events.StopPayload{Reason: "done", StopHookActive: true},
			},
			checks: map[string]any{"reason": "done", "stop_hook_active": true},
		},
		{
			name: "UserPromptPayload",
			evt: events.Event{
				Type:    events.UserPromptSubmit,
				Payload: events.UserPromptPayload{Prompt: "hello"},
			},
			checks: map[string]any{"user_prompt": "hello"},
		},
		{
			name: "PreCompactPayload",
			evt: events.Event{
				Type:    events.PreCompact,
				Payload: events.PreCompactPayload{Trigger: "auto", CustomInstructions: "keep it short"},
			},
			checks: map[string]any{"trigger": "auto", "custom_instructions": "keep it short"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data, err := buildPayload(tc.evt)
			if err != nil {
				t.Fatalf("buildPayload: %v", err)
			}
			var got map[string]any
			if err := json.Unmarshal(data, &got); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			for k, want := range tc.checks {
				v, ok := got[k]
				if !ok {
					t.Errorf("missing key %q", k)
					continue
				}
				wantStr := fmt.Sprintf("%v", want)
				gotStr := fmt.Sprintf("%v", v)
				if wantStr != gotStr {
					t.Errorf("key %q: want %v, got %v", k, want, v)
				}
			}
		})
	}
}

func TestExtractMatcherTargetAllTypes(t *testing.T) {
	t.Parallel()
	cases := []struct {
		eventType events.EventType
		payload   any
		want      string
	}{
		{events.PreToolUse, events.ToolUsePayload{Name: "Bash"}, "Bash"},
		{events.PostToolUse, events.ToolResultPayload{Name: "Read"}, "Read"},
		{events.PostToolUseFailure, events.ToolResultPayload{Name: "Write"}, "Write"},
		{events.PermissionRequest, events.PermissionRequestPayload{ToolName: "Glob"}, "Glob"},
		{events.SessionStart, events.SessionStartPayload{Source: "cli"}, "cli"},
		{events.SessionEnd, events.SessionEndPayload{Reason: "user_exit"}, "user_exit"},
		{events.Notification, events.NotificationPayload{NotificationType: "info"}, "info"},
		{events.PreCompact, events.PreCompactPayload{Trigger: "auto"}, "auto"},
		{events.SubagentStart, events.SubagentStartPayload{AgentType: "code", Name: "a"}, "code"},
		{events.SubagentStart, events.SubagentStartPayload{Name: "fallback"}, "fallback"},
		{events.SubagentStop, events.SubagentStopPayload{AgentType: "code", Name: "a"}, "code"},
		{events.SubagentStop, events.SubagentStopPayload{Name: "fallback"}, "fallback"},
		{events.UserPromptSubmit, events.UserPromptPayload{Prompt: "hi"}, ""},
		{events.Stop, events.StopPayload{Reason: "done"}, ""},
	}
	for _, tc := range cases {
		got := extractMatcherTarget(tc.eventType, tc.payload)
		if got != tc.want {
			t.Errorf("%s: want %q, got %q", tc.eventType, tc.want, got)
		}
	}
}

func TestClassifyExitHelpers(t *testing.T) {
	t.Parallel()
	// nil error = allow
	d, code := classifyExit(nil)
	if d != DecisionAllow || code != 0 {
		t.Fatalf("nil: want allow/0, got %s/%d", d, code)
	}
	// non-ExitError = blocking
	d, code = classifyExit(fmt.Errorf("not found"))
	if d != DecisionBlockingError || code != -1 {
		t.Fatalf("non-exit: want blocking/-1, got %s/%d", d, code)
	}
}

func TestDecodeHookOutput(t *testing.T) {
	t.Parallel()
	out, err := decodeHookOutput(`{"decision":"deny","reason":"nope"}`)
	if err != nil {
		t.Fatal(err)
	}
	if out.Decision != "deny" || out.Reason != "nope" {
		t.Fatalf("unexpected: %+v", out)
	}
	// Invalid JSON
	_, err = decodeHookOutput(`{bad`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestDecisionStringer(t *testing.T) {
	t.Parallel()
	if DecisionAllow.String() != "allow" {
		t.Fatalf("allow: %s", DecisionAllow)
	}
	if DecisionBlockingError.String() != "blocking_error" {
		t.Fatalf("blocking: %s", DecisionBlockingError)
	}
	if DecisionNonBlocking.String() != "non_blocking" {
		t.Fatalf("non_blocking: %s", DecisionNonBlocking)
	}
}

func TestValidateEventRejectsUnsupported(t *testing.T) {
	t.Parallel()
	if err := validateEvent("BogusEvent"); err == nil {
		t.Fatal("expected error for unsupported event")
	}
	if err := validateEvent(events.PreToolUse); err != nil {
		t.Fatalf("PreToolUse should be valid: %v", err)
	}
}

func TestNewExecutorZeroTimeout(t *testing.T) {
	t.Parallel()
	exec := NewExecutor(WithTimeout(0))
	if exec.timeout != defaultHookTimeout {
		t.Fatalf("expected default timeout, got %v", exec.timeout)
	}
}

func TestSelectorMatchNoToolName(t *testing.T) {
	t.Parallel()
	sel, err := NewSelector("^Bash$", "")
	require.NoError(t, err)
	// Notification has no tool name — matcher target is NotificationType
	evt := events.Event{Type: events.Notification, Payload: events.NotificationPayload{Message: "hi"}}
	if sel.Match(evt) {
		t.Fatal("expected no match when matcher target is empty")
	}
}

func TestSelectorPayloadPattern(t *testing.T) {
	t.Parallel()
	sel, err := NewSelector("", `"command":"ls"`)
	require.NoError(t, err)
	evt := events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "Bash", Params: map[string]any{"command": "ls"}},
	}
	if !sel.Match(evt) {
		t.Fatal("expected payload pattern to match")
	}
	evt2 := events.Event{
		Type:    events.PreToolUse,
		Payload: events.ToolUsePayload{Name: "Bash", Params: map[string]any{"command": "rm"}},
	}
	if sel.Match(evt2) {
		t.Fatal("expected payload pattern NOT to match")
	}
}

func TestAsyncHookFireAndForget(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "async_marker")
	errCh := make(chan error, 1)
	exec := NewExecutor(
		WithWorkDir(dir),
		WithErrorHandler(func(_ events.EventType, err error) {
			if err == nil {
				return
			}
			select {
			case errCh <- err:
			default:
			}
		}),
	)
	exec.Register(ShellHook{Event: events.Notification, Command: ": > async_marker", Async: true})

	results, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err != nil {
		t.Fatalf("async execute: %v", err)
	}
	// Async hooks don't return results
	if len(results) != 0 {
		t.Fatalf("expected 0 results for async, got %d", len(results))
	}
	// Wait for async to complete on slower systems.
	deadline := time.Now().Add(10 * time.Second)
	for {
		select {
		case execErr := <-errCh:
			t.Fatalf("async hook returned error: %v", execErr)
		default:
		}
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			select {
			case execErr := <-errCh:
				t.Fatalf("async hook returned error: %v", execErr)
			default:
			}
			t.Fatal("async hook did not execute")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func TestOnceHookExecutesOnlyOnce(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	counter := filepath.Join(dir, "counter")
	// Append a line each time the hook runs
	script := writeScript(t, dir, "once.sh", fmt.Sprintf("#!/bin/sh\necho x >> %q\n", counter))

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Notification, Command: script, Once: true, Name: "once-hook"})

	evt := events.Event{Type: events.Notification, SessionID: "s1"}
	for i := 0; i < 3; i++ {
		_, err := exec.Execute(context.Background(), evt)
		if err != nil {
			t.Fatalf("execute %d: %v", i, err)
		}
	}
	data, err := os.ReadFile(counter)
	require.NoError(t, err)
	lines := strings.Count(strings.TrimSpace(string(data)), "x")
	if lines != 1 {
		t.Fatalf("expected once hook to run 1 time, ran %d times", lines)
	}
}

func TestBuildPayloadSessionStartEnd(t *testing.T) {
	t.Parallel()
	// SessionStart
	data, err := buildPayload(events.Event{
		Type:    events.SessionStart,
		Payload: events.SessionStartPayload{SessionID: "s1", Source: "cli", Model: "claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]any
	require.NoError(t, json.Unmarshal(data, &got))
	if got["source"] != "cli" || got["model"] != "claude" {
		t.Fatalf("SessionStart: %v", got)
	}

	// SessionEnd
	data, err = buildPayload(events.Event{
		Type:    events.SessionEnd,
		Payload: events.SessionEndPayload{SessionID: "s1", Reason: "user_exit"},
	})
	if err != nil {
		t.Fatal(err)
	}
	require.NoError(t, json.Unmarshal(data, &got))
	if got["reason"] != "user_exit" {
		t.Fatalf("SessionEnd: %v", got)
	}
}

func TestBuildPayloadUnsupportedType(t *testing.T) {
	t.Parallel()
	_, err := buildPayload(events.Event{Type: events.PreToolUse, Payload: struct{ X int }{42}})
	if err == nil || !strings.Contains(err.Error(), "unsupported payload type") {
		t.Fatalf("expected unsupported payload error, got %v", err)
	}
}

func TestExecuteAcceptsAllValidEvents(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "noop.sh", "#!/bin/sh\nexit 0\n")

	validEvents := []events.EventType{
		events.PreToolUse, events.PostToolUse, events.PostToolUseFailure,
		events.PreCompact, events.ContextCompacted, events.Notification,
		events.UserPromptSubmit, events.SessionStart, events.SessionEnd,
		events.Stop, events.SubagentStart, events.SubagentStop,
		events.PermissionRequest, events.ModelSelected,
	}
	for _, et := range validEvents {
		exec := NewExecutor()
		exec.Register(ShellHook{Event: et, Command: script})
		_, err := exec.Execute(context.Background(), events.Event{Type: et})
		if err != nil {
			t.Errorf("event %s should be valid: %v", et, err)
		}
	}
}

// writeScript creates an executable shell script in dir and returns its path.
func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o700); err != nil { //nolint:gosec // test helper needs executable scripts
		t.Fatalf("writeScript: %v", err)
	}
	return path
}
