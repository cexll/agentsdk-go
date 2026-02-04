package api

import (
	"context"
	"fmt"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
)

type stringerType struct{}

func (s stringerType) String() string { return " str " }

func TestRequestHelperUtilities(t *testing.T) {
	t.Parallel()

	prompt := "line1\n/echo hi\nline3"
	invs := []commands.Invocation{{Position: 2}}
	if got := removeCommandLines(prompt, nil); got != prompt {
		t.Fatalf("expected unchanged prompt, got %q", got)
	}
	if got := removeCommandLines(prompt, invs); got != "line1\nline3" {
		t.Fatalf("unexpected prompt %q", got)
	}

	meta := map[string]any{
		"api.prepend_prompt":  "first",
		"api.append_prompt":   "last",
		"api.prompt_override": "override",
	}
	if got := applyPromptMetadata(" base ", meta); got != "first\noverride\nlast" {
		t.Fatalf("unexpected prompt override %q", got)
	}

	req := &Request{}
	mergeTags(req, map[string]any{"api.tags": map[string]string{"a": "1"}})
	if req.Tags["a"] != "1" {
		t.Fatalf("expected tag merge")
	}
	mergeTags(req, map[string]any{"api.tags": map[string]any{"b": 2}})
	if req.Tags["b"] != "2" {
		t.Fatalf("expected map[string]any merge")
	}

	applyCommandMetadata(req, map[string]any{
		"api.target_subagent": "explore",
		"api.tool_whitelist":  []any{"Bash", "Read"},
	})
	if req.TargetSubagent != "explore" || len(req.ToolWhitelist) != 2 {
		t.Fatalf("expected metadata applied, got %+v", req)
	}

	if _, ok := applySubagentTarget(nil); ok {
		t.Fatalf("nil request should not match")
	}
	if def, ok := applySubagentTarget(&Request{TargetSubagent: " "}); ok || def.Name != "" {
		t.Fatalf("empty target should not match")
	}
	req.TargetSubagent = "plan"
	def, ok := applySubagentTarget(req)
	if !ok || def.Name == "" || req.TargetSubagent != def.Name {
		t.Fatalf("expected builtin match")
	}
	req.TargetSubagent = "  Custom Tool "
	def, ok = applySubagentTarget(req)
	if ok || def.Name != "" || req.TargetSubagent != "custom tool" {
		t.Fatalf("expected canonicalized target, got %q", req.TargetSubagent)
	}

	subCtx, ok := buildSubagentContext(Request{SessionID: "sess"}, subagents.Definition{}, false)
	if !ok || subCtx.SessionID != "sess" {
		t.Fatalf("expected session context")
	}
	subCtx, ok = buildSubagentContext(Request{Metadata: map[string]any{"task.description": "d", "task.model": "Haiku"}}, subagents.Definition{}, false)
	if !ok || subCtx.Metadata["task.model"] != "haiku" {
		t.Fatalf("expected metadata context")
	}
	if _, ok := buildSubagentContext(Request{}, subagents.Definition{}, false); ok {
		t.Fatalf("expected empty context")
	}

	if metadataString(nil, "x") != "" {
		t.Fatalf("expected empty metadata string")
	}
	if val := metadataString(map[string]any{"x": " y "}, "x"); val != "y" {
		t.Fatalf("unexpected metadata string %q", val)
	}

	if got := canonicalToolName("  AbC "); got != "abc" {
		t.Fatalf("unexpected canonical name %q", got)
	}
	if got := toLowerSet([]string{" ", "A", "a"}); len(got) != 1 {
		t.Fatalf("unexpected set %v", got)
	}

	if got := combineToolWhitelists(nil, nil); got != nil {
		t.Fatalf("expected nil whitelist")
	}
	if got := combineToolWhitelists([]string{"a"}, nil); len(got) != 1 {
		t.Fatalf("expected request whitelist")
	}
	if got := combineToolWhitelists(nil, []string{"b"}); len(got) != 1 {
		t.Fatalf("expected subagent whitelist")
	}
	if got := combineToolWhitelists([]string{"a", "b"}, []string{"b"}); len(got) != 1 {
		t.Fatalf("expected intersection")
	}

	reg := skills.NewRegistry()
	_ = reg.Register(skills.Definition{Name: "demo"}, skills.HandlerFunc(func(ctx context.Context, ac skills.ActivationContext) (skills.Result, error) {
		return skills.Result{Output: "ok"}, nil
	}))
	if got := orderedForcedSkills(nil, []string{"demo"}); got != nil {
		t.Fatalf("expected nil with nil registry")
	}
	if got := orderedForcedSkills(reg, nil); got != nil {
		t.Fatalf("expected nil with empty names")
	}
	if got := orderedForcedSkills(reg, []string{"missing", "demo"}); len(got) != 1 {
		t.Fatalf("expected filtered activations")
	}

	if got := combinePrompt("", nil); got != "" {
		t.Fatalf("expected empty combine")
	}
	if got := combinePrompt("a", " b "); got != "a\nb" {
		t.Fatalf("unexpected combine %q", got)
	}
	if got := prependPrompt("x", ""); got != "x" {
		t.Fatalf("unexpected prepend %q", got)
	}
	if got := prependPrompt("", "y"); got != "y" {
		t.Fatalf("unexpected prepend %q", got)
	}

	if got := mergeMetadata(nil, nil); got != nil {
		t.Fatalf("expected nil metadata")
	}
	if got := mergeMetadata(nil, map[string]any{"k": "v"}); got["k"] != "v" {
		t.Fatalf("expected merged metadata")
	}

	if got, ok := anyToString(" x "); !ok || got != "x" {
		t.Fatalf("unexpected string conversion")
	}
	if got, ok := anyToString(stringerType{}); !ok || got != "str" {
		t.Fatalf("unexpected stringer conversion %q", got)
	}
	if _, ok := anyToString(nil); ok {
		t.Fatalf("expected nil conversion to fail")
	}
	if got, ok := anyToString(12); !ok || got != "12" {
		t.Fatalf("unexpected fmt conversion %q", got)
	}

	if got := stringSlice([]string{"b", "a"}); got[0] != "a" {
		t.Fatalf("expected sorted slice %v", got)
	}
	if got := stringSlice([]any{"b", 1}); len(got) != 2 || got[0] != "1" {
		t.Fatalf("unexpected any slice %v", got)
	}
	if got := stringSlice("  x "); len(got) != 1 || got[0] != "x" {
		t.Fatalf("unexpected string slice %v", got)
	}
	if got := stringSlice(42); got != nil {
		t.Fatalf("expected nil slice")
	}
	if got, ok := anyToString(fmt.Errorf("err")); !ok || got != "err" {
		t.Fatalf("unexpected error string %q", got)
	}
}

func TestCombineToolWhitelistsNilInputs(t *testing.T) {
	if got := combineToolWhitelists(nil, nil); got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestCombineToolWhitelistsIntersection(t *testing.T) {
	got := combineToolWhitelists([]string{"a", "b"}, []string{"b", "c"})
	if len(got) != 1 {
		t.Fatalf("expected intersection, got %v", got)
	}
}
