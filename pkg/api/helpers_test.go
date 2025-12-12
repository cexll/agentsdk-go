package api

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

type namedTool struct{ name string }

func (n *namedTool) Name() string             { return n.name }
func (n *namedTool) Description() string      { return "named" }
func (n *namedTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (n *namedTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: n.name}, nil
}

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
	err := registerMCPServers(context.Background(), registry, mgr, []string{"http://denied.example"})
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
	err := registerMCPServers(context.Background(), registry, mgr, []string{""})
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
	root := t.TempDir()
	extra := filepath.Join(root, "workspace")
	settings := &config.Settings{Permissions: &config.PermissionsConfig{AdditionalDirectories: []string{extra}}}
	allowed := filepath.Join(root, "extra")
	if err := os.MkdirAll(extra, 0o755); err != nil {
		t.Fatalf("extra dir: %v", err)
	}
	if err := os.MkdirAll(allowed, 0o755); err != nil {
		t.Fatalf("allowed dir: %v", err)
	}
	opts := Options{ProjectRoot: root, Sandbox: SandboxOptions{AllowedPaths: []string{allowed}, ResourceLimit: sandbox.ResourceLimits{MaxCPUPercent: 10}}}
	mgr, sbRoot := buildSandboxManager(opts, settings)
	if sbRoot == "" {
		t.Fatal("expected non-empty root")
	}
	resolvedExtra, err := filepath.EvalSymlinks(extra)
	if err != nil {
		t.Fatalf("eval workspace symlink: %v", err)
	}
	if resolvedExtra == "" {
		resolvedExtra = extra
	}
	if err := mgr.CheckPath(filepath.Join(resolvedExtra, "file")); err != nil {
		t.Fatalf("expected workspace allowed: %v", err)
	}
	resolvedAllowed, err := filepath.EvalSymlinks(allowed)
	if err != nil {
		t.Fatalf("eval allowed symlink: %v", err)
	}
	if resolvedAllowed == "" {
		resolvedAllowed = allowed
	}
	if err := mgr.CheckPath(filepath.Join(resolvedAllowed, "child")); err != nil {
		t.Fatalf("expected allowed path honored: %v", err)
	}
	limits := mgr.Limits()
	if limits.MaxCPUPercent != 10 {
		t.Fatalf("unexpected limits: %+v", limits)
	}
}

func TestLoadSettingsAppliesOverrides(t *testing.T) {
	root := t.TempDir()
	settingsPath := filepath.Join(root, "settings.json")
	payload := `{"model":"claude-foo","permissions":{"additionalDirectories":["/tmp"]}}`
	if err := os.WriteFile(settingsPath, []byte(payload), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}
	override := &config.Settings{Model: "override-model"}
	opts := Options{ProjectRoot: root, SettingsPath: settingsPath, SettingsOverrides: override}
	settings, err := loadSettings(opts)
	if err != nil {
		t.Fatalf("load settings: %v", err)
	}
	if settings.Model != "override-model" {
		t.Fatalf("expected override to win, got %s", settings.Model)
	}
	if settings.Permissions == nil || len(settings.Permissions.AdditionalDirectories) == 0 {
		t.Fatalf("expected permissions from file, got %+v", settings.Permissions)
	}
}

func TestProjectConfigFromSettings(t *testing.T) {
	settings := &config.Settings{
		Env: map[string]string{"K": "V"},
		Permissions: &config.PermissionsConfig{
			AdditionalDirectories: []string{"/data"},
		},
	}
	cfg := projectConfigFromSettings(settings)
	if cfg == nil {
		t.Fatal("expected config snapshot")
	}
	if cfg.Env["K"] != "V" {
		t.Fatalf("env not propagated: %+v", cfg.Env)
	}
	if cfg.Permissions == nil || len(cfg.Permissions.AdditionalDirectories) != 1 || cfg.Permissions.AdditionalDirectories[0] != "/data" {
		t.Fatalf("permissions not propagated: %+v", cfg.Permissions)
	}
}

func TestRegisterToolsUsesDefaultImplementations(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir()}
	if taskTool, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
		_ = taskTool
	} else if taskTool == nil {
		t.Fatal("expected task tool to be registered")
	}
	tools := registry.List()
	expected := []string{"Bash", "Read", "Write", "Edit", "WebFetch", "WebSearch", "BashOutput", "TodoWrite", "Skill", "SlashCommand", "Grep", "Glob", "Task"}
	if len(tools) != len(expected) {
		t.Fatalf("expected %d default tools, got %d", len(expected), len(tools))
	}
	seen := map[string]struct{}{}
	for _, impl := range tools {
		if strings.TrimSpace(impl.Name()) == "" {
			t.Fatalf("tool missing name: %+v", impl)
		}
		seen[impl.Name()] = struct{}{}
	}
	for _, name := range expected {
		if _, ok := seen[name]; !ok {
			t.Fatalf("missing default tool %s", name)
		}
	}
}

func TestRegisterToolsRespectsEnabledWhitelist(t *testing.T) {
	registry := tool.NewRegistry()
	root := t.TempDir()
	opts := Options{ProjectRoot: root, EnabledBuiltinTools: []string{"bash", "grep"}}
	if taskTool, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	} else if taskTool != nil {
		t.Fatalf("task tool should not be auto-registered when not whitelisted")
	}
	tools := registry.List()
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	seen := map[string]struct{}{}
	for _, impl := range tools {
		seen[strings.ToLower(impl.Name())] = struct{}{}
	}
	for _, want := range []string{"bash", "grep"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing tool %s", want)
		}
	}
}

