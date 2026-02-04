package api

import (
	"context"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

type helperStubTool struct {
	name string
}

func (s *helperStubTool) Name() string        { return s.name }
func (s *helperStubTool) Description() string { return "desc" }
func (s *helperStubTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{Type: "object"}
}
func (s *helperStubTool) Execute(context.Context, map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Success: true}, nil
}

func TestAvailableToolsAndSchemaToMap(t *testing.T) {
	t.Parallel()

	reg := tool.NewRegistry()
	_ = reg.Register(&helperStubTool{name: "Bash"})
	defs := availableTools(reg, map[string]struct{}{"bash": {}})
	if len(defs) != 1 || defs[0].Name != "Bash" {
		t.Fatalf("unexpected tool defs %v", defs)
	}
	if schema := schemaToMap(&tool.JSONSchema{Type: "object", Properties: map[string]any{"a": "b"}}); schema["type"] != "object" {
		t.Fatalf("unexpected schema map %v", schema)
	}
}

func TestRegisterHelpers(t *testing.T) {
	t.Parallel()

	if _, err := registerSkills([]SkillRegistration{{Definition: skills.Definition{Name: "s"}, Handler: nil}}); err == nil {
		t.Fatalf("expected skill handler error")
	}
	if _, err := registerCommands([]CommandRegistration{{Definition: commands.Definition{Name: "c"}, Handler: nil}}); err == nil {
		t.Fatalf("expected command handler error")
	}
	if _, err := registerSubagents([]SubagentRegistration{{Definition: subagents.Definition{Name: "x"}, Handler: nil}}); err == nil {
		t.Fatalf("expected subagent handler error")
	}
}

func TestConvertMessages(t *testing.T) {
	t.Parallel()

	msgs := []message.Message{
		{Role: "user", Content: "hi", ToolCalls: []message.ToolCall{{ID: "1", Name: "t", Arguments: map[string]any{"a": "b"}}}},
	}
	modelMsgs := convertMessages(msgs)
	if len(modelMsgs) != 1 || modelMsgs[0].Role != "user" {
		t.Fatalf("unexpected converted messages %v", modelMsgs)
	}
	if modelMsgs[0].ToolCalls[0].Arguments["a"] != "b" {
		t.Fatalf("unexpected tool call arguments")
	}
	if clone := cloneArguments(modelMsgs[0].ToolCalls[0].Arguments); clone["a"] != "b" {
		t.Fatalf("unexpected cloned args")
	}
	if def := availableTools(nil, nil); def != nil {
		t.Fatalf("expected nil defs")
	}
	if schema := schemaToMap(nil); schema != nil {
		t.Fatalf("expected nil schema")
	}
	def := model.ToolDefinition{Name: "x"}
	if def.Name == "" {
		t.Fatalf("unexpected empty def")
	}
}
