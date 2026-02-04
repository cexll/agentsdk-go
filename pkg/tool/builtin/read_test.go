package toolbuiltin

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestReadToolCatFormatting(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "sample.txt")
	content := "alpha\nbeta\n\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewReadToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{"file_path": path})
	if err != nil {
		t.Fatalf("read execute failed: %v", err)
	}
	if !strings.Contains(res.Output, "     1\talpha") {
		t.Fatalf("missing numbered output: %q", res.Output)
	}
	if !strings.Contains(res.Output, "     2\tbeta") {
		t.Fatalf("missing second line: %q", res.Output)
	}
	if !strings.Contains(res.Output, "     3\t") {
		t.Fatalf("missing blank line entry: %q", res.Output)
	}
	data := res.Data.(map[string]any)
	if data["returned_lines"].(int) != 4 {
		t.Fatalf("expected 4 returned lines, got %#v", data["returned_lines"])
	}
	if data["truncated"].(bool) {
		t.Fatalf("unexpected truncation flag")
	}
}

func TestReadToolOffsetLimitAndTruncation(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "long.txt")
	lines := []string{"one", "two", "three", strings.Repeat("x", readMaxLineLength+10)}
	if err := os.WriteFile(path, []byte(strings.Join(lines, "\n")), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewReadToolWithRoot(dir)

	params := map[string]any{
		"file_path": path,
		"offset":    json.Number("3"),
		"limit":     uint32(2),
	}
	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("read execute failed: %v", err)
	}
	if strings.Count(res.Output, "\n") != 1 {
		t.Fatalf("expected two lines in output, got %q", res.Output)
	}
	if strings.Contains(res.Output, "one") || strings.Contains(res.Output, "two") || !strings.Contains(res.Output, "three") {
		t.Fatalf("unexpected output subset: %q", res.Output)
	}
	if !strings.Contains(res.Output, "...(truncated)") {
		t.Fatalf("expected truncation suffix: %q", res.Output)
	}
	data := res.Data.(map[string]any)
	if !data["truncated"].(bool) {
		t.Fatalf("expected truncated flag for limited read")
	}
	if data["line_truncations"].(int) == 0 {
		t.Fatalf("expected at least one truncated line")
	}
}

func TestReadToolOffsetOutOfRange(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "short.txt")
	if err := os.WriteFile(path, []byte("only\nline"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewReadToolWithRoot(dir)

	res, err := tool.Execute(context.Background(), map[string]any{
		"file_path": path,
		"offset":    99,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(res.Output, "no content") {
		t.Fatalf("expected range warning, got %q", res.Output)
	}
	data := res.Data.(map[string]any)
	if !data["range_out_of_file"].(bool) {
		t.Fatalf("expected range_out_of_file flag")
	}
}

func TestReadToolValidationErrors(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "data.txt")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewReadToolWithRoot(dir)

	cases := []struct {
		name    string
		params  map[string]any
		message string
	}{
		{"missing path", map[string]any{}, "file_path"},
		{"invalid offset type", map[string]any{"file_path": path, "offset": []int{1}}, "offset"},
		{"negative offset", map[string]any{"file_path": path, "offset": -1}, "offset"},
		{"non integer limit", map[string]any{"file_path": path, "limit": 1.5}, "limit"},
		{"sandbox escape", map[string]any{"file_path": filepath.Join(dir, "..", "escape.txt")}, "path not in sandbox"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tool.Execute(context.Background(), tc.params)
			if err == nil || !strings.Contains(err.Error(), tc.message) {
				t.Fatalf("expected error containing %q, got %v", tc.message, err)
			}
		})
	}

	if _, err := tool.Execute(nil, map[string]any{"file_path": path}); err == nil {
		t.Fatalf("expected nil context error")
	}
	var uninitialised ReadTool
	if _, err := uninitialised.Execute(context.Background(), map[string]any{"file_path": path}); err == nil {
		t.Fatalf("expected not initialised error")
	}
	if _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatalf("expected params nil error")
	}
}

func TestReadToolRejectsBinaryFiles(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	path := filepath.Join(dir, "binary.bin")
	if err := os.WriteFile(path, []byte{0x00, 0x01, 0x02}, 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	tool := NewReadToolWithRoot(dir)

	_, err := tool.Execute(context.Background(), map[string]any{"file_path": path})
	if err == nil || !strings.Contains(err.Error(), "binary file") {
		t.Fatalf("expected binary file error, got %v", err)
	}
}

func TestReadToolMetadataAndHelpers(t *testing.T) {
	tool := NewReadTool()
	if tool.Name() != "Read" {
		t.Fatalf("unexpected name %q", tool.Name())
	}
	if tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("metadata should not be empty")
	}
	if got := splitFileLines(""); got != nil {
		t.Fatalf("expected nil lines for empty content")
	}
	if got := splitFileLines("a\nb"); len(got) != 2 {
		t.Fatalf("expected two lines got %v", got)
	}

	cases := []struct {
		name    string
		value   interface{}
		want    int
		wantErr bool
	}{
		{"int", int(3), 3, false},
		{"int8", int8(2), 2, false},
		{"int16", int16(-2), -2, false},
		{"int32", int32(4), 4, false},
		{"int64", int64(-5), -5, false},
		{"uint", uint(6), 6, false},
		{"uint8", uint8(7), 7, false},
		{"uint16", uint16(8), 8, false},
		{"uint32", uint32(9), 9, false},
		{"uint64", uint64(10), 10, false},
		{"uint64 overflow", uint64(math.MaxInt64) + 1, 0, true},
		{"float32 ok", float32(11), 11, false},
		{"float32 frac", float32(11.5), 0, true},
		{"float64 ok", float64(12), 12, false},
		{"float64 negative", float64(-3), -3, false},
		{"float64 frac", float64(12.25), 0, true},
		{"json number int", json.Number("13"), 13, false},
		{"json number float", json.Number("13.1"), 0, true},
		{"json number invalid", json.Number("abc"), 0, true},
		{"json number overflow", json.Number(strconv.FormatInt(math.MaxInt64, 10) + "9"), 0, true},
		{"json number float int", json.Number("20.0"), 20, false},
		{"string ok", "14", 14, false},
		{"string empty", "  ", 0, true},
		{"unsupported", []byte("15"), 0, true},
	}

	if strconv.IntSize == 64 {
		next := uint64(math.MaxInt)
		next++
		cases = append(cases, struct {
			name    string
			value   interface{}
			want    int
			wantErr bool
		}{"uint overflow", uint(next), 0, true})
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, err := coerceInt(tc.value)
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
				t.Fatalf("expected %d got %d", tc.want, got)
			}
		})
	}

	if val, err := parseLineNumber(nil, "offset"); err != nil || val != 0 {
		t.Fatalf("expected zero for nil params")
	}
	if val, err := parseLineNumber(map[string]any{"offset": nil}, "offset"); err != nil || val != 0 {
		t.Fatalf("expected zero for nil value, got %d err %v", val, err)
	}

	tool.maxLineLength = 0
	if got, truncated := tool.applyLineTruncation("short"); got != "short" || truncated {
		t.Fatalf("expected passthrough for zero max length")
	}
	tool.maxLineLength = 5
	if got, truncated := tool.applyLineTruncation("0123456789"); !truncated || !strings.Contains(got, "...(truncated)") {
		t.Fatalf("expected truncation suffix, got %q truncated=%v", got, truncated)
	}
}
