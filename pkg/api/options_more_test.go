package api

import (
	"context"
	"strings"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func TestRuntimeHookAdapterNilExecutorNoops(t *testing.T) {
	t.Parallel()

	var adapter *runtimeHookAdapter
	params, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{Name: "Echo", Params: map[string]any{"k": "v"}})
	if err != nil || params["k"] != "v" {
		t.Fatalf("expected passthrough params, got %v err=%v", params, err)
	}
	if err := adapter.PostToolUse(context.Background(), coreevents.ToolResultPayload{Name: "Echo"}); err != nil {
		t.Fatalf("unexpected post tool use error: %v", err)
	}
	if err := adapter.UserPrompt(context.Background(), "hi"); err != nil {
		t.Fatalf("unexpected user prompt error: %v", err)
	}
	if err := adapter.Stop(context.Background(), "done"); err != nil {
		t.Fatalf("unexpected stop error: %v", err)
	}
	if got, err := adapter.PermissionRequest(context.Background(), coreevents.PermissionRequestPayload{ToolName: "Bash"}); err != nil || got != coreevents.PermissionAsk {
		t.Fatalf("unexpected permission request result %v err=%v", got, err)
	}
	if err := adapter.SessionStart(context.Background(), coreevents.SessionPayload{}); err != nil {
		t.Fatalf("unexpected session start error: %v", err)
	}
	if err := adapter.SessionEnd(context.Background(), coreevents.SessionPayload{}); err != nil {
		t.Fatalf("unexpected session end error: %v", err)
	}
	if err := adapter.SubagentStart(context.Background(), coreevents.SubagentStartPayload{}); err != nil {
		t.Fatalf("unexpected subagent start error: %v", err)
	}
	if err := adapter.SubagentStop(context.Background(), coreevents.SubagentStopPayload{}); err != nil {
		t.Fatalf("unexpected subagent stop error: %v", err)
	}
	if err := adapter.ModelSelected(context.Background(), coreevents.ModelSelectedPayload{}); err != nil {
		t.Fatalf("unexpected model selected error: %v", err)
	}
}

func TestRuntimeHookAdapterErrorPaths(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	badScript := writeScript(t, dir, "bad.sh", `#!/bin/sh
echo '{bad'
`)

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreToolUse, Command: badScript})
	adapter := &runtimeHookAdapter{executor: exec}

	if _, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{Name: "Echo", Params: map[string]any{"k": "v"}}); err == nil {
		t.Fatalf("expected pre tool use error")
	}

	failExec := corehooks.NewExecutor()
	failExec.Register(corehooks.ShellHook{Event: coreevents.PostToolUse, Command: "exit 3"})
	failAdapter := &runtimeHookAdapter{executor: failExec}
	if err := failAdapter.PostToolUse(context.Background(), coreevents.ToolResultPayload{Name: "Echo"}); err == nil {
		t.Fatalf("expected post tool use error")
	}

	permExec := corehooks.NewExecutor()
	permExec.Register(corehooks.ShellHook{Event: coreevents.PermissionRequest, Command: "nonexistent-command-xyz"})
	permAdapter := &runtimeHookAdapter{executor: permExec}
	if _, err := permAdapter.PermissionRequest(context.Background(), coreevents.PermissionRequestPayload{ToolName: "Bash"}); err == nil {
		t.Fatalf("expected permission request error")
	}

	publishExec := corehooks.NewExecutor()
	publishExec.Register(corehooks.ShellHook{Event: coreevents.UserPromptSubmit, Command: "exit 3"})
	publishAdapter := &runtimeHookAdapter{executor: publishExec}
	if err := publishAdapter.UserPrompt(context.Background(), "hi"); err == nil {
		t.Fatalf("expected user prompt error")
	}

	notifyExec := corehooks.NewExecutor()
	notifyExec.Register(corehooks.ShellHook{Event: coreevents.Stop, Command: "exit 3"})
	notifyAdapter := &runtimeHookAdapter{executor: notifyExec}
	if err := notifyAdapter.Stop(context.Background(), strings.Repeat("x", 1)); err == nil {
		t.Fatalf("expected stop error")
	}
}
