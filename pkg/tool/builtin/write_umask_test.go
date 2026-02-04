//go:build unix

package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"syscall"
	"testing"
)

func TestWriteToolRespectsUmask(t *testing.T) {
	dir := cleanTempDir(t)
	tool := NewWriteToolWithRoot(dir)

	oldUmask := syscall.Umask(0o002)
	defer syscall.Umask(oldUmask)

	target := filepath.Join("nested", "perm.txt")
	if _, err := tool.Execute(context.Background(), map[string]any{
		"file_path": target,
		"content":   "x",
	}); err != nil {
		t.Fatalf("write execute failed: %v", err)
	}

	info, err := os.Stat(filepath.Join(dir, target))
	if err != nil {
		t.Fatalf("stat written file: %v", err)
	}
	if got, want := info.Mode().Perm(), os.FileMode(0o664); got != want {
		t.Fatalf("unexpected file permissions: got %o want %o", got, want)
	}
}
