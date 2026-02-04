package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/security"
)

func TestGrepToolExecuteContent(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, "a.txt")
	if err := os.WriteFile(path, []byte("hello\nworld\n"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
	tool.SetRespectGitignore(false)

	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern":     "hello",
		"output_mode": "content",
		"path":        root,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success || !strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestGrepSearchOptionsAllow(t *testing.T) {
	t.Parallel()

	opts := grepSearchOptions{
		glob:      "*.go",
		typeGlobs: []string{"*.go"},
		root:      "/root",
	}
	ok, err := opts.allow("/root/file.go")
	if err != nil || !ok {
		t.Fatalf("expected allow, got %v err=%v", ok, err)
	}
	ok, err = opts.allow("/root/file.txt")
	if err != nil || ok {
		t.Fatalf("expected deny, got %v err=%v", ok, err)
	}
}
