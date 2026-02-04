package api

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
	toolbuiltin "github.com/cexll/agentsdk-go/pkg/tool/builtin"
)

type agentHelperStubTool struct {
	name string
}

func (s agentHelperStubTool) Name() string        { return s.name }
func (s agentHelperStubTool) Description() string { return "stub" }
func (s agentHelperStubTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object"}
}
func (s agentHelperStubTool) Execute(context.Context, map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Success: true}, nil
}

func TestStreamEmitContextHelpers(t *testing.T) {
	if streamEmitFromContext(nil) != nil {
		t.Fatalf("expected nil emit from nil context")
	}
	ctx := withStreamEmit(context.Background(), func(context.Context, StreamEvent) {})
	if streamEmitFromContext(ctx) == nil {
		t.Fatalf("expected emit func from context")
	}
	if got := withStreamEmit(nil, nil); got == nil {
		t.Fatalf("expected non-nil context")
	}
}

func TestPermissionReasonHelpers(t *testing.T) {
	if got := buildPermissionReason(security.PermissionDecision{}); got != "" {
		t.Fatalf("expected empty reason, got %q", got)
	}
	if got := buildPermissionReason(security.PermissionDecision{Target: "x"}); !strings.Contains(got, "target") {
		t.Fatalf("unexpected reason %q", got)
	}
	if got := buildPermissionReason(security.PermissionDecision{Rule: "r"}); !strings.Contains(got, "rule") {
		t.Fatalf("unexpected reason %q", got)
	}
	if got := buildPermissionReason(security.PermissionDecision{Rule: "r", Target: "t"}); !strings.Contains(got, "for") {
		t.Fatalf("unexpected reason %q", got)
	}
	if cmd := formatApprovalCommand("", ""); cmd != "tool" {
		t.Fatalf("expected default tool name, got %q", cmd)
	}
	if cmd := formatApprovalCommand("Bash", "ls"); cmd != "Bash(ls)" {
		t.Fatalf("unexpected approval command %q", cmd)
	}
	if actor := approvalActor(" "); actor != "host" {
		t.Fatalf("expected host fallback, got %q", actor)
	}
	if actor := approvalActor("alice"); actor != "alice" {
		t.Fatalf("unexpected actor %q", actor)
	}
}

func TestRegisterToolsDisallowedAndDuplicates(t *testing.T) {
	reg := tool.NewRegistry()
	opts := Options{
		Tools: []tool.Tool{
			agentHelperStubTool{name: "Bash"},
			agentHelperStubTool{name: "Bash"},
			agentHelperStubTool{name: "Read"},
			toolbuiltin.NewTaskTool(),
		},
	}
	settings := &config.Settings{DisallowedTools: []string{"bash"}}
	taskTool, err := registerTools(reg, opts, settings, nil, nil)
	if err != nil {
		t.Fatalf("register tools failed: %v", err)
	}
	if taskTool == nil {
		t.Fatalf("expected task tool")
	}
	if _, err := reg.Get("Read"); err != nil {
		t.Fatalf("expected Read tool registered: %v", err)
	}
	if _, err := reg.Get("Bash"); err == nil {
		t.Fatalf("expected Bash to be disallowed")
	}
}

func TestLocateTaskTool(t *testing.T) {
	if got := locateTaskTool(nil); got != nil {
		t.Fatalf("expected nil task tool")
	}
	toolList := []tool.Tool{agentHelperStubTool{name: "x"}, toolbuiltin.NewTaskTool()}
	if got := locateTaskTool(toolList); got == nil {
		t.Fatalf("expected task tool")
	}
}

func TestResolveModelErrors(t *testing.T) {
	if _, err := resolveModel(context.Background(), Options{}); !errors.Is(err, ErrMissingModel) {
		t.Fatalf("expected missing model error, got %v", err)
	}
	factoryErr := errors.New("boom")
	_, err := resolveModel(context.Background(), Options{ModelFactory: modelProviderFunc{err: factoryErr}})
	if err == nil || !strings.Contains(err.Error(), "model factory") {
		t.Fatalf("expected model factory error, got %v", err)
	}
}

type modelProviderFunc struct {
	err error
}

func (m modelProviderFunc) Model(context.Context) (model.Model, error) {
	return nil, m.err
}

func TestRunTaskInvocationErrors(t *testing.T) {
	rt := &Runtime{mode: ModeContext{EntryPoint: EntryPointCLI}}
	if _, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: "hi"}); err == nil {
		t.Fatalf("expected subagent manager error")
	}
	rt.subMgr = subagents.NewManager()
	if _, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: ""}); err == nil {
		t.Fatalf("expected empty prompt error")
	}
	if _, err := rt.runTaskInvocation(context.Background(), toolbuiltin.TaskRequest{Prompt: "hi"}); err == nil {
		t.Fatalf("expected no result error")
	}
}

func TestConvertTaskToolResultDefaults(t *testing.T) {
	res := subagents.Result{Subagent: "demo", Output: "", Error: "boom", Metadata: map[string]any{"k": "v"}}
	out := convertTaskToolResult(res)
	if out.Success {
		t.Fatalf("expected failure")
	}
	if !strings.Contains(out.Output, "demo") {
		t.Fatalf("unexpected output %q", out.Output)
	}
	if data, ok := out.Data.(map[string]any); !ok || data["error"] != "boom" {
		t.Fatalf("expected error metadata")
	}
}

func TestFilterBuiltinNamesAndTaskRegistration(t *testing.T) {
	order := []string{"file_read", "bash", "task"}
	if got := filterBuiltinNames([]string{"FILE-READ", "bash"}, order); len(got) != 2 {
		t.Fatalf("unexpected filtered names %v", got)
	}
	if got := filterBuiltinNames([]string{}, order); got != nil {
		t.Fatalf("expected nil filtered names, got %v", got)
	}
	if !shouldRegisterTaskTool(EntryPointCLI) {
		t.Fatalf("expected task tool for CLI")
	}
	if shouldRegisterTaskTool(EntryPointCI) {
		t.Fatalf("expected task tool disabled for CI")
	}
}

func TestEnforceSandboxHost(t *testing.T) {
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList("allowed.com"), nil)
	if err := enforceSandboxHost(mgr, "https://allowed.com"); err != nil {
		t.Fatalf("expected allowed host, got %v", err)
	}
	if err := enforceSandboxHost(mgr, "https://blocked.com"); err == nil {
		t.Fatalf("expected blocked host error")
	}
}
