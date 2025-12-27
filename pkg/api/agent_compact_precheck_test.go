package api

import (
	"context"
	"strings"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
)

func TestRuntimePrepare_PrecheckCompactsHistory(t *testing.T) {
	auto := CompactConfig{Enabled: true, Threshold: 0.8, PreserveCount: 1}
	rt := newTestRuntime(t, staticModel{content: "SUM"}, auto)

	sessionID := "sess"
	hist := rt.histories.Get(sessionID)
	for i := 0; i < 10; i++ {
		hist.Append(msgWithTokens("user", 20))
	}

	prep, err := rt.prepare(context.Background(), Request{Prompt: "hello", SessionID: sessionID})
	if err != nil {
		t.Fatalf("prepare: %v", err)
	}

	got := hist.All()
	if len(got) == 0 || got[0].Role != "system" || !strings.Contains(got[0].Content, "SUM") {
		t.Fatalf("expected history to be compacted during prepare, got %+v", got)
	}
	for _, msg := range got {
		if msg.Role == "user" && msg.Content == "hello" {
			t.Fatalf("prompt should not be appended during prepare")
		}
	}

	events := prep.recorder.Drain()
	mustContainEventType(t, events, coreevents.PreCompact)
	mustContainEventType(t, events, coreevents.ContextCompacted)
}
