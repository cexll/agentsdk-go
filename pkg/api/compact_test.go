package api

import (
	"context"
	"errors"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
)

type compactStubModel struct {
	resp string
	err  error
}

func (s *compactStubModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &model.Response{Message: model.Message{Content: s.resp}}, nil
}

func (s *compactStubModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	return nil
}

func TestCompactConfigDefaults(t *testing.T) {
	t.Parallel()

	cfg := CompactConfig{Enabled: true, PreserveCount: 0, Threshold: 2}
	got := cfg.withDefaults()
	if got.PreserveCount < 1 {
		t.Fatalf("expected preserve count default")
	}
	if got.Threshold != defaultCompactThreshold {
		t.Fatalf("expected default threshold")
	}
}

func TestCompactorMaybeCompact(t *testing.T) {
	t.Parallel()

	hist := message.NewHistory()
	hist.Append(message.Message{Role: "user", Content: "one"})
	hist.Append(message.Message{Role: "assistant", Content: "two"})
	hist.Append(message.Message{Role: "user", Content: "three"})

	comp := newCompactor("", CompactConfig{Enabled: true, PreserveCount: 1, Threshold: 0.1}, &compactStubModel{resp: "summary"}, 1, nil)
	if comp == nil {
		t.Fatalf("expected compactor")
	}
	res, ok, err := comp.maybeCompact(context.Background(), hist, "sess", nil)
	if err != nil || !ok || res.summary == "" {
		t.Fatalf("unexpected compact result res=%+v ok=%v err=%v", res, ok, err)
	}
	if hist.Len() < 2 {
		t.Fatalf("expected compacted history retained")
	}
}

func TestCompactorCompleteSummaryError(t *testing.T) {
	t.Parallel()

	comp := &compactor{cfg: CompactConfig{Enabled: true}, model: &compactStubModel{err: errors.New("boom")}}
	if _, err := comp.completeSummary(context.Background(), model.Request{}); err == nil {
		t.Fatalf("expected summary error")
	}
}
