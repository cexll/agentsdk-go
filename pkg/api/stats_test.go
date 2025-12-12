package api

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
)

func TestTokenTracker_RecordAndGetStats(t *testing.T) {
	tr := newTokenTracker(true, nil)
	if !tr.IsEnabled() {
		t.Fatalf("expected tracker enabled")
	}

	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)
	tr.Record(TokenStats{
		InputTokens:   10,
		OutputTokens:  5,
		TotalTokens:   15,
		CacheCreation: 2,
		CacheRead:     1,
		Model:         "m1",
		SessionID:     "s1",
		RequestID:     "r1",
		Timestamp:     base,
	})
	tr.Record(TokenStats{
		InputTokens:  3,
		OutputTokens: 7,
		TotalTokens:  10,
		Model:        "m2",
		SessionID:    "s1",
		Timestamp:    base.Add(time.Minute),
	})
	// Record with empty model should not create per-model stats.
	tr.Record(TokenStats{
		InputTokens:  1,
		OutputTokens: 1,
		TotalTokens:  2,
		SessionID:    "s1",
		Timestamp:    base.Add(2 * time.Minute),
	})

	s := tr.GetSessionStats("s1")
	if s == nil {
		t.Fatalf("expected session stats")
	}
	if s.TotalInput != 14 || s.TotalOutput != 13 || s.TotalTokens != 27 {
		t.Fatalf("unexpected totals: %+v", s)
	}
	if s.CacheCreated != 2 || s.CacheRead != 1 {
		t.Fatalf("unexpected cache totals: %+v", s)
	}
	if s.RequestCount != 3 {
		t.Fatalf("unexpected request count: %d", s.RequestCount)
	}
	if !s.FirstRequest.Equal(base) || !s.LastRequest.Equal(base.Add(2*time.Minute)) {
		t.Fatalf("unexpected request timestamps: %+v", s)
	}
	if len(s.ByModel) != 2 {
		t.Fatalf("expected 2 models, got %d", len(s.ByModel))
	}
	if m1 := s.ByModel["m1"]; m1 == nil || m1.TotalTokens != 15 || m1.RequestCount != 1 {
		t.Fatalf("unexpected m1 stats: %+v", m1)
	}
	if m2 := s.ByModel["m2"]; m2 == nil || m2.TotalTokens != 10 || m2.RequestCount != 1 {
		t.Fatalf("unexpected m2 stats: %+v", m2)
	}

	total := tr.GetTotalStats()
	if total == nil {
		t.Fatalf("expected total stats")
	}
	if total.SessionID != "_total" {
		t.Fatalf("unexpected total session id: %s", total.SessionID)
	}
	if total.TotalInput != s.TotalInput || total.TotalOutput != s.TotalOutput || total.TotalTokens != s.TotalTokens {
		t.Fatalf("unexpected total totals: %+v", total)
	}
	if total.RequestCount != s.RequestCount {
		t.Fatalf("unexpected total request count: %d", total.RequestCount)
	}

	// Ensure copies are returned.
	s.TotalInput = 0
	s.ByModel["m1"].InputTokens = 0
	again := tr.GetSessionStats("s1")
	if again.TotalInput == 0 || again.ByModel["m1"].InputTokens == 0 {
		t.Fatalf("expected stats to be copied, got %+v", again)
	}

	if tr.GetSessionStats("missing") != nil {
		t.Fatalf("expected nil for missing session")
	}
}

func TestTokenTracker_ConcurrencySafety(t *testing.T) {
	tr := newTokenTracker(true, nil)
	base := time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC)

	const goroutines = 200
	const sessions = 5
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		i := i
		go func() {
			defer wg.Done()
			tr.Record(TokenStats{
				InputTokens:  1,
				OutputTokens: 1,
				TotalTokens:  2,
				Model:        "m",
				SessionID:    fmt.Sprintf("s%d", i%sessions),
				Timestamp:    base.Add(time.Duration(i) * time.Millisecond),
			})
		}()
	}
	wg.Wait()

	total := tr.GetTotalStats()
	if total.TotalInput != goroutines || total.TotalOutput != goroutines || total.TotalTokens != goroutines*2 {
		t.Fatalf("unexpected concurrent totals: %+v", total)
	}
	if total.RequestCount != goroutines {
		t.Fatalf("unexpected concurrent request count: %d", total.RequestCount)
	}

	for i := 0; i < sessions; i++ {
		s := tr.GetSessionStats(fmt.Sprintf("s%d", i))
		if s == nil {
			t.Fatalf("expected session stats for s%d", i)
		}
		if s.RequestCount == 0 {
			t.Fatalf("expected non-zero request count for s%d", i)
		}
	}
}

func TestTokenTracker_Callback(t *testing.T) {
	ch := make(chan TokenStats, 1)
	cb := func(stats TokenStats) {
		ch <- stats
	}
	tr := newTokenTracker(true, cb)
	ts := TokenStats{
		InputTokens:  2,
		OutputTokens: 3,
		TotalTokens:  5,
		Model:        "m",
		SessionID:    "s",
		Timestamp:    time.Now().UTC(),
	}
	tr.Record(ts)
	select {
	case got := <-ch:
		if got.TotalTokens != ts.TotalTokens || got.SessionID != ts.SessionID {
			t.Fatalf("unexpected callback stats: %+v", got)
		}
	case <-time.After(time.Second):
		t.Fatalf("expected callback to fire")
	}

	disabled := newTokenTracker(false, cb)
	disabled.Record(ts)
	select {
	case <-ch:
		t.Fatalf("did not expect callback when disabled")
	case <-time.After(50 * time.Millisecond):
	}
}

func TestTokenStatsFromUsage(t *testing.T) {
	usage := model.Usage{
		InputTokens:         4,
		OutputTokens:        6,
		TotalTokens:         10,
		CacheReadTokens:     2,
		CacheCreationTokens: 3,
	}
	stats := tokenStatsFromUsage(usage, "model-x", "sess", "req")
	if stats.InputTokens != 4 || stats.OutputTokens != 6 || stats.TotalTokens != 10 {
		t.Fatalf("unexpected token mapping: %+v", stats)
	}
	if stats.CacheRead != 2 || stats.CacheCreation != 3 {
		t.Fatalf("unexpected cache mapping: %+v", stats)
	}
	if stats.Model != "model-x" || stats.SessionID != "sess" || stats.RequestID != "req" {
		t.Fatalf("unexpected identifiers: %+v", stats)
	}
	if stats.Timestamp.IsZero() {
		t.Fatalf("expected timestamp to be set")
	}
}

func TestTokenTracker_NilReceiver(t *testing.T) {
	var tr *tokenTracker
	tr.Record(TokenStats{})
	if tr.GetSessionStats("s") != nil {
		t.Fatalf("expected nil stats for nil receiver")
	}
	if tr.GetTotalStats() != nil {
		t.Fatalf("expected nil total stats for nil receiver")
	}
	if tr.IsEnabled() {
		t.Fatalf("expected disabled for nil receiver")
	}
}
