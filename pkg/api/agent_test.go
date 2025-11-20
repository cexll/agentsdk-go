package api

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

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

func TestRuntimeLoadsConfigFallback(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	manifestDir := filepath.Join(home, ".claude", "plugins", "marketplaces")
	if err := os.MkdirAll(manifestDir, 0o755); err != nil {
		t.Fatalf("manifest dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(manifestDir, "manifest.yaml"), []byte("plugins: []\n"), 0o600); err != nil {
		t.Fatalf("manifest: %v", err)
	}

	opts := Options{ProjectRoot: t.TempDir(), Model: &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "ok"}}}}}
	rt, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	if rt.Config() == nil {
		t.Fatal("expected fallback config")
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
	if err := os.WriteFile(filepath.Join(claude, "config.yaml"), []byte("version: '1.0'\n"), 0o600); err != nil {
		t.Fatalf("config: %v", err)
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
