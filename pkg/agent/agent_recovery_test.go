package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

func TestWatchdogHandlerTriggersAutoSealAndStops(t *testing.T) {
	t.Parallel()

	sess := newTimeoutSession(t, "watchdog-session", 0, 0)
	pending := session.Message{
		Role: "assistant",
		ToolCalls: []session.ToolCall{{
			ID:        "pending-tool",
			Name:      "slow_tool",
			Arguments: map[string]any{"payload": 1},
			Timestamp: time.Now(),
		}},
	}
	require.NoError(t, sess.Append(pending))

	agent := &basicAgent{
		cfg:     Config{Name: "watchdog", EnableRecovery: true, DefaultContext: RunContext{SessionID: "watchdog-session"}},
		session: sess,
	}
	agent.recoveryMgr = NewRecoveryManager(sess)

	var canceled atomic.Bool
	cancelFunc := context.CancelFunc(func() {
		canceled.Store(true)
	})

	handler := agent.watchdogHandler(agent.cfg.DefaultContext, cancelFunc, 25*time.Millisecond)
	require.NotNil(t, handler)

	var timeouts atomic.Int32
	wd := NewWatchdog(20*time.Millisecond, func() {
		timeouts.Add(1)
		handler()
	})
	require.NotNil(t, wd)

	ctx, ctxCancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		wd.Stop()
		ctxCancel()
	})
	wd.Start(ctx)

	require.Eventually(t, func() bool {
		return timeouts.Load() >= 1
	}, time.Second, 5*time.Millisecond, "watchdog never triggered")

	require.Eventually(t, func() bool {
		return canceled.Load()
	}, time.Second, 5*time.Millisecond, "watchdog did not invoke cancel")
	require.Eventually(t, func() bool {
		msgs, err := sess.List(session.Filter{})
		require.NoError(t, err)
		if len(msgs) < 2 {
			return false
		}
		last := msgs[len(msgs)-1]
		if len(last.ToolCalls) == 0 {
			return false
		}
		auto, _ := last.ToolCalls[0].Metadata["auto_sealed"].(bool)
		return last.Role == "tool" && auto
	}, time.Second, 5*time.Millisecond, "auto seal record missing")

	require.GreaterOrEqual(t, sess.listCount(), 1, "watchdog should scan session")

	wd.Stop()
	prev := timeouts.Load()
	time.Sleep(40 * time.Millisecond)
	assert.Equal(t, prev, timeouts.Load(), "watchdog triggered after Stop")
}

func TestWatchdogHandlerDisabledReturnsNil(t *testing.T) {
	t.Parallel()
	agent := &basicAgent{
		cfg: Config{Name: "disabled", EnableRecovery: false},
	}
	handler := agent.watchdogHandler(RunContext{}, nil, time.Second)
	assert.Nil(t, handler)
}

func TestAutoSealPendingSealsUnfinishedCalls(t *testing.T) {
	t.Parallel()

	sess := newTimeoutSession(t, "autoseal-session", 0, 0)
	require.NoError(t, sess.Append(session.Message{
		Role: "assistant",
		ToolCalls: []session.ToolCall{{
			ID:        "tool-1",
			Name:      "fsync",
			Arguments: map[string]any{"path": "/tmp"},
			Timestamp: time.Now(),
		}},
	}))

	agent := &basicAgent{
		cfg:         Config{Name: "autoseal", EnableRecovery: true},
		session:     sess,
		recoveryMgr: NewRecoveryManager(sess),
	}

	agent.autoSealPending(nil)

	msgs, err := sess.List(session.Filter{})
	require.NoError(t, err)
	require.Len(t, msgs, 2, "sealed tool call should be appended")
	toolMsg := msgs[1]
	require.Equal(t, "tool", toolMsg.Role)
	require.NotEmpty(t, toolMsg.ToolCalls)
	auto, _ := toolMsg.ToolCalls[0].Metadata["auto_sealed"].(bool)
	assert.True(t, auto, "auto sealed flag missing")
	assert.GreaterOrEqual(t, sess.listCount(), 1, "AutoSeal should enumerate existing history")

	t.Run("disabled recovery", func(t *testing.T) {
		disabledSession := newTimeoutSession(t, "autoseal-off", 0, 0)
		agent := &basicAgent{
			cfg:         Config{Name: "off", EnableRecovery: false},
			session:     disabledSession,
			recoveryMgr: NewRecoveryManager(disabledSession),
		}
		agent.autoSealPending(context.Background())
		msgs, err := disabledSession.List(session.Filter{})
		require.NoError(t, err)
		assert.Len(t, msgs, 0, "no records expected when recovery disabled")
	})
}

