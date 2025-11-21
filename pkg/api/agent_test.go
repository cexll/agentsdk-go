package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/agent"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestRuntimeRequiresModelFactory(t *testing.T) {
	_, err := New(context.Background(), Options{ProjectRoot: t.TempDir()})
	if err == nil {
		t.Fatal("expected model error")
	}
}

func TestRuntimeLoadsSettingsFallback(t *testing.T) {
	opts := Options{ProjectRoot: t.TempDir(), Model: &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "ok"}}}}}
	rt, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.Settings() == nil {
		t.Fatal("expected fallback settings")
	}
}

func TestRuntimeRunSimple(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "done"}}}}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), Request{Prompt: "hello"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Result == nil || resp.Result.Output != "done" {
		t.Fatalf("unexpected result: %+v", resp.Result)
	}
	if rt.Sandbox() == nil {
		t.Fatal("sandbox manager missing")
	}
}
func TestRuntimePropagatesModelError(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{err: errors.New("model refused")}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, runErr := rt.Run(context.Background(), Request{Prompt: "please help"})
	if !errors.Is(runErr, mdl.err) {
		t.Fatalf("expected model error, got %v", runErr)
	}
	if resp != nil {
		t.Fatalf("expected no response on model error, got %+v", resp)
	}
}

func TestRuntimeToolFlow(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{{ID: "1", Name: "echo", Arguments: map[string]any{"text": "hi"}}}}},
		{Message: model.Message{Role: "assistant", Content: "done"}},
	}}

	toolImpl := &echoTool{}
	opts := Options{ProjectRoot: root, Model: mdl, Tools: []tool.Tool{toolImpl}, Sandbox: SandboxOptions{AllowedPaths: []string{root}, Root: root, NetworkAllow: []string{"localhost"}}}
	rt, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), Request{Prompt: "call tool", ToolWhitelist: []string{"echo"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Result == nil || resp.Result.Output != "done" {
		t.Fatalf("unexpected output: %+v", resp.Result)
	}
	if len(resp.HookEvents) == 0 {
		t.Fatal("expected hook events")
	}
	if toolImpl.calls == 0 {
		t.Fatal("expected tool execution")
	}
}

func TestRuntimeToolExecutor_ErrorHistory(t *testing.T) {
	cases := []struct {
		name   string
		errMsg string
	}{
		{name: "records error output", errMsg: "network unreachable"},
		{name: "escapes quotes for json", errMsg: `input "invalid"`},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			fail := &failingTool{err: errors.New(tc.errMsg)}
			if err := reg.Register(fail); err != nil {
				t.Fatalf("register tool: %v", err)
			}
			exec := tool.NewExecutor(reg, nil)
			history := message.NewHistory()
			rtExec := &runtimeToolExecutor{
				executor: exec,
				hooks:    &runtimeHookAdapter{},
				history:  history,
				host:     "localhost",
			}

			call := agent.ToolCall{ID: "c1", Name: fail.Name(), Input: map[string]any{"k": "v"}}
			res, err := rtExec.Execute(context.Background(), call, agent.NewContext())
			if err == nil {
				t.Fatal("expected tool execution error")
			}
			if res.Metadata == nil || res.Metadata["error"] != fail.err.Error() {
				t.Fatalf("expected error metadata, got %+v", res.Metadata)
			}

			msgs := history.All()
			if len(msgs) != 1 {
				t.Fatalf("expected history entry, got %d", len(msgs))
			}
			var payload map[string]string
			if unmarshalErr := json.Unmarshal([]byte(msgs[0].Content), &payload); unmarshalErr != nil {
				t.Fatalf("history content not valid json: %v", unmarshalErr)
			}
			if payload["error"] != fail.err.Error() {
				t.Fatalf("expected error field, got %+v", payload)
			}
			if msgs[0].Role != "tool" || len(msgs[0].ToolCalls) != 1 || msgs[0].ToolCalls[0].Name != call.Name {
				t.Fatalf("tool history entry malformed: %+v", msgs[0])
			}
		})
	}
}

func TestNewRejectsDisallowedMCPServer(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "ok"}}}}
	opts := Options{
		ProjectRoot: root,
		Model:       mdl,
		Sandbox:     SandboxOptions{NetworkAllow: []string{"allowed.example"}},
		MCPServers:  []string{"http://bad.example"},
	}
	if _, err := New(context.Background(), opts); err == nil {
		t.Fatal("expected MCP host guard error")
	}
}

func TestRuntimeCommandAndSkillIntegration(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "ok"}}}}

	skill := SkillRegistration{
		Definition: skills.Definition{Name: "tagger", Matchers: []skills.Matcher{skills.KeywordMatcher{Any: []string{"trigger"}}}},
		Handler: skills.HandlerFunc(func(context.Context, skills.ActivationContext) (skills.Result, error) {
			return skills.Result{Output: "skill-prefix", Metadata: map[string]any{"api.tags": map[string]string{"skill": "true"}}}, nil
		}),
	}
	command := CommandRegistration{
		Definition: commands.Definition{Name: "tag"},
		Handler: commands.HandlerFunc(func(context.Context, commands.Invocation) (commands.Result, error) {
			return commands.Result{Metadata: map[string]any{"api.tags": map[string]string{"severity": "info"}}}, nil
		}),
	}

	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Skills: []SkillRegistration{skill}, Commands: []CommandRegistration{command}})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	resp, err := rt.Run(context.Background(), Request{Prompt: "/tag\ntrigger"})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp.Tags["skill"] != "true" || resp.Tags["severity"] != "info" {
		t.Fatalf("tags missing: %+v", resp.Tags)
	}
}

func newClaudeProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatalf("claude dir: %v", err)
	}
	settings := []byte(`{"model":"claude-3-opus"}`)
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), settings, 0o600); err != nil {
		t.Fatalf("settings: %v", err)
	}
	return root
}

type stubModel struct {
	responses []*model.Response
	idx       int
	err       error
}

func (s *stubModel) Complete(context.Context, model.Request) (*model.Response, error) {
	if s.err != nil {
		return nil, s.err
	}
	if len(s.responses) == 0 {
		return &model.Response{Message: model.Message{Role: "assistant"}}, nil
	}
	if s.idx >= len(s.responses) {
		return s.responses[len(s.responses)-1], nil
	}
	resp := s.responses[s.idx]
	s.idx++
	return resp, nil
}

func (s *stubModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return errors.New("stream not supported")
}

type echoTool struct {
	calls int
}

func (e *echoTool) Name() string             { return "echo" }
func (e *echoTool) Description() string      { return "echo text" }
func (e *echoTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (e *echoTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	e.calls++
	text := params["text"]
	return &tool.ToolResult{Output: fmt.Sprint(text)}, nil
}

type failingTool struct {
	err error
}

func (f *failingTool) Name() string             { return "fail" }
func (f *failingTool) Description() string      { return "always fails" }
func (f *failingTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (f *failingTool) Execute(context.Context, map[string]interface{}) (*tool.ToolResult, error) {
	return nil, f.err
}
