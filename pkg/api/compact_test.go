package api

import (
	"context"
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
	c := newCompactor(cfg, &stubModel{}, 100, nil)

	below := []message.Message{
		msgWithTokens("user", 30),
		msgWithTokens("assistant", 40),
	}
	if c.shouldCompact(below) {
		t.Fatalf("expected no compaction below threshold")
	}

	above := []message.Message{
		msgWithTokens("user", 50),
		msgWithTokens("assistant", 40),
	}
	if !c.shouldCompact(above) {
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
	c := newCompactor(cfg, mdl, 50, nil)

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
	c := newCompactor(cfg, mdl, 50, hooks)

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
