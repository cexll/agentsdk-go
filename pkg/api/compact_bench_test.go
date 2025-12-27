package api

import (
	"testing"

	"github.com/cexll/agentsdk-go/pkg/message"
)

var benchShouldCompact bool

func oldShouldCompact(cfg CompactConfig, limit int, msgs []message.Message) bool {
	if !cfg.Enabled {
		return false
	}
	if len(msgs) <= cfg.PreserveCount {
		return false
	}
	var counter message.NaiveCounter
	total := 0
	for _, msg := range msgs {
		total += counter.Count(msg)
	}
	if total <= 0 || limit <= 0 {
		return false
	}
	return float64(total)/float64(limit) >= cfg.Threshold
}

func BenchmarkShouldCompact_Old(b *testing.B) {
	cfg := CompactConfig{Enabled: true, Threshold: 0.8, PreserveCount: 5}
	limit := 100000
	msgs := make([]message.Message, 0, 2048)
	for i := 0; i < cap(msgs); i++ {
		msgs = append(msgs, msgWithTokens("user", 20))
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchShouldCompact = oldShouldCompact(cfg, limit+(i&1), msgs)
	}
}

func BenchmarkShouldCompact_New(b *testing.B) {
	cfg := CompactConfig{Enabled: true, Threshold: 0.8, PreserveCount: 5}
	limit := 100000
	c := &compactor{cfg: cfg.withDefaults(), limit: limit}

	msgs := make([]message.Message, 0, 2048)
	for i := 0; i < cap(msgs); i++ {
		msgs = append(msgs, msgWithTokens("user", 20))
	}
	var counter message.NaiveCounter
	tokenCount := 0
	for _, msg := range msgs {
		tokenCount += counter.Count(msg)
	}

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		benchShouldCompact = c.shouldCompact(len(msgs), tokenCount+(i&1))
	}
}
