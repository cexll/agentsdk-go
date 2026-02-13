package api

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestResolveProjectRootFromEnv(t *testing.T) {
	root := t.TempDir()
	linkDir := t.TempDir()
	link := filepath.Join(linkDir, "link")
	if err := os.Symlink(root, link); err != nil {
		if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "privilege")) {
			t.Skipf("symlink requires extra privilege on windows: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}
	t.Setenv("AGENTSDK_PROJECT_ROOT", link)

	resolved, err := ResolveProjectRoot()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if want == "" {
		want = root
	}
	if resolved != want {
		t.Fatalf("expected symlink to resolve to %s, got %s", want, resolved)
	}
}

func TestResolveProjectRootFromEnvMissingPath(t *testing.T) {
	root := filepath.Join(t.TempDir(), "missing")
	t.Setenv("AGENTSDK_PROJECT_ROOT", root)
	resolved, err := ResolveProjectRoot()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.Abs(root)
	if err != nil {
		t.Fatalf("abs: %v", err)
	}
	if resolved != want {
		t.Fatalf("expected abs path %s, got %s", want, resolved)
	}
}

func TestResolveProjectRootWalksUpForGoMod(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/demo\n"), 0o600); err != nil {
		t.Fatalf("go.mod: %v", err)
	}
	sub := filepath.Join(root, "a", "b")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir sub: %v", err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(sub); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	t.Setenv("AGENTSDK_PROJECT_ROOT", "")

	resolved, err := ResolveProjectRoot()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if want == "" {
		want = root
	}
	if resolved != want {
		t.Fatalf("expected root %s, got %s", want, resolved)
	}
}

func TestResolveProjectRootFallsBackToCWD(t *testing.T) {
	dir := t.TempDir()
	origWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("get wd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chdir(origWD); err != nil {
			t.Fatalf("restore wd: %v", err)
		}
	})
	t.Setenv("AGENTSDK_PROJECT_ROOT", "")

	resolved, err := ResolveProjectRoot()
	if err != nil {
		t.Fatalf("resolve: %v", err)
	}
	want, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatalf("eval symlink: %v", err)
	}
	if want == "" {
		want = dir
	}
	if resolved != want {
		t.Fatalf("expected cwd fallback %s, got %s", want, resolved)
	}
}
