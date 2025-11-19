package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestRemoveCommandLines(t *testing.T) {
	prompt := "/tag foo=bar\ncontent"
	inv, err := commands.Parse(prompt)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	clean := removeCommandLines(prompt, inv)
	if clean != "content" {
		t.Fatalf("unexpected clean prompt: %s", clean)
	}
}

func TestApplyPromptMetadata(t *testing.T) {
	meta := map[string]any{"api.prepend_prompt": "intro", "api.append_prompt": "outro"}
	result := applyPromptMetadata("body", meta)
	if result != "intro\nbody\noutro" {
		t.Fatalf("metadata merge failed: %s", result)
	}
}

func TestApplyPromptMetadataOverride(t *testing.T) {
	meta := map[string]any{"api.prompt_override": " replacement ", "api.append_prompt": "tail"}
	result := applyPromptMetadata("body", meta)
	if result != "replacement\ntail" {
		t.Fatalf("expected override applied, got %q", result)
	}
}

func TestOrderedForcedSkills(t *testing.T) {
	reg := skills.NewRegistry()
	if err := reg.Register(skills.Definition{Name: "alpha"}, skills.HandlerFunc(func(context.Context, skills.ActivationContext) (skills.Result, error) {
		return skills.Result{}, nil
	})); err != nil {
		t.Fatalf("register skill: %v", err)
	}
	activations := orderedForcedSkills(reg, []string{"alpha", "missing"})
	if len(activations) != 1 {
		t.Fatalf("expected one activation")
	}
}

func TestEnforceSandboxHostNoManager(t *testing.T) {
	if err := enforceSandboxHost(nil, "http://example.com"); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestEnforceSandboxHostDenied(t *testing.T) {
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList("allowed.example"), nil)
	if err := enforceSandboxHost(mgr, "http://bad.example"); err == nil {
		t.Fatal("expected host denial")
	}
}

func TestEnforceSandboxHostIgnoresSTDIO(t *testing.T) {
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList("deny"), nil)
	if err := enforceSandboxHost(mgr, "stdio://cmd arg"); err != nil {
		t.Fatalf("expected stdio server to bypass network checks: %v", err)
	}
}

func TestRegisterMCPServersDeniesUnauthorizedHost(t *testing.T) {
	registry := tool.NewRegistry()
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList("allowed.example"), nil)
	err := registerMCPServers(registry, mgr, []string{"http://denied.example"})
	if err == nil {
		t.Fatal("expected host denial error")
	}
	if !errors.Is(err, sandbox.ErrDomainDenied) {
		t.Fatalf("expected domain denied error, got %v", err)
	}
}

func TestRegisterMCPServersPropagatesRegistryErrors(t *testing.T) {
	registry := tool.NewRegistry()
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList(), nil)
	err := registerMCPServers(registry, mgr, []string{""})
	if err == nil {
		t.Fatal("expected registry error")
	}
	if !strings.Contains(err.Error(), "server path is empty") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSnapshotSandboxEmpty(t *testing.T) {
	report := snapshotSandbox(nil)
	if report.ResourceLimits != (sandbox.ResourceLimits{}) {
		t.Fatalf("unexpected limits: %+v", report.ResourceLimits)
	}
}

func TestBuildSandboxManager(t *testing.T) {
	cfg := &config.ProjectConfig{Sandbox: config.SandboxBlock{AllowedPaths: []string{"workspace"}}}
	opts := Options{ProjectRoot: t.TempDir(), Sandbox: SandboxOptions{AllowedPaths: []string{"extra"}, ResourceLimit: sandbox.ResourceLimits{MaxCPUPercent: 10}}}
	mgr, root := buildSandboxManager(opts, cfg)
	if root == "" {
		t.Fatal("expected non-empty root")
	}
	if err := mgr.CheckPath("workspace/file"); err != nil {
		t.Fatalf("expected workspace allowed: %v", err)
	}
	limits := mgr.Limits()
	if limits.MaxCPUPercent != 10 {
		t.Fatalf("unexpected limits: %+v", limits)
	}
}

