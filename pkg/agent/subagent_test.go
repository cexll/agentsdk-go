package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

func TestForkToolWhitelistEnforced(t *testing.T) {
	t.Parallel()
	ag := newTestAgent(t)
	allowed := &mockTool{name: "allowed", result: &tool.ToolResult{Success: true, Output: "ok"}}
	blocked := &mockTool{name: "blocked", result: &tool.ToolResult{Success: true, Output: "nope"}}
	if err := ag.AddTool(allowed); err != nil {
		t.Fatalf("add allowed: %v", err)
	}
	if err := ag.AddTool(blocked); err != nil {
		t.Fatalf("add blocked: %v", err)
	}
	child, err := ag.Fork(WithForkToolWhitelist("allowed", "allowed", " "))
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	base := child.(*basicAgent)
	if len(base.tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(base.tools))
	}
	if _, ok := base.tools["allowed"]; !ok {
		t.Fatalf("allowed tool missing")
	}
	if res, err := child.Run(context.Background(), "tool:blocked {}"); err != nil {
		t.Fatalf("blocked tool run err=%v", err)
	} else if len(res.ToolCalls) == 0 || !strings.Contains(res.ToolCalls[0].Error, "not registered") {
		t.Fatalf("expected blocked tool error, got %+v", res.ToolCalls)
	}
	if res, err := child.Run(context.Background(), "tool:allowed {}"); err != nil || res.StopReason != "complete" {
		t.Fatalf("allowed tool run err=%v stop=%s", err, res.StopReason)
	}
}

func TestSubAgentManagerDelegationIsolatedSession(t *testing.T) {
	t.Parallel()
	ag := newTestAgent(t)
	echo := &mockTool{name: "echo", result: &tool.ToolResult{Success: true, Output: "pong"}}
	if err := ag.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	sess, err := session.NewMemorySession("root")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	progress := make(chan event.Event, 32)
	control := make(chan event.Event, 32)
	monitor := make(chan event.Event, 32)
	bus := event.NewEventBus(progress, control, monitor, event.WithBufferSize(1))

	mgr, err := NewSubAgentManager(
		ag,
		WithSubAgentBaseRunContext(RunContext{SessionID: "root"}),
		WithSubAgentSession(sess),
		WithSubAgentEventBus(bus),
		WithSubAgentIDPrefix("child"),
	)
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	req := workflow.SubAgentRequest{
		ID:            "delegate-1",
		Instruction:   "tool:echo {\"msg\":\"ok\"}",
		ToolWhitelist: []string{"echo"},
		ShareEventBus: true,
		Metadata:      map[string]any{"priority": "p1"},
	}
	res, err := mgr.Delegate(nil, req)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.SessionID == sess.ID() {
		t.Fatalf("expected forked session, got %s", res.SessionID)
	}
	if res.Session == nil || res.Session.ID() != res.SessionID {
		t.Fatalf("session pointer mismatch: %+v", res.Session)
	}
	if res.StopReason != "complete" {
		t.Fatalf("stop reason = %s", res.StopReason)
	}
	if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "echo" {
		t.Fatalf("unexpected tool calls %+v", res.ToolCalls)
	}
	if res.Metadata["priority"] != "p1" {
		t.Fatalf("metadata missing: %+v", res.Metadata)
	}
	if res.Usage.TotalTokens == 0 {
		t.Fatalf("expected usage populated")
	}
	if len(res.Events) == 0 {
		t.Fatalf("expected events captured")
	}
	select {
	case evt := <-progress:
		if evt.Type != event.EventProgress && evt.Type != event.EventCompletion {
			t.Fatalf("unexpected forwarded event %s", evt.Type)
		}
	default:
		t.Fatal("no events forwarded to bus")
	}
	req.Metadata["priority"] = "changed"
	if res.Metadata["priority"] != "p1" {
		t.Fatalf("metadata not cloned: %+v", res.Metadata)
	}
}

func TestSubAgentManagerShareSessionAndErrors(t *testing.T) {
	t.Parallel()
	ag := newTestAgent(t)
	echo := &mockTool{name: "echo", result: &tool.ToolResult{Success: true, Output: "pong"}}
	if err := ag.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	sess, err := session.NewMemorySession("live")
	if err != nil {
		t.Fatalf("memory session: %v", err)
	}
	mgr, err := NewSubAgentManager(ag, WithSubAgentSession(sess))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	req := workflow.SubAgentRequest{
		Instruction:  "hello",
		Session:      sess,
		ShareSession: true,
		Metadata:     map[string]any{"tag": "shared"},
	}
	res, err := mgr.Delegate(context.Background(), req)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.Session != sess || res.SessionID != sess.ID() || !res.SharedSession {
		t.Fatalf("session not shared: %+v", res)
	}
	if res.Output != "hello processed" {
		t.Fatalf("unexpected output: %s", res.Output)
	}

	// Trigger tool whitelist rejection.
	req2 := workflow.SubAgentRequest{
		Instruction:   "tool:echo {}",
		ToolWhitelist: []string{"noop"},
	}
	res2, err := mgr.Delegate(context.Background(), req2)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if len(res2.ToolCalls) == 0 || !strings.Contains(res2.ToolCalls[0].Error, "not registered") {
		t.Fatalf("expected missing tool error, got %+v", res2.ToolCalls)
	}
}

func TestSubAgentManagerNilCasesAndPrefixes(t *testing.T) {
	t.Parallel()
	if _, err := NewSubAgentManager(nil); err == nil {
		t.Fatal("expected nil parent error")
	}
	var nilMgr *SubAgentManager
	if _, err := nilMgr.Delegate(context.Background(), workflow.SubAgentRequest{Instruction: "x"}); err == nil {
		t.Fatal("expected nil manager error")
	}
	sess, err := session.NewMemorySession("closed")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := sess.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	ag := newTestAgent(t)
	mgr, err := NewSubAgentManager(ag, WithSubAgentSession(sess), WithSubAgentIDPrefix("pref"))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	_, err = mgr.Delegate(context.Background(), workflow.SubAgentRequest{Instruction: "hi"})
	if err == nil || !errors.Is(err, session.ErrSessionClosed) {
		t.Fatalf("expected session closed error, got %v", err)
	}
	mgr, err = NewSubAgentManager(ag, WithSubAgentIDPrefix("pref"))
	if err != nil {
		t.Fatalf("manager (no session): %v", err)
	}
	res, err := mgr.Delegate(context.Background(), workflow.SubAgentRequest{Instruction: "hi"})
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if !strings.HasPrefix(res.SessionID, "pref-") {
		t.Fatalf("prefix not applied: %s", res.SessionID)
	}
	var nilAgent *basicAgent
	if _, err := nilAgent.Fork(); err == nil {
		t.Fatal("expected nil agent error")
	}
}

func TestCloneHelpersAndNextSessionFallback(t *testing.T) {
	t.Parallel()
	if clone := cloneEvents(nil); clone != nil {
		t.Fatalf("expected nil clone")
	}
	eventList := []event.Event{event.NewEvent(event.EventProgress, "sess", nil)}
	cloned := cloneEvents(eventList)
	if len(cloned) != 1 || &cloned[0] == &eventList[0] {
		t.Fatalf("events not cloned: %+v", cloned)
	}
	mgr, err := NewSubAgentManager(newTestAgent(t))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	mgr.prefix = ""
	id := mgr.nextSessionName("")
	if !strings.HasPrefix(id, "subagent-") {
		t.Fatalf("fallback prefix not applied: %s", id)
	}
}
