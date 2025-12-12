package api

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestWithMaxSessionsRespectsPositiveOnly(t *testing.T) {
	opts := Options{MaxSessions: 5}
	WithMaxSessions(42)(&opts)
	if opts.MaxSessions != 42 {
		t.Fatalf("expected max sessions updated, got %d", opts.MaxSessions)
	}
	WithMaxSessions(0)(&opts)
	if opts.MaxSessions != 42 {
		t.Fatalf("non-positive override should be ignored, got %d", opts.MaxSessions)
	}
}

func TestOptionsWithDefaultsPopulatesMissingFields(t *testing.T) {
	root := t.TempDir()
	t.Setenv("AGENTSDK_PROJECT_ROOT", root)

	raw := Options{ProjectRoot: "", SettingsPath: "  settings.json  "}
	applied := raw.withDefaults()
	if applied.EntryPoint != defaultEntrypoint || applied.Mode.EntryPoint != defaultEntrypoint {
		t.Fatalf("entrypoint defaults not applied: %+v", applied)
	}
	wantRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if wantRoot == "" {
		wantRoot = root
	}
	if applied.ProjectRoot != wantRoot {
		t.Fatalf("project root not resolved: %s (want %s)", applied.ProjectRoot, wantRoot)
	}
	if applied.Sandbox.Root != applied.ProjectRoot {
		t.Fatalf("sandbox root should mirror project root, got %s", applied.Sandbox.Root)
	}
	if applied.MaxSessions != defaultMaxSessions {
		t.Fatalf("expected default max sessions, got %d", applied.MaxSessions)
	}
	if len(applied.Sandbox.NetworkAllow) == 0 {
		t.Fatalf("network allow list not defaulted")
	}
	if !filepath.IsAbs(applied.SettingsPath) {
		t.Fatalf("settings path not absolutised: %s", applied.SettingsPath)
	}
}

func TestRuntimeHookAdapterRecordsAndIgnoresNilRecorder(t *testing.T) {
	adapter := &runtimeHookAdapter{executor: &corehooks.Executor{}}
	if _, err := adapter.PreToolUse(context.Background(), coreevents.ToolUsePayload{Name: "ping"}); err != nil {
		t.Fatalf("pre tool use: %v", err)
	}

	recorder := &hookRecorder{}
	adapter.recorder = recorder
	if err := adapter.Stop(context.Background(), "done"); err != nil {
		t.Fatalf("stop: %v", err)
	}
	drained := recorder.Drain()
	if len(drained) == 0 {
		t.Fatal("expected recorded events when recorder is present")
	}
}

func TestRuntimeHookAdapterStopNilExecutor(t *testing.T) {
	var adapter *runtimeHookAdapter
	if err := adapter.Stop(context.Background(), "any"); err != nil {
		t.Fatalf("nil adapter should no-op, got %v", err)
	}
}

func TestOptionsToolFieldsDefaultsAndPriority(t *testing.T) {
	root := t.TempDir()
	legacy := &stubTool{name: "legacy"}
	custom := &stubTool{name: "custom"}
	enabled := []string{"bash", "grep"}

	opts := Options{
		ProjectRoot:         root,
		Tools:               []tool.Tool{legacy},
		EnabledBuiltinTools: enabled,
		CustomTools:         []tool.Tool{custom},
	}
	applied := opts.withDefaults()
	if len(applied.Tools) != 1 || applied.Tools[0] != legacy {
		t.Fatalf("tools slice should be preserved for legacy override")
	}
	if !reflect.DeepEqual(applied.EnabledBuiltinTools, enabled) {
		t.Fatalf("enabled builtins should remain untouched; got %+v", applied.EnabledBuiltinTools)
	}
	if len(applied.CustomTools) != 1 || applied.CustomTools[0] != custom {
		t.Fatalf("custom tools should be preserved; got %+v", applied.CustomTools)
	}

	empty := Options{ProjectRoot: root, EnabledBuiltinTools: []string{}, CustomTools: nil}
	emptyApplied := empty.withDefaults()
	if emptyApplied.EnabledBuiltinTools == nil {
		t.Fatalf("empty slice should not be defaulted to nil")
	}
	if emptyApplied.CustomTools != nil {
		t.Fatalf("custom tools should remain nil when unset")
	}
}

func TestOptionsCompactAndTokenOptions(t *testing.T) {
	var opts Options
	cfg := CompactConfig{Enabled: true, Threshold: 0.9, PreserveCount: 3, SummaryModel: "haiku"}
	WithAutoCompact(cfg)(&opts)
	if opts.AutoCompact != cfg {
		t.Fatalf("expected AutoCompact set, got %+v", opts.AutoCompact)
	}
	WithTokenTracking(true)(&opts)
	if !opts.TokenTracking {
		t.Fatalf("expected TokenTracking enabled")
	}
	called := false
	cb := func(TokenStats) { called = true }
	WithTokenCallback(cb)(&opts)
	if opts.TokenCallback == nil || !opts.TokenTracking {
		t.Fatalf("expected TokenCallback set and tracking enabled")
	}
	opts.TokenCallback(TokenStats{})
	if !called {
		t.Fatalf("expected callback invoked")
	}
}

type stubTool struct{ name string }

func (t *stubTool) Name() string             { return t.name }
func (t *stubTool) Description() string      { return "stub" }
func (t *stubTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{Type: "object"} }
func (t *stubTool) Execute(context.Context, map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Output: t.name}, nil
}