func TestConfigureRecoveryComponents(t *testing.T) {
	t.Parallel()

	sess := newTimeoutSession(t, "config-session", 0, 0)
	store := newTestEventStore()
	agent := &basicAgent{
		cfg:        Config{Name: "cfg", EnableRecovery: true},
		session:    sess,
		eventStore: store,
	}
	require.NoError(t, store.Append(event.Event{Type: event.EventProgress, Bookmark: &event.Bookmark{Seq: 1}}))
	require.NoError(t, store.Append(event.Event{Type: event.EventProgress, Bookmark: &event.Bookmark{Seq: 2}}))
	last := store.Events()[len(store.Events())-1].Bookmark
	require.NotNil(t, last)

	agent.configureRecoveryComponents()
	require.NotNil(t, agent.recoveryMgr, "recovery manager should be initialized")
	assert.Equal(t, sess, agent.recoveryMgr.session)

	agent.initEventSequencer()
	assert.Equal(t, last.Seq, agent.eventSeq.Load(), "event sequencer should pick up bookmark")

	t.Run("recovery disabled", func(t *testing.T) {
		disabled := &basicAgent{
			cfg:     Config{Name: "cfg-off", EnableRecovery: false},
			session: sess,
		}
		disabled.configureRecoveryComponents()
		assert.Nil(t, disabled.recoveryMgr)
	})
}

func TestCrashRecoveryIntegration(t *testing.T) {
	t.Parallel()

	sess := newTimeoutSession(t, "crash-session", 0, 0)
	require.NoError(t, sess.Append(session.Message{
		Role: "assistant",
		ToolCalls: []session.ToolCall{{
			ID:        "pending-crash",
			Name:      "long_task",
			Arguments: map[string]any{"step": "prepare"},
			Timestamp: time.Now(),
		}},
	}))

	store := newTestEventStore()
	cfg := Config{
		Name:           "crash-agent",
		EnableRecovery: true,
		DefaultContext: RunContext{SessionID: "crash-session"},
		EventStore:     store,
	}

	agentAny, err := New(cfg, WithModel(newCrashingModel(errors.New("model crashed"))), WithSession(sess))
	require.NoError(t, err)
	agent := agentAny.(*basicAgent)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	result, runErr := agent.Run(ctx, "please crash")
	require.Error(t, runErr)
	require.NotNil(t, result)
	assert.Equal(t, "model_error", result.StopReason)

	msgs, err := sess.List(session.Filter{})
	require.NoError(t, err)
	assert.Condition(t, func() bool {
		for _, msg := range msgs {
			for _, tc := range msg.ToolCalls {
				if auto, _ := tc.Metadata["auto_sealed"].(bool); auto {
					return true
				}
			}
		}
		return false
	}, "auto seal record not found after crash recovery")

	events := store.Events()
	require.GreaterOrEqual(t, len(events), 2, "run should emit events before crashing")
	assert.Equal(t, event.EventProgress, events[0].Type)
	assert.Equal(t, event.EventError, events[len(events)-1].Type)
	for _, evt := range events {
		require.NotNil(t, evt.Bookmark, "event should carry bookmark for resume")
	}

	resumeFrom := events[0].Bookmark
	resume, err := agent.Resume(context.Background(), resumeFrom)
	require.NoError(t, err)
	require.NotNil(t, resume)
	assert.Equal(t, "resume_replay", resume.StopReason)
	require.GreaterOrEqual(t, len(resume.Events), len(events)-1)
	assert.Equal(t, events[len(events)-1].Type, resume.Events[len(resume.Events)-1].Type)
}

