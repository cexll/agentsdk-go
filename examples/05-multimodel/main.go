// Package main demonstrates multi-model support with subagent-level model binding.
// This example shows how to configure different models for different subagent types
// to optimize costs (e.g., use cheaper models for exploration tasks).
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		log.Fatal("ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN required")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	// Create model providers for different tiers.
	// Recommended production setup (inspired by Claude Code's model selection):
	// - high: claude-opus-4 (planning, complex reasoning)
	// - mid:  claude-sonnet-4 (exploration, general-purpose tasks)
	// - low:  claude-3-5-haiku (optional, for simple tasks if needed)
	haikuProvider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-3-5-haiku-20241022",
	}
	sonnetProvider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-20250514",
	}
	// Using sonnet as a placeholder for high tier in this example.
	opusProvider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-20250514",
	}

	haiku, err := haikuProvider.Model(ctx)
	if err != nil {
		log.Fatalf("failed to create haiku model: %v", err)
	}
	sonnet, err := sonnetProvider.Model(ctx)
	if err != nil {
		log.Fatalf("failed to create sonnet model: %v", err)
	}
	opus, err := opusProvider.Model(ctx)
	if err != nil {
		log.Fatalf("failed to create opus model: %v", err)
	}

	// Configure runtime with multi-model support.
	rt, err := api.New(ctx, api.Options{
		ProjectRoot: ".",
		Model:       sonnet, // Default model for main agent loop.

		// Model pool for cost optimization.
		// Maps tier constants to actual model instances.
		ModelPool: map[api.ModelTier]modelpkg.Model{
			api.ModelTierLow:  haiku,
			api.ModelTierMid:  sonnet,
			api.ModelTierHigh: opus,
		},

		// Subagent type to model tier mapping (inspired by Claude Code's "opus plan").
		// Keys should be lowercase subagent type names.
		// Note: low tier is available in pool for custom use but not mapped by default.
		SubagentModelMapping: map[string]api.ModelTier{
			"plan":            api.ModelTierHigh, // Use Opus for planning (needs strong reasoning)
			"explore":         api.ModelTierMid,  // Use Sonnet for exploration
			"general-purpose": api.ModelTierMid,  // Use Sonnet for general tasks
		},

		MaxIterations: 10,
		Timeout:       5 * time.Minute,
	})
	if err != nil {
		log.Fatalf("failed to create runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("Multi-model runtime configured successfully!")
	fmt.Println("\nModel Pool:")
	fmt.Println("  - high: claude-sonnet-4 (placeholder for Opus)")
	fmt.Println("  - mid:  claude-sonnet-4 (balanced)")
	fmt.Println("  - low:  claude-3-5-haiku (available for custom use)")
	fmt.Println("\nSubagent Mappings (inspired by Claude Code's opus plan):")
	fmt.Println("  - plan            -> high (Opus for complex reasoning)")
	fmt.Println("  - explore         -> mid  (Sonnet for exploration)")
	fmt.Println("  - general-purpose -> mid  (Sonnet for general tasks)")
	fmt.Println("\nSubagents not in mapping use the default model (Sonnet).")

	// Example 1: Normal request uses default model
	resp, err := rt.Run(ctx, api.Request{
		Prompt:    "List the files in the current directory.",
		SessionID: "multimodel-demo",
	})
	if err != nil {
		log.Fatalf("failed to run: %v", err)
	}

	fmt.Println("\n--- Response (default model) ---")
	if resp.Result != nil {
		fmt.Println(resp.Result.Output)
	}

	// Example 2: Request with explicit model tier override
	resp2, err := rt.Run(ctx, api.Request{
		Prompt:    "What is 2+2?",
		SessionID: "multimodel-demo-override",
		Model:     api.ModelTierLow, // Force use of Haiku for this simple task
	})
	if err != nil {
		log.Fatalf("failed to run with override: %v", err)
	}

	fmt.Println("\n--- Response (low tier override) ---")
	if resp2.Result != nil {
		fmt.Println(resp2.Result.Output)
	}
}
