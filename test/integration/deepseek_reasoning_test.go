//go:build integration
// +build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
)

const (
	deepseekBaseURL = "https://api.deepseek.com"
	deepseekModel   = "deepseek-reasoner"
)

func newDeepSeekModel(t *testing.T) model.Model {
	t.Helper()
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set, skipping DeepSeek integration test")
	}
	mdl, err := model.NewOpenAI(model.OpenAIConfig{
		APIKey:    apiKey,
		BaseURL:   deepseekBaseURL,
		Model:     deepseekModel,
		MaxTokens: 4096,
	})
	if err != nil {
		t.Fatalf("create deepseek model: %v", err)
	}
	return mdl
}

func TestDeepSeekReasonerNonStreaming(t *testing.T) {
	mdl := newDeepSeekModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 15 * 37? Think step by step."},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("Content: %s", resp.Message.Content)
	t.Logf("ReasoningContent length: %d", len(resp.Message.ReasoningContent))
	if len(resp.Message.ReasoningContent) > 200 {
		t.Logf("ReasoningContent (first 200): %s...", resp.Message.ReasoningContent[:200])
	} else {
		t.Logf("ReasoningContent: %s", resp.Message.ReasoningContent)
	}
	t.Logf("Usage: input=%d output=%d total=%d", resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)

	if resp.Message.Content == "" {
		t.Error("expected non-empty content")
	}
	if !strings.Contains(resp.Message.Content, "555") {
		t.Errorf("expected content to contain '555' (15*37=555), got: %s", resp.Message.Content)
	}
	if resp.Message.ReasoningContent == "" {
		t.Error("expected non-empty ReasoningContent from deepseek-reasoner")
	}
}

func TestDeepSeekReasonerStreaming(t *testing.T) {
	mdl := newDeepSeekModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var (
		finalResp *model.Response
		deltas    []string
	)

	err := mdl.CompleteStream(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 23 + 89? Think step by step."},
		},
	}, func(sr model.StreamResult) error {
		if sr.Delta != "" {
			deltas = append(deltas, sr.Delta)
		}
		if sr.Final && sr.Response != nil {
			finalResp = sr.Response
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream failed: %v", err)
	}
	if finalResp == nil {
		t.Fatal("no final response received")
	}

	content := finalResp.Message.Content
	reasoning := finalResp.Message.ReasoningContent

	t.Logf("Streaming content: %s", content)
	t.Logf("Streaming ReasoningContent length: %d", len(reasoning))
	if len(reasoning) > 200 {
		t.Logf("Streaming ReasoningContent (first 200): %s...", reasoning[:200])
	} else {
		t.Logf("Streaming ReasoningContent: %s", reasoning)
	}
	t.Logf("Deltas received: %d", len(deltas))

	if content == "" {
		t.Error("expected non-empty streaming content")
	}
	if !strings.Contains(content, "112") {
		t.Errorf("expected content to contain '112' (23+89=112), got: %s", content)
	}
	if reasoning == "" {
		t.Error("expected non-empty ReasoningContent from streaming deepseek-reasoner")
	}
}

func TestDeepSeekReasonerMultiTurn(t *testing.T) {
	mdl := newDeepSeekModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	// First turn
	resp1, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 7 * 8?"},
		},
	})
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}

	t.Logf("Turn 1 content: %s", resp1.Message.Content)
	t.Logf("Turn 1 reasoning length: %d", len(resp1.Message.ReasoningContent))

	if resp1.Message.ReasoningContent == "" {
		t.Error("expected reasoning in first turn")
	}

	// Second turn: echo back reasoning_content in history
	resp2, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 7 * 8?"},
			{
				Role:             "assistant",
				Content:          resp1.Message.Content,
				ReasoningContent: resp1.Message.ReasoningContent,
			},
			{Role: "user", Content: "Now multiply that result by 2"},
		},
	})
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}

	t.Logf("Turn 2 content: %s", resp2.Message.Content)
	t.Logf("Turn 2 reasoning length: %d", len(resp2.Message.ReasoningContent))

	if resp2.Message.Content == "" {
		t.Error("expected non-empty content in second turn")
	}
	if !strings.Contains(resp2.Message.Content, "112") {
		t.Logf("WARNING: expected '112' in multi-turn result (56*2=112), got: %s", resp2.Message.Content)
	}

	fmt.Println("Multi-turn with reasoning_content passthrough succeeded")
}

// ── Anthropic-compatible endpoint tests ──────────────────────────────────

const deepseekAnthropicBaseURL = "https://api.deepseek.com/anthropic"

