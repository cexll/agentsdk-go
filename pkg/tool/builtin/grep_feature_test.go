package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

func writeGrepFixture(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir fixture dir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture %s: %v", name, err)
	}
	return path
}

func grepData(t *testing.T, res *tool.ToolResult) map[string]any {
	t.Helper()
	data, ok := res.Data.(map[string]any)
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	return data
}

func sameSet(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	copyA := append([]string(nil), a...)
	copyB := append([]string(nil), b...)
	sort.Strings(copyA)
	sort.Strings(copyB)
	for i := range copyA {
		if copyA[i] != copyB[i] {
			return false
		}
	}
	return true
}

func TestGrepOutputModeContent(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	file := writeGrepFixture(t, dir, "sample.txt", "alpha\nbeta hit\nomega")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name       string
		params     map[string]any
		wantLine   string
		wantLineNo int
	}{
		{
			name:       "content_mode",
			params:     map[string]any{"pattern": "hit", "path": file, "output_mode": "content"},
			wantLine:   "beta hit",
			wantLineNo: 2,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), tc.params)
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			if !strings.Contains(res.Output, tc.wantLine) {
				t.Fatalf("content output missing line %q: %s", tc.wantLine, res.Output)
			}
			data := grepData(t, res)
			matches, ok := data["matches"].([]GrepMatch)
			if !ok || len(matches) != 1 {
				t.Fatalf("expected single match slice, got %#v", data["matches"])
			}
			if matches[0].Line != tc.wantLineNo || matches[0].Match != tc.wantLine {
				t.Fatalf("unexpected match payload: %#v", matches[0])
			}
			if data["output_mode"] != "content" {
				t.Fatalf("output_mode mismatch: %#v", data["output_mode"])
			}
			if data["display_count"] != 1 || data["count"] != 1 || data["total_matches"] != 1 {
				t.Fatalf("display=count mismatch: %#v", data)
			}
		})
	}
}

func TestGrepOutputModeFiles(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	first := writeGrepFixture(t, dir, "a.txt", "needle here")
	writeGrepFixture(t, dir, "b.txt", "another needle")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name   string
		params map[string]any
		want   []string
	}{
		{"explicit", map[string]any{"pattern": "needle", "path": dir, "output_mode": "files_with_matches"}, []string{"a.txt", "b.txt"}},
		{"default_mode", map[string]any{"pattern": "needle", "path": first}, []string{"a.txt"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), tc.params)
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			for _, f := range tc.want {
				if !strings.Contains(res.Output, f) {
					t.Fatalf("expected file %q in output %q", f, res.Output)
				}
			}
			if strings.Contains(res.Output, "needle") {
				t.Fatalf("files output leaked content: %s", res.Output)
			}
			data := grepData(t, res)
			files, ok := data["files"].([]string)
			if !ok {
				t.Fatalf("files not present: %#v", data)
			}
			if !sameSet(files, tc.want) {
				t.Fatalf("files mismatch got %v want %v", files, tc.want)
			}
			matches, ok := data["matches"].([]GrepMatch)
			if !ok || len(matches) != 0 {
				t.Fatalf("expected matches empty for files mode, got %#v", data["matches"])
			}
			if data["display_count"] != len(files) {
				t.Fatalf("display_count mismatch: %#v", data["display_count"])
			}
		})
	}
}

func TestGrepOutputModeCount(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name  string
		files map[string]string
		want  map[string]int
	}{
		{
			name: "basic_counts",
			files: map[string]string{
				"first.txt":  "hit\nhit",
				"second.txt": "hit once",
			},
			want: map[string]int{"first.txt": 2, "second.txt": 1},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			for name, content := range tc.files {
				writeGrepFixture(t, dir, name, content)
			}
			tool := NewGrepToolWithRoot(dir)
			res, err := tool.Execute(context.Background(), map[string]any{"pattern": "hit", "path": dir, "output_mode": "count"})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			for file, count := range tc.want {
				line := file + ": " + strconv.Itoa(count)
				if !strings.Contains(res.Output, line) {
					t.Fatalf("missing count %q in output %q", line, res.Output)
				}
			}
			data := grepData(t, res)
			counts, ok := data["counts"].(map[string]int)
			if !ok {
				t.Fatalf("counts not present: %#v", data)
			}
			if !reflect.DeepEqual(counts, tc.want) {
				t.Fatalf("counts mismatch got %#v want %#v", counts, tc.want)
			}
			if data["display_count"] != len(counts) {
				t.Fatalf("display_count mismatch: %#v", data["display_count"])
			}
		})
	}
}

func TestGrepOutputModeInvalid(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name string
		mode string
	}{
		{"unknown", "nope"},
		{"empty", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "sample.txt", "value")
			tool := NewGrepToolWithRoot(dir)
			params := map[string]any{"pattern": "value", "path": file, "output_mode": tc.mode}
			if _, err := tool.Execute(context.Background(), params); err == nil || !strings.Contains(err.Error(), "output_mode") {
				t.Fatalf("expected output_mode error for %q, got %v", tc.mode, err)
			}
		})
	}
}

