package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEditToolSingleReplacement(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "sample.txt")
	if err := os.WriteFile(path, []byte("first\nsecond\n"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "second",
		"new_string": "SECOND",
	})
	if err != nil {
		t.Fatalf("edit execute failed: %v", err)
	}
	data := res.Data.(map[string]any)
	if data["replaced"].(int) != 1 || data["matches"].(int) != 1 {
		t.Fatalf("unexpected replacement counts: %#v", data)
	}
	updated, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read back file: %v", err)
	}
	if !strings.Contains(string(updated), "SECOND") {
		t.Fatalf("edit did not apply: %q", string(updated))
	}
}

func TestEditToolReplaceAll(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "multi.txt")
	if err := os.WriteFile(path, []byte("foo bar foo baz"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{
		"file_path":   path,
		"old_string":  "foo",
		"new_string":  "FOO",
		"replace_all": "true",
	})
	if err != nil {
		t.Fatalf("replace_all edit failed: %v", err)
	}
	data := res.Data.(map[string]any)
	if data["replaced"].(int) != 2 || !data["replace_all"].(bool) {
		t.Fatalf("unexpected replace_all metadata: %#v", data)
	}
	content, _ := os.ReadFile(path)
	if strings.Count(string(content), "FOO") != 2 {
		t.Fatalf("expected two replacements, got %q", string(content))
	}
}

func TestEditToolValidationErrors(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("hello world"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	testCases := []struct {
		name    string
		params  map[string]any
		message string
	}{
		{"nil params", nil, "params"},
		{"missing file", map[string]any{"old_string": "a", "new_string": "b"}, "file_path"},
		{"missing old", map[string]any{"file_path": path, "new_string": "b"}, "old_string"},
		{"missing new", map[string]any{"file_path": path, "old_string": "a"}, "new_string"},
		{"empty old", map[string]any{"file_path": path, "old_string": "", "new_string": "x"}, "old_string"},
		{"same strings", map[string]any{"file_path": path, "old_string": "x", "new_string": "x"}, "new_string"},
		{"replace_all type", map[string]any{"file_path": path, "old_string": "x", "new_string": "y", "replace_all": []int{1}}, "replace_all"},
		{"directory path", map[string]any{"file_path": dir, "old_string": "a", "new_string": "b"}, "directory"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected error containing %q, got %v", tc.message, err)
			}
		})
	}
}

func TestEditToolRejectsBinaryFiles(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	_, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "a",
		"new_string": "b",
	})
	if err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Fatalf("expected binary file error, got %v", err)
	}
}

func TestEditToolUniquenessAndSizeChecks(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "multi.txt")
	if err := os.WriteFile(path, []byte("foo foo bar"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	if _, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "foo",
		"new_string": "bar",
	}); err == nil || !strings.Contains(err.Error(), "unique") {
		t.Fatalf("expected unique constraint error, got %v", err)
	}

	if _, err := tool.Execute(context.Background(), map[string]any{
		"file_path":  path,
		"old_string": "missing",
		"new_string": "bar",
	}); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}

	tool.base.maxBytes = 1
	if _, err := tool.Execute(context.Background(), map[string]any{
		"file_path":   path,
		"old_string":  "bar",
		"new_string":  "xxxx",
		"replace_all": true,
	}); err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestEditToolSandboxAndContext(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "note.txt")
	if err := os.WriteFile(path, []byte("a\nb"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewEditToolWithRoot(dir)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := tool.Execute(ctx, map[string]any{"file_path": path, "old_string": "a", "new_string": "b"}); err == nil {
		t.Fatalf("expected context cancellation error")
	}

	if _, err := tool.Execute(context.Background(), map[string]any{"file_path": filepath.Join(dir, "..", "escape.txt"), "old_string": "a", "new_string": "b"}); err == nil || !strings.Contains(err.Error(), "sandbox") {
		t.Fatalf("expected sandbox error, got %v", err)
	}

	if _, err := tool.Execute(context.Background(), map[string]any{"file_path": path, "old_string": "a", "new_string": "b", "replace_all": true}); err != nil {
		t.Fatalf("expected valid replace_all bool: %v", err)
	}

	if _, err := tool.Execute(nil, map[string]any{"file_path": path, "old_string": "a", "new_string": "b"}); err == nil {
		t.Fatalf("expected nil context error")
	}

	var uninitialised EditTool
	if _, err := uninitialised.Execute(context.Background(), map[string]any{"file_path": path, "old_string": "a", "new_string": "b"}); err == nil {
		t.Fatalf("expected not initialised error")
	}
}

func TestEditToolMetadataAndBoolParser(t *testing.T) {
	tool := NewEditTool()
	if tool.Name() != "Edit" {
		t.Fatalf("unexpected tool name %q", tool.Name())
	}
	if tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("metadata missing")
	}

	cases := []struct {
		name    string
		value   interface{}
		want    bool
		wantErr bool
	}{
		{"bool true", true, true, false},
		{"bool false", false, false, false},
		{"string yes", "YES", true, false},
		{"string no", "n", false, false},
		{"empty string", " ", false, true},
		{"int", int(1), true, false},
		{"uint zero", uint(0), false, false},
		{"int32", int32(-1), true, false},
		{"int64", int64(0), false, false},
		{"uint32", uint32(5), true, false},
		{"uint64", uint64(0), false, false},
		{"unsupported type", []byte("true"), false, true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := coerceBool(tc.value)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error for %q", tc.name)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Fatalf("expected %v got %v", tc.want, got)
			}
		})
	}
}

func TestEditToolHelperErrors(t *testing.T) {
	tool := NewEditTool()
	if _, err := tool.parseRequiredString(nil, "foo"); err == nil {
		t.Fatalf("expected error for nil params")
	}
	if _, err := tool.parseRequiredString(map[string]any{"foo": 123}, "foo"); err == nil {
		t.Fatalf("expected error for non-string value")
	}
	if val, err := tool.parseReplaceAll(nil); err != nil || val {
		t.Fatalf("nil params should default to false, got %v err %v", val, err)
	}
	if _, err := tool.parseReplaceAll(map[string]any{"replace_all": []int{1}}); err == nil {
		t.Fatalf("expected type error for replace_all helper")
	}
}
