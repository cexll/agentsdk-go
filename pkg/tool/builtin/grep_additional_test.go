package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

func TestGrepToolRejectsInvalidRegex(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGrepToolWithRoot(dir)
	if _, err := tool.Execute(context.Background(), map[string]any{"pattern": "[", "path": dir}); err == nil || !strings.Contains(err.Error(), "compile pattern") {
		t.Fatalf("expected pattern compile error, got %v", err)
	}
}

func TestGrepToolContextLinesValidation(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGrepToolWithRoot(dir)
	params := map[string]any{"pattern": "foo", "path": dir, "context_lines": -1}
	if _, err := tool.Execute(context.Background(), params); err == nil || !strings.Contains(err.Error(), "context_lines") {
		t.Fatalf("expected context_lines error, got %v", err)
	}
}

func TestGrepToolRejectsTraversalInPath(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGrepToolWithRoot(dir)
	if _, err := tool.Execute(context.Background(), map[string]any{"pattern": "foo", "path": "../.."}); err == nil || !strings.Contains(err.Error(), "path not in sandbox") {
		t.Fatalf("expected sandbox error, got %v", err)
	}
}

func TestRelativeDepthOutsideReturnsZero(t *testing.T) {
	if depth := relativeDepth("/tmp/base", "/etc/passwd"); depth != 0 {
		t.Fatalf("expected depth 0 for outside path, got %d", depth)
	}
}

func TestIntFromParamAdditionalTypes(t *testing.T) {
	tests := []struct {
		value   any
		want    int
		wantErr bool
	}{
		{int16(3), 3, false},
		{uint8(7), 7, false},
		{uint(5), 5, false},
		{float32(2), 2, false},
		{float32(2.5), 0, true},
	}
	for _, tt := range tests {
		got, err := intFromParam(tt.value)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("expected error for %v", tt.value)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("intFromParam(%v)=%d err=%v want=%d", tt.value, got, err, tt.want)
		}
	}
}

func TestSearchFileTruncatesAtLimit(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "matches.txt")
	if err := os.WriteFile(target, []byte("hit\nhit\n"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewGrepToolWithRoot(dir)
	tool.maxResults = 1
	re := regexp.MustCompile("hit")
	var matches []GrepMatch
	truncated, err := tool.searchFile(context.Background(), target, re, grepSearchOptions{}, &matches)
	if err != nil {
		t.Fatalf("searchFile failed: %v", err)
	}
	if !truncated {
		t.Fatalf("expected truncation when exceeding maxResults")
	}
	if len(matches) != 1 {
		t.Fatalf("expected single match recorded, got %d", len(matches))
	}
}

func TestGrepSearchDirectoryRespectsCancellation(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("hit"), 0600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewGrepToolWithRoot(dir)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var matches []GrepMatch
	if _, err := tool.searchDirectory(ctx, dir, regexp.MustCompile("hit"), grepSearchOptions{}, &matches); err == nil || !strings.Contains(err.Error(), "context canceled") {
		t.Fatalf("expected cancellation error, got %v", err)
	}
}

func TestGrepSearchDirectoryHonorsDepthLimit(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	nested := filepath.Join(dir, "nested", "deeper")
	if err := os.MkdirAll(nested, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "root.txt"), []byte("hit"), 0600); err != nil {
		t.Fatalf("write root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("hit"), 0600); err != nil {
		t.Fatalf("write deep: %v", err)
	}
	tool := NewGrepToolWithRoot(dir)
	tool.maxDepth = 0
	var matches []GrepMatch
	truncated, err := tool.searchDirectory(context.Background(), dir, regexp.MustCompile("hit"), grepSearchOptions{}, &matches)
	if err != nil {
		t.Fatalf("searchDirectory failed: %v", err)
	}
	if truncated {
		t.Fatalf("did not expect truncation for depth test")
	}
	if len(matches) != 1 || matches[0].File != "root.txt" {
		t.Fatalf("expected only root match, got %#v", matches)
	}
}

func TestGrepSearchDirectorySkipsSymlinkDirs(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	realDir := filepath.Join(dir, "real")
	if err := os.Mkdir(realDir, 0o755); err != nil {
		t.Fatalf("mkdir real: %v", err)
	}
	if err := os.WriteFile(filepath.Join(realDir, "file.txt"), []byte("hit"), 0600); err != nil {
		t.Fatalf("write real: %v", err)
	}
	linkDir := filepath.Join(dir, "alias")
	if err := os.Symlink(realDir, linkDir); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}
	tool := NewGrepToolWithRoot(dir)
	var matches []GrepMatch
	truncated, err := tool.searchDirectory(context.Background(), dir, regexp.MustCompile("hit"), grepSearchOptions{}, &matches)
	if err != nil {
		t.Fatalf("searchDirectory failed: %v", err)
	}
	if truncated {
		t.Fatalf("did not expect truncation")
	}
	if len(matches) != 1 {
		t.Fatalf("expected single match despite symlink, got %d", len(matches))
	}
}

func TestIntFromUint64Bounds(t *testing.T) {
	if _, err := intFromUint64(uint64(maxIntValue) + 1); err == nil {
		t.Fatalf("expected overflow error for uint64")
	}
}
