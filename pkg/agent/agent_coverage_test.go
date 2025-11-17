package agent

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"

	"github.com/cexll/agentsdk-go/pkg/approval"
	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/telemetry"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

// Ensure RunWorkflow wires registered tools into the execution context.
func TestAgentRunWorkflowInjectsTools(t *testing.T) {
	ag := newTestAgent(t)
	stub := &mockTool{name: "echo", result: &tool.ToolResult{Output: "pong"}}
	if err := ag.AddTool(stub); err != nil {
		t.Fatalf("add tool: %v", err)
	}

	g := workflow.NewGraph()
	if err := g.AddNode(workflow.NewAction("start", func(ctx *workflow.ExecutionContext) error {
		tools := ctx.Tools()
		if _, ok := tools["echo"]; !ok {
			t.Fatalf("tool not injected into workflow context")
		}
		// Execute to ensure tool wiring works when called directly.
		toolInstance := tools["echo"]
		if _, err := toolInstance.Execute(ctx.Context(), map[string]any{"msg": "hi"}); err != nil {
			return err
		}
		return nil
	})); err != nil {
		t.Fatalf("add node: %v", err)
	}

	if err := ag.RunWorkflow(context.Background(), g); err != nil {
		t.Fatalf("run workflow: %v", err)
	}
	if stub.calls != 1 {
		t.Fatalf("expected tool to be executed once, got %d", stub.calls)
	}
}

func TestAgentRunWorkflowErrors(t *testing.T) {
	ag := newTestAgent(t)
	if err := ag.RunWorkflow(nil, workflow.NewGraph()); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("expected context error, got %v", err)
	}
	if err := ag.RunWorkflow(context.Background(), nil); err == nil || !strings.Contains(err.Error(), "graph is nil") {
		t.Fatalf("expected graph error, got %v", err)
	}
}

// Cover approval pending → approve → whitelist auto-approval path.
func TestAgentApprovalLifecycle(t *testing.T) {
	ag := newTestAgent(t)
	ag.(*basicAgent).approval = approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	echo := &mockTool{name: "echo", result: &tool.ToolResult{Output: "ok"}}
	if err := ag.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	ctx := WithRunContext(context.Background(), RunContext{
		SessionID:    "sess-approval",
		ApprovalMode: ApprovalRequired,
	})

	_, err := ag.Run(ctx, "tool:echo {}")
	if err == nil || !errors.Is(err, errApprovalPending) {
		t.Fatalf("expected approval pending error, got %v", err)
	}
	id := strings.TrimPrefix(err.Error(), "approval pending: ")
	if id == "" {
		t.Fatalf("missing approval id in error: %v", err)
	}
	if err := ag.Approve(id, true); err != nil {
		t.Fatalf("approve: %v", err)
	}

	res, err := ag.Run(ctx, "tool:echo {}")
	if err != nil {
		t.Fatalf("run after approval: %v", err)
	}
	if res.ToolCalls[0].Metadata["approval_auto"] != true {
		t.Fatalf("approval metadata missing: %+v", res.ToolCalls[0].Metadata)
	}

	// Whitelist should auto-approve without pending state.
	res, err = ag.Run(ctx, "tool:echo {}")
	if err != nil {
		t.Fatalf("run after whitelist: %v", err)
	}
	if res.StopReason != "complete" {
		t.Fatalf("unexpected stop reason %s", res.StopReason)
	}
}

// Validate hooks and telemetry wiring.
func TestAgentHooksAndTelemetry(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tp := sdktrace.NewTracerProvider()
	mgr, err := telemetry.NewManager(telemetry.Config{
		ServiceName:    "hooked",
		ServiceVersion: "test",
		MeterProvider:  mp,
		TracerProvider: tp,
	})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(func() {
		telemetry.SetDefault(nil)
		_ = mgr.Shutdown(context.Background())
	})
	hook := &spyHook{}
	sess, err := session.NewMemorySession("hooks")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	ag, err := New(
		Config{Name: "hooked", DefaultContext: RunContext{SessionID: "hooks"}},
		WithTelemetry(mgr),
		WithModel(newMockModel()),
		WithSession(sess),
	)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	ag = ag.WithHook(hook)
	echo := &mockTool{name: "echo", result: &tool.ToolResult{Output: "ok"}}
	if err := ag.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	res, err := ag.Run(context.Background(), "tool:echo {}")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if res.StopReason != "complete" {
		t.Fatalf("unexpected stop %s", res.StopReason)
	}
	if !hook.preRun || !hook.postRun || !hook.preTool || !hook.postTool {
		t.Fatalf("hooks not invoked: %+v", hook)
	}
	ba := ag.(*basicAgent)
	// Exercise masking helpers.
	masked := ba.maskSensitive("sk-secret-123456")
	if strings.Contains(masked, "secret") {
		t.Fatalf("maskSensitive did not sanitize: %s", masked)
	}
	out := ba.sanitizeAttributes()
	if len(out) != 0 {
		t.Fatalf("expected empty attrs passthrough, got %+v", out)
	}
}

