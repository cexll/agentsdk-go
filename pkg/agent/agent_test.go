package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestAgentRun(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		input   string
		setup   func(t *testing.T, ag Agent) *mockTool
		wantErr string
		assert  func(t *testing.T, res *RunResult, stub *mockTool)
	}{
		{
			name:  "default response trims input",
			ctx:   context.Background(),
			input: "   hello  ",
			assert: func(t *testing.T, res *RunResult, _ *mockTool) {
				t.Helper()
				if res.StopReason != "complete" {
					t.Fatalf("stop reason = %s", res.StopReason)
				}
				if res.Output != "hello processed" {
					t.Fatalf("output = %s", res.Output)
				}
			},
		},
		{
			name:  "tool instruction executes registered tool",
			ctx:   context.Background(),
			input: "tool:echo {\"msg\":\"ok\"}",
			setup: func(t *testing.T, ag Agent) *mockTool {
				t.Helper()
				stub := &mockTool{name: "echo", result: &tool.ToolResult{Output: "pong"}}
				if err := ag.AddTool(stub); err != nil {
					t.Fatalf("add tool: %v", err)
				}
				return stub
			},
			assert: func(t *testing.T, res *RunResult, stub *mockTool) {
				t.Helper()
				if res.StopReason != "complete" {
					t.Fatalf("stop reason = %s", res.StopReason)
				}
				if len(res.ToolCalls) != 1 || res.ToolCalls[0].Name != "echo" {
					t.Fatalf("tool calls = %+v", res.ToolCalls)
				}
				if stub.calls != 1 {
					t.Fatalf("tool executions = %d", stub.calls)
				}
				if got := res.ToolCalls[0].Output.(*tool.ToolResult).Output; got != "pong" {
					t.Fatalf("tool output = %v", got)
				}
			},
		},
		{
			name:    "malformed tool payload propagates error",
			ctx:     context.Background(),
			input:   "tool:echo {",
			wantErr: "parse tool params",
			assert: func(t *testing.T, res *RunResult, _ *mockTool) {
				t.Helper()
				if res.StopReason != "input_error" {
					t.Fatalf("stop reason = %s", res.StopReason)
				}
			},
		},
		{
			name:    "nil context rejected",
			ctx:     nil,
			input:   "hello",
			wantErr: "context is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgent(t)
			var stub *mockTool
			if tt.setup != nil {
				stub = tt.setup(t, ag)
			}
			res, err := ag.Run(tt.ctx, tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run failed: %v", err)
			}
			if tt.assert != nil {
				tt.assert(t, res, stub)
			}
		})
	}
}

func TestAgentRunStream(t *testing.T) {
	tests := []struct {
		name    string
		ctx     context.Context
		input   string
		wantErr string
		assert  func(t *testing.T, events []event.Event)
	}{
		{
			name:  "successful stream emits progress and completion",
			ctx:   context.Background(),
			input: "hi",
			assert: func(t *testing.T, events []event.Event) {
				t.Helper()
				if len(events) == 0 {
					t.Fatal("no events emitted")
				}
				if events[0].Type != event.EventProgress {
					t.Fatalf("first event = %s", events[0].Type)
				}
				if events[len(events)-1].Type != event.EventCompletion {
					t.Fatalf("last event = %s", events[len(events)-1].Type)
				}
			},
		},
		{
			name:    "invalid input rejected",
			ctx:     context.Background(),
			input:   "   ",
			wantErr: "input is empty",
		},
		{
			name:    "nil context rejected",
			ctx:     nil,
			input:   "hi",
			wantErr: "context is nil",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgent(t)
			ch, err := ag.RunStream(tt.ctx, tt.input)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q, got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("run stream failed: %v", err)
			}
			var events []event.Event
			for evt := range ch {
				events = append(events, evt)
			}
			tt.assert(t, events)
		})
	}
}

