package api

import (
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestAdditionalSandboxPathsHandlesNilAndDedup(t *testing.T) {
	if extra := additionalSandboxPaths(nil); extra != nil {
		t.Fatalf("expected nil extras for nil settings, got %+v", extra)
	}

	settings := &config.Settings{Permissions: &config.PermissionsConfig{AdditionalDirectories: []string{" /tmp ", "/tmp"}}}
	extras := additionalSandboxPaths(settings)
	want, err := filepath.Abs("/tmp")
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if len(extras) != 1 || filepath.Clean(extras[0]) != filepath.Clean(want) {
		t.Fatalf("expected deduped absolute path, got %+v", extras)
	}
}

func TestBuildSandboxManagerAppliesDefaultNetworkAllow(t *testing.T) {
	root := t.TempDir()
	opts := Options{ProjectRoot: root}
	mgr, sbRoot := buildSandboxManager(opts, nil)
	if want, err := filepath.EvalSymlinks(root); err != nil {
		t.Fatalf("eval symlink: %v", err)
	} else if want != "" && sbRoot != want {
		t.Fatalf("unexpected sandbox root %s, want %s", sbRoot, want)
	}
	if err := mgr.CheckNetwork("localhost"); err != nil {
		t.Fatalf("expected localhost allowed by default: %v", err)
	}
	if err := mgr.CheckNetwork("example.com"); err == nil {
		t.Fatal("expected example.com to be denied by default allow list")
	}
}

func TestAdditionalSandboxPathsSkipsInvalidEntries(t *testing.T) {
	settings := &config.Settings{Permissions: &config.PermissionsConfig{AdditionalDirectories: []string{"", "../relative"}}}
	extras := additionalSandboxPaths(settings)
	if len(extras) != 1 || !filepath.IsAbs(extras[0]) {
		t.Fatalf("expected relative path to be resolved absolute, got %+v", extras)
	}
}

func TestBuildSandboxManagerRespectsDisabledSetting(t *testing.T) {
	root := t.TempDir()
	disabled := false
	settings := &config.Settings{
		Sandbox: &config.SandboxConfig{
			Enabled: &disabled,
		},
	}
	opts := Options{ProjectRoot: root}
	mgr, sbRoot := buildSandboxManager(opts, settings)

	if sbRoot == "" {
		t.Fatal("expected non-empty sandbox root")
	}

	// When sandbox is disabled, Manager should pass through (nil policies)
	// CheckPath/CheckNetwork should return nil for any input
	if err := mgr.CheckPath("/nonexistent/path/outside/sandbox"); err != nil {
		t.Fatalf("disabled sandbox should allow any path, got error: %v", err)
	}
	if err := mgr.CheckNetwork("any-domain.example"); err != nil {
		t.Fatalf("disabled sandbox should allow any network, got error: %v", err)
	}
}

func TestBuildSandboxManagerEnabledByDefault(t *testing.T) {
	root := t.TempDir()
	// No Sandbox config, should default to enabled
	opts := Options{ProjectRoot: root}
	mgr, _ := buildSandboxManager(opts, nil)

	// Should enforce path restrictions
	if err := mgr.CheckPath("/nonexistent/path/outside/sandbox"); err == nil {
		t.Fatal("expected path check to fail when sandbox enabled by default")
	}
}

func TestNoopFileSystemPolicyRootsAndAllow(t *testing.T) {
	p := &noopFileSystemPolicy{root: t.TempDir()}
	p.Allow("ignored")
	roots := p.Roots()
	if len(roots) != 1 || roots[0] == "" {
		t.Fatalf("unexpected roots %+v", roots)
	}

	var nilPolicy *noopFileSystemPolicy
	if roots := nilPolicy.Roots(); roots != nil {
		t.Fatalf("expected nil roots for nil policy, got %+v", roots)
	}
	if roots := (&noopFileSystemPolicy{root: "   "}).Roots(); roots != nil {
		t.Fatalf("expected nil roots for blank root, got %+v", roots)
	}
}
