package api

import (
	"context"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
)

func TestDefinitionSnapshotVariants(t *testing.T) {
	// nil executor path
	def := definitionSnapshot(nil, "Mixed")
	if def.Name != "mixed" {
		t.Fatalf("unexpected default definition: %+v", def)
	}

	exec := commands.NewExecutor()
	if err := exec.Register(commands.Definition{Name: "tag"}, commands.HandlerFunc(func(context.Context, commands.Invocation) (commands.Result, error) {
		return commands.Result{}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	def = definitionSnapshot(exec, "TAG")
	if def.Name != "tag" {
		t.Fatalf("expected registered definition, got %+v", def)
	}
	def = definitionSnapshot(exec, "missing")
	if def.Name != "missing" {
		t.Fatalf("expected fallback for missing, got %+v", def)
	}
}
