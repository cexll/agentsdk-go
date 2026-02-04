package api

import (
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/middleware"
)

func TestOptionsWithDefaults(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("AGENTSDK_PROJECT_ROOT", dir)

	opts := Options{}
	got := opts.withDefaults()
	if got.EntryPoint == "" {
		t.Fatalf("expected entrypoint default")
	}
	wantRoot, err := filepath.EvalSymlinks(dir)
	if err != nil || wantRoot == "" {
		wantRoot = dir
	}
	if got.ProjectRoot != filepath.Clean(wantRoot) {
		t.Fatalf("unexpected project root %q", got.ProjectRoot)
	}
	if got.Sandbox.Root != got.ProjectRoot {
		t.Fatalf("unexpected sandbox root %q", got.Sandbox.Root)
	}
	if len(got.Sandbox.NetworkAllow) == 0 {
		t.Fatalf("expected network allow defaults")
	}
	if got.MaxSessions != defaultMaxSessions {
		t.Fatalf("unexpected max sessions %d", got.MaxSessions)
	}
}

func TestOptionsFrozenCopiesSlices(t *testing.T) {
	t.Parallel()

	opts := Options{
		Middleware:         []middleware.Middleware{nil},
		EnabledBuiltinTools: []string{"bash"},
		DisallowedTools:    []string{"read"},
		Sandbox: SandboxOptions{
			AllowedPaths: []string{"/tmp"},
		},
	}
	frozen := opts.frozen()
	opts.EnabledBuiltinTools[0] = "mutated"
	if frozen.EnabledBuiltinTools[0] != "bash" {
		t.Fatalf("expected frozen copy")
	}
}

func TestRequestNormalized(t *testing.T) {
	t.Parallel()

	req := Request{Prompt: "hi", ToolWhitelist: []string{"b", "a"}}
	defaultMode := ModeContext{EntryPoint: EntryPointCLI}
	out := req.normalized(defaultMode, "sess")
	if out.SessionID != "sess" {
		t.Fatalf("expected fallback session id")
	}
	if len(out.ToolWhitelist) != 2 || out.ToolWhitelist[0] != "a" {
		t.Fatalf("expected sorted tool whitelist, got %v", out.ToolWhitelist)
	}
	if out.Tags == nil || out.Metadata == nil {
		t.Fatalf("expected tags/metadata maps initialized")
	}
}
