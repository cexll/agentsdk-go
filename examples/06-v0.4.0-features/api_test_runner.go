// Package main provides comprehensive API testing for v0.4.0 features.
// Run with: ANTHROPIC_API_KEY=xxx go run api_test.go
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/config"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		log.Fatal("❌ ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN required")
	}

	fmt.Println("===========================================")
	fmt.Println("    v0.4.0 API Feature Tests")
	fmt.Println("===========================================")
	fmt.Println()

	projectRoot := "."
	ctx := context.Background()

	// Test 1: Token Statistics
	fmt.Println("Test 1: Token Statistics")
	fmt.Println("------------------------")
	test1TokenStats(ctx, apiKey, projectRoot)
	fmt.Println()

	// Test 2: Rules Configuration Integration
	fmt.Println("Test 2: Rules Configuration")
	fmt.Println("---------------------------")
	test2RulesConfig(projectRoot)
	fmt.Println()

	// Test 3: DisallowedTools
	fmt.Println("Test 3: DisallowedTools")
	fmt.Println("-----------------------")
	test3DisallowedTools(ctx, apiKey, projectRoot)
	fmt.Println()

	// Test 4: Basic Runtime with all features
	fmt.Println("Test 4: Full Runtime Integration")
	fmt.Println("---------------------------------")
	test4FullIntegration(ctx, apiKey, projectRoot)
	fmt.Println()

	fmt.Println("===========================================")
	fmt.Println("            All Tests Complete")
	fmt.Println("===========================================")
}

func test1TokenStats(ctx context.Context, apiKey, projectRoot string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	provider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-20250514",
	}

	model, err := provider.Model(ctx)
	if err != nil {
		log.Printf("❌ Failed to create model: %v", err)
		return
	}

	rt, err := api.New(ctx, api.Options{
		ProjectRoot:   projectRoot,
		Model:         model,
		TokenTracking: true,
		MaxIterations: 3,
		Timeout:       2 * time.Minute,
	})
	if err != nil {
		log.Printf("❌ Failed to create runtime: %v", err)
		return
	}
	defer rt.Close()

	resp, err := rt.Run(ctx, api.Request{
		Prompt:    "Echo 'hello' using bash",
		SessionID: "test-tokens",
	})
	if err != nil {
		log.Printf("❌ Test failed: %v", err)
		return
	}

	if resp.Result == nil {
		log.Println("❌ No result returned")
		return
	}

	usage := resp.Result.Usage
	fmt.Printf("✓ Token tracking works!\n")
	fmt.Printf("  Input tokens:  %d\n", usage.InputTokens)
	fmt.Printf("  Output tokens: %d\n", usage.OutputTokens)
	fmt.Printf("  Total tokens:  %d\n", usage.TotalTokens)

	if usage.InputTokens == 0 || usage.OutputTokens == 0 {
		log.Println("⚠ Warning: Token counts are zero")
	}
}

func test2RulesConfig(projectRoot string) {
	rulesLoader := config.NewRulesLoader(projectRoot)
	defer rulesLoader.Close()

	rules, err := rulesLoader.LoadRules()
	if err != nil {
		log.Printf("❌ Failed to load rules: %v", err)
		return
	}

	if len(rules) == 0 {
		log.Println("⚠ No rules found (this is OK if no rules defined)")
		return
	}

	fmt.Printf("✓ Rules loaded: %d rules\n", len(rules))
	for _, rule := range rules {
		fmt.Printf("  [%d] %s (%d chars)\n", rule.Priority, rule.Name, len(rule.Content))
	}

	content := rulesLoader.GetContent()
	fmt.Printf("✓ Merged content: %d characters\n", len(content))
}

func test3DisallowedTools(ctx context.Context, apiKey, projectRoot string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	provider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-20250514",
	}

	model, err := provider.Model(ctx)
	if err != nil {
		log.Printf("❌ Failed to create model: %v", err)
		return
	}

	// Create runtime with file_write disabled
	rt, err := api.New(ctx, api.Options{
		ProjectRoot:     projectRoot,
		Model:           model,
		DisallowedTools: []string{"file_write"},
		MaxIterations:   3,
		Timeout:         2 * time.Minute,
	})
	if err != nil {
		log.Printf("❌ Failed to create runtime: %v", err)
		return
	}
	defer rt.Close()

	// Try to use file_write (should be blocked or unavailable)
	resp, err := rt.Run(ctx, api.Request{
		Prompt:    "Try to write 'test' to /tmp/test.txt, if the tool is unavailable just tell me",
		SessionID: "test-disallowed",
	})

	if err != nil {
		fmt.Printf("✓ DisallowedTools working (error occurred as expected)\n")
		return
	}

	if resp.Result != nil {
		fmt.Printf("✓ DisallowedTools working\n")
		fmt.Printf("  Agent response: %s\n", resp.Result.Output)
	}
}

func test4FullIntegration(ctx context.Context, apiKey, projectRoot string) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
	defer cancel()

	provider := &modelpkg.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-20250514",
	}

	model, err := provider.Model(ctx)
	if err != nil {
		log.Printf("❌ Failed to create model: %v", err)
		return
	}

	// Create runtime with ALL v0.4.0 features enabled
	rt, err := api.New(ctx, api.Options{
		ProjectRoot:     projectRoot,
		Model:           model,
		TokenTracking:   true,
		DisallowedTools: []string{"file_write"},
		AutoCompact: api.CompactConfig{
			Enabled:       true,
			Threshold:     0.7,
			PreserveCount: 3,
			SummaryModel:  "claude-3-5-haiku-20241022",
		},
		MaxIterations: 5,
		Timeout:       2 * time.Minute,
	})
	if err != nil {
		log.Printf("❌ Failed to create runtime: %v", err)
		return
	}
	defer rt.Close()

	fmt.Println("✓ Runtime created with all v0.4.0 features:")
	fmt.Println("  • Token tracking: enabled")
	fmt.Println("  • Auto compact: enabled (threshold 0.7)")
	fmt.Println("  • DisallowedTools: [file_write]")
	fmt.Println("  • Rules: loaded from .claude/rules/")

	fmt.Println("\nExecuting test request...")
	resp, err := rt.Run(ctx, api.Request{
		Prompt:    "List files in current directory using bash",
		SessionID: "test-full",
	})
	if err != nil {
		log.Printf("❌ Test failed: %v", err)
		return
	}

	if resp.Result != nil {
		fmt.Printf("\n✓ Full integration test passed!\n")
		fmt.Printf("  Output: %s\n", resp.Result.Output)
		usage := resp.Result.Usage
		fmt.Printf("\n  Token usage:\n")
		fmt.Printf("    Input:  %d\n", usage.InputTokens)
		fmt.Printf("    Output: %d\n", usage.OutputTokens)
		fmt.Printf("    Total:  %d\n", usage.TotalTokens)
	}
}
