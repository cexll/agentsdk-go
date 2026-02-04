package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToolExecute(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if err := os.WriteFile(filepath.Join(root, "a.txt"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	tool := NewGlobToolWithRoot(root)
	tool.SetRespectGitignore(false)
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"pattern": "*.txt",
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success || !strings.Contains(res.Output, "a.txt") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestParseGlobPatternErrorsBuiltin(t *testing.T) {
	t.Parallel()

	if _, err := parseGlobPattern(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := parseGlobPattern(map[string]interface{}{}); err == nil {
		t.Fatalf("expected missing pattern error")
	}
	if _, err := parseGlobPattern(map[string]interface{}{"pattern": ""}); err == nil {
		t.Fatalf("expected empty pattern error")
	}
}