func TestRegisterToolsUsesDefaultImplementations(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir()}
	if err := registerTools(registry, opts, &config.ProjectConfig{}); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	tools := registry.List()
	if len(tools) != 4 {
		t.Fatalf("expected four default tools (Bash, File, Grep, Glob), got %d", len(tools))
	}
	for _, impl := range tools {
		if strings.TrimSpace(impl.Name()) == "" {
			t.Fatalf("tool missing name: %+v", impl)
		}
	}
}

func TestRegisterToolsSkipsNilEntries(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir(), Tools: []tool.Tool{nil, &echoTool{}}}
	if err := registerTools(registry, opts, &config.ProjectConfig{}); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	tools := registry.List()
	if len(tools) != 1 || tools[0].Name() != "echo" {
		t.Fatalf("expected only echo tool, got %+v", tools)
	}
}

func TestCfgSandboxPathsNormalizes(t *testing.T) {
	cfg := &config.ProjectConfig{Sandbox: config.SandboxBlock{AllowedPaths: []string{" ", "foo", " foo ", "bar"}}}
	paths := cfgSandboxPaths(cfg)
	if len(paths) != 2 || paths[0] != "bar" || paths[1] != "foo" {
		t.Fatalf("unexpected normalized paths: %+v", paths)
	}
	if cfgSandboxPaths(nil) != nil {
		t.Fatal("expected nil config to return nil slice")
	}
}

func TestLoadProjectConfigHandlesMissingClaudeDir(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	loader, err := config.NewLoader(root)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loadProjectConfig(loader)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected fallback config")
		return
	}
	if cfg.ClaudeDir != "" {
		t.Fatalf("expected empty claude dir, got %q", cfg.ClaudeDir)
	}
	if cfg.Environment == nil || len(cfg.Environment) != 0 {
		t.Fatalf("expected empty environment map, got %+v", cfg.Environment)
	}
}

func TestLoadProjectConfigHandlesPluginManifestError(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	claude := writeClaudeConfig(t, root, "version: '1.0'\nplugins:\n  - name: broken\n")
	brokenDir := filepath.Join(claude, "plugins", "broken")
	if err := os.MkdirAll(brokenDir, 0o755); err != nil {
		t.Fatalf("broken dir: %v", err)
	}
	loader, err := config.NewLoader(root)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loadProjectConfig(loader)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg == nil {
		t.Fatalf("expected fallback state, got nil")
		return
	}
	if cfg.ClaudeDir != "" {
		t.Fatalf("expected empty claude dir, got %+v", cfg)
	}
}

func TestLoadProjectConfigHandlesInvalidPluginName(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	root := t.TempDir()
	claude := writeClaudeConfig(t, root, "version: '1.0'\nplugins:\n  - name: bad\n")
	pluginDir := filepath.Join(claude, "plugins", "bad")
	if err := os.MkdirAll(pluginDir, 0o755); err != nil {
		t.Fatalf("plugin dir: %v", err)
	}
	manifest := "name: BADNAME\n"
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.yaml"), []byte(manifest), 0o600); err != nil {
		t.Fatalf("manifest: %v", err)
	}
	loader, err := config.NewLoader(root)
	if err != nil {
		t.Fatalf("new loader: %v", err)
	}
	cfg, err := loadProjectConfig(loader)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg == nil || cfg.ClaudeDir != "" {
		t.Fatalf("expected fallback config, got %+v", cfg)
	}
}

func TestStringSlice(t *testing.T) {
	values := stringSlice([]any{"a", "b"})
	if len(values) != 2 {
		t.Fatalf("unexpected values: %+v", values)
	}
	values = stringSlice("single")
	if len(values) != 1 || values[0] != "single" {
		t.Fatalf("conversion failed: %+v", values)
	}
	values = stringSlice([]string{"c", "a"})
	if len(values) != 2 || values[0] != "a" || values[1] != "c" {
		t.Fatalf("unexpected sorted slice: %+v", values)
	}
}