func TestAgentAddTool(t *testing.T) {
	tests := []struct {
		name        string
		tool        tool.Tool
		preRegister bool
		wantErr     string
		verifyRun   bool
	}{
		{name: "nil tool", tool: nil, wantErr: "tool is nil"},
		{name: "empty name", tool: &mockTool{name: ""}, wantErr: "tool name is empty"},
		{name: "duplicate name", tool: &mockTool{name: "dup"}, preRegister: true, wantErr: "already registered"},
		{name: "success registers callable tool", tool: &mockTool{name: "echo", result: &tool.ToolResult{Output: "done"}}, verifyRun: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ag := newTestAgent(t)
			if tt.preRegister {
				if err := ag.AddTool(tt.tool); err != nil {
					t.Fatalf("setup add failed: %v", err)
				}
			}
			err := ag.AddTool(tt.tool)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("add tool failed: %v", err)
			}
			if tt.verifyRun {
				_, runErr := ag.Run(context.Background(), "tool:echo {}")
				if runErr != nil {
					t.Fatalf("run failed: %v", runErr)
				}
				if stub, ok := tt.tool.(*mockTool); ok {
					if stub.calls != 1 {
						t.Fatalf("tool not invoked, calls=%d", stub.calls)
					}
				}
			}
		})
	}
}

func TestAgentResumeReplaysEvents(t *testing.T) {
	store := &memoryEventStore{}
	cfg := Config{
		Name:           "resume-agent",
		DefaultContext: RunContext{SessionID: "resume-session"},
		EventStore:     store,
	}
	ag := newAgentWithConfig(t, cfg)
	if _, err := ag.Run(context.Background(), "first run"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	bookmark := store.lastBookmark()
	if bookmark == nil {
		t.Fatal("bookmark missing after first run")
	}
	if _, err := ag.Run(context.Background(), "second run"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	res, err := ag.Resume(context.Background(), bookmark)
	if err != nil {
		t.Fatalf("resume: %v", err)
	}
	if len(res.Events) == 0 {
		t.Fatal("resume should replay events")
	}
	if res.StopReason != StopReasonComplete {
		t.Fatalf("unexpected stop reason: %s", res.StopReason)
	}
	if strings.TrimSpace(res.Output) == "" {
		t.Fatal("resume output should not be empty")
	}
}

func TestAgent_MaxIterations(t *testing.T) {
	tests := []struct {
		name           string
		maxIterations  int
		mockToolCalls  int
		wantStopReason string
		wantModelCalls int
	}{
		{
			name:           "completes before limit",
			maxIterations:  5,
			mockToolCalls:  2,
			wantStopReason: StopReasonComplete,
			wantModelCalls: 3,
		},
		{
			name:           "hits configured limit",
			maxIterations:  3,
			mockToolCalls:  20,
			wantStopReason: StopReasonMaxIterations,
			wantModelCalls: 3,
		},
		{
			name:           "zero max iterations uses default",
			maxIterations:  0,
			mockToolCalls:  50,
			wantStopReason: StopReasonMaxIterations,
			wantModelCalls: defaultMaxIterations,
		},
	}

	for idx, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sessionID := fmt.Sprintf("max-iter-%d", idx)
			mockModel := &mockModelWithEndlessTools{remainingToolCalls: tt.mockToolCalls}
			agent := newAgentWithModelForTest(t, sessionID, mockModel)
			if err := agent.AddTool(&mockTool{name: "test_tool"}); err != nil {
				t.Fatalf("add tool: %v", err)
			}
			runCtx := RunContext{
				SessionID:     sessionID,
				MaxIterations: tt.maxIterations,
			}
			res, err := agent.runWithEmitter(context.Background(), "do something", runCtx, nil)
			if err != nil {
				t.Fatalf("runWithEmitter: %v", err)
			}
			if res.StopReason != tt.wantStopReason {
				t.Fatalf("stop reason = %s want %s", res.StopReason, tt.wantStopReason)
			}
			if mockModel.callCount != tt.wantModelCalls {
				t.Fatalf("model calls = %d want %d", mockModel.callCount, tt.wantModelCalls)
			}
		})
	}
}

func TestRunWithEmitterContextCancellation(t *testing.T) {
	t.Helper()
	model := newSlowMockModel("slow-tool")
	agent := newAgentWithModelForTest(t, "ctx-cancel", model)
	require.NoError(t, agent.AddTool(&mockTool{name: "slow-tool"}))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rc := RunContext{SessionID: "ctx-cancel", MaxIterations: 5}

	resultCh := make(chan struct {
		res *RunResult
		err error
	}, 1)
	go func() {
		res, err := agent.runWithEmitter(ctx, "hello", rc, nil)
		resultCh <- struct {
			res *RunResult
			err error
		}{res: res, err: err}
	}()

	model.waitForFirstCall(t)
	cancel()
	model.releaseFirstCall()

	out := <-resultCh
	require.ErrorIs(t, out.err, context.Canceled)
	require.NotNil(t, out.res)
	assert.Equal(t, "context_cancelled", out.res.StopReason)
	assert.GreaterOrEqual(t, model.callCount(), 1)
}