func TestRegisterToolsDisablesAllBuiltinsWhenEmptyWhitelist(t *testing.T) {
	registry := tool.NewRegistry()
	root := t.TempDir()
	opts := Options{ProjectRoot: root, EnabledBuiltinTools: []string{}}
	if _, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if got := len(registry.List()); got != 0 {
		t.Fatalf("expected no builtins, got %d", got)
	}
}

func TestRegisterToolsSkipsDuplicateNames(t *testing.T) {
	registry := tool.NewRegistry()
	root := t.TempDir()
	dup := &namedTool{name: "Bash"}
	opts := Options{ProjectRoot: root, CustomTools: []tool.Tool{dup}}
	if _, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	tools := registry.List()
	seen := map[string]int{}
	for _, impl := range tools {
		seen[strings.ToLower(impl.Name())]++
	}
	if seen["bash"] != 1 {
		t.Fatalf("expected bash registered once, got %d", seen["bash"])
	}
}

func TestRegisterToolsWhitelistCaseInsensitive(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir(), EnabledBuiltinTools: []string{"BASH", "GrEp", "FILE_READ"}}
	if _, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	seen := map[string]struct{}{}
	for _, impl := range registry.List() {
		seen[strings.ToLower(impl.Name())] = struct{}{}
	}
	for _, want := range []string{"bash", "grep", "read"} {
		if _, ok := seen[want]; !ok {
			t.Fatalf("missing tool %s", want)
		}
	}
	if len(seen) != 3 {
		t.Fatalf("expected 3 tools, got %d", len(seen))
	}
}

func TestRegisterToolsIgnoresUnknownWhitelistEntries(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir(), EnabledBuiltinTools: []string{"missing"}}
	if _, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	if got := len(registry.List()); got != 0 {
		t.Fatalf("expected no tools, got %d", got)
	}
}

func TestRegisterToolsAppendsCustomTools(t *testing.T) {
	registry := tool.NewRegistry()
	custom := &namedTool{name: "custom"}
	opts := Options{ProjectRoot: t.TempDir(), EnabledBuiltinTools: []string{}, CustomTools: []tool.Tool{nil, custom}}
	if _, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	}
	tools := registry.List()
	if len(tools) != 1 || tools[0].Name() != "custom" {
		t.Fatalf("expected only custom tool, got %+v", tools)
	}
}

func TestRegisterToolsLegacyToolsOverride(t *testing.T) {
	registry := tool.NewRegistry()
	legacy := &namedTool{name: "legacy"}
	opts := Options{
		ProjectRoot:         t.TempDir(),
		Tools:               []tool.Tool{legacy},
		EnabledBuiltinTools: []string{"bash"},
		CustomTools:         []tool.Tool{&namedTool{name: "custom"}},
	}
	if taskTool, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	} else if taskTool != nil {
		t.Fatalf("task tool should not be auto-wired when legacy Tools provided")
	}
	tools := registry.List()
	if len(tools) != 1 || tools[0].Name() != "legacy" {
		t.Fatalf("expected only legacy tool, got %+v", tools)
	}
}

func TestRegisterToolsTaskNotAddedForCI(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir(), EntryPoint: EntryPointCI}
	if taskTool, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
	} else if taskTool != nil {
		t.Fatalf("task tool should not be attached in CI entrypoint")
	}
	seen := map[string]struct{}{}
	for _, impl := range registry.List() {
		seen[impl.Name()] = struct{}{}
	}
	if _, ok := seen["Task"]; ok {
		t.Fatal("Task tool should be absent in CI mode")
	}
	if len(seen) != 12 { // all built-ins except Task
		t.Fatalf("expected 12 built-ins without Task, got %d", len(seen))
	}
}

func TestRegisterToolsSkipsNilEntries(t *testing.T) {
	registry := tool.NewRegistry()
	opts := Options{ProjectRoot: t.TempDir(), Tools: []tool.Tool{nil, &namedTool{name: "echo"}}}
	if taskTool, err := registerTools(registry, opts, nil, nil, nil); err != nil {
		t.Fatalf("register tools: %v", err)
		_ = taskTool
	} else if taskTool != nil {
		t.Fatalf("task tool should not be auto-wired when custom tools provided")
	}
	tools := registry.List()
	if len(tools) != 1 || tools[0].Name() != "echo" {
		t.Fatalf("expected only echo tool, got %+v", tools)
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
	exec := newHookExecutor(Options{}, rec, nil)
	adapter := &runtimeHookAdapter{executor: exec, recorder: rec}

	if _, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{Name: "t"}); err != nil {
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
	// Create a ShellHook that runs a simple command
	hook := corehooks.ShellHook{
		Event:   coreevents.PreToolUse,
		Command: "true", // Always succeeds
		Name:    "test-hook",
	}
	exec := newHookExecutor(Options{TypedHooks: []corehooks.ShellHook{hook}}, defaultHookRecorder(), nil)
	evt := coreevents.Event{Type: coreevents.PreToolUse, Payload: coreevents.ToolUsePayload{Name: "echo"}}
	if err := exec.Publish(evt); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

type fakeStringer struct {
	text string
}

func (f fakeStringer) String() string {
	return f.text
}