func TestApplyCommandMetadata(t *testing.T) {
	req := &Request{}
	meta := map[string]any{"api.target_subagent": "ops", "api.tool_whitelist": []any{"a", "b"}}
	applyCommandMetadata(req, meta)
	if req.TargetSubagent != "ops" || len(req.ToolWhitelist) != 2 {
		t.Fatalf("metadata not applied: %+v", req)
	}
}

func TestApplyCommandMetadataIgnoresNil(t *testing.T) {
	applyCommandMetadata(nil, map[string]any{"api.target_subagent": "ops"})
	req := &Request{}
	applyCommandMetadata(req, map[string]any{})
	if req.TargetSubagent != "" || len(req.ToolWhitelist) != 0 {
		t.Fatalf("expected no changes for empty metadata, got %+v", req)
	}
}

func TestCombineAndPrependPrompt(t *testing.T) {
	combined := combinePrompt("existing", "extra")
	if combined == "existing" {
		t.Fatal("combine prompt failed")
	}
	if empty := combinePrompt("", "solo"); empty != "solo" {
		t.Fatalf("expected solo prompt, got %q", empty)
	}
	prepended := prependPrompt("body", "intro")
	if prepended[:5] != "intro" {
		t.Fatal("prepend failed")
	}
	if kept := prependPrompt("body", "   "); kept != "body" {
		t.Fatalf("expected body unchanged, got %q", kept)
	}
	if onlyPrefix := prependPrompt("   ", "intro"); onlyPrefix != "intro" {
		t.Fatalf("expected intro only, got %q", onlyPrefix)
	}
}

func TestAnyToString(t *testing.T) {
	if val, ok := anyToString(nil); ok || val != "" {
		t.Fatal("expected empty")
	}
	val, ok := anyToString(123)
	if !ok || val == "" {
		t.Fatal("conversion failed")
	}
}

func TestAnyToStringCoversStringer(t *testing.T) {
	val, ok := anyToString("  spaced  ")
	if !ok || val != "spaced" {
		t.Fatalf("expected trimmed string, got %q", val)
	}
	val, ok = anyToString(fakeStringer{text: "  custom  "})
	if !ok || val != "custom" {
		t.Fatalf("expected stringer conversion, got %q", val)
	}
}

func TestOptionsModeContext(t *testing.T) {
	opts := Options{}
	mode := opts.modeContext()
	if mode.EntryPoint != defaultEntrypoint {
		t.Fatalf("unexpected default entrypoint: %v", mode.EntryPoint)
	}
}

func TestActivationContext(t *testing.T) {
	req := Request{Prompt: "p", Tags: map[string]string{"k": "v"}, Metadata: map[string]any{"m": "v"}, Channels: []string{"cli"}}
	act := req.activationContext("prompt")
	if act.Prompt != "prompt" || len(act.Tags) != 1 {
		t.Fatalf("unexpected activation: %+v", act)
	}
}

func TestDefaultSessionID(t *testing.T) {
	if id := defaultSessionID(EntryPointCI); id == "" {
		t.Fatal("session ID empty")
	}
}

func TestMergeTagsUtility(t *testing.T) {
	req := &Request{Tags: map[string]string{"existing": "x"}}
	meta := map[string]any{"api.tags": map[string]any{"new": "y"}}
	mergeTags(req, meta)
	if req.Tags["new"] != "y" {
		t.Fatalf("tags not merged: %+v", req.Tags)
	}
}