func TestRunWithEmitterMaxIterationsBoundaries(t *testing.T) {
	t.Helper()
	tests := []struct {
		name          string
		maxIterations int
		wantCalls     int
	}{
		{name: "zero uses default", maxIterations: 0, wantCalls: defaultMaxIterations},
		{name: "negative uses default", maxIterations: -1, wantCalls: defaultMaxIterations},
		{name: "explicit limit", maxIterations: 3, wantCalls: 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			model := newEndlessMockModel("loop-tool")
			sessionID := fmt.Sprintf("max-boundary-%s", strings.ReplaceAll(tt.name, " ", "-"))
			agent := newAgentWithModelForTest(t, sessionID, model)
			require.NoError(t, agent.AddTool(&mockTool{name: "loop-tool"}))
			rc := RunContext{SessionID: sessionID, MaxIterations: tt.maxIterations}

			res, err := agent.runWithEmitter(context.Background(), "loop", rc, nil)
			require.NoError(t, err)
			assert.Equal(t, StopReasonMaxIterations, res.StopReason)
			assert.Equal(t, tt.wantCalls, model.callCount())
		})
	}
}

func TestRunWithEmitterCompletesWithoutToolCalls(t *testing.T) {
	t.Helper()
	sessionID := "no-tool-calls"
	agent := newAgentWithModelForTest(t, sessionID, newMockModel())
	rc := RunContext{SessionID: sessionID}

	res, err := agent.runWithEmitter(context.Background(), "plain input", rc, nil)
	require.NoError(t, err)
	assert.Equal(t, StopReasonComplete, res.StopReason)
	assert.Len(t, res.ToolCalls, 0)
	assert.Equal(t, "plain input processed", res.Output)
}

func TestRunWithEmitterSessionAppendError(t *testing.T) {
	t.Helper()
	sess := newErrorSession(errors.New("boom"))
	defer func() { _ = sess.Close() }()
	cfg := Config{Name: "session-error", DefaultContext: RunContext{SessionID: "session-error"}}
	agentAny, err := New(cfg, WithModel(newMockModel()), WithSession(sess))
	require.NoError(t, err)
	agent := agentAny.(*basicAgent)

	rc := RunContext{SessionID: "session-error"}
	res, runErr := agent.runWithEmitter(context.Background(), "hello", rc, nil)
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "session append user")
	assert.Equal(t, "session_error", res.StopReason)
}

func TestRunWithEmitterApprovalPending(t *testing.T) {
	t.Helper()
	model := newEndlessMockModel("needs-approval")
	sessionID := "approval-pending"
	agent := newAgentWithModelForTest(t, sessionID, model)
	require.NoError(t, agent.AddTool(&mockTool{name: "needs-approval"}))
	rc := RunContext{SessionID: sessionID, ApprovalMode: ApprovalRequired}

	res, err := agent.runWithEmitter(context.Background(), "invoke tool", rc, nil)
	require.ErrorIs(t, err, errApprovalPending)
	assert.Equal(t, "approval_pending", res.StopReason)
	assert.Equal(t, 1, model.callCount())
}

func TestRunWithEmitterModelError(t *testing.T) {
	t.Helper()
	sessionID := "model-error"
	failErr := errors.New("model boom")
	agent := newAgentWithModelForTest(t, sessionID, failingModel{err: failErr})
	rc := RunContext{SessionID: sessionID}

	res, err := agent.runWithEmitter(context.Background(), "boom", rc, nil)
	require.ErrorIs(t, err, failErr)
	assert.Equal(t, "model_error", res.StopReason)
	assert.Nil(t, res.ToolCalls)
}

func TestRunWithEmitterAssistantSessionError(t *testing.T) {
	t.Helper()
	sess := newErrorSessionAfter(2, errors.New("assistant append failed"))
	defer func() { _ = sess.Close() }()
	cfg := Config{Name: "assistant-session-error", DefaultContext: RunContext{SessionID: "assistant-session-error"}}
	agentAny, err := New(cfg, WithModel(newMockModel()), WithSession(sess))
	require.NoError(t, err)
	agent := agentAny.(*basicAgent)

	rc := RunContext{SessionID: "assistant-session-error"}
	res, runErr := agent.runWithEmitter(context.Background(), "hello", rc, nil)
	require.Error(t, runErr)
	assert.Contains(t, runErr.Error(), "session append assistant")
	assert.Equal(t, "session_error", res.StopReason)
}

