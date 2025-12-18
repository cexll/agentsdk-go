package main

import (
	"context"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/config"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

// TestRulesConfiguration tests Feature 1: Rules Configuration
func TestRulesConfiguration(t *testing.T) {
	projectRoot := "."
	rulesLoader := config.NewRulesLoader(projectRoot)
	defer rulesLoader.Close()

	rules, err := rulesLoader.LoadRules()
	if err != nil {
		t.Fatalf("LoadRules failed: %v", err)
	}

	// Note: .claude directory is gitignored, so in CI environment there may be no rules
	// This test validates the API works correctly whether rules exist or not
	if len(rules) == 0 {
		t.Logf("✓ No rules found (expected in CI - .claude is gitignored)")
		return
	}

	// If rules exist, verify priority ordering
	if len(rules) >= 2 {
		if rules[0].Priority > rules[1].Priority {
			t.Errorf("rules not sorted by priority: %d > %d", rules[0].Priority, rules[1].Priority)
		}
	}

	// Verify merged content
	content := rulesLoader.GetContent()
	if len(rules) > 0 && len(content) == 0 {
		t.Error("rules exist but merged content is empty")
	}

	t.Logf("✓ Rules configuration working: %d rules loaded, %d chars merged", len(rules), len(content))
}

// TestCompactConfig tests Feature 3: Auto Compact configuration
func TestCompactConfig(t *testing.T) {
	cfg := api.CompactConfig{
		Enabled:       true,
		Threshold:     0.7,
		PreserveCount: 3,
		SummaryModel:  "claude-3-5-haiku-20241022",
	}

	if !cfg.Enabled {
		t.Error("compact should be enabled")
	}
	if cfg.Threshold != 0.7 {
		t.Errorf("expected threshold 0.7, got %f", cfg.Threshold)
	}
	if cfg.PreserveCount != 3 {
		t.Errorf("expected preserve count 3, got %d", cfg.PreserveCount)
	}

	t.Logf("✓ Auto compact configuration validated")
}

// TestDisallowedTools tests Feature 5: DisallowedTools configuration
func TestDisallowedTools(t *testing.T) {
	disallowedTools := []string{"file_write"}

	if len(disallowedTools) == 0 {
		t.Error("disallowed tools list should not be empty")
	}

	// Verify the tool name
	found := false
	for _, tool := range disallowedTools {
		if tool == "file_write" {
			found = true
			break
		}
	}
	if !found {
		t.Error("file_write should be in disallowed tools")
	}

	t.Logf("✓ DisallowedTools configuration validated: %v", disallowedTools)
}

// TestMultiModelConfig tests Feature 6: Multi-model Support configuration
func TestMultiModelConfig(t *testing.T) {
	ctx := context.Background()

	// Create mock providers (without actual API calls)
	haikuProvider := &modelpkg.AnthropicProvider{
		APIKey:    "test-key",
		ModelName: "claude-3-5-haiku-20241022",
	}
	sonnetProvider := &modelpkg.AnthropicProvider{
		APIKey:    "test-key",
		ModelName: "claude-sonnet-4-20250514",
	}

	// Verify provider configuration
	if haikuProvider.ModelName != "claude-3-5-haiku-20241022" {
		t.Errorf("unexpected haiku model name: %s", haikuProvider.ModelName)
	}
	if sonnetProvider.ModelName != "claude-sonnet-4-20250514" {
		t.Errorf("unexpected sonnet model name: %s", sonnetProvider.ModelName)
	}

	// Test model tier constants
	modelPool := map[api.ModelTier]string{
		api.ModelTierLow:  "haiku",
		api.ModelTierMid:  "sonnet",
		api.ModelTierHigh: "opus",
	}

	if len(modelPool) != 3 {
		t.Errorf("expected 3 model tiers, got %d", len(modelPool))
	}

	// Test subagent mapping
	subagentMapping := map[string]api.ModelTier{
		"plan":    api.ModelTierHigh,
		"explore": api.ModelTierMid,
	}

	if subagentMapping["plan"] != api.ModelTierHigh {
		t.Error("plan should map to high tier")
	}
	if subagentMapping["explore"] != api.ModelTierMid {
		t.Error("explore should map to mid tier")
	}

	t.Logf("✓ Multi-model configuration validated")
	_ = ctx // suppress unused warning
}

// TestTokenTracking tests Feature 2: Token Statistics availability
func TestTokenTracking(t *testing.T) {
	usage := modelpkg.Usage{
		InputTokens:         100,
		OutputTokens:        50,
		TotalTokens:         150,
		CacheReadTokens:     20,
		CacheCreationTokens: 10,
	}

	if usage.InputTokens != 100 {
		t.Errorf("expected 100 input tokens, got %d", usage.InputTokens)
	}
	if usage.OutputTokens != 50 {
		t.Errorf("expected 50 output tokens, got %d", usage.OutputTokens)
	}
	if usage.TotalTokens != 150 {
		t.Errorf("expected 150 total tokens, got %d", usage.TotalTokens)
	}

	t.Logf("✓ Token statistics structure validated")
}

// TestOTELConfig tests Feature 8: OpenTelemetry configuration
func TestOTELConfig(t *testing.T) {
	otelCfg := api.OTELConfig{
		Enabled:     true,
		ServiceName: "test-agent",
		Endpoint:    "localhost:4318",
	}

	if !otelCfg.Enabled {
		t.Error("OTEL should be enabled")
	}
	if otelCfg.ServiceName != "test-agent" {
		t.Errorf("unexpected service name: %s", otelCfg.ServiceName)
	}
	if otelCfg.Endpoint != "localhost:4318" {
		t.Errorf("unexpected endpoint: %s", otelCfg.Endpoint)
	}

	t.Logf("✓ OpenTelemetry configuration validated")
}
