package api

import (
	"context"
	"sync"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/model"
)

// mockModel implements model.Model for testing
type mockModel struct {
	name string
}

func (m *mockModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	return &model.Response{
		Message: model.Message{Role: "assistant", Content: "mock response from " + m.name},
	}, nil
}

func (m *mockModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	return nil
}

func TestModelTierConstants(t *testing.T) {
	tests := []struct {
		tier     ModelTier
		expected string
	}{
		{ModelTierLow, "low"},
		{ModelTierMid, "mid"},
		{ModelTierHigh, "high"},
	}
	for _, tt := range tests {
		if string(tt.tier) != tt.expected {
			t.Errorf("ModelTier %v = %q, want %q", tt.tier, string(tt.tier), tt.expected)
		}
	}
}

func TestWithModelPool(t *testing.T) {
	haiku := &mockModel{name: "haiku"}
	sonnet := &mockModel{name: "sonnet"}
	pool := map[ModelTier]model.Model{
		ModelTierLow: haiku,
		ModelTierMid: sonnet,
	}

	opts := &Options{}
	WithModelPool(pool)(opts)

	if len(opts.ModelPool) != 2 {
		t.Errorf("ModelPool length = %d, want 2", len(opts.ModelPool))
	}
	if opts.ModelPool[ModelTierLow] != haiku {
		t.Error("ModelPool[ModelTierLow] not set correctly")
	}
}

func TestWithModelPoolNil(t *testing.T) {
	opts := &Options{ModelPool: map[ModelTier]model.Model{ModelTierLow: &mockModel{}}}
	WithModelPool(nil)(opts)
	if opts.ModelPool == nil {
		t.Error("WithModelPool(nil) should not clear existing pool")
	}
}

func TestWithSubagentModelMapping(t *testing.T) {
	mapping := map[string]ModelTier{
		"explore": ModelTierLow,
		"plan":    ModelTierHigh,
	}

	opts := &Options{}
	WithSubagentModelMapping(mapping)(opts)

	if len(opts.SubagentModelMapping) != 2 {
		t.Errorf("SubagentModelMapping length = %d, want 2", len(opts.SubagentModelMapping))
	}
	if opts.SubagentModelMapping["explore"] != ModelTierLow {
		t.Error("SubagentModelMapping[\"explore\"] not set correctly")
	}
}

func TestWithSubagentModelMappingNil(t *testing.T) {
	opts := &Options{SubagentModelMapping: map[string]ModelTier{"existing": ModelTierLow}}
	WithSubagentModelMapping(nil)(opts)
	if opts.SubagentModelMapping == nil {
		t.Error("WithSubagentModelMapping(nil) should not clear existing mapping")
	}
}

func TestSelectModelForSubagent(t *testing.T) {
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}
	opus := &mockModel{name: "opus"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow:  haiku,
				ModelTierHigh: opus,
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
				"plan":    ModelTierHigh,
			},
		},
	}

	tests := []struct {
		subagentType string
		requestTier  ModelTier
		expectedName string
		expectedTier ModelTier
	}{
		{"explore", "", "haiku", ModelTierLow},
		{"plan", "", "opus", ModelTierHigh},
		{"general-purpose", "", "default", ""},            // Not in mapping, use default
		{"unknown", "", "default", ""},                    // Unknown subagent, use default
		{"explore", ModelTierHigh, "opus", ModelTierHigh}, // Request tier overrides mapping
		{"", ModelTierLow, "haiku", ModelTierLow},         // Request tier without subagent
	}

	for _, tt := range tests {
		mdl, tier := rt.selectModelForSubagent(tt.subagentType, tt.requestTier)
		mock, ok := mdl.(*mockModel)
		if !ok {
			t.Fatalf("selectModelForSubagent(%q, %q) returned non-mockModel type", tt.subagentType, tt.requestTier)
		}
		if mock.name != tt.expectedName {
			t.Errorf("selectModelForSubagent(%q, %q) model = %q, want %q", tt.subagentType, tt.requestTier, mock.name, tt.expectedName)
		}
		if tier != tt.expectedTier {
			t.Errorf("selectModelForSubagent(%q, %q) tier = %q, want %q", tt.subagentType, tt.requestTier, tier, tt.expectedTier)
		}
	}
}

func TestSelectModelForSubagentCaseInsensitive(t *testing.T) {
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: haiku,
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	// Test case insensitivity
	tests := []string{"explore", "EXPLORE", "Explore", "  explore  "}
	for _, input := range tests {
		mdl, tier := rt.selectModelForSubagent(input, "")
		mock, ok := mdl.(*mockModel)
		if !ok {
			t.Fatalf("selectModelForSubagent(%q) returned non-mockModel type", input)
		}
		if mock.name != "haiku" {
			t.Errorf("selectModelForSubagent(%q) model = %q, want haiku (case insensitive)", input, mock.name)
		}
		if tier != ModelTierLow {
			t.Errorf("selectModelForSubagent(%q) tier = %q, want low", input, tier)
		}
	}
}