func TestRunWithEmitterNoModelFallsBack(t *testing.T) {
	t.Helper()
	agent := newTestAgent(t).(*basicAgent)
	agent.model = nil
	rc := agent.cfg.DefaultContext

	input := "hello"
	res, err := agent.runWithEmitter(context.Background(), input, rc, nil)
	require.NoError(t, err)
	assert.Equal(t, "no_model", res.StopReason)
	assert.Equal(t, fmt.Sprintf("session %s: %s", rc.SessionID, input), res.Output)
}

func newTestAgent(t *testing.T) Agent {
	t.Helper()
	cfg := Config{Name: "unit", DefaultContext: RunContext{SessionID: "test-session"}}
	return newAgentWithConfig(t, cfg)
}

func newAgentWithConfig(t *testing.T, cfg Config, extra ...Option) Agent {
	t.Helper()
	sessionID := cfg.DefaultContext.SessionID
	if strings.TrimSpace(sessionID) == "" {
		sessionID = "test-session"
		cfg.DefaultContext.SessionID = sessionID
	}
	sess, err := session.NewMemorySession(sessionID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	opts := []Option{
		WithModel(newMockModel()),
		WithSession(sess),
	}
	opts = append(opts, extra...)
	ag, err := New(cfg, opts...)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return ag
}

type memoryEventStore struct {
	mu     sync.RWMutex
	events []event.Event
}

func (s *memoryEventStore) Append(evt event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, cloneEvent(evt))
	return nil
}

func (s *memoryEventStore) ReadSince(bookmark *event.Bookmark) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var history []event.Event
	for _, evt := range s.events {
		bm := evt.Bookmark
		if bm == nil {
			continue
		}
		if bookmark == nil || bm.Seq > bookmark.Seq {
			history = append(history, cloneEvent(evt))
		}
	}
	return history, nil
}

func (s *memoryEventStore) ReadRange(start, end *event.Bookmark) ([]event.Event, error) {
	events, _ := s.ReadSince(start)
	if end == nil {
		return events, nil
	}
	var filtered []event.Event
	for _, evt := range events {
		if evt.Bookmark != nil && evt.Bookmark.Seq <= end.Seq {
			filtered = append(filtered, evt)
		}
	}
	return filtered, nil
}

func (s *memoryEventStore) LastBookmark() (*event.Bookmark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return nil, nil
	}
	last := s.events[len(s.events)-1]
	if last.Bookmark == nil {
		return nil, nil
	}
	return last.Bookmark.Clone(), nil
}

func (s *memoryEventStore) lastBookmark() *event.Bookmark {
	bm, _ := s.LastBookmark()
	return bm
}

func cloneEvent(evt event.Event) event.Event {
	if evt.Bookmark != nil {
		copy := *evt.Bookmark
		evt.Bookmark = &copy
	}
	return evt
}

type mockModel struct {
	counter atomic.Uint64
}

func newMockModel() *mockModel {
	return &mockModel{}
}

func (m *mockModel) Generate(_ context.Context, messages []model.Message) (model.Message, error) {
	return m.generateFromMessages(messages)
}

func (m *mockModel) GenerateWithTools(ctx context.Context, messages []model.Message, _ []map[string]any) (model.Message, error) {
	return m.generateFromMessages(messages)
}

func (m *mockModel) GenerateStream(ctx context.Context, messages []model.Message, cb model.StreamCallback) error {
	resp, err := m.generateFromMessages(messages)
	if err != nil {
		return err
	}
	if cb == nil {
		return nil
	}
	if len(resp.ToolCalls) == 0 && strings.TrimSpace(resp.Content) != "" {
		chunk := resp
		chunk.Content = truncateChunk(resp.Content)
		if err := cb(model.StreamResult{Message: chunk}); err != nil {
			return err
		}
	}
	return cb(model.StreamResult{Message: resp, Final: true})
}

