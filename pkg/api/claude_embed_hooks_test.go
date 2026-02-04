package api

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
)

func TestMaterializeEmbeddedClaudeHooks(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	fs := fstest.MapFS{
		".claude/hooks/pre.sh":      {Data: []byte("echo pre")},
		".claude/hooks/sub/post.sh": {Data: []byte("echo post")},
	}
	if err := materializeEmbeddedClaudeHooks(root, fs); err != nil {
		t.Fatalf("materialize failed: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(root, ".claude/hooks/pre.sh"))
	if err != nil || !strings.Contains(string(data), "pre") {
		t.Fatalf("unexpected hook content %q err=%v", data, err)
	}

	// existing file should not be overwritten
	dest := filepath.Join(root, ".claude/hooks/pre.sh")
	if err := os.WriteFile(dest, []byte("local"), 0o600); err != nil {
		t.Fatalf("write dest: %v", err)
	}
	if err := materializeEmbeddedClaudeHooks(root, fs); err != nil {
		t.Fatalf("materialize second failed: %v", err)
	}
	data, _ = os.ReadFile(dest)
	if string(data) != "local" {
		t.Fatalf("expected local file preserved")
	}
}

func TestMaterializeEmbeddedClaudeHooksErrors(t *testing.T) {
	t.Parallel()

	if err := materializeEmbeddedClaudeHooks(" ", nil); err != nil {
		t.Fatalf("expected nil embedfs ok, got %v", err)
	}
	if err := materializeEmbeddedClaudeHooks(t.TempDir(), fstest.MapFS{}); err != nil {
		t.Fatalf("expected missing dir to be ignored, got %v", err)
	}
	fs := fstest.MapFS{
		".claude/hooks": {Data: []byte("not dir")},
	}
	if err := materializeEmbeddedClaudeHooks(t.TempDir(), fs); err == nil {
		t.Fatalf("expected not-a-dir error")
	}

	errFS := statErrFS{err: errors.New("stat failed")}
	if err := materializeEmbeddedClaudeHooks(t.TempDir(), errFS); err == nil || !strings.Contains(err.Error(), "stat embedded") {
		t.Fatalf("expected stat error, got %v", err)
	}

	readFS := readErrFS{FS: fstest.MapFS{".claude/hooks/pre.sh": {Data: []byte("hi")}}, err: errors.New("read failed")}
	if err := materializeEmbeddedClaudeHooks(t.TempDir(), readFS); err == nil || !strings.Contains(err.Error(), "read embedded") {
		t.Fatalf("expected read error, got %v", err)
	}
}

type statErrFS struct{ err error }

func (s statErrFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }
func (s statErrFS) Stat(string) (fs.FileInfo, error) {
	return nil, s.err
}

type readErrFS struct {
	fs.FS
	err error
}

func (r readErrFS) ReadFile(string) ([]byte, error) {
	return nil, r.err
}