func TestLifecycleHooks(t *testing.T) {
	hooks := LifecycleHooks{
		lifecycleSpy{onStartErr: errors.New("start fail")},
	}
	if err := hooks.RunStart(RunContext{}); err == nil {
		t.Fatal("expected start error")
	}
	hooks = LifecycleHooks{lifecycleSpy{}, lifecycleSpy{}}
	if err := hooks.RunStart(RunContext{}); err != nil {
		t.Fatalf("run start: %v", err)
	}
	if err := hooks.RunStop(RunContext{}); err != nil {
		t.Fatalf("run stop: %v", err)
	}
}

type spyHook struct {
	preRun, postRun   bool
	preTool, postTool bool
}

func (h *spyHook) PreRun(context.Context, string) error {
	h.preRun = true
	return nil
}

func (h *spyHook) PostRun(context.Context, *RunResult) error {
	h.postRun = true
	return nil
}

func (h *spyHook) PreToolCall(context.Context, string, map[string]any) error {
	h.preTool = true
	return nil
}

func (h *spyHook) PostToolCall(context.Context, string, ToolCall) error {
	h.postTool = true
	return nil
}

type lifecycleSpy struct {
	onStartErr error
}

func (l lifecycleSpy) OnStart(RunContext) error { return l.onStartErr }
func (l lifecycleSpy) OnStop(RunContext) error  { return nil }

type stopErrHook struct {
	err error
}

func (s stopErrHook) OnStart(RunContext) error { return nil }
func (s stopErrHook) OnStop(RunContext) error  { return s.err }

func TestAgentConfigHelpers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "cfg.json")
	payload := []byte(`{"name":"demo","default_context":{"session_id":"cfg"}}`)
	if err := os.WriteFile(path, payload, 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	loaded, err := LoadConfig(path)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if loaded.DefaultContext.SessionID != "cfg" {
		t.Fatalf("expected session id cfg, got %+v", loaded.DefaultContext)
	}
	data := []byte(`{"name":"demo","stream_buffer":8}`)
	cfg, err := DecodeConfig(data)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	cfg.DefaultContext.SessionID = ""
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	rc := cfg.ResolveContext(RunContext{SessionID: "override"})
	if rc.SessionID != "override" {
		t.Fatalf("expected override session, got %+v", rc)
	}
}

func TestRunContextMergeAndNormalize(t *testing.T) {
	base := RunContext{SessionID: "base", MaxIterations: 0, ApprovalMode: ApprovalRequired}
	override := RunContext{SessionID: "override", MaxIterations: 3, ApprovalMode: ApprovalAuto}
	merged := base.Merge(override)
	if merged.SessionID != "override" || merged.MaxIterations != 3 || merged.ApprovalMode != ApprovalAuto {
		t.Fatalf("merge failed: %+v", merged)
	}
	norm := RunContext{Temperature: 0}.Normalize()
	if norm.Temperature == 0 {
		t.Fatalf("normalize should set default temperature")
	}
}

func TestRunContextWithContext(t *testing.T) {
	ctx := WithRunContext(context.Background(), RunContext{SessionID: "ctx"})
	rc, ok := GetRunContext(ctx)
	if !ok || rc.SessionID != "ctx" {
		t.Fatalf("get run context failed: %+v %v", rc, ok)
	}
	rc, ok = GetRunContext(context.Background())
	if ok || rc.SessionID != "" {
		t.Fatalf("expected missing run context")
	}
	ctx = WithRunContext(nil, RunContext{SessionID: "nil"})
	rc, ok = GetRunContext(ctx)
	if !ok || rc.SessionID != "nil" {
		t.Fatalf("nil context wrapper failed: %+v %v", rc, ok)
	}
	rc, ok = GetRunContext(nil)
	if ok || rc.SessionID != "" {
		t.Fatalf("expected no run context on nil ctx")
	}
}

func TestToolCallFailedHelper(t *testing.T) {
	call := ToolCall{}
	if call.Failed() {
		t.Fatal("expected false when error is empty")
	}
	call.Error = "boom"
	if !call.Failed() {
		t.Fatal("expected true when error is set")
	}
}

