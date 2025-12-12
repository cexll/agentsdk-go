package hooks

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/core/middleware"
)

func TestExecuteSerializesPayloadAndParsesPermission(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "payload.json")

	script := writeScript(t, dir, "dump_and_allow.sh", fmt.Sprintf(`#!/bin/sh
cat > "%s"
printf '{"permission":"allow","reason":"ok"}'
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
	toolInput, ok := got["tool_input"].(map[string]any)
	if !ok {
		t.Fatalf("tool_input type mismatch: %T", got["tool_input"])
	}
	if toolInput["name"] != "Write" {
		t.Fatalf("tool name mismatch: %v", toolInput["name"])
	}

	perm := results[0].Permission
	if perm["permission"] != "allow" || perm["reason"] != "ok" {
		t.Fatalf("unexpected permission payload: %v", perm)
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
		{1, DecisionDeny, false},
		{2, DecisionAsk, false},
		{5, DecisionError, true},
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

func TestStderrCapturedOnFailure(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "stderr.sh", "#!/bin/sh\necho boom >&2\nexit 3\n")

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Notification, Command: script})

	_, err := exec.Execute(context.Background(), events.Event{Type: events.Notification})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stderr in error, got %v", err)
	}
}

func TestSelectorFiltersToolName(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "marker.txt")
	script := writeScript(t, dir, "marker.sh", fmt.Sprintf("#!/bin/sh\nprintf 'hit' > %s\n", marker))

	selector, err := NewSelector("Write|Edit", "")
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script, Selector: selector})

	matchEvt := events.Event{Type: events.PreToolUse, Payload: events.ToolUsePayload{Name: "WriteFile"}}
	if _, err := exec.Execute(context.Background(), matchEvt); err != nil {
		t.Fatalf("execute match: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected marker file to exist: %v", err)
	}

	_ = os.Remove(marker)
	missEvt := events.Event{Type: events.PreToolUse, Payload: events.ToolUsePayload{Name: "List"}}
	if _, err := exec.Execute(context.Background(), missEvt); err != nil {
		t.Fatalf("execute miss: %v", err)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("marker should not be recreated on selector miss")
	}
}

func TestConcurrentCallsAreIsolated(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "session_echo.sh", "#!/bin/sh\ncat\n")

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PreToolUse, Command: script})

	sessions := []string{"s1", "s2", "s3"}
	results := make([]PermissionDecision, len(sessions))
	wg := sync.WaitGroup{}
	for i, id := range sessions {
		i, id := i, id
		wg.Add(1)
		go func() {
			defer wg.Done()
			evt := events.Event{Type: events.PreToolUse, SessionID: id, Payload: events.ToolUsePayload{Name: "Write"}}
			res, err := exec.Execute(context.Background(), evt)
			if err != nil || len(res) != 1 {
				t.Errorf("execution failed: %v", err)
				return
			}
			results[i] = res[0].Permission
		}()
	}
	wg.Wait()

	for i, id := range sessions {
		if results[i] == nil || results[i]["session_id"] != id {
			t.Fatalf("session %s got wrong permission payload %v", id, results[i])
		}
	}
}

func TestDefaultCommandFallbackAndPublishWrapper(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "default_marker.txt")
	script := writeScript(t, dir, "default.sh", fmt.Sprintf("#!/bin/sh\nprintf done > %s\n", marker))

	exec := NewExecutor(WithCommand(script))
	if err := exec.Publish(events.Event{Type: events.Notification}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	exec.Close()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected default command to run: %v", err)
	}
}

func TestMiddlewareAndErrorHandler(t *testing.T) {
	t.Parallel()
	called := false
	errCalled := false

	exec := NewExecutor(
		WithMiddleware(func(next middleware.Handler) middleware.Handler {
			return func(ctx context.Context, evt events.Event) error {
				called = true
				return next(ctx, evt)
			}
		}),
		WithErrorHandler(func(events.EventType, error) { errCalled = true }),
	)
	// Missing command triggers an error path.
	exec.Register(ShellHook{Event: events.Notification})

	if _, err := exec.Execute(context.Background(), events.Event{Type: events.Notification}); err == nil {
		t.Fatalf("expected error for missing command")
	}
	if !called {
		t.Fatalf("middleware not invoked")
	}
	if !errCalled {
		t.Fatalf("error handler not invoked")
	}
}

func TestSanitizedToolResultSerialization(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	payloadPath := filepath.Join(dir, "post_payload.json")
	script := writeScript(t, dir, "post.sh", fmt.Sprintf("#!/bin/sh\ncat > %s\n", payloadPath))

	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.PostToolUse, Command: script})

	errExample := fmt.Errorf("boom")
	evt := events.Event{Type: events.PostToolUse, Payload: events.ToolResultPayload{Name: "Edit", Duration: 120 * time.Millisecond, Err: errExample}}
	if _, err := exec.Execute(context.Background(), evt); err != nil {
		t.Fatalf("execute: %v", err)
	}

	raw, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	resp, ok := decoded["tool_response"].(map[string]any)
	if !ok {
		t.Fatalf("tool_response type mismatch: %T", decoded["tool_response"])
	}
	if resp["error"] != "boom" {
		t.Fatalf("expected error string, got %v", resp["error"])
	}
	duration, ok := resp["duration_ms"].(float64)
	if !ok {
		t.Fatalf("duration_ms type mismatch: %T", resp["duration_ms"])
	}
	if duration < 119 {
		t.Fatalf("duration missing: %v", duration)
	}
}

func TestEnvIsMergedIntoCommand(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	output := filepath.Join(dir, "env.txt")
	script := writeScript(t, dir, "env.sh", fmt.Sprintf("#!/bin/sh\nprintf \"%%s\" \"$CUSTOM_VAR\" > %s\n", output))

	exec := NewExecutor()
	selector, err := NewSelector("", "")
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	exec.Register(ShellHook{Event: events.Notification, Command: script, Selector: selector, Env: map[string]string{"CUSTOM_VAR": "hello"}})

	if _, err := exec.Execute(context.Background(), events.Event{Type: events.Notification}); err != nil {
		t.Fatalf("execute: %v", err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("read env output: %v", err)
	}
	if string(data) != "hello" {
		t.Fatalf("env not passed, got %q", string(data))
	}
}

func TestBuildPayloadSerializesNewPayloadTypes(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		evt       events.Event
		key       string
		wantField string
	}{
		{
			name: "session_end",
			evt: events.Event{
				Type:    events.SessionEnd,
				Payload: events.SessionPayload{SessionID: "sess", Metadata: map[string]any{"k": "v"}},
			},
			key:       "session",
			wantField: "SessionID",
		},
		{
			name: "subagent_start",
			evt: events.Event{
				Type:    events.SubagentStart,
				Payload: events.SubagentStartPayload{Name: "sa", AgentID: "agent-1"},
			},
			key:       "subagent_start",
			wantField: "AgentID",
		},
		{
			name: "subagent_stop",
			evt: events.Event{
				Type: events.SubagentStop,
				Payload: events.SubagentStopPayload{
					Name:           "sa",
					Reason:         "done",
					AgentID:        "agent-1",
					TranscriptPath: "/tmp/t.json",
				},
			},
			key:       "subagent_stop",
			wantField: "TranscriptPath",
		},
		{
			name: "permission_request",
			evt: events.Event{
				Type: events.PermissionRequest,
				Payload: events.PermissionRequestPayload{
					ToolName:   "Bash",
					ToolParams: map[string]any{"cmd": "ls"},
					Reason:     "test",
				},
			},
			key:       "permission_request",
			wantField: "ToolName",
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			raw, err := buildPayload(tc.evt)
			if err != nil {
				t.Fatalf("buildPayload: %v", err)
			}
			var decoded map[string]any
			if err := json.Unmarshal(raw, &decoded); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			block, ok := decoded[tc.key].(map[string]any)
			if !ok {
				t.Fatalf("expected %s block, got %T", tc.key, decoded[tc.key])
			}
			if _, ok := block[tc.wantField]; !ok {
				t.Fatalf("missing %s in %s: %+v", tc.wantField, tc.key, block)
			}
		})
	}
}

func TestExecuteAcceptsNewEvents(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	types := []events.EventType{
		events.SessionStart,
		events.SessionEnd,
		events.SubagentStart,
		events.SubagentStop,
		events.PermissionRequest,
	}
	for _, typ := range types {
		typ := typ
		t.Run(string(typ), func(t *testing.T) {
			t.Parallel()
			if _, err := exec.Execute(context.Background(), events.Event{Type: typ}); err != nil {
				t.Fatalf("expected %s to be supported: %v", typ, err)
			}
		})
	}
}

func TestValidateEventRejectsUnsupported(t *testing.T) {
	t.Parallel()
	exec := NewExecutor()
	if _, err := exec.Execute(context.Background(), events.Event{Type: events.EventType("Unknown")}); err == nil {
		t.Fatalf("expected unsupported event error")
	}
}

func TestSelectorPayloadPattern(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	marker := filepath.Join(dir, "pattern.txt")
	script := writeScript(t, dir, "pattern.sh", fmt.Sprintf("#!/bin/sh\nprintf match > %s\n", marker))

	sel, err := NewSelector("", "alert")
	if err != nil {
		t.Fatalf("selector: %v", err)
	}
	exec := NewExecutor()
	exec.Register(ShellHook{Event: events.Notification, Command: script, Selector: sel})

	evt := events.Event{Type: events.Notification, Payload: events.NotificationPayload{Message: "alert: hi"}}
	if _, err := exec.Execute(context.Background(), evt); err != nil {
		t.Fatalf("execute: %v", err)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("expected marker from pattern match")
	}
}

func TestHelperBranches(t *testing.T) {
	t.Parallel()
	if got := effectiveTimeout(2*time.Second, 0); got != 2*time.Second {
		t.Fatalf("expected hook timeout override, got %s", got)
	}
	if got := effectiveTimeout(0, 0); got != defaultHookTimeout {
		t.Fatalf("expected defaultHookTimeout fallback, got %s", got)
	}

	if dec, code, err := classifyExit(errors.New("boom")); dec != DecisionError || code != -1 || err == nil {
		t.Fatalf("classifyExit fallback mismatch: %v %d %v", dec, code, err)
	}
	if _, err := decodePermission("not json"); err == nil {
		t.Fatalf("expected decodePermission error")
	}

	if _, err := buildPayload(events.Event{Type: events.PreToolUse, Payload: 123}); err == nil {
		t.Fatalf("expected buildPayload to fail on unsupported type")
	}
	if _, err := buildPayload(events.Event{Type: events.Stop}); err != nil {
		t.Fatalf("nil payload should be allowed: %v", err)
	}

	if name := extractToolName(events.ToolResultPayload{Name: "after"}); name != "after" {
		t.Fatalf("extractToolName failed: %s", name)
	}

	exec := NewExecutor()
	exec.Close() // no-op but counted for coverage
}

func TestDecisionStringer(t *testing.T) {
	t.Parallel()
	if DecisionAllow.String() != "allow" || DecisionDeny.String() != "deny" || DecisionAsk.String() != "ask" || DecisionError.String() != "error" {
		t.Fatalf("decision stringer mismatch")
	}
}

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		t.Fatalf("create script: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatalf("write script: %v", err)
	}
	// Sync to avoid "Text file busy" race condition in CI
	if err := f.Sync(); err != nil {
		f.Close()
		t.Fatalf("sync script: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close script: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	return path
}