type timeoutSession struct {
	inner       session.Session
	appendDelay time.Duration
	listDelay   time.Duration
	appendCalls atomic.Int32
	listCalls   atomic.Int32
}

func newTimeoutSession(t *testing.T, id string, listDelay, appendDelay time.Duration) *timeoutSession {
	t.Helper()
	inner, err := session.NewMemorySession(id)
	require.NoError(t, err)
	ts := &timeoutSession{
		inner:       inner,
		appendDelay: appendDelay,
		listDelay:   listDelay,
	}
	t.Cleanup(func() { _ = inner.Close() })
	return ts
}

func (s *timeoutSession) ID() string {
	return s.inner.ID()
}

func (s *timeoutSession) Append(msg session.Message) error {
	if s.appendDelay > 0 {
		time.Sleep(s.appendDelay)
	}
	s.appendCalls.Add(1)
	return s.inner.Append(msg)
}

func (s *timeoutSession) List(filter session.Filter) ([]session.Message, error) {
	if s.listDelay > 0 {
		time.Sleep(s.listDelay)
	}
	s.listCalls.Add(1)
	return s.inner.List(filter)
}

func (s *timeoutSession) Checkpoint(name string) error {
	return s.inner.Checkpoint(name)
}

func (s *timeoutSession) Resume(name string) error {
	return s.inner.Resume(name)
}

func (s *timeoutSession) Fork(name string) (session.Session, error) {
	return s.inner.Fork(name)
}

func (s *timeoutSession) Close() error {
	return s.inner.Close()
}

func (s *timeoutSession) appendCount() int {
	return int(s.appendCalls.Load())
}

func (s *timeoutSession) listCount() int {
	return int(s.listCalls.Load())
}

type crashingModel struct {
	err   error
	calls atomic.Int32
}

func newCrashingModel(err error) model.Model {
	if err == nil {
		err = errors.New("crashing model")
	}
	return &crashingModel{err: err}
}

func (m *crashingModel) Generate(context.Context, []model.Message) (model.Message, error) {
	m.calls.Add(1)
	return model.Message{}, m.err
}

func (m *crashingModel) GenerateStream(context.Context, []model.Message, model.StreamCallback) error {
	m.calls.Add(1)
	return m.err
}

type testEventStore struct {
	mu     sync.RWMutex
	events []event.Event
}

func newTestEventStore() *testEventStore {
	return &testEventStore{}
}

func (s *testEventStore) Append(evt event.Event) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, cloneEvent(evt))
	return nil
}

func (s *testEventStore) ReadSince(bookmark *event.Bookmark) ([]event.Event, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var history []event.Event
	for _, evt := range s.events {
		if evt.Bookmark == nil {
			continue
		}
		if bookmark == nil || evt.Bookmark.Seq > bookmark.Seq {
			history = append(history, cloneEvent(evt))
		}
	}
	return history, nil
}

func (s *testEventStore) ReadRange(start, end *event.Bookmark) ([]event.Event, error) {
	events, err := s.ReadSince(start)
	if err != nil {
		return nil, err
	}
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

func (s *testEventStore) LastBookmark() (*event.Bookmark, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if len(s.events) == 0 {
		return nil, nil
	}
	last := s.events[len(s.events)-1].Bookmark
	if last == nil {
		return nil, nil
	}
	copy := *last
	return &copy, nil
}

func (s *testEventStore) Events() []event.Event {
	s.mu.RLock()
	defer s.mu.RUnlock()
	copyEvents := make([]event.Event, len(s.events))
	for i, evt := range s.events {
		copyEvents[i] = cloneEvent(evt)
	}
	return copyEvents
}
