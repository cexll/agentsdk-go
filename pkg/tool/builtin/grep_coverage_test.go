package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/security"
)

func TestNewGrepToolWithSandbox(t *testing.T) {
	loc := cleanTempDir(t)
	custom := security.NewDisabledSandbox()
	tool := NewGrepToolWithSandbox(loc, custom)

	if tool.sandbox != custom {
		t.Fatalf("expected custom sandbox to be used")
	}
	if want := resolveRoot(loc); tool.root != want {
		t.Fatalf("root mismatch: got %q want %q", tool.root, want)
	}
	if tool.maxResults != grepResultLimit || tool.maxDepth != grepMaxDepth || tool.maxContext != grepMaxContext {
		t.Fatalf("unexpected limits: %#v", tool)
	}
}

func TestResolveTypeGlobsMappings(t *testing.T) {
	cases := map[string][]string{
		"ts":   {"*.ts"},
		"tsx":  {"*.tsx"},
		"jsx":  {"*.jsx"},
		"rust": {"*.rs"},
		"java": {"*.java"},
		"c":    {"*.c"},
		"cpp":  {"*.cpp", "*.cc", "*.cxx", "*.c++"},
		"h":    {"*.h"},
		"hpp":  {"*.hpp", "*.hh", "*.hxx"},
		"rb":   {"*.rb"},
		"php":  {"*.php"},
		"sh":   {"*.sh"},
		"yaml": {"*.yaml"},
		"yml":  {"*.yml"},
		"json": {"*.json"},
		"xml":  {"*.xml"},
		"html": {"*.html"},
		"css":  {"*.css"},
		"md":   {"*.md"},
		"txt":  {"*.txt"},
	}
	for fileType, want := range cases {
		got := resolveTypeGlobs(fileType)
		if !reflect.DeepEqual(got, want) {
			t.Fatalf("resolveTypeGlobs(%q)=%v want %v", fileType, got, want)
		}
	}

	if globs := resolveTypeGlobs(""); globs != nil {
		t.Fatalf("expected nil for empty file type, got %v", globs)
	}
	if globs := resolveTypeGlobs("weird"); len(globs) != 1 || globs[0] != "*.weird" {
		t.Fatalf("unexpected fallback for unknown type: %v", globs)
	}
}

func TestIntFromInt64Boundaries(t *testing.T) {
	if v, err := intFromInt64(maxIntValue); err != nil || int64(v) != maxIntValue {
		t.Fatalf("max boundary failed: %v %d", err, v)
	}
	if v, err := intFromInt64(minIntValue); err != nil || int64(v) != minIntValue {
		t.Fatalf("min boundary failed: %v %d", err, v)
	}
}

func TestSearchFileReadFailures(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewGrepToolWithRoot(dir)
	missing := filepath.Join(dir, "missing.txt")

	var matches []GrepMatch
	if _, err := tool.searchFile(context.Background(), missing, regexp.MustCompile("hit"), grepSearchOptions{}, &matches); err == nil || !strings.Contains(err.Error(), "read file") {
		t.Fatalf("expected read file error, got %v", err)
	}
}

func TestSearchFileEmptyFile(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	target := filepath.Join(dir, "empty.txt")
	if err := os.WriteFile(target, nil, 0o600); err != nil {
		t.Fatalf("write empty file: %v", err)
	}
	tool := NewGrepToolWithRoot(dir)

	var matches []GrepMatch
	truncated, err := tool.searchFile(context.Background(), target, regexp.MustCompile("nomatch"), grepSearchOptions{}, &matches)
	if err != nil {
		t.Fatalf("searchFile returned error: %v", err)
	}
	if truncated {
		t.Fatalf("did not expect truncation for empty file")
	}
	if len(matches) != 0 {
		t.Fatalf("expected no matches for empty file, got %d", len(matches))
	}
}

func TestParseGrepPatternWhitespace(t *testing.T) {
	pattern, err := parseGrepPattern(map[string]any{"pattern": " \tfoo\n"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pattern != "foo" {
		t.Fatalf("expected trimmed pattern, got %q", pattern)
	}
	if _, err := parseGrepPattern(map[string]any{"pattern": "\n\t "}); err == nil {
		t.Fatalf("expected error for whitespace-only pattern")
	}
}

func TestParseContextLinesBoundaries(t *testing.T) {
	tests := []struct {
		name    string
		value   any
		want    int
		wantErr bool
	}{
		{"nil", nil, 0, false},
		{"within", 3, 3, false},
		{"clamped", 10, 5, false},
		{"negative", -1, 0, true},
		{"invalid", "nope", 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			params := map[string]any{}
			if tt.value != nil {
				params["context_lines"] = tt.value
			}
			got, err := parseContextLines(params, 5)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error")
				}
				return
			}
			if err != nil || got != tt.want {
				t.Fatalf("parseContextLines=%d err=%v want=%d", got, err, tt.want)
			}
		})
	}
}

func TestSplitGrepLinesEdges(t *testing.T) {
	if lines := splitGrepLines(""); lines != nil {
		t.Fatalf("expected nil slice for empty content, got %v", lines)
	}
	lines := splitGrepLines("\n")
	if len(lines) != 2 || lines[0] != "" || lines[1] != "" {
		t.Fatalf("unexpected lines for lone newline: %#v", lines)
	}
}

func TestApplyWindowBoundaries(t *testing.T) {
	if window, truncated := applyWindow([]int{}, 0, 1); window != nil || truncated {
		t.Fatalf("empty input should return nil,false got %v,%v", window, truncated)
	}
	if window, truncated := applyWindow([]int{1, 2}, 5, 0); window != nil || !truncated {
		t.Fatalf("offset beyond length should truncate, got %v,%v", window, truncated)
	}
	if window, truncated := applyWindow([]int{1, 2, 3}, 1, 1); !reflect.DeepEqual(window, []int{2}) || !truncated {
		t.Fatalf("expected window [2] truncated=true, got %v,%v", window, truncated)
	}
	if window, truncated := applyWindow([]int{1, 2}, 0, 0); !reflect.DeepEqual(window, []int{1, 2}) || truncated {
		t.Fatalf("head=0 should return all without truncation, got %v,%v", window, truncated)
	}
}

func TestUniqueFilesAndCountsEmpty(t *testing.T) {
	if files := uniqueFiles(nil); len(files) != 0 {
		t.Fatalf("expected empty files slice, got %v", files)
	}
	if counts := collectFileCounts(nil); counts != nil {
		t.Fatalf("expected nil counts for empty matches, got %v", counts)
	}
	if counts := countsToMap(nil); counts != nil {
		t.Fatalf("expected nil map for empty entries")
	}
}

func TestFormatCountOutputBoundaries(t *testing.T) {
	if got := formatCountOutput(nil); got != "no matches" {
		t.Fatalf("expected no matches string, got %q", got)
	}
	entries := []fileCount{{File: "a.go", Count: 2}, {File: "b.go", Count: 1}}
	if got := formatCountOutput(entries); got != "a.go: 2\nb.go: 1" {
		t.Fatalf("unexpected formatted output: %q", got)
	}
}

func TestAppendTruncatedNoteBranches(t *testing.T) {
	if got := appendTruncatedNote("content", false, 3); got != "content" {
		t.Fatalf("unexpected output when not truncated: %q", got)
	}
	if got := appendTruncatedNote("", true, 2); got != "... truncated to 2 results" {
		t.Fatalf("unexpected output for empty truncated case: %q", got)
	}
	if got := appendTruncatedNote("content", true, 1); got != "content\n... truncated to 1 results" {
		t.Fatalf("unexpected output for appended note: %q", got)
	}
}
