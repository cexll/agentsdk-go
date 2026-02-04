package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
)

type summaryModel struct {
	content string
	errs    []error
	calls   []model.Request
}

func (m *summaryModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	m.calls = append(m.calls, req)
	if len(m.errs) > 0 {
		err := m.errs[0]
		m.errs = m.errs[1:]
		if err != nil {
			return nil, err
		}
	}
	return &model.Response{Message: model.Message{Content: m.content}}, nil
}

func (m *summaryModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return nil
}

func TestCompactor_ShouldCompactThreshold(t *testing.T) {
	cfg := CompactConfig{Enabled: true, Threshold: 0.5, PreserveCount: 1}.withDefaults()
	c := &compactor{cfg: cfg, limit: 100}
	if c.shouldCompact(1, 80) {
		t.Fatalf("expected preserve count to block compaction")
	}
	if c.shouldCompact(2, 49) {
		t.Fatalf("expected threshold to block compaction")
	}
	if !c.shouldCompact(2, 50) {
		t.Fatalf("expected threshold to trigger compaction")
	}
}

func TestCompactor_CompactFlow(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(msgWithTokens("user", 20))
	hist.Append(msgWithTokens("assistant", 20))
	hist.Append(msgWithTokens("user", 20))
	hist.Append(msgWithTokens("assistant", 20))

	mdl := &summaryModel{content: "summary"}
	comp := newCompactor("", CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1}, mdl, 10, nil)
	res, ok, err := comp.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil || !ok || res.summary == "" {
		t.Fatalf("unexpected result ok=%v err=%v res=%+v", ok, err, res)
	}
	msgs := hist.All()
	foundSummary := false
	for _, msg := range msgs {
		if msg.Role == "system" && msg.Content != "" {
			foundSummary = true
		}
	}
	if !foundSummary {
		t.Fatalf("expected summary message")
	}
}

func TestCompactor_HookDenySkips(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(msgWithTokens("user", 20))
	hist.Append(msgWithTokens("assistant", 20))
	hist.Append(msgWithTokens("user", 20))

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreCompact, Command: "exit 1"})

	comp := newCompactor("", CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1}, &summaryModel{content: "x"}, 10, exec)
	_, ok, err := comp.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected compaction to be skipped")
	}
}

func TestCompactor_PersistsRolloutEvent(t *testing.T) {
	root := t.TempDir()
	hist := message.NewHistory()
	hist.Append(msgWithTokens("user", 20))
	hist.Append(msgWithTokens("assistant", 20))
	hist.Append(msgWithTokens("user", 20))

	comp := newCompactor(root, CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1, RolloutDir: "rollout"}, &summaryModel{content: "sum"}, 10, nil)
	_, ok, err := comp.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil || !ok {
		t.Fatalf("expected compaction, ok=%v err=%v", ok, err)
	}

	entries, err := os.ReadDir(filepath.Join(root, "rollout"))
	if err != nil {
		t.Fatalf("read rollout: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected rollout files")
	}
}

func TestCompactor_RetryWithFallbackModel(t *testing.T) {
	mdl := &summaryModel{
		content: "ok",
		errs:    []error{errors.New("boom"), nil},
	}
	cfg := CompactConfig{Enabled: true, MaxRetries: 1, RetryDelay: 0, FallbackModel: "fallback"}
	comp := &compactor{cfg: cfg.withDefaults(), model: mdl, limit: 100}
	req := model.Request{Model: "primary", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	if _, err := comp.completeSummary(context.Background(), req); err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if len(mdl.calls) < 2 {
		t.Fatalf("expected retry call, got %d", len(mdl.calls))
	}
	if got := mdl.calls[1].Model; got != "fallback" {
		t.Fatalf("expected fallback model, got %q", got)
	}
}

func TestCompactor_SmartPreserveInitialAndUserText(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "system", Content: "init"})
	hist.Append(msgWithTokens("user", 8))
	hist.Append(message.Message{Role: "assistant", Content: "a1"})
	hist.Append(msgWithTokens("user", 8))
	hist.Append(message.Message{Role: "assistant", Content: "tail"})

	cfg := CompactConfig{
		Enabled:          true,
		Threshold:        0.1,
		PreserveCount:    1,
		PreserveInitial:  true,
		InitialCount:     1,
		PreserveUserText: true,
		UserTextTokens:   6,
	}
	comp := &compactor{cfg: cfg.withDefaults(), model: &summaryModel{content: "sum"}, limit: 10}

	res, err := comp.compact(context.Background(), hist, hist.All(), hist.TokenCount())
	if err != nil {
		t.Fatalf("compact error: %v", err)
	}
	if res.preservedMsgs < 2 {
		t.Fatalf("expected preserved messages, got %d", res.preservedMsgs)
	}
	msgs := hist.All()
	foundInit := false
	foundUser := false
	for _, msg := range msgs {
		if msg.Role == "system" && msg.Content == "init" {
			foundInit = true
		}
		if msg.Role == "user" {
			foundUser = true
		}
	}
	if !foundInit || !foundUser {
		t.Fatalf("expected initial and user messages preserved: %+v", msgs)
	}
}