func TestMergeMetadataInitializesDestination(t *testing.T) {
	dst := mergeMetadata(nil, map[string]any{"k": "v"})
	if v, ok := dst["k"].(string); !ok || v != "v" {
		t.Fatalf("expected metadata to be initialised, got %+v", dst)
	}
	dst = mergeMetadata(dst, map[string]any{"k": "override"})
	if v, ok := dst["k"].(string); !ok || v != "override" {
		t.Fatalf("expected override applied, got %+v", dst)
	}
	same := mergeMetadata(dst, nil)
	if sameVal, ok := same["k"].(string); !ok || sameVal != "override" {
		t.Fatalf("expected nil source to be ignored, got %+v", same)
	}
}

func TestModelFactoryFuncModel(t *testing.T) {
	called := false
	fn := ModelFactoryFunc(func(context.Context) (model.Model, error) {
		called = true
		return &stubModel{}, nil
	})
	m, err := fn.Model(context.Background())
	if err != nil || m == nil || !called {
		t.Fatalf("factory not invoked correctly: m=%v err=%v called=%v", m, err, called)
	}
}

func TestModelFactoryFuncNil(t *testing.T) {
	var fn ModelFactoryFunc
	if _, err := fn.Model(context.Background()); !errors.Is(err, ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
}

func TestRuntimeHookAdapterRecordsEvents(t *testing.T) {
	rec := defaultHookRecorder()
	exec := newHookExecutor(Options{}, rec)
	adapter := &runtimeHookAdapter{executor: exec, recorder: rec}

	if err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{Name: "t"}); err != nil {
		t.Fatalf("pre: %v", err)
	}
	if err := adapter.PostToolUse(context.Background(), coreevents.ToolResultPayload{Name: "t"}); err != nil {
		t.Fatalf("post: %v", err)
	}
	if err := adapter.UserPrompt(context.Background(), "p"); err != nil {
		t.Fatalf("prompt: %v", err)
	}
	if err := adapter.Stop(context.Background(), "reason"); err != nil {
		t.Fatalf("stop: %v", err)
	}

	events := rec.Drain()
	if len(events) == 0 {
		t.Fatal("expected recorded events")
	}
	foundStop := false
	for _, e := range events {
		if e.Type == coreevents.Stop {
			foundStop = true
			break
		}
	}
	if !foundStop {
		t.Fatalf("expected stop event in %+v", events)
	}
	if len(rec.Drain()) != 0 {
		t.Fatal("expected drained recorder to be empty")
	}
}

func TestNewHookExecutorRegistersTypedHooks(t *testing.T) {
	hook := newRecordingTypedHook()
	exec := newHookExecutor(Options{TypedHooks: []any{hook}}, defaultHookRecorder())
	evt := coreevents.Event{Type: coreevents.PreToolUse, Payload: coreevents.ToolUsePayload{Name: "echo"}}
	if err := exec.Publish(evt); err != nil {
		t.Fatalf("publish: %v", err)
	}
	hook.WaitForCall(t)
}

func writeClaudeConfig(t *testing.T, projectRoot, payload string) string {
	t.Helper()
	claudeDir := filepath.Join(projectRoot, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("claude dir: %v", err)
	}
	configPath := filepath.Join(claudeDir, "config.yaml")
	if err := os.WriteFile(configPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("config file: %v", err)
	}
	return claudeDir
}

type fakeStringer struct {
	text string
}

func (f fakeStringer) String() string {
	return f.text
}

type recordingTypedHook struct {
	signals chan struct{}
}

func newRecordingTypedHook() *recordingTypedHook {
	return &recordingTypedHook{signals: make(chan struct{}, 1)}
}

func (h *recordingTypedHook) PreToolUse(context.Context, coreevents.ToolUsePayload) error {
	select {
	case h.signals <- struct{}{}:
	default:
	}
	return nil
}

func (h *recordingTypedHook) WaitForCall(t *testing.T) {
	t.Helper()
	select {
	case <-h.signals:
	case <-time.After(time.Second):
		t.Fatal("typed hook was not invoked")
	}
}
