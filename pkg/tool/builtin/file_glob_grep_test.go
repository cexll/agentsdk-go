package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestFileToolWriteReadDelete(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewFileToolWithRoot(dir)
	ctx := context.Background()

	if tool.Name() == "" || tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("metadata not populated")
	}

	_, err := tool.Execute(ctx, map[string]any{"operation": "write", "path": "note.txt", "content": "hello"})
	if err != nil {
		t.Fatalf("write failed: %v", err)
	}

	res, err := tool.Execute(ctx, map[string]any{"operation": "read", "path": "note.txt"})
	if err != nil {
		t.Fatalf("read failed: %v", err)
	}
	if !strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected output: %s", res.Output)
	}

	if _, err := tool.Execute(ctx, map[string]any{"operation": "delete", "path": "note.txt"}); err != nil {
		t.Fatalf("delete failed: %v", err)
	}

	tool.maxBytes = 1
	if _, err := tool.Execute(ctx, map[string]any{"operation": "write", "path": "note.txt", "content": "toolong"}); err == nil {
		t.Fatalf("expected size error")
	}
	if _, err := tool.Execute(ctx, map[string]any{"operation": "read", "path": "note.txt"}); err == nil {
		t.Fatalf("expected size error on read")
	}
	if _, err := tool.Execute(ctx, map[string]any{"operation": "read", "path": dir}); err == nil {
		t.Fatalf("expected directory read error")
	}

	if _, err := tool.Execute(ctx, map[string]any{"operation": "unknown", "path": "note.txt"}); err == nil {
		t.Fatalf("expected error for invalid operation")
	}
}

func TestFileToolDeleteErrors(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewFileToolWithRoot(dir)
	if _, err := tool.Execute(context.Background(), map[string]any{"operation": "delete", "path": "missing.txt"}); err == nil {
		t.Fatalf("expected missing file error")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"operation": "write", "path": ""}); err == nil {
		t.Fatalf("expected empty path error")
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"operation": "write", "path": "no-content"}); err == nil {
		t.Fatalf("expected missing content error")
	}

	sub := filepath.Join(dir, "subdir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := tool.Execute(context.Background(), map[string]any{"operation": "delete", "path": "subdir"}); err == nil {
		t.Fatalf("expected directory delete rejection")
	}
}

func TestGlobToolListsMatches(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("two"), 0o644)
	fileDirErr := NewGlobToolWithRoot(dir)
	if _, err := fileDirErr.Execute(context.Background(), map[string]any{"pattern": "*", "dir": "a.txt"}); err == nil {
		t.Fatalf("expected dir validation error")
	}
	tool := NewGlobToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "*.txt"})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}
	if !strings.Contains(res.Output, "a.txt") || strings.Contains(res.Output, "b.go") {
		t.Fatalf("unexpected glob output: %s", res.Output)
	}
}

func TestGrepToolSearchesFile(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "sample.txt")
	content := "first line\nfoo line\nbar"
	if err := os.WriteFile(target, []byte(content), 0o644); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tool := NewGrepToolWithRoot(dir)
	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": target, "context_lines": 1})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if !strings.Contains(res.Output, "foo line") {
		t.Fatalf("missing match output: %s", res.Output)
	}

	res2, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": target, "context_lines": 42})
	if err != nil {
		t.Fatalf("unexpected error for clamped context: %v", err)
	}
	data, _ := res2.Data.(map[string]any)
	if data == nil || data["count"].(int) != len(res2.Data.(map[string]any)["matches"].([]GrepMatch)) {
		t.Fatalf("invalid result payload: %#v", res2.Data)
	}
}

func TestGrepToolSearchDirectory(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	_ = os.WriteFile(filepath.Join(dir, "one.txt"), []byte("foo"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "two.txt"), []byte("foo again"), 0o644)
	sub := filepath.Join(dir, "sub")
	_ = os.Mkdir(sub, 0o755)
	_ = os.Symlink(filepath.Join(dir, "one.txt"), filepath.Join(sub, "link.txt"))
	tool := NewGrepToolWithRoot(dir)
	tool.maxResults = 1 // force truncation path

	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": dir})
	if err != nil {
		t.Fatalf("grep dir failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("grep dir not successful: %#v", res)
	}
}

func TestGlobAndGrepMetadata(t *testing.T) {
	if NewFileTool().Name() == "" {
		t.Fatalf("file tool name empty")
	}
	if g := NewGlobTool(); g.Schema() == nil || g.Name() == "" || g.Description() == "" {
		t.Fatalf("metadata missing")
	}
	if g := NewGrepTool(); g.Description() == "" || g.Name() == "" || g.Schema() == nil {
		t.Fatalf("grep metadata missing")
	}
	if _, err := NewGlobTool().Execute(context.Background(), map[string]any{"pattern": "*", "dir": "missing"}); err == nil {
		t.Fatalf("expected stat error for missing dir")
	}
}

func TestParseGlobPatternErrors(t *testing.T) {
	if _, err := parseGlobPattern(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := parseGlobPattern(map[string]any{"pattern": " "}); err == nil {
		t.Fatalf("expected empty pattern error")
	}
}

func TestGlobHelpers(t *testing.T) {
	output := formatGlobOutput([]string{"a", "b"}, true)
	if !strings.Contains(output, "truncated") {
		t.Fatalf("expected truncated note in %q", output)
	}
}

func TestNilContextExecutions(t *testing.T) {
	if _, err := NewGrepTool().Execute(nil, map[string]any{"pattern": "x", "path": "."}); err == nil {
		t.Fatalf("expected context error")
	}
	if _, err := NewGlobTool().Execute(nil, map[string]any{"pattern": "*"}); err == nil {
		t.Fatalf("expected context error")
	}
}