func TestNopHookMethods(t *testing.T) {
	var hook NopHook
	if err := hook.PreRun(context.Background(), ""); err != nil {
		t.Fatalf("pre run: %v", err)
	}
	if err := hook.PostRun(context.Background(), &RunResult{}); err != nil {
		t.Fatalf("post run: %v", err)
	}
	if err := hook.PreToolCall(context.Background(), "", nil); err != nil {
		t.Fatalf("pre tool: %v", err)
	}
	if err := hook.PostToolCall(context.Background(), "", ToolCall{}); err != nil {
		t.Fatalf("post tool: %v", err)
	}
}

func TestAgentApproveRejectPath(t *testing.T) {
	ag := newTestAgent(t).(*basicAgent)
	ag.approval = approval.NewQueue(approval.NewMemoryStore(), approval.NewWhitelist())
	rec, _, err := ag.approval.Request("sess", "echo", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if err := ag.Approve(rec.ID, false); err != nil {
		t.Fatalf("reject path: %v", err)
	}
}

func TestConfigValidationErrors(t *testing.T) {
	if _, err := DecodeConfig(nil); err == nil {
		t.Fatal("expected empty payload error")
	}
	if err := (&Config{}).Validate(); err == nil {
		t.Fatal("expected missing name error")
	}
	cfg := Config{Name: "demo"}
	if err := cfg.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if cfg.ResolveContext(RunContext{}).SessionID != cfg.DefaultContext.SessionID {
		t.Fatalf("resolve context mismatch")
	}
}

func TestCapsFromMetadata(t *testing.T) {
	input := map[string]any{"capabilities": []any{"plan", 1, "build"}}
	caps := capsFromMetadata(input)
	if len(caps) != 2 {
		t.Fatalf("expected 2 caps, got %+v", caps)
	}
	if out := capsFromMetadata(map[string]any{"capabilities": "one, two"}); len(out) != 2 {
		t.Fatalf("string capabilities parsing failed: %+v", out)
	}
}

func TestRunStreamToolError(t *testing.T) {
	ag := newTestAgent(t)
	errTool := &mockTool{name: "boom", err: errors.New("fail")}
	if err := ag.AddTool(errTool); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	ch, err := ag.RunStream(context.Background(), "tool:boom {}")
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	var sawError bool
	for evt := range ch {
		switch evt.Type {
		case event.EventError:
			sawError = true
		case event.EventToolResult:
			if data, ok := evt.Data.(event.ToolResultData); ok && strings.TrimSpace(data.Error) != "" {
				sawError = true
			}
		}
	}
	if !sawError {
		t.Fatal("expected error event from failing tool")
	}
}

func TestTeamRoleMatches(t *testing.T) {
	if !TeamRoleLeader.Matches(TeamRoleLeader) {
		t.Fatal("leader should match leader")
	}
	if !TeamRoleUnknown.Matches(TeamRoleReviewer) {
		t.Fatal("unknown should match any")
	}
	if TeamRoleWorker.Matches(TeamRoleReviewer) {
		t.Fatal("worker should not match reviewer")
	}
}

func TestStringifyVariants(t *testing.T) {
	if stringify("value") != "value" {
		t.Fatalf("string stringify failed")
	}
	if out := stringify(stringerStub{v: "ok"}); out != "fmt:ok" {
		t.Fatalf("stringer stringify failed: %s", out)
	}
	if out := stringify([]byte("bytes")); out != "bytes" {
		t.Fatalf("byte stringify failed: %s", out)
	}
	ch := make(chan int)
	expect := fmt.Sprint(ch)
	if out := stringify(ch); out != expect {
		t.Fatalf("fallback stringify failed: %s vs %s", out, expect)
	}
}

func TestRunHooksBehavior(t *testing.T) {
	err := runHooks([]Hook{errHook{err: errors.New("boom")}}, false, func(h Hook) error {
		return h.PreRun(context.Background(), "")
	})
	if err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected immediate error, got %v", err)
	}
	err = runHooks([]Hook{errHook{err: errors.New("boom")}, errHook{err: errors.New("zip")}}, true, func(h Hook) error {
		return h.PreRun(context.Background(), "")
	})
	if err == nil || !strings.Contains(err.Error(), "zip") || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected aggregated error, got %v", err)
	}
}

func TestClampStreamBuffer(t *testing.T) {
	if clampStreamBuffer(0) != minStreamBufferSize {
		t.Fatalf("expected min buffer clamp")
	}
	if clampStreamBuffer(1000) != maxStreamBufferSize {
		t.Fatalf("expected max buffer clamp")
	}
	if clampStreamBuffer(8) != 8 {
		t.Fatalf("expected pass-through for mid values")
	}
}