func TestCompactorCompactNoModel(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(msgWithTokens("user", 10))
	comp := &compactor{cfg: CompactConfig{Enabled: true}.withDefaults(), model: nil, limit: 100}
	if _, err := comp.compact(context.Background(), hist, hist.All(), hist.TokenCount()); err == nil {
		t.Fatalf("expected summary model error")
	}
}

func TestCompactorNoSummarizeReturnsNoCompaction(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(message.Message{Role: "system", Content: "init"})
	hist.Append(message.Message{Role: "user", Content: "keep"})

	cfg := CompactConfig{
		Enabled:          true,
		PreserveCount:    1,
		PreserveInitial:  true,
		InitialCount:     1,
		PreserveUserText: true,
		UserTextTokens:   100,
	}
	comp := &compactor{cfg: cfg.withDefaults(), model: &summaryModel{content: "sum"}, limit: 10}
	_, err := comp.compact(context.Background(), hist, hist.All(), hist.TokenCount())
	if err == nil {
		t.Fatalf("expected no compaction error")
	}
	if !errors.Is(err, errNoCompaction) {
		t.Fatalf("expected errNoCompaction, got %v", err)
	}
}

func TestCompactorCompleteSummaryNilContext(t *testing.T) {
	comp := &compactor{cfg: CompactConfig{Enabled: true}, model: &summaryModel{content: "ok"}}
	if _, err := comp.completeSummary(nil, model.Request{Messages: []model.Message{{Role: "user", Content: "hi"}}}); err != nil {
		t.Fatalf("expected summary with nil context, got %v", err)
	}
}

func TestCompactConfigWithDefaultsBounds(t *testing.T) {
	cfg := CompactConfig{
		Enabled:          true,
		Threshold:        2,
		PreserveCount:    0,
		PreserveInitial:  true,
		InitialCount:     -1,
		PreserveUserText: true,
		UserTextTokens:   -5,
		MaxRetries:       -2,
		RetryDelay:       -time.Second,
		FallbackModel:    "  ",
		RolloutDir:       "  ",
	}
	cfg = cfg.withDefaults()
	if cfg.Threshold != defaultCompactThreshold {
		t.Fatalf("expected threshold default, got %v", cfg.Threshold)
	}
	if cfg.PreserveCount != defaultCompactPreserve {
		t.Fatalf("expected preserve count default, got %d", cfg.PreserveCount)
	}
	if cfg.InitialCount != 1 {
		t.Fatalf("expected initial count defaulted to 1, got %d", cfg.InitialCount)
	}
	if cfg.UserTextTokens != 0 || cfg.MaxRetries != 0 || cfg.RetryDelay != 0 {
		t.Fatalf("expected negative values reset to 0")
	}
	if cfg.FallbackModel != "" || cfg.RolloutDir != "" {
		t.Fatalf("expected trimmed empty strings")
	}
}

func TestCompactor_HookAskSkips(t *testing.T) {
	hist := message.NewHistory()
	hist.Append(msgWithTokens("user", 20))
	hist.Append(msgWithTokens("assistant", 20))
	hist.Append(msgWithTokens("user", 20))

	exec := corehooks.NewExecutor()
	exec.Register(corehooks.ShellHook{Event: coreevents.PreCompact, Command: "exit 2"})

	comp := newCompactor("", CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1}, &summaryModel{content: "x"}, 10, exec)
	_, ok, err := comp.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if ok {
		t.Fatalf("expected compaction to be skipped on ask")
	}
}

func TestCompactor_CompleteSummaryContextCanceled(t *testing.T) {
	mdl := &summaryModel{
		content: "ok",
		errs:    []error{errors.New("boom"), errors.New("boom")},
	}
	cfg := CompactConfig{Enabled: true, MaxRetries: 1, RetryDelay: time.Millisecond}
	comp := &compactor{cfg: cfg.withDefaults(), model: mdl, limit: 100}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := model.Request{Model: "primary", Messages: []model.Message{{Role: "user", Content: "hi"}}}
	if _, err := comp.completeSummary(ctx, req); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
}
