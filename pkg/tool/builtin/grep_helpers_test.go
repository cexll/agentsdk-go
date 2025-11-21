package toolbuiltin

import (
	"context"
	"encoding/json"
	"regexp"
	"testing"
)

func TestIntFromParamVariants(t *testing.T) {
	if v, err := intFromParam(json.Number("3")); err != nil || v != 3 {
		t.Fatalf("json number failed: %v %d", err, v)
	}
	if v, err := intFromParam(int64(4)); err != nil || v != 4 {
		t.Fatalf("int64 failed: %v %d", err, v)
	}
	if v, err := intFromParam(int8(2)); err != nil || v != 2 {
		t.Fatalf("int8 failed: %v %d", err, v)
	}
	if v, err := intFromParam(uint32(7)); err != nil || v != 7 {
		t.Fatalf("uint32 failed: %v %d", err, v)
	}
	if v, err := intFromParam(uint64(5)); err != nil || v != 5 {
		t.Fatalf("uint64 failed: %v %d", err, v)
	}
	if _, err := intFromParam(json.Number("bad")); err == nil {
		t.Fatalf("expected error for invalid json number")
	}
	if _, err := intFromParam("oops"); err == nil {
		t.Fatalf("expected error for string input")
	}
	if _, err := intFromParam(float64(maxIntValue) * 2); err == nil {
		t.Fatalf("expected range error")
	}
	if _, err := intFromParam(float64(1.5)); err == nil {
		t.Fatalf("expected non-integer error")
	}
	if _, err := intFromParam(""); err == nil {
		t.Fatalf("expected empty string error")
	}
	if _, err := intFromParam(struct{}{}); err == nil {
		t.Fatalf("expected unsupported type error")
	}
	if v, err := intFromParam(" 42 "); err != nil || v != 42 {
		t.Fatalf("string parse failed: %v %d", err, v)
	}
}

func TestRelativeDepth(t *testing.T) {
	depth := relativeDepth("/tmp", "/tmp/a/b")
	if depth == 0 {
		t.Fatalf("expected depth >0")
	}
}

func TestSearchDirectoryCancelled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	tool := NewGrepToolWithRoot(".")
	var matches []GrepMatch
	if _, err := tool.searchDirectory(ctx, ".", regexp.MustCompile("noop"), grepSearchOptions{}, &matches); err == nil {
		t.Fatalf("expected cancellation error")
	}
}

func TestResolveSearchPathErrors(t *testing.T) {
	tool := NewGrepToolWithRoot(".")
	if _, _, err := tool.resolveSearchPath(map[string]any{}); err == nil {
		t.Fatalf("expected missing path error")
	}
	if _, _, err := tool.resolveSearchPath(map[string]any{"path": ""}); err == nil {
		t.Fatalf("expected empty path error")
	}
}

func TestSearchDirectoryMissingRoot(t *testing.T) {
	tool := NewGrepToolWithRoot(".")
	var matches []GrepMatch
	if _, err := tool.searchDirectory(context.Background(), "/tmp/definitely-missing", regexp.MustCompile("noop"), grepSearchOptions{}, &matches); err == nil {
		t.Fatalf("expected walk error")
	}
}

func TestParseGrepPatternErrors(t *testing.T) {
	if _, err := parseGrepPattern(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := parseGrepPattern(map[string]any{"pattern": " "}); err == nil {
		t.Fatalf("expected empty pattern error")
	}
}