func TestGrepGlobFilter(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	writeGrepFixture(t, dir, "main.go", "needle")
	writeGrepFixture(t, dir, "view.js", "needle")
	writeGrepFixture(t, dir, "types.ts", "needle")
	writeGrepFixture(t, dir, "readme.md", "needle")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name string
		glob string
		want []string
	}{
		{"go_only", "*.go", []string{"main.go"}},
		{"js_or_ts", "*.[jt]s", []string{"view.js", "types.ts"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]any{"pattern": "needle", "path": dir, "glob": tc.glob})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			files, ok := data["files"].([]string)
			if !ok {
				t.Fatalf("files missing: %#v", data)
			}
			if !sameSet(files, tc.want) {
				t.Fatalf("glob=%s files mismatch got %v want %v", tc.glob, files, tc.want)
			}
		})
	}
}

func TestGrepTypeFilter(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	writeGrepFixture(t, dir, "main.go", "token")
	writeGrepFixture(t, dir, "script.js", "token")
	writeGrepFixture(t, dir, "notes.py", "token")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name     string
		fileType string
		want     []string
	}{
		{"go", "go", []string{"main.go"}},
		{"js", "js", []string{"script.js"}},
		{"py", "py", []string{"notes.py"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]any{"pattern": "token", "path": dir, "type": tc.fileType})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			files, ok := grepData(t, res)["files"].([]string)
			if !ok || !sameSet(files, tc.want) {
				t.Fatalf("type %s files mismatch got %v want %v", tc.fileType, files, tc.want)
			}
		})
	}
}

func TestGrepGlobAndType(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name      string
		glob      string
		fileType  string
		wantFiles []string
	}{
		{"mismatch", "*.go", "js", nil},
		{"aligned", "*.js", "js", []string{"script.js"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			writeGrepFixture(t, dir, "main.go", "token")
			writeGrepFixture(t, dir, "script.js", "token")
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern": "token",
				"path":    dir,
				"glob":    tc.glob,
				"type":    tc.fileType,
			})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			if tc.wantFiles == nil {
				if files, ok := data["files"].([]string); ok && len(files) > 0 {
					t.Fatalf("expected no files recorded, got %v", files)
				}
				if data["display_count"] != 0 || data["count"] != 0 {
					t.Fatalf("expected no matches when glob and type disagree: %#v", data)
				}
				return
			}
			files, ok := data["files"].([]string)
			if !ok {
				t.Fatalf("files missing: %#v", data)
			}
			if !sameSet(files, tc.wantFiles) {
				t.Fatalf("glob/type combo mismatch got %v want %v", files, tc.wantFiles)
			}
		})
	}
}

func TestGrepCaseInsensitive(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	file := writeGrepFixture(t, dir, "case.txt", "Value")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name       string
		pattern    string
		ignoreCase bool
		wantMatch  bool
	}{
		{"case_insensitive", "value", true, true},
		{"case_sensitive", "value", false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     tc.pattern,
				"path":        file,
				"-i":          tc.ignoreCase,
				"output_mode": "content",
			})
			if tc.wantMatch {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				if !strings.Contains(res.Output, "Value") {
					t.Fatalf("expected match in output: %s", res.Output)
				}
				data := grepData(t, res)
				if data["case_insensitive"] != true {
					t.Fatalf("case_insensitive flag not set: %#v", data["case_insensitive"])
				}
			} else {
				if err != nil {
					t.Fatalf("case sensitive search should not error: %v", err)
				}
				if res.Output != "no matches" {
					t.Fatalf("expected no matches, got %q", res.Output)
				}
			}
		})
	}
}

func TestGrepLineNumbers(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	file := writeGrepFixture(t, dir, "lines.txt", "needle")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name          string
		show          bool
		wantLineToken string
	}{
		{"default_on", true, ":1:"},
		{"disabled", false, "needle"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			params := map[string]any{"pattern": "needle", "path": file, "output_mode": "content"}
			if !tc.show {
				params["-n"] = false
			}
			res, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			if tc.show && !strings.Contains(res.Output, tc.wantLineToken) {
				t.Fatalf("expected line numbers in %q", res.Output)
			}
			if !tc.show && strings.Contains(res.Output, ":1:") {
				t.Fatalf("line numbers should be hidden: %s", res.Output)
			}
			data := grepData(t, res)
			expected := tc.show
			if !tc.show {
				expected = false
			}
			if data["line_numbers"] != expected {
				t.Fatalf("line_numbers flag mismatch: %#v", data["line_numbers"])
			}
		})
	}
}

func TestGrepMultiline(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	file := writeGrepFixture(t, dir, "multi.txt", "first line\nsecond line\nthird line")
	tool := NewGrepToolWithRoot(dir)

	cases := []struct {
		name      string
		multiline bool
		wantCount int
	}{
		{"enabled", true, 1},
		{"disabled", false, 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     "first.*third",
				"path":        file,
				"multiline":   tc.multiline,
				"output_mode": "content",
			})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			if data["total_matches"] != tc.wantCount {
				t.Fatalf("multiline=%v total_matches=%v want %d", tc.multiline, data["total_matches"], tc.wantCount)
			}
		})
	}
}
