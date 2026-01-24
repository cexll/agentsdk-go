package toolbuiltin

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGlobToolListsMatches(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	_ = os.WriteFile(filepath.Join(dir, "a.txt"), []byte("one"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "b.go"), []byte("two"), 0600)
	fileDirErr := NewGlobToolWithRoot(dir)
	if _, err := fileDirErr.Execute(context.Background(), map[string]any{"pattern": "*", "path": "a.txt"}); err == nil {
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

func TestGlobToolTruncatesResults(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	for i := 0; i < 2; i++ {
		name := fmt.Sprintf("f%d.txt", i)
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0600); err != nil {
			t.Fatalf("write fixture: %v", err)
		}
	}
	tool := NewGlobToolWithRoot(dir)
	tool.maxResults = 1
	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "*.txt"})
	if err != nil {
		t.Fatalf("glob execute failed: %v", err)
	}
	data, _ := res.Data.(map[string]any)
	if data == nil || data["truncated"] != true {
		t.Fatalf("expected truncated flag, got %#v", res.Data)
	}
	if !strings.Contains(res.Output, "truncated") {
		t.Fatalf("expected truncated note in output: %s", res.Output)
	}
}

func TestGlobToolContextCancellation(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGlobToolWithRoot(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, map[string]any{"pattern": "*"}); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestGlobToolRejectsEscapePatterns(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGlobToolWithRoot(dir)
	if _, err := tool.Execute(context.Background(), map[string]any{"pattern": "../*.txt"}); err == nil || !strings.Contains(err.Error(), "path not in sandbox") {
		t.Fatalf("expected sandbox error, got %v", err)
	}
}

func TestGrepToolSearchesFile(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "sample.txt")
	content := "first line\nfoo line\nbar"
	if err := os.WriteFile(target, []byte(content), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}

	tool := NewGrepToolWithRoot(dir)
	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": target, "context_lines": 1, "output_mode": "content"})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}
	if !strings.Contains(res.Output, "foo line") {
		t.Fatalf("missing match output: %s", res.Output)
	}

	res2, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": target, "context_lines": 42, "output_mode": "content"})
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
	_ = os.WriteFile(filepath.Join(dir, "one.txt"), []byte("foo"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "two.txt"), []byte("foo again"), 0600)
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
	if r := NewReadTool(); r.Description() == "" || r.Name() == "" || r.Schema() == nil {
		t.Fatalf("read tool metadata missing")
	}
	if g := NewGlobTool(); g.Schema() == nil || g.Name() == "" || g.Description() == "" {
		t.Fatalf("glob metadata missing")
	}
	if g := NewGrepTool(); g.Description() == "" || g.Name() == "" || g.Schema() == nil {
		t.Fatalf("grep metadata missing")
	}
	if _, err := NewGlobTool().Execute(context.Background(), map[string]any{"pattern": "*", "path": "missing"}); err == nil {
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

// TestGlobToolRespectsGitignore verifies that GlobTool filters out .gitignore patterns.
func TestGlobToolRespectsGitignore(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)

	// Create .gitignore
	gitignoreContent := "*.log\nnode_modules/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("debug output"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "app.txt"), []byte("app content"), 0600)

	// Create node_modules directory with files
	nodeModules := filepath.Join(dir, "node_modules")
	_ = os.Mkdir(nodeModules, 0755)
	_ = os.WriteFile(filepath.Join(nodeModules, "package.json"), []byte("{}"), 0600)

	tool := NewGlobToolWithRoot(dir)
	// respectGitignore is true by default

	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "*"})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	// Should include main.go and app.txt
	if !strings.Contains(res.Output, "main.go") {
		t.Errorf("Expected main.go in output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "app.txt") {
		t.Errorf("Expected app.txt in output: %s", res.Output)
	}

	// Should NOT include debug.log (matches *.log)
	if strings.Contains(res.Output, "debug.log") {
		t.Errorf("Expected debug.log to be filtered out: %s", res.Output)
	}

	// Should NOT include node_modules (directory ignored)
	if strings.Contains(res.Output, "node_modules") {
		t.Errorf("Expected node_modules to be filtered out: %s", res.Output)
	}
}

// TestGlobToolDisableGitignore verifies that SetRespectGitignore(false) disables filtering.
func TestGlobToolDisableGitignore(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)

	// Create .gitignore
	gitignoreContent := "*.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("debug output"), 0600)

	tool := NewGlobToolWithRoot(dir)
	tool.SetRespectGitignore(false)

	res, err := tool.Execute(context.Background(), map[string]any{"pattern": "*"})
	if err != nil {
		t.Fatalf("glob failed: %v", err)
	}

	// Both files should be included when gitignore is disabled
	if !strings.Contains(res.Output, "main.go") {
		t.Errorf("Expected main.go in output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "debug.log") {
		t.Errorf("Expected debug.log in output when gitignore disabled: %s", res.Output)
	}
}

// TestGrepToolRespectsGitignore verifies that GrepTool filters out .gitignore patterns.
func TestGrepToolRespectsGitignore(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)

	// Create .gitignore
	gitignoreContent := "*.log\nnode_modules/\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files with searchable content
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("function test()"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("function debug()"), 0600)

	// Create node_modules with searchable content
	nodeModules := filepath.Join(dir, "node_modules")
	_ = os.Mkdir(nodeModules, 0755)
	_ = os.WriteFile(filepath.Join(nodeModules, "lib.js"), []byte("function library()"), 0600)

	tool := NewGrepToolWithRoot(dir)
	// respectGitignore is true by default

	res, err := tool.Execute(context.Background(), map[string]any{
		"pattern":     "function",
		"path":        dir,
		"output_mode": "files_with_matches",
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}

	// Should find main.go
	if !strings.Contains(res.Output, "main.go") {
		t.Errorf("Expected main.go in grep output: %s", res.Output)
	}

	// Should NOT find debug.log (matches *.log)
	if strings.Contains(res.Output, "debug.log") {
		t.Errorf("Expected debug.log to be filtered out: %s", res.Output)
	}

	// Should NOT find node_modules content
	if strings.Contains(res.Output, "node_modules") || strings.Contains(res.Output, "lib.js") {
		t.Errorf("Expected node_modules to be filtered out: %s", res.Output)
	}
}

// TestGrepToolDisableGitignore verifies that SetRespectGitignore(false) disables filtering.
func TestGrepToolDisableGitignore(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)

	// Create .gitignore
	gitignoreContent := "*.log\n"
	if err := os.WriteFile(filepath.Join(dir, ".gitignore"), []byte(gitignoreContent), 0644); err != nil {
		t.Fatal(err)
	}

	// Create files
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("function test()"), 0600)
	_ = os.WriteFile(filepath.Join(dir, "debug.log"), []byte("function debug()"), 0600)

	tool := NewGrepToolWithRoot(dir)
	tool.SetRespectGitignore(false)

	res, err := tool.Execute(context.Background(), map[string]any{
		"pattern":     "function",
		"path":        dir,
		"output_mode": "files_with_matches",
	})
	if err != nil {
		t.Fatalf("grep failed: %v", err)
	}

	// Both files should be found when gitignore is disabled
	if !strings.Contains(res.Output, "main.go") {
		t.Errorf("Expected main.go in output: %s", res.Output)
	}
	if !strings.Contains(res.Output, "debug.log") {
		t.Errorf("Expected debug.log in output when gitignore disabled: %s", res.Output)
	}
}