func TestStreamDispatcherBackpressure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ch := make(chan event.Event, 1)
	dispatcher := newStreamDispatcher(ctx, ch, "sess", 1)
	if err := dispatcher.emit(progressEvent("sess", "stage", "first", nil)); err != nil {
		t.Fatalf("emit first: %v", err)
	}
	var mu sync.Mutex
	var events []event.Event
	consume := func(expected int) {
		for i := 0; i < expected; i++ {
			select {
			case evt := <-ch:
				mu.Lock()
				events = append(events, evt)
				mu.Unlock()
			case <-time.After(100 * time.Millisecond):
				t.Fatalf("timeout waiting for event %d", i)
			}
		}
	}
	done := make(chan error, 1)
	go func() {
		done <- dispatcher.emit(progressEvent("sess", "stage", "second", nil))
	}()
	time.Sleep(10 * time.Millisecond)
	consume(3) // original, throttled, second emit
	if err := <-done; err != nil {
		t.Fatalf("emit second: %v", err)
	}
	time.Sleep(10 * time.Millisecond)
	consume(1) // recovery event
	cancel()
	ctx2, cancel2 := context.WithCancel(context.Background())
	cancel2()
	dispatcher2 := newStreamDispatcher(ctx2, make(chan event.Event), "sess", 1)
	if err := dispatcher2.emit(progressEvent("sess", "stage", "after-cancel", nil)); err == nil {
		t.Fatalf("expected context error after cancel")
	}
}

func TestStreamDispatcherPushTerminal(t *testing.T) {
	ch := make(chan event.Event, 1)
	dispatcher := newStreamDispatcher(context.Background(), ch, "sess", 1)
	ch <- progressEvent("sess", "stage", "filled", nil)
	done := make(chan struct{})
	go func() {
		dispatcher.pushTerminal(event.Event{Type: event.EventCompletion, Data: map[string]any{"msg": "done"}})
		close(done)
	}()
	time.Sleep(10 * time.Millisecond)
	<-ch // drain the buffered event to free space
	select {
	case evt := <-ch:
		if evt.Type != event.EventCompletion {
			t.Fatalf("unexpected event %+v", evt)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("terminal event missing")
	}
	select {
	case <-done:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("pushTerminal goroutine did not finish")
	}
}

func TestAgentNewOptionError(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected config validation error")
	}
	optErr := errors.New("option failed")
	opt := func(*basicAgent) error { return optErr }
	_, err := New(Config{Name: "opts", DefaultContext: RunContext{SessionID: "opts"}}, opt)
	if err == nil || !errors.Is(err, optErr) {
		t.Fatalf("expected option error, got %v", err)
	}
}

func TestDefaultResponseVariants(t *testing.T) {
	agent := &basicAgent{}
	withSession := agent.defaultResponse("hello", RunContext{SessionID: "sess"})
	if !strings.Contains(withSession, "sess") {
		t.Fatalf("expected session prefix, got %s", withSession)
	}
	without := agent.defaultResponse("hello", RunContext{})
	if without != "processed: hello" {
		t.Fatalf("unexpected fallback response %s", without)
	}
}

func TestSanitizeHelpersWithoutTelemetry(t *testing.T) {
	agent := &basicAgent{}
	attrs := agent.sanitizeAttributes()
	if len(attrs) != 0 {
		t.Fatalf("expected passthrough attrs, got %+v", attrs)
	}
	masked := agent.maskSensitive("secret-token")
	if masked == "" {
		t.Fatalf("expected masking, got %s", masked)
	}
}

func TestErrorEventNilError(t *testing.T) {
	evt := errorEvent("sess", "kind", nil, true)
	data, ok := evt.Data.(event.ErrorData)
	if !ok {
		t.Fatalf("expected error data, got %T", evt.Data)
	}
	if data.Message != "unknown error" || !data.Recoverable {
		t.Fatalf("unexpected error data: %+v", data)
	}
}

func TestConvertUsageZero(t *testing.T) {
	if convertUsage(TokenUsage{}) != nil {
		t.Fatalf("expected nil usage for zero tokens")
	}
	usage := convertUsage(TokenUsage{InputTokens: 1, OutputTokens: 2, TotalTokens: 3})
	if usage == nil || usage.TotalTokens != 3 {
		t.Fatalf("unexpected usage conversion: %+v", usage)
	}
}

