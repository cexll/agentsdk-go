package subagents

import (
	"context"
	"testing"
)

func TestContextCloneIsolation(t *testing.T) {
	ctx := Context{
		SessionID:     "root",
		Metadata:      map[string]any{"a": 1},
		ToolWhitelist: []string{"bash", "curl"},
	}
	cloned := ctx.Clone().WithMetadata(map[string]any{"b": 2}).RestrictTools("curl")
	if ctx.SessionID != "root" || len(ctx.Metadata) != 1 || len(ctx.ToolWhitelist) != 2 {
		t.Fatalf("clone mutated original: %+v", ctx)
	}
	if !cloned.Allows("curl") || cloned.Allows("bash") {
		t.Fatalf("expected restriction to keep curl only: %+v", cloned.ToolList())
	}
}

func TestContextStorageOnStdlibContext(t *testing.T) {
	sub := Context{SessionID: "child", ToolWhitelist: []string{"bash"}}
	ctx := WithContext(context.Background(), sub)
	recovered, ok := FromContext(ctx)
	if !ok {
		t.Fatalf("expected context present")
	}
	if recovered.SessionID != "child" || !recovered.Allows("bash") {
		t.Fatalf("unexpected recovered context: %+v", recovered)
	}

	if _, ok := FromContext(context.TODO()); ok {
		t.Fatalf("expected no context from nil")
	}

	merged := Context{}.WithMetadata(map[string]any{"k": "v"})
	if merged.Metadata["k"] != "v" {
		t.Fatalf("metadata merge failed")
	}
}

func TestContextAllowsAndToolList(t *testing.T) {
	var unrestricted Context
	if !unrestricted.Allows("anything") {
		t.Fatalf("empty whitelist should allow any tool")
	}
	if unrestricted.ToolList() != nil {
		t.Fatalf("tool list for empty whitelist should be nil")
	}

	ctx := Context{ToolWhitelist: []string{"bash", "curl", "curl"}}
	ctx = ctx.RestrictTools(" CURL ", "unknown ")
	if ctx.Allows("bash") {
		t.Fatalf("bash should have been filtered out by restriction")
	}
	if !ctx.Allows("curl") {
		t.Fatalf("curl should remain allowed")
	}
	list := ctx.ToolList()
	if len(list) != 1 || list[0] != "curl" {
		t.Fatalf("unexpected tool list: %v", list)
	}
}
