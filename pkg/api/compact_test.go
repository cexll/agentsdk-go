package api

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
)

func msgWithTokens(role string, tokens int) message.Message {
	if tokens < 1 {
		tokens = 1
	}
	return message.Message{
		Role:    role,
		Content: strings.Repeat("a", tokens*4),
	}
}

func TestCompactor_ShouldCompactThreshold(t *testing.T) {
	cfg := CompactConfig{Enabled: true, Threshold: 0.8, PreserveCount: 1}
	c := newCompactor(t.TempDir(), cfg, &stubModel{}, 100, nil)

	below := []message.Message{
		msgWithTokens("user", 30),
		msgWithTokens("assistant", 40),
	}
	if c.shouldCompact(len(below), 70) {
		t.Fatalf("expected no compaction below threshold")
	}

	above := []message.Message{
		msgWithTokens("user", 50),
		msgWithTokens("assistant", 40),
	}
	if !c.shouldCompact(len(above), 90) {
		t.Fatalf("expected compaction above threshold")
	}
}

func TestCompactor_CompactFlow(t *testing.T) {
	hist := message.NewHistory()
	original := []message.Message{
		msgWithTokens("user", 10),
		msgWithTokens("assistant", 10),
		msgWithTokens("user", 10),
		msgWithTokens("assistant", 10),
		msgWithTokens("user", 10),
	}
	for _, m := range original {
		hist.Append(m)
	}

	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", Content: "SUM"}},
	}}
	rec := defaultHookRecorder()
	cfg := CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 2}
	c := newCompactor(t.TempDir(), cfg, mdl, 50, nil)

	_, compacted, err := c.maybeCompact(context.Background(), hist, "sess", rec)
	if err != nil {
		t.Fatalf("maybeCompact returned error: %v", err)
	}
	if !compacted {
		t.Fatalf("expected history to be compacted")
	}

	got := hist.All()
	if len(got) != 3 {
		t.Fatalf("expected 3 messages after compaction, got %d", len(got))
	}
	if got[0].Role != "system" || !strings.Contains(got[0].Content, "SUM") {
		t.Fatalf("expected system summary message, got %+v", got[0])
	}
	if got[1].Content != original[len(original)-2].Content || got[2].Content != original[len(original)-1].Content {
		t.Fatalf("preserved messages mismatch: %+v", got[1:])
	}

	events := rec.Drain()
	if len(events) != 2 {
		t.Fatalf("expected 2 events, got %d", len(events))
	}
	if events[0].Type != coreevents.PreCompact {
		t.Fatalf("expected first event PreCompact, got %s", events[0].Type)
	}
	if events[1].Type != coreevents.ContextCompacted {
		t.Fatalf("expected second event ContextCompacted, got %s", events[1].Type)
	}
}

func TestCompactor_HookDenySkips(t *testing.T) {
	hist := message.NewHistory()
	for i := 0; i < 4; i++ {
		hist.Append(msgWithTokens("user", 20))
	}

	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", Content: "NOPE"}},
	}}
	hooks := corehooks.NewExecutor()
	hooks.Register(corehooks.ShellHook{Event: coreevents.PreCompact, Command: "exit 1"})

	rec := defaultHookRecorder()
	cfg := CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1}
	c := newCompactor(t.TempDir(), cfg, mdl, 50, hooks)

	_, compacted, err := c.maybeCompact(context.Background(), hist, "sess", rec)
	if err != nil {
		t.Fatalf("maybeCompact returned error: %v", err)
	}
	if compacted {
		t.Fatalf("expected compaction to be skipped on deny")
	}
	if mdl.idx != 0 {
		t.Fatalf("summary model should not be called when denied")
	}
	if got := hist.All(); len(got) != 4 {
		t.Fatalf("history should remain unchanged, got %d messages", len(got))
	}

	events := rec.Drain()
	if len(events) != 1 || events[0].Type != coreevents.PreCompact {
		t.Fatalf("expected only PreCompact event, got %+v", events)
	}
}

type flakyModel struct {
	requests []model.Request
	calls    int
}

func (m *flakyModel) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	m.calls++
	m.requests = append(m.requests, req)
	if m.calls == 1 {
		return nil, errors.New("boom")
	}
	return &model.Response{Message: model.Message{Role: "assistant", Content: "SUM"}}, nil
}

func (m *flakyModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return errors.New("stream not supported")
}