func (m *mockModel) generateFromMessages(messages []model.Message) (model.Message, error) {
	resp := model.Message{Role: "assistant"}
	lastUserIdx := -1
	var lastUserContent string
	for i := len(messages) - 1; i >= 0; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "user" || role == "" {
			lastUserIdx = i
			lastUserContent = strings.TrimSpace(messages[i].Content)
			break
		}
	}
	if lastUserIdx == -1 {
		resp.Content = "processed"
		return resp, nil
	}

	toolAfterUser := false
	for i := len(messages) - 1; i > lastUserIdx; i-- {
		role := strings.ToLower(strings.TrimSpace(messages[i].Role))
		if role == "tool" {
			toolAfterUser = true
			break
		}
	}

	name, params, wantsTool, parseErr := parseToolInstruction(lastUserContent)
	if parseErr != nil {
		return model.Message{}, parseErr
	}
	if wantsTool && !toolAfterUser {
		resp.Content = ""
		resp.ToolCalls = []model.ToolCall{{
			ID:        m.nextCallID(),
			Name:      name,
			Arguments: cloneArgs(params),
		}}
		return resp, nil
	}

	resp.Content = lastUserContent + " processed"
	return resp, nil
}

func (m *mockModel) nextCallID() string {
	id := m.counter.Add(1)
	return fmt.Sprintf("mock-call-%d", id)
}

func truncateChunk(content string) string {
	const chunk = 4
	runes := []rune(content)
	if len(runes) <= chunk {
		return content
	}
	return string(runes[:chunk])
}

func cloneArgs(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]any, len(src))
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

type mockModelWithEndlessTools struct {
	remainingToolCalls int
	callCount          int
}

func (m *mockModelWithEndlessTools) Generate(ctx context.Context, messages []model.Message) (model.Message, error) {
	m.callCount++
	if m.remainingToolCalls > 0 {
		m.remainingToolCalls--
		return model.Message{
			Role:    "assistant",
			Content: "calling tool",
			ToolCalls: []model.ToolCall{{
				ID:        fmt.Sprintf("call-%d", m.callCount),
				Name:      "test_tool",
				Arguments: map[string]any{},
			}},
		}, nil
	}
	return model.Message{
		Role:    "assistant",
		Content: "done",
	}, nil
}

func (m *mockModelWithEndlessTools) GenerateWithTools(ctx context.Context, messages []model.Message, tools []map[string]any) (model.Message, error) {
	return m.Generate(ctx, messages)
}

func (m *mockModelWithEndlessTools) GenerateStream(ctx context.Context, messages []model.Message, fn model.StreamCallback) error {
	msg, err := m.Generate(ctx, messages)
	if err != nil {
		return err
	}
	if fn == nil {
		return nil
	}
	return fn(model.StreamResult{Message: msg, Final: true})
}

type mockTool struct {
	name    string
	schema  *tool.JSONSchema
	result  *tool.ToolResult
	err     error
	calls   int
	lastCtx context.Context
	params  map[string]any
}

func (m *mockTool) Name() string             { return strings.TrimSpace(m.name) }
func (m *mockTool) Description() string      { return "mock" }
func (m *mockTool) Schema() *tool.JSONSchema { return m.schema }

func (m *mockTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	m.calls++
	m.lastCtx = ctx
	m.params = map[string]any{}
	for k, v := range params {
		m.params[k] = v
	}
	if m.result == nil {
		m.result = &tool.ToolResult{}
	}
	return m.result, m.err
}

func newAgentWithModelForTest(t *testing.T, sessionID string, model model.Model) *basicAgent {
	t.Helper()
	sess, err := session.NewMemorySession(sessionID)
	if err != nil {
		t.Fatalf("session: %v", err)
	}
	t.Cleanup(func() { _ = sess.Close() })
	ag, err := New(
		Config{Name: "unit", DefaultContext: RunContext{SessionID: sessionID}},
		WithModel(model),
		WithSession(sess),
	)
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	return ag.(*basicAgent)
}

type slowMockModel struct {
	toolName string
	started  chan struct{}
	release  chan struct{}
	once     sync.Once
	counter  atomic.Int32
}

func newSlowMockModel(toolName string) *slowMockModel {
	return &slowMockModel{
		toolName: toolName,
		started:  make(chan struct{}, 1),
		release:  make(chan struct{}),
	}
}

func (m *slowMockModel) waitForFirstCall(t *testing.T) {
	t.Helper()
	select {
	case <-m.started:
	case <-time.After(2 * time.Second):
		t.Fatal("slow mock model never started")
	}
}

func (m *slowMockModel) releaseFirstCall() {
	m.once.Do(func() { close(m.release) })
}