func TestTelemetryOptionErrors(t *testing.T) {
	opt := WithTelemetry(nil)
	if err := opt(&basicAgent{}); err == nil || !strings.Contains(err.Error(), "telemetry manager is nil") {
		t.Fatalf("expected nil manager error, got %v", err)
	}
	mgr, err := telemetry.NewManager(telemetry.Config{ServiceName: "opts"})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(func() { _ = mgr.Shutdown(context.Background()) })
	if err := WithTelemetry(mgr)(nil); err == nil || !strings.Contains(err.Error(), "agent is nil") {
		t.Fatalf("expected nil agent error, got %v", err)
	}
}

func TestLifecycleRunStopError(t *testing.T) {
	hooks := LifecycleHooks{stopErrHook{err: errors.New("stop fail")}}
	if err := hooks.RunStop(RunContext{}); err == nil || !strings.Contains(err.Error(), "stop fail") {
		t.Fatalf("expected stop error, got %v", err)
	}
}

func TestTeamRoleParsingVariants(t *testing.T) {
	role, err := ParseTeamRole("Leader")
	if err != nil || role != TeamRoleLeader {
		t.Fatalf("parse leader failed: %v %v", role, err)
	}
	if _, err := ParseTeamRole("invalid"); err == nil {
		t.Fatal("expected invalid role error")
	}
	if TeamRole("").String() != "unknown" {
		t.Fatalf("unknown role string mismatch")
	}
	if err := TeamRole("mystery").Validate(); err == nil {
		t.Fatal("expected validation error for unknown role")
	}
}

func TestLoadAndDecodeConfigErrors(t *testing.T) {
	if _, err := LoadConfig("missing-file.json"); err == nil {
		t.Fatal("expected missing file error")
	}
	if _, err := DecodeConfig([]byte("{broken")); err == nil {
		t.Fatal("expected decode error")
	}
	cfg := Config{Name: "bad", StreamBuffer: -1}
	if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), "stream_buffer") {
		t.Fatalf("expected stream buffer error, got %v", err)
	}
}

func TestSetupRunTimeoutBranch(t *testing.T) {
	agent := newTestAgent(t).(*basicAgent)
	baseCtx := WithRunContext(context.Background(), RunContext{Timeout: time.Millisecond})
	ctx, _, _, cancel, err := agent.setupRun(baseCtx, "hello")
	if err != nil || cancel == nil {
		t.Fatalf("setup run with timeout failed: %v", err)
	}
	defer cancel()
	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Millisecond):
		t.Fatal("expected timeout to fire quickly")
	}
	cancelCtx, cancelFunc := context.WithCancel(context.Background())
	cancelFunc()
	if _, _, _, _, err := agent.setupRun(cancelCtx, "hello"); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context error, got %v", err)
	}
}

func TestRunWithEmitterStopsOnEmitError(t *testing.T) {
	agent := newTestAgent(t).(*basicAgent)
	rc := agent.cfg.DefaultContext
	emitErr := errors.New("emit fail")
	if _, err := agent.runWithEmitter(context.Background(), "hello", rc, func(event.Event) error {
		return emitErr
	}); err == nil || !errors.Is(err, emitErr) {
		t.Fatalf("expected emit error, got %v", err)
	}
}

func TestRunStreamWithTelemetry(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	mp := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	tp := sdktrace.NewTracerProvider()
	mgr, err := telemetry.NewManager(telemetry.Config{
		ServiceName:    "stream-otel",
		ServiceVersion: "test",
		MeterProvider:  mp,
		TracerProvider: tp,
	})
	if err != nil {
		t.Fatalf("telemetry: %v", err)
	}
	t.Cleanup(func() {
		telemetry.SetDefault(nil)
		_ = mgr.Shutdown(context.Background())
	})
	sess, err := session.NewMemorySession("stream-otel")
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	agent, err := New(
		Config{Name: "stream-otel", DefaultContext: RunContext{SessionID: "otel"}},
		WithTelemetry(mgr),
		WithModel(newMockModel()),
		WithSession(sess),
	)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	echo := &mockTool{name: "echo", result: &tool.ToolResult{Output: "ok"}}
	if err := agent.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	ch, err := agent.RunStream(context.Background(), "tool:echo {}")
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	count := 0
	for range ch {
		count++
	}
	if count == 0 {
		t.Fatal("expected telemetry stream events")
	}
}

type stringerStub struct{ v string }

func (s stringerStub) String() string { return "fmt:" + s.v }

type errHook struct {
	Hook
	err error
}

func (e errHook) PreRun(context.Context, string) error { return e.err }
