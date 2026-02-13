package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func TestPreToolUseAllowsInputModification(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 0 with JSON containing hookSpecificOutput.updatedInput
	script := writeScript(t, dir, "modify.sh", shScript(
		"#!/bin/sh\nprintf '{\"hookSpecificOutput\":{\"updatedInput\":{\"k\":\"v2\"}}}'\n",
		"@echo {\"hookSpecificOutput\":{\"updatedInput\":{\"k\":\"v2\"}}}\r\n",
	))

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreToolUse, Command: script})
	adapter := &runtimeHookAdapter{executor: exec}

	params, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{
		Name:   "Echo",
		Params: map[string]any{"k": "v1"},
	})
	if err != nil {
		t.Fatalf("pre tool use: %v", err)
	}
	if params["k"] != "v2" {
		t.Fatalf("expected modified param, got %+v", params)
	}
}

func TestPreToolUseDeniesExecution(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 0 with JSON decision=deny
	script := writeScript(t, dir, "deny.sh", shScript(
		"#!/bin/sh\nprintf '{\"decision\":\"deny\",\"reason\":\"blocked\"}'\n",
		"@echo {\"decision\":\"deny\",\"reason\":\"blocked\"}\r\n",
	))

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreToolUse, Command: script})
	adapter := &runtimeHookAdapter{executor: exec}

	_, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{
		Name:   "Echo",
		Params: map[string]any{"k": "v"},
	})
	if err == nil {
		t.Fatalf("expected deny error")
	}
	if !errors.Is(err, ErrToolUseDenied) {
		t.Fatalf("expected ErrToolUseDenied, got %v", err)
	}
}

func TestPreToolUseBlockingError(t *testing.T) {
	t.Parallel()

	// Exit 2 = blocking error
	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{
		Event:   coreevents.PreToolUse,
		Command: shCmd("echo blocked >&2; exit 2", "echo blocked >&2 & exit /b 2"),
	})
	adapter := &runtimeHookAdapter{executor: exec}

	_, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{
		Name:   "Echo",
		Params: map[string]any{"k": "v"},
	})
	if err == nil {
		t.Fatalf("expected blocking error")
	}
}

func TestPreToolUseAsksForApproval(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// Exit 0 with JSON hookSpecificOutput.permissionDecision=ask
	script := writeScript(t, dir, "ask.sh", shScript(
		"#!/bin/sh\nprintf '{\"hookSpecificOutput\":{\"permissionDecision\":\"ask\"}}'\n",
		"@echo {\"hookSpecificOutput\":{\"permissionDecision\":\"ask\"}}\r\n",
	))

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreToolUse, Command: script})
	adapter := &runtimeHookAdapter{executor: exec}

	_, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{
		Name:   "Echo",
		Params: map[string]any{"k": "v"},
	})
	if err == nil {
		t.Fatalf("expected ask error")
	}
	if !errors.Is(err, ErrToolUseRequiresApproval) {
		t.Fatalf("expected ErrToolUseRequiresApproval, got %v", err)
	}
}

func TestPermissionRequestDecisionMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name   string
		output string
		want   coreevents.PermissionDecisionType
	}{
		{name: "allow", output: `{"decision":"allow"}`, want: coreevents.PermissionAllow},
		{name: "deny", output: `{"decision":"deny"}`, want: coreevents.PermissionDeny},
		{name: "ask", output: `{"decision":"ask"}`, want: coreevents.PermissionAsk},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			script := writeScript(t, dir, tc.name+".sh", shScript(
				fmt.Sprintf("#!/bin/sh\nprintf '%s'\n", tc.output),
				fmt.Sprintf("@echo %s\r\n", tc.output),
			))
			exec := corehooks.NewExecutor()
			exec.Register(corehooks.ShellHook{
				Event:   coreevents.PermissionRequest,
				Command: script,
			})
			adapter := &runtimeHookAdapter{executor: exec}
			got, err := adapter.PermissionRequest(context.Background(), coreevents.PermissionRequestPayload{ToolName: "Bash"})
			if err != nil {
				t.Fatalf("permission request: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %s, got %s", tc.want, got)
			}
		})
	}
}

func TestRuntimeHookAdapterNewEventsRecord(t *testing.T) {
	t.Parallel()
	rec := defaultHookRecorder()
	exec := corehooks.NewExecutor()
	adapter := &runtimeHookAdapter{executor: exec, recorder: rec}

	if err := adapter.SessionStart(context.Background(), coreevents.SessionPayload{SessionID: "s"}); err != nil {
		t.Fatalf("session start: %v", err)
	}
	if err := adapter.SessionEnd(context.Background(), coreevents.SessionPayload{SessionID: "s"}); err != nil {
		t.Fatalf("session end: %v", err)
	}
	if err := adapter.SubagentStart(context.Background(), coreevents.SubagentStartPayload{Name: "sa", AgentID: "a1"}); err != nil {
		t.Fatalf("subagent start: %v", err)
	}
	if err := adapter.SubagentStop(context.Background(), coreevents.SubagentStopPayload{Name: "sa", AgentID: "a1"}); err != nil {
		t.Fatalf("subagent stop: %v", err)
	}

	drained := rec.Drain()
	want := map[coreevents.EventType]bool{
		coreevents.SessionStart:  false,
		coreevents.SessionEnd:    false,
		coreevents.SubagentStart: false,
		coreevents.SubagentStop:  false,
	}
	for _, evt := range drained {
		if _, ok := want[evt.Type]; ok {
			want[evt.Type] = true
		}
	}
	for typ, seen := range want {
		if !seen {
			t.Fatalf("expected %s event recorded", typ)
		}
	}
}

func writeScript(t *testing.T, dir, name, content string) string {
	t.Helper()
	if runtime.GOOS == "windows" {
		name = strings.TrimSuffix(name, ".sh") + ".bat"
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	return path
}

func shScript(unix, win string) string {
	if runtime.GOOS == "windows" {
		return win
	}
	return unix
}

func shCmd(unix, win string) string {
	if runtime.GOOS == "windows" {
		return win
	}
	return unix
}