func (m *slowMockModel) callCount() int {
	return int(m.counter.Load())
}

func (m *slowMockModel) Generate(ctx context.Context, _ []model.Message) (model.Message, error) {
	call := m.counter.Add(1)
	if call == 1 {
		select {
		case m.started <- struct{}{}:
		default:
		}
		<-m.release
	}
	return model.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("slow-%d", call),
		ToolCalls: []model.ToolCall{{
			ID:        fmt.Sprintf("slow-call-%d", call),
			Name:      m.toolName,
			Arguments: map[string]any{},
		}},
	}, nil
}

func (m *slowMockModel) GenerateWithTools(ctx context.Context, messages []model.Message, _ []map[string]any) (model.Message, error) {
	return m.Generate(ctx, messages)
}

func (m *slowMockModel) GenerateStream(ctx context.Context, messages []model.Message, cb model.StreamCallback) error {
	msg, err := m.Generate(ctx, messages)
	if err != nil || cb == nil {
		return err
	}
	return cb(model.StreamResult{Message: msg, Final: true})
}

type endlessMockModel struct {
	toolName string
	counter  atomic.Int32
}

func newEndlessMockModel(toolName string) *endlessMockModel {
	return &endlessMockModel{toolName: toolName}
}

func (m *endlessMockModel) callCount() int {
	return int(m.counter.Load())
}

func (m *endlessMockModel) Generate(ctx context.Context, _ []model.Message) (model.Message, error) {
	call := m.counter.Add(1)
	return model.Message{
		Role:    "assistant",
		Content: fmt.Sprintf("loop-%d", call),
		ToolCalls: []model.ToolCall{{
			ID:        fmt.Sprintf("loop-call-%d", call),
			Name:      m.toolName,
			Arguments: map[string]any{},
		}},
	}, nil
}

func (m *endlessMockModel) GenerateWithTools(ctx context.Context, messages []model.Message, _ []map[string]any) (model.Message, error) {
	return m.Generate(ctx, messages)
}

func (m *endlessMockModel) GenerateStream(context.Context, []model.Message, model.StreamCallback) error {
	return errors.New("endlessMockModel: stream not supported")
}

type failingModel struct {
	err error
}

func (m failingModel) Generate(context.Context, []model.Message) (model.Message, error) {
	return model.Message{}, m.err
}

func (m failingModel) GenerateWithTools(ctx context.Context, messages []model.Message, _ []map[string]any) (model.Message, error) {
	return m.Generate(ctx, messages)
}

func (m failingModel) GenerateStream(context.Context, []model.Message, model.StreamCallback) error {
	return m.err
}

type errorSession struct {
	id        string
	appendErr error
}

func newErrorSession(err error) *errorSession {
	return &errorSession{
		id:        "error-session",
		appendErr: err,
	}
}

func (s *errorSession) ID() string { return s.id }

func (s *errorSession) Append(session.Message) error { return s.appendErr }

func (s *errorSession) List(session.Filter) ([]session.Message, error) { return nil, nil }

func (s *errorSession) Checkpoint(string) error { return nil }

func (s *errorSession) Resume(string) error { return nil }

func (s *errorSession) Fork(string) (session.Session, error) { return nil, nil }

func (s *errorSession) Close() error { return nil }

type errorSessionAfter struct {
	id        string
	failOn    int
	err       error
	mu        sync.Mutex
	messages  []session.Message
	appendCnt int
}

func newErrorSessionAfter(failOn int, err error) *errorSessionAfter {
	if failOn <= 0 {
		failOn = 1
	}
	return &errorSessionAfter{id: "error-session-after", failOn: failOn, err: err}
}

func (s *errorSessionAfter) ID() string { return s.id }

func (s *errorSessionAfter) Append(msg session.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.appendCnt++
	if s.appendCnt >= s.failOn {
		return s.err
	}
	s.messages = append(s.messages, msg)
	return nil
}

func (s *errorSessionAfter) List(session.Filter) ([]session.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]session.Message, len(s.messages))
	copy(out, s.messages)
	return out, nil
}

func (s *errorSessionAfter) Checkpoint(string) error { return nil }

func (s *errorSessionAfter) Resume(string) error { return nil }

func (s *errorSessionAfter) Fork(string) (session.Session, error) { return nil, nil }

func (s *errorSessionAfter) Close() error { return nil }