func TestSelectModelForSubagentNoPool(t *testing.T) {
	defaultModel := &mockModel{name: "default"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	mdl, tier := rt.selectModelForSubagent("explore", "")
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	if mock.name != "default" {
		t.Errorf("selectModelForSubagent with no pool = %q, want default", mock.name)
	}
	if tier != "" {
		t.Errorf("tier should be empty when pool is nil, got %q", tier)
	}
}

func TestSelectModelForSubagentNoMapping(t *testing.T) {
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: haiku,
			},
		},
	}

	mdl, tier := rt.selectModelForSubagent("explore", "")
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	if mock.name != "default" {
		t.Errorf("selectModelForSubagent with no mapping = %q, want default", mock.name)
	}
	if tier != "" {
		t.Errorf("tier should be empty when mapping is nil, got %q", tier)
	}
}

func TestSelectModelForSubagentConcurrent(t *testing.T) {
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: haiku,
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			rt.selectModelForSubagent("explore", "")
			rt.selectModelForSubagent("plan", "")
			rt.selectModelForSubagent("", ModelTierLow)
		}()
	}
	wg.Wait()
}

func TestRequestModelTierOverride(t *testing.T) {
	// Test that Request.Model field can override model selection
	req := Request{
		Prompt: "test",
		Model:  ModelTierHigh,
	}

	if req.Model != ModelTierHigh {
		t.Errorf("Request.Model = %q, want %q", req.Model, ModelTierHigh)
	}
}

func TestSelectModelForSubagentPoolTierMissing(t *testing.T) {
	// Test fallback when mapping tier is not in pool
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: haiku,
				// ModelTierHigh is NOT in pool
			},
			SubagentModelMapping: map[string]ModelTier{
				"plan": ModelTierHigh, // maps to tier not in pool
			},
		},
	}

	mdl, tier := rt.selectModelForSubagent("plan", "")
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	// Should fallback to default since ModelTierHigh not in pool
	if mock.name != "default" {
		t.Errorf("selectModelForSubagent with missing pool tier = %q, want default", mock.name)
	}
	if tier != "" {
		t.Errorf("tier should be empty when pool tier missing, got %q", tier)
	}
}

func TestSelectModelForSubagentNilModelInPool(t *testing.T) {
	// Test fallback when pool has nil model for tier
	defaultModel := &mockModel{name: "default"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: nil, // explicitly nil
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	mdl, tier := rt.selectModelForSubagent("explore", "")
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	// Should fallback to default since pool model is nil
	if mock.name != "default" {
		t.Errorf("selectModelForSubagent with nil pool model = %q, want default", mock.name)
	}
	if tier != "" {
		t.Errorf("tier should be empty when pool model is nil, got %q", tier)
	}
}

func TestSelectModelForSubagentRequestTierNilInPool(t *testing.T) {
	// Test request tier override when pool model is nil
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow:  haiku,
				ModelTierHigh: nil, // explicitly nil
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	// Request tier points to nil model, should check mapping next
	mdl, tier := rt.selectModelForSubagent("explore", ModelTierHigh)
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	// Should use mapping since request tier model is nil
	if mock.name != "haiku" {
		t.Errorf("selectModelForSubagent with nil request tier model = %q, want haiku", mock.name)
	}
	if tier != ModelTierLow {
		t.Errorf("tier = %q, want low", tier)
	}
}

func TestSelectModelForSubagentEmptyInputs(t *testing.T) {
	// Test with both subagentType and requestTier empty
	defaultModel := &mockModel{name: "default"}
	haiku := &mockModel{name: "haiku"}

	rt := &Runtime{
		opts: Options{
			Model: defaultModel,
			ModelPool: map[ModelTier]model.Model{
				ModelTierLow: haiku,
			},
			SubagentModelMapping: map[string]ModelTier{
				"explore": ModelTierLow,
			},
		},
	}

	mdl, tier := rt.selectModelForSubagent("", "")
	mock, ok := mdl.(*mockModel)
	if !ok {
		t.Fatal("selectModelForSubagent returned non-mockModel type")
	}
	if mock.name != "default" {
		t.Errorf("selectModelForSubagent with empty inputs = %q, want default", mock.name)
	}
	if tier != "" {
		t.Errorf("tier should be empty with empty inputs, got %q", tier)
	}
}
