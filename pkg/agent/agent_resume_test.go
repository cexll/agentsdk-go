package agent

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

// helper to build events with deterministic bookmark sequencing.
func makeEvent(seq int64, typ event.EventType, sessionID string, data any) event.Event {
	return event.Event{
		Type:      typ,
		SessionID: sessionID,
		Data:      data,
		Bookmark: &event.Bookmark{
			Seq:       seq,
			Timestamp: time.Now().UTC(),
		},
	}
}

// mockEventStore captures calls and returns preset events.
type mockEventStore struct {
	mu            sync.Mutex
	events        []event.Event
	readSinceArg  *event.Bookmark
	readSinceCall int
	appendCalls   []event.Event
	readErr       error
}

func (m *mockEventStore) Append(evt event.Event) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.appendCalls = append(m.appendCalls, cloneEvent(evt))
	return nil
}

func (m *mockEventStore) ReadSince(bookmark *event.Bookmark) ([]event.Event, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.readSinceArg = bookmark
	m.readSinceCall++
	if m.readErr != nil {
		return nil, m.readErr
	}
	var out []event.Event
	for _, evt := range m.events {
		if evt.Bookmark == nil {
			continue
		}
		if bookmark == nil || evt.Bookmark.Seq > bookmark.Seq {
			out = append(out, cloneEvent(evt))
		}
	}
	return out, nil
}

func (m *mockEventStore) ReadRange(start, end *event.Bookmark) ([]event.Event, error) {
	events, _ := m.ReadSince(start)
	if end == nil {
		return events, nil
	}
	var filtered []event.Event
	for _, evt := range events {
		if evt.Bookmark != nil && evt.Bookmark.Seq <= end.Seq {
			filtered = append(filtered, cloneEvent(evt))
		}
	}
	return filtered, nil
}

func (m *mockEventStore) LastBookmark() (*event.Bookmark, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if len(m.events) == 0 {
		return nil, nil
	}
	last := m.events[len(m.events)-1].Bookmark
	if last == nil {
		return nil, nil
	}
	return last.Clone(), nil
}

// resumableSession is a lightweight Session mock that tracks resume and append operations.
type resumableSession struct {
	mu           sync.Mutex
	id           string
	messages     []session.Message
	checkpoints  map[string][]session.Message
	resumeCalled []string
}

func newResumableSession(id string, initial []session.Message) *resumableSession {
	cp := make([]session.Message, len(initial))
	copy(cp, initial)
	return &resumableSession{
		id:          id,
		messages:    cp,
		checkpoints: map[string][]session.Message{},
	}
}

func (s *resumableSession) ID() string { return s.id }

func (s *resumableSession) Append(msg session.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.messages = append(s.messages, msg)
	return nil
}

func (s *resumableSession) List(_ session.Filter) ([]session.Message, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]session.Message, len(s.messages))
	copy(out, s.messages)
	return out, nil
}

func (s *resumableSession) Checkpoint(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make([]session.Message, len(s.messages))
	copy(snap, s.messages)
	s.checkpoints[name] = snap
	return nil
}

func (s *resumableSession) Resume(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.resumeCalled = append(s.resumeCalled, name)
	snap, ok := s.checkpoints[name]
	if !ok {
		return session.ErrCheckpointNotFound
	}
	s.messages = make([]session.Message, len(snap))
	copy(s.messages, snap)
	return nil
}

func (s *resumableSession) Fork(name string) (session.Session, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	snap := make([]session.Message, len(s.messages))
	copy(snap, s.messages)
	return &resumableSession{
		id:           name,
		messages:     snap,
		checkpoints:  map[string][]session.Message{},
		resumeCalled: nil,
	}, nil
}

func (s *resumableSession) Close() error {
	return nil
}