func newDeepSeekAnthropicModel(t *testing.T) model.Model {
	t.Helper()
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		t.Skip("DEEPSEEK_API_KEY not set, skipping DeepSeek Anthropic integration test")
	}
	mdl, err := model.NewAnthropic(model.AnthropicConfig{
		APIKey:    apiKey,
		BaseURL:   deepseekAnthropicBaseURL,
		Model:     deepseekModel,
		MaxTokens: 4096,
	})
	if err != nil {
		t.Fatalf("create deepseek anthropic model: %v", err)
	}
	return mdl
}

func TestDeepSeekAnthropicNonStreaming(t *testing.T) {
	mdl := newDeepSeekAnthropicModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	resp, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 15 * 37? Think step by step."},
		},
	})
	if err != nil {
		t.Fatalf("Complete failed: %v", err)
	}

	t.Logf("[Anthropic] Content: %s", resp.Message.Content)
	t.Logf("[Anthropic] ReasoningContent length: %d", len(resp.Message.ReasoningContent))
	if len(resp.Message.ReasoningContent) > 200 {
		t.Logf("[Anthropic] ReasoningContent (first 200): %s...", resp.Message.ReasoningContent[:200])
	} else {
		t.Logf("[Anthropic] ReasoningContent: %s", resp.Message.ReasoningContent)
	}
	t.Logf("[Anthropic] Usage: input=%d output=%d total=%d",
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)

	if resp.Message.Content == "" {
		t.Error("expected non-empty content")
	}
	if !strings.Contains(resp.Message.Content, "555") {
		t.Errorf("expected content to contain '555' (15*37=555), got: %s", resp.Message.Content)
	}
	if resp.Message.ReasoningContent == "" {
		t.Error("expected non-empty ReasoningContent from Anthropic-compatible deepseek-reasoner")
	}
}

func TestDeepSeekAnthropicStreaming(t *testing.T) {
	mdl := newDeepSeekAnthropicModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	var (
		finalResp *model.Response
		deltas    []string
	)

	err := mdl.CompleteStream(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 23 + 89? Think step by step."},
		},
	}, func(sr model.StreamResult) error {
		if sr.Delta != "" {
			deltas = append(deltas, sr.Delta)
		}
		if sr.Final && sr.Response != nil {
			finalResp = sr.Response
		}
		return nil
	})
	if err != nil {
		t.Fatalf("CompleteStream failed: %v", err)
	}
	if finalResp == nil {
		t.Fatal("no final response received")
	}

	content := finalResp.Message.Content
	reasoning := finalResp.Message.ReasoningContent

	t.Logf("[Anthropic] Streaming content: %s", content)
	t.Logf("[Anthropic] Streaming ReasoningContent length: %d", len(reasoning))
	if len(reasoning) > 200 {
		t.Logf("[Anthropic] Streaming ReasoningContent (first 200): %s...", reasoning[:200])
	} else {
		t.Logf("[Anthropic] Streaming ReasoningContent: %s", reasoning)
	}
	t.Logf("[Anthropic] Deltas received: %d", len(deltas))

	if content == "" {
		t.Error("expected non-empty streaming content")
	}
	if !strings.Contains(content, "112") {
		t.Errorf("expected content to contain '112' (23+89=112), got: %s", content)
	}
	if reasoning == "" {
		t.Error("expected non-empty ReasoningContent from streaming Anthropic-compatible deepseek-reasoner")
	}
}

func TestDeepSeekAnthropicMultiTurn(t *testing.T) {
	mdl := newDeepSeekAnthropicModel(t)
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	resp1, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 7 * 8?"},
		},
	})
	if err != nil {
		t.Fatalf("first turn failed: %v", err)
	}

	t.Logf("[Anthropic] Turn 1 content: %s", resp1.Message.Content)
	t.Logf("[Anthropic] Turn 1 reasoning length: %d", len(resp1.Message.ReasoningContent))

	if resp1.Message.ReasoningContent == "" {
		t.Error("expected reasoning in first turn")
	}

	resp2, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 7 * 8?"},
			{
				Role:             "assistant",
				Content:          resp1.Message.Content,
				ReasoningContent: resp1.Message.ReasoningContent,
			},
			{Role: "user", Content: "Now multiply that result by 2"},
		},
	})
	if err != nil {
		t.Fatalf("second turn failed: %v", err)
	}

	t.Logf("[Anthropic] Turn 2 content: %s", resp2.Message.Content)
	t.Logf("[Anthropic] Turn 2 reasoning length: %d", len(resp2.Message.ReasoningContent))

	if resp2.Message.Content == "" {
		t.Error("expected non-empty content in second turn")
	}
	if !strings.Contains(resp2.Message.Content, "112") {
		t.Logf("WARNING: expected '112' in multi-turn result (56*2=112), got: %s", resp2.Message.Content)
	}

	fmt.Println("[Anthropic] Multi-turn with thinking block passthrough succeeded")
}
