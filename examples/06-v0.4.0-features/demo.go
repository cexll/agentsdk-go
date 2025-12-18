// Package main demonstrates v0.4.0 new features WITHOUT requiring API calls.
// This is a configuration and structure demonstration.
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
	fmt.Println("===========================================")
	fmt.Println("   v0.4.0 New Features Demonstration")
	fmt.Println("===========================================")
	fmt.Println()

	projectRoot := "."

	// ============================================================
	// Feature 1: Rules Configuration (.claude/rules/)
	// ============================================================
	fmt.Println("=== Feature 1: Rules Configuration ===")
	fmt.Println("Location: .claude/rules/")
	fmt.Println("Supports: Markdown files with priority prefixes (01-xxx.md)")
	fmt.Println()

	rulesLoader := config.NewRulesLoader(projectRoot)
	rules, err := rulesLoader.LoadRules()
	if err != nil {
		log.Printf("warning: rules load failed: %v", err)
	}
	defer rulesLoader.Close()

	if len(rules) > 0 {
		fmt.Printf("✓ Loaded %d rules:\n", len(rules))
		for _, rule := range rules {
			fmt.Printf("  [%d] %s (%d chars)\n", rule.Priority, rule.Name, len(rule.Content))
		}
		fmt.Println("\n✓ Rules can be hot-reloaded when files change")
		fmt.Println("✓ Merged content automatically injected into system prompt")
	} else {
		fmt.Println("✗ No rules found")
	}

	// ============================================================
	// Feature 2: Token Statistics
	// ============================================================
	fmt.Println("\n=== Feature 2: Token Statistics ===")
	fmt.Println("Enable with: api.Options.TokenTracking = true")
	fmt.Println()
	fmt.Println("Tracks per request:")
	fmt.Println("  • Input tokens")
	fmt.Println("  • Output tokens")
	fmt.Println("  • Total tokens")
	fmt.Println("  • Cache creation tokens")
	fmt.Println("  • Cache read tokens")
	fmt.Println()
	fmt.Println("Access via: response.Result.Usage")
	fmt.Println("Callback: api.Options.TokenCallback for custom processing")

	// ============================================================
	// Feature 3: Auto Compact (Automatic Context Compression)
	// ============================================================
	fmt.Println("\n=== Feature 3: Auto Compact ===")
	fmt.Println("Automatically compresses context when threshold reached")
	fmt.Println()

	compactCfg := api.CompactConfig{
		Enabled:       true,
		Threshold:     0.7,  // Trigger at 70% of context limit
		PreserveCount: 3,     // Keep latest 3 messages
		SummaryModel:  "claude-3-5-haiku-20241022",
	}

	fmt.Println("Configuration:")
	fmt.Printf("  Enabled:       %v\n", compactCfg.Enabled)
	fmt.Printf("  Threshold:     %.1f (triggers at %.0f%%)\n", compactCfg.Threshold, compactCfg.Threshold*100)
	fmt.Printf("  PreserveCount: %d messages\n", compactCfg.PreserveCount)
	fmt.Printf("  SummaryModel:  %s (use cheaper model)\n", compactCfg.SummaryModel)
	fmt.Println()
	fmt.Println("Benefits:")
	fmt.Println("  • Prevents context overflow in long conversations")
	fmt.Println("  • Reduces costs by using cheaper model for summaries")
	fmt.Println("  • Keeps recent messages for continuity")

	// ============================================================
	// Feature 4: Async Bash (Background Command Execution)
	// ============================================================
	fmt.Println("\n=== Feature 4: Async Bash ===")
	fmt.Println("Built-in bash tool now supports background execution")
	fmt.Println()
	fmt.Println("Example tool call:")
	fmt.Println(`  {
    "name": "bash",
    "arguments": {
      "command": "sleep 10 && echo done",
      "background": true
    }
  }`)
	fmt.Println()
	fmt.Println("Benefits:")
	fmt.Println("  • Non-blocking execution for long-running tasks")
	fmt.Println("  • Agent can continue with other work")
	fmt.Println("  • Useful for builds, tests, deployments")

	// ============================================================
	// Feature 5: DisallowedTools
	// ============================================================
	fmt.Println("\n=== Feature 5: DisallowedTools ===")
	fmt.Println("Block specific tools at runtime for security")
	fmt.Println()

	disallowedTools := []string{"file_write"}
	fmt.Println("Configuration (.claude/settings.json):")
	fmt.Println(`  {
    "disallowed_tools": ["file_write"]
  }`)
	fmt.Println()
	fmt.Printf("Or via API: api.Options.DisallowedTools = %v\n", disallowedTools)
	fmt.Println()
	fmt.Println("Benefits:")
	fmt.Println("  • Enhanced security by preventing specific operations")
	fmt.Println("  • Runtime control without code changes")
	fmt.Println("  • Useful for read-only or restricted environments")

	// ============================================================
	// Feature 6: Multi-model Support
	// ============================================================
	fmt.Println("\n=== Feature 6: Multi-model Support ===")
	fmt.Println("Bind different models to different subagent types")
	fmt.Println()
	fmt.Println("Example configuration:")
	fmt.Println(`  api.Options{
    Model: sonnet,  // Default model
    ModelPool: map[api.ModelTier]model.Model{
      api.ModelTierLow:  haiku,
      api.ModelTierMid:  sonnet,
      api.ModelTierHigh: opus,
    },
    SubagentModelMapping: map[string]api.ModelTier{
      "plan":    api.ModelTierHigh,  // Opus for planning
      "explore": api.ModelTierMid,   // Sonnet for exploration
    },
  }`)
	fmt.Println()
	fmt.Println("Benefits:")
	fmt.Println("  • Cost optimization (use cheaper models for simple tasks)")
	fmt.Println("  • Performance tuning (use powerful models for complex reasoning)")
	fmt.Println("  • Flexible model selection per subagent type")
	fmt.Println()
	fmt.Println("See: examples/05-multimodel for full working example")

	// ============================================================
	// Feature 7: Hooks System Extension
	// ============================================================
	fmt.Println("\n=== Feature 7: Hooks System Extension ===")
	fmt.Println("New hook events added in v0.4.0:")
	fmt.Println()
	fmt.Println("  • PermissionRequest - Request permission for sensitive operations")
	fmt.Println("  • SessionStart/End  - Track session lifecycle")
	fmt.Println("  • SubagentStart/Stop - Monitor subagent execution")
	fmt.Println("  • PreToolUse enhancement - Can now modify tool inputs")
	fmt.Println()
	fmt.Println("All hooks run as shell commands (stdin JSON, exit code = decision)")

	// ============================================================
	// Feature 8: OpenTelemetry Integration
	// ============================================================
	fmt.Println("\n=== Feature 8: OpenTelemetry Integration ===")
	fmt.Println("Distributed tracing support with span propagation")
	fmt.Println()
	fmt.Println("Configuration:")
	fmt.Println(`  api.Options{
    OTEL: api.OTELConfig{
      Enabled:     true,
      ServiceName: "my-agent",
      Endpoint:    "localhost:4318",
    },
  }`)
	fmt.Println()
	fmt.Println("Features:")
	fmt.Println("  • Request-level UUID tracking")
	fmt.Println("  • Span propagation across agent/model/tool calls")
	fmt.Println("  • Integration with Jaeger, Zipkin, etc.")

	// ============================================================
	// Demonstration: Create Runtime with v0.4.0 Features
	// ============================================================
	fmt.Println("\n=== Runtime Configuration Demo ===")
	fmt.Println("Creating runtime with all v0.4.0 features enabled...")

	// Check for API key
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}

	if apiKey == "" {
		fmt.Println("\n⚠ No API key found - skipping actual runtime creation")
		fmt.Println("Set ANTHROPIC_API_KEY to test with real API calls")
	} else {
		// Increase timeout to 2 minutes for API calls
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()

		provider := &modelpkg.AnthropicProvider{
			APIKey:    apiKey,
			ModelName: "claude-sonnet-4-20250514",
		}

		mainModel, err := provider.Model(ctx)
		if err != nil {
			log.Fatalf("failed to create model: %v", err)
		}

		rt, err := api.New(ctx, api.Options{
			ProjectRoot: projectRoot,
			Model:       mainModel,

			// v0.4.0 features
			TokenTracking:   true,
			DisallowedTools: []string{"file_write"},
			AutoCompact:     compactCfg,

			MaxIterations: 5,
			Timeout:       2 * time.Minute,
		})
		if err != nil {
			log.Fatalf("failed to create runtime: %v", err)
		}
		defer rt.Close()

		fmt.Println("✓ Runtime created successfully with v0.4.0 features!")
		fmt.Println("\nTesting with simple prompt...")

		resp, err := rt.Run(ctx, api.Request{
			Prompt:    "Echo 'v0.4.0 works!' using bash",
			SessionID: "v0.4.0-demo",
		})
		if err != nil {
			log.Printf("⚠ Test run failed: %v", err)
		} else if resp.Result != nil {
			fmt.Printf("\n✓ Agent output: %s\n", resp.Result.Output)
			usage := resp.Result.Usage
			fmt.Printf("\n✓ Token statistics:\n")
			fmt.Printf("  Input:  %d tokens\n", usage.InputTokens)
			fmt.Printf("  Output: %d tokens\n", usage.OutputTokens)
			fmt.Printf("  Total:  %d tokens\n", usage.TotalTokens)
			if usage.CacheReadTokens > 0 {
				fmt.Printf("  Cache read: %d tokens\n", usage.CacheReadTokens)
			}
			if usage.CacheCreationTokens > 0 {
				fmt.Printf("  Cache creation: %d tokens\n", usage.CacheCreationTokens)
			}
		}
	}

	// ============================================================
	// Summary
	// ============================================================
	fmt.Println("\n===========================================")
	fmt.Println("              Summary")
	fmt.Println("===========================================")
	fmt.Println("\nv0.4.0 introduces 8 major features:")
	fmt.Println("  1. ✓ Rules Configuration (.claude/rules/)")
	fmt.Println("  2. ✓ Token Statistics (cost tracking)")
	fmt.Println("  3. ✓ Auto Compact (context compression)")
	fmt.Println("  4. ✓ Async Bash (background execution)")
	fmt.Println("  5. ✓ DisallowedTools (security)")
	fmt.Println("  6. ✓ Multi-model Support (cost optimization)")
	fmt.Println("  7. ✓ Hooks System Extension")
	fmt.Println("  8. ✓ OpenTelemetry Integration")
	fmt.Println("\nAll features are production-ready!")
	fmt.Println("See individual examples for detailed usage.")
}
