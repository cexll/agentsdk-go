package api

import (
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
)

func TestDefaultSubagentDefinitions(t *testing.T) {
	defs := DefaultSubagentDefinitions()
	if len(defs) < 3 {
		t.Fatalf("expected builtin definitions, got %d", len(defs))
	}
	catalog := map[string]subagents.Definition{}
	for _, def := range defs {
		catalog[def.Name] = def
	}
	explore, ok := catalog[subagents.TypeExplore]
	if !ok || explore.DefaultModel != subagents.ModelHaiku {
		t.Fatalf("expected explore definition with haiku default, got %+v", explore)
	}
	defs[0].Name = "mutated"
	refreshed := DefaultSubagentDefinitions()
	if refreshed[0].Name == "mutated" {
		t.Fatalf("expected helper to return clones")
	}
}
