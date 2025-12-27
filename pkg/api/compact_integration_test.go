package api

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/message"
)

func TestRuntimeRun_LongConversationCompactsAndPersists(t *testing.T) {
	auto := CompactConfig{
		Enabled:          true,
		Threshold:        0.8,
		PreserveCount:    2,
		PreserveInitial:  true,
		InitialCount:     2,
		PreserveUserText: true,
		UserTextTokens:   40,
		RolloutDir:       filepath.Join(".trace", "rollout"),
	}
	rt := newTestRuntime(t, staticModel{content: "SUM"}, auto)

	sessionID := "sess-long"
	hist := rt.histories.Get(sessionID)
	hist.Append(message.Message{Role: "system", Content: "INIT_SYSTEM"})
	hist.Append(message.Message{Role: "user", Content: "INIT_USER"})
	for i := 0; i < 120; i++ {
		role := "assistant"
		if i%2 == 0 {
			role = "user"
		}
		hist.Append(msgWithTokens(role, 10))
	}

	if _, err := rt.Run(context.Background(), Request{Prompt: "hello", SessionID: sessionID}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	got := hist.All()
	foundInit := false
	foundSummary := false
	for _, msg := range got {
		if msg.Role == "system" && msg.Content == "INIT_SYSTEM" {
			foundInit = true
		}
		if msg.Role == "system" && strings.Contains(msg.Content, "对话摘要") {
			foundSummary = true
		}
	}
	if !foundInit {
		t.Fatalf("expected initial context to be preserved, got %+v", got)
	}
	if !foundSummary {
		t.Fatalf("expected summary message to be present, got %+v", got)
	}

	entries, err := os.ReadDir(filepath.Join(rt.opts.ProjectRoot, ".trace", "rollout"))
	if err != nil {
		t.Fatalf("read rollout dir: %v", err)
	}
	if len(entries) == 0 {
		t.Fatalf("expected rollout files to be written")
	}

	maxOriginal := 0
	for _, entry := range entries {
		raw, err := os.ReadFile(filepath.Join(rt.opts.ProjectRoot, ".trace", "rollout", entry.Name()))
		if err != nil {
			t.Fatalf("read rollout file: %v", err)
		}
		var evt CompactEvent
		if err := json.Unmarshal(raw, &evt); err != nil {
			t.Fatalf("unmarshal rollout: %v", err)
		}
		if evt.SessionID != sessionID {
			continue
		}
		if evt.OriginalMessages > maxOriginal {
			maxOriginal = evt.OriginalMessages
		}
	}
	if maxOriginal < 100 {
		t.Fatalf("expected OriginalMessages>=100 for session %q, got %d", sessionID, maxOriginal)
	}
}
