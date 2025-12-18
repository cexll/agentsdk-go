package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/model"
)

func TestOptionsFrozenPreventsExternalMutationRaces(t *testing.T) {
	root := newClaudeProject(t)
	mdl := staticOKModel{content: "ok"}

	pool := map[ModelTier]model.Model{
		ModelTierLow: mdl,
	}
	mapping := map[string]ModelTier{
		"explore": ModelTierLow,
	}

	rt, err := New(context.Background(), Options{
		ProjectRoot:          root,
		Model:                mdl,
		ModelPool:            pool,
		SubagentModelMapping: mapping,
		EnabledBuiltinTools:  []string{},
		RulesEnabled:         ptrBool(false),
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 1000; i++ {
			pool[ModelTierLow] = mdl
			mapping[fmt.Sprintf("k-%d", i)] = ModelTierLow
		}
	}()

	for i := 0; i < 100; i++ {
		_, err := rt.Run(context.Background(), Request{
			Prompt:         "ok",
			SessionID:      fmt.Sprintf("sess-%d", i),
			TargetSubagent: "explore",
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	}
	<-done
}
