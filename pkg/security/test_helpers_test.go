package security

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func tempDirClean(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != "" {
		dir = resolved
	}
	if runtime.GOOS == "darwin" && strings.HasPrefix(dir, "/var/") {
		if resolved, err := filepath.EvalSymlinks("/var"); err == nil && resolved != "" {
			dir = filepath.Join(resolved, strings.TrimPrefix(dir, "/var/"))
		}
	}
	if abs, err := filepath.Abs(dir); err == nil {
		dir = abs
	}
	return filepath.Clean(dir)
}

func mustSymlink(t *testing.T, oldname, newname string) {
	t.Helper()
	if runtime.GOOS == "windows" {
		t.Skip("symlink support varies on windows")
	}
	if err := os.MkdirAll(filepath.Dir(newname), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(newname), err)
	}
	if err := os.Symlink(oldname, newname); err != nil {
		t.Fatalf("symlink %s -> %s: %v", newname, oldname, err)
	}
}
