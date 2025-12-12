package api

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func TestPreToolUseAllowsInputModification(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	script := writeScript(t, dir, "modify.sh", `#!/bin/sh
printf '{"tool_input":{"name":"Echo","params":{"k":"v2"}}}'
`)

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

func TestPermissionRequestDecisionMapping(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		code int
		want coreevents.PermissionDecisionType
	}{
		{name: "allow", code: 0, want: coreevents.PermissionAllow},
		{name: "deny", code: 1, want: coreevents.PermissionDeny},
		{name: "ask", code: 2, want: coreevents.PermissionAsk},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			exec := corehooks.NewExecutor()
			exec.Register(corehooks.ShellHook{
				Event:   coreevents.PermissionRequest,
				Command: fmt.Sprintf("exit %d", tc.code),
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
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.Chmod(path, 0o700); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	return path
}
