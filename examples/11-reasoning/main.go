// Package main demonstrates reasoning_content passthrough for thinking models
// (DeepSeek-R1, Kimi k2.5, etc.) through both OpenAI and Anthropic providers.
//
// Usage:
//   DEEPSEEK_API_KEY=sk-xxx go run ./examples/11-reasoning
//   DEEPSEEK_API_KEY=sk-xxx go run ./examples/11-reasoning --provider anthropic
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	apiKey := os.Getenv("DEEPSEEK_API_KEY")
	if apiKey == "" {
		log.Fatal("DEEPSEEK_API_KEY required")
	}

	provider := "openai"
	for _, arg := range os.Args[1:] {
		if arg == "--provider" || arg == "-p" {
			continue
		}
		if arg == "anthropic" || arg == "--provider=anthropic" || arg == "-p=anthropic" {
			provider = "anthropic"
		}
	}
	// Also check: --provider anthropic (two-arg form)
	for i, arg := range os.Args[1:] {
		if (arg == "--provider" || arg == "-p") && i+2 < len(os.Args) {
			provider = os.Args[i+2]
		}
	}

	mdl := createModel(apiKey, provider)

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	fmt.Printf("Provider: %s\n\n", provider)

	// ── Demo 1: Non-streaming ─────────────────────────────────────────
	fmt.Println("═══════════════════════════════════════════════════")
	fmt.Println(" Demo 1: Non-Streaming (Complete)")
	fmt.Println("═══════════════════════════════════════════════════")

	resp, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 15 * 37? Think step by step."},
		},
	})
	if err != nil {
		log.Fatalf("Complete: %v", err)
	}

	printResponse(resp)

	// ── Demo 2: Streaming ─────────────────────────────────────────────
	fmt.Println("\n═══════════════════════════════════════════════════")
	fmt.Println(" Demo 2: Streaming (CompleteStream)")
	fmt.Println("═══════════════════════════════════════════════════")

	var streamResp *model.Response
	var deltaCount int

	err = mdl.CompleteStream(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 23 + 89? Think step by step."},
		},
	}, func(sr model.StreamResult) error {
		if sr.Delta != "" {
			deltaCount++
			// Print content deltas in real-time
			fmt.Print(sr.Delta)
		}
		if sr.Final && sr.Response != nil {
			streamResp = sr.Response
		}
		return nil
	})
	if err != nil {
		log.Fatalf("CompleteStream: %v", err)
	}
	fmt.Println() // newline after streaming output

	if streamResp != nil {
		fmt.Printf("\n[Streaming stats: %d deltas received]\n", deltaCount)
		fmt.Println("\n┌─ ReasoningContent (from streaming) ─────────────")
		printBoxed(streamResp.Message.ReasoningContent)
		fmt.Println("└─────────────────────────────────────────────────")
	}

	// ── Demo 3: Multi-turn with reasoning passthrough ────────────────
	fmt.Println("\n═══════════════════════════════════════════════════")
	fmt.Println(" Demo 3: Multi-Turn (reasoning_content passthrough)")
	fmt.Println("═══════════════════════════════════════════════════")

	fmt.Println("\n>> Turn 1: What is 7 * 8?")
	resp1, err := mdl.Complete(ctx, model.Request{
		Messages: []model.Message{
			{Role: "user", Content: "What is 7 * 8?"},
		},
	})
	if err != nil {
		log.Fatalf("Turn 1: %v", err)
	}
	printResponse(resp1)

	fmt.Println("\n>> Turn 2: Now multiply that result by 2")
	fmt.Println("   (echoing back reasoning_content from Turn 1)")

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
		log.Fatalf("Turn 2: %v", err)
	}
	printResponse(resp2)

	fmt.Println("\n✓ All demos completed successfully.")
}

func printResponse(resp *model.Response) {
	fmt.Println("\n┌─ Content ────────────────────────────────────────")
	fmt.Printf("│ %s\n", strings.ReplaceAll(resp.Message.Content, "\n", "\n│ "))
	fmt.Println("├─ ReasoningContent ───────────────────────────────")
	printBoxed(resp.Message.ReasoningContent)
	fmt.Println("├─ Usage ──────────────────────────────────────────")
	fmt.Printf("│ input=%d  output=%d  total=%d\n",
		resp.Usage.InputTokens, resp.Usage.OutputTokens, resp.Usage.TotalTokens)
	fmt.Println("└──────────────────────────────────────────────────")
}

func printBoxed(text string) {
	if text == "" {
		fmt.Println("│ (empty)")
		return
	}
	lines := strings.Split(text, "\n")
	for _, line := range lines {
		fmt.Printf("│ %s\n", line)
	}
}

func createModel(apiKey, provider string) model.Model {
	switch provider {
	case "anthropic":
		mdl, err := model.NewAnthropic(model.AnthropicConfig{
			APIKey:    apiKey,
			BaseURL:   "https://api.deepseek.com/anthropic",
			Model:     "deepseek-reasoner",
			MaxTokens: 4096,
		})
		if err != nil {
			log.Fatalf("create anthropic model: %v", err)
		}
		return mdl
	default:
		mdl, err := model.NewOpenAI(model.OpenAIConfig{
			APIKey:    apiKey,
			BaseURL:   "https://api.deepseek.com",
			Model:     "deepseek-reasoner",
			MaxTokens: 4096,
		})
		if err != nil {
			log.Fatalf("create openai model: %v", err)
		}
		return mdl
	}
}