func TestCompactor_RetryWithFallbackModel(t *testing.T) {
	hist := message.NewHistory()
	for i := 0; i < 4; i++ {
		hist.Append(msgWithTokens("user", 20))
	}

	mdl := &flakyModel{}
	cfg := CompactConfig{
		Enabled:       true,
		Threshold:     0.1,
		PreserveCount: 1,
		SummaryModel:  "primary",
		MaxRetries:    1,
		RetryDelay:    0,
		FallbackModel: "fallback",
	}
	c := newCompactor(t.TempDir(), cfg, mdl, 50, nil)

	_, compacted, err := c.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil {
		t.Fatalf("maybeCompact returned error: %v", err)
	}
	if !compacted {
		t.Fatalf("expected history to be compacted")
	}
	if mdl.calls != 2 {
		t.Fatalf("expected 2 summary attempts, got %d", mdl.calls)
	}
	if got := mdl.requests[0].Model; got != "primary" {
		t.Fatalf("attempt 1 model=%q, want %q", got, "primary")
	}
	if got := mdl.requests[1].Model; got != "fallback" {
		t.Fatalf("attempt 2 model=%q, want %q", got, "fallback")
	}
}

func TestCompactor_SmartPreserveInitialAndUserText(t *testing.T) {
	hist := message.NewHistory()
	original := []message.Message{
		msgWithTokens("system", 1),
		msgWithTokens("user", 2),
		msgWithTokens("assistant", 3),
		msgWithTokens("user", 10),
		msgWithTokens("assistant", 3),
		msgWithTokens("user", 20),
		msgWithTokens("assistant", 3),
		msgWithTokens("user", 20),
		msgWithTokens("assistant", 1),
		msgWithTokens("user", 1),
	}
	for _, m := range original {
		hist.Append(m)
	}

	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", Content: "SUM"}},
	}}
	cfg := CompactConfig{
		Enabled:          true,
		Threshold:        0.8,
		PreserveCount:    2,
		PreserveInitial:  true,
		InitialCount:     2,
		PreserveUserText: true,
		UserTextTokens:   30,
	}
	c := newCompactor(t.TempDir(), cfg, mdl, 50, nil)

	_, compacted, err := c.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil {
		t.Fatalf("maybeCompact returned error: %v", err)
	}
	if !compacted {
		t.Fatalf("expected history to be compacted")
	}

	got := hist.All()
	if len(got) != 7 {
		t.Fatalf("expected 7 messages after compaction, got %d", len(got))
	}
	if got[0].Content != original[0].Content || got[1].Content != original[1].Content {
		t.Fatalf("initial context mismatch: %+v", got[:2])
	}
	if got[2].Role != "system" || !strings.Contains(got[2].Content, "SUM") {
		t.Fatalf("expected summary message at index 2, got %+v", got[2])
	}
	if got[3].Content != original[5].Content || got[4].Content != original[7].Content {
		t.Fatalf("preserved user text mismatch: %+v", got[3:5])
	}
	if got[5].Content != original[8].Content || got[6].Content != original[9].Content {
		t.Fatalf("preserved tail mismatch: %+v", got[5:])
	}
}

func TestCompactor_PersistsRolloutEvent(t *testing.T) {
	root := t.TempDir()
	rolloutDir := filepath.Join(".trace", "rollout")

	hist := message.NewHistory()
	for i := 0; i < 5; i++ {
		hist.Append(msgWithTokens("user", 20))
	}

	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", Content: "SUM"}},
	}}
	cfg := CompactConfig{
		Enabled:       true,
		Threshold:     0.1,
		PreserveCount: 1,
		RolloutDir:    rolloutDir,
	}
	c := newCompactor(root, cfg, mdl, 50, nil)

	_, compacted, err := c.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil {
		t.Fatalf("maybeCompact returned error: %v", err)
	}
	if !compacted {
		t.Fatalf("expected history to be compacted")
	}

	entries, err := os.ReadDir(filepath.Join(root, rolloutDir))
	if err != nil {
		t.Fatalf("read rollout dir: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 rollout file, got %d", len(entries))
	}
	path := filepath.Join(root, rolloutDir, entries[0].Name())
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read rollout file: %v", err)
	}
	var evt CompactEvent
	if err := json.Unmarshal(raw, &evt); err != nil {
		t.Fatalf("unmarshal rollout: %v", err)
	}
	if evt.SessionID != "sess" {
		t.Fatalf("SessionID=%q, want %q", evt.SessionID, "sess")
	}
	if evt.Summary != "SUM" {
		t.Fatalf("Summary=%q, want %q", evt.Summary, "SUM")
	}
	if evt.OriginalMessages != 5 {
		t.Fatalf("OriginalMessages=%d, want %d", evt.OriginalMessages, 5)
	}
	if evt.EstimatedTokensAfter <= 0 || evt.EstimatedTokensBefore <= 0 {
		t.Fatalf("expected token estimates to be populated: %+v", evt)
	}
}