func TestResumeFromValidBookmark(t *testing.T) {
	sessionID := "resume-session"
	events := []event.Event{
		makeEvent(1, event.EventProgress, sessionID, nil),
		makeEvent(2, event.EventToolCall, sessionID, event.ToolCallData{Name: "echo"}),
		makeEvent(3, event.EventCompletion, sessionID, event.CompletionData{Output: "done", StopReason: StopReasonComplete}),
	}
	store := &mockEventStore{events: events}
	sess := newResumableSession(sessionID, nil)

	cfg := Config{Name: "resume", DefaultContext: RunContext{SessionID: sessionID}, EventStore: store}
	agAny, err := New(cfg, WithSession(sess), WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agAny.(*basicAgent)

	bookmark := &event.Bookmark{Seq: 1}
	res, runErr := ag.Resume(context.Background(), bookmark)
	require.NoError(t, runErr)

	assert.Equal(t, 2, len(res.Events))
	assert.Equal(t, StopReasonComplete, res.StopReason)
	assert.Equal(t, "done", res.Output)
	assert.Equal(t, 1, store.readSinceCall)
	assert.NotNil(t, store.readSinceArg)
	assert.Equal(t, bookmark.Seq, store.readSinceArg.Seq)
}

func TestResumeNilBookmarkStartsFromHead(t *testing.T) {
	sessionID := "nil-bookmark"
	events := []event.Event{
		makeEvent(1, event.EventProgress, sessionID, nil),
		makeEvent(2, event.EventProgress, sessionID, nil),
	}
	store := &mockEventStore{events: events}
	cfg := Config{Name: "resume-nil", DefaultContext: RunContext{SessionID: sessionID}, EventStore: store}
	agAny, err := New(cfg, WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agAny.(*basicAgent)

	res, runErr := ag.Resume(context.Background(), nil)
	require.NoError(t, runErr)

	assert.Equal(t, len(events), len(res.Events))
	assert.Nil(t, store.readSinceArg)
	assert.Equal(t, 1, store.readSinceCall)
}

func TestResumeEmptyEventStore(t *testing.T) {
	store := &mockEventStore{events: nil}
	cfg := Config{Name: "empty-store", DefaultContext: RunContext{SessionID: "empty"}, EventStore: store}
	agAny, err := New(cfg, WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agAny.(*basicAgent)

	res, runErr := ag.Resume(context.Background(), nil)
	require.NoError(t, runErr)

	assert.Empty(t, res.Events)
	assert.Equal(t, "resume_replay", res.StopReason)
	assert.Equal(t, 1, store.readSinceCall)
}

func TestResumeEventReplayKeepsSessionState(t *testing.T) {
	sessionID := "replay-session"
	initial := []session.Message{{Role: "system", Content: "boot"}}
	sess := newResumableSession(sessionID, initial)

	events := []event.Event{
		makeEvent(5, event.EventCompletion, sessionID, event.CompletionData{
			Output:     "summary",
			StopReason: StopReasonComplete,
			Usage:      &event.UsageData{InputTokens: 1, OutputTokens: 2},
		}),
	}
	store := &mockEventStore{events: events}
	cfg := Config{Name: "replay", DefaultContext: RunContext{SessionID: sessionID}, EventStore: store}
	agAny, err := New(cfg, WithSession(sess), WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agAny.(*basicAgent)

	res, runErr := ag.Resume(context.Background(), &event.Bookmark{Seq: 1})
	require.NoError(t, runErr)

	assert.Equal(t, StopReasonComplete, res.StopReason)
	assert.Equal(t, "summary", res.Output)

	history, err := sess.List(session.Filter{})
	require.NoError(t, err)
	assert.Len(t, history, len(initial))
	assert.Empty(t, sess.resumeCalled)
}

func TestResumeThenContinueExecution(t *testing.T) {
	sessionID := "continue-session"
	events := []event.Event{
		makeEvent(2, event.EventCompletion, sessionID, event.CompletionData{Output: "old", StopReason: StopReasonComplete}),
	}
	store := &mockEventStore{events: events}
	sess := newResumableSession(sessionID, nil)

	cfg := Config{Name: "continue", DefaultContext: RunContext{SessionID: sessionID}, EventStore: store}
	agAny, err := New(cfg, WithSession(sess), WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agAny.(*basicAgent)

	res, runErr := ag.Resume(context.Background(), &event.Bookmark{Seq: 1})
	require.NoError(t, runErr)
	assert.Equal(t, StopReasonComplete, res.StopReason)

	toolStub := &mockTool{name: "echo", result: &tool.ToolResult{Output: "pong"}}
	require.NoError(t, ag.AddTool(toolStub))

	runRes, runErr := ag.Run(context.Background(), "tool:echo {}")
	require.NoError(t, runErr)
	assert.Equal(t, StopReasonComplete, runRes.StopReason)
	assert.Equal(t, 1, toolStub.calls)

	store.mu.Lock()
	appended := len(store.appendCalls)
	store.mu.Unlock()
	assert.Greater(t, appended, 0, "resume should not break subsequent event persistence")
}

func TestResumeNilContext(t *testing.T) {
	ag := newBasicAgentForResumeTest(t, Config{Name: "nil-ctx", DefaultContext: RunContext{SessionID: "test"}})
	_, err := ag.Resume(nil, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "context is nil")
}

func TestResumeContextError(t *testing.T) {
	ag := newBasicAgentForResumeTest(t, Config{Name: "ctx-error", DefaultContext: RunContext{SessionID: "test"}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := ag.Resume(ctx, nil)
	require.Error(t, err)
	assert.ErrorIs(t, err, context.Canceled)
}

func TestResumeWithoutEventStore(t *testing.T) {
	cfg := Config{Name: "no-store", DefaultContext: RunContext{SessionID: "test"}}
	sess := newResumableSession(cfg.DefaultContext.SessionID, nil)
	agentAny, err := New(cfg, WithSession(sess), WithModel(newMockModel()))
	require.NoError(t, err)
	ag := agentAny.(*basicAgent)

	_, err = ag.Resume(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "event store not configured")
}

func TestResumeEventStoreError(t *testing.T) {
	store := &mockEventStore{readErr: errors.New("boom")}
	cfg := Config{Name: "store-error", DefaultContext: RunContext{SessionID: "test"}, EventStore: store}
	ag := newBasicAgentForResumeTest(t, cfg)
	_, err := ag.Resume(context.Background(), nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "boom")
}

func newBasicAgentForResumeTest(t *testing.T, cfg Config) *basicAgent {
	t.Helper()
	if cfg.EventStore == nil {
		cfg.EventStore = &mockEventStore{}
	}
	resumeSession := newResumableSession(cfg.DefaultContext.SessionID, nil)
	agentAny, err := New(cfg, WithSession(resumeSession), WithModel(newMockModel()))
	require.NoError(t, err)
	return agentAny.(*basicAgent)
}
