package api

import (
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
)

func TestDefaultSubagentDefinitions(t *testing.T) {
	defs := DefaultSubagentDefinitions()
	if len(defs) == 0 {
		t.Fatalf("expected default subagent definitions")
	}
	seen := map[string]struct{}{}
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, name := range []string{subagents.TypeGeneralPurpose, subagents.TypeExplore, subagents.TypePlan} {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing default subagent %q", name)
		}
	}
}
