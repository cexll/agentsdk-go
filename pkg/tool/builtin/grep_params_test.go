package toolbuiltin

import (
	"strconv"
	"testing"
)

func TestGrepParamParsing(t *testing.T) {
	t.Parallel()

	if _, err := parseGrepPattern(nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := parseGrepPattern(map[string]interface{}{}); err == nil {
		t.Fatalf("expected missing pattern error")
	}
	if _, err := parseGrepPattern(map[string]interface{}{"pattern": 1}); err == nil {
		t.Fatalf("expected non-string pattern error")
	}
	if _, err := parseGrepPattern(map[string]interface{}{"pattern": " "}); err == nil {
		t.Fatalf("expected empty pattern error")
	}

	if _, err := parseContextLines(map[string]interface{}{"context_lines": -1}, 5); err == nil {
		t.Fatalf("expected negative context error")
	}
	if v, err := parseContextLines(map[string]interface{}{"context_lines": 9}, 5); err != nil || v != 5 {
		t.Fatalf("expected context clamp, got %d err=%v", v, err)
	}

	if _, err := parseOutputMode(map[string]interface{}{"output_mode": "bad"}); err == nil {
		t.Fatalf("expected invalid output mode")
	}

	if _, _, err := parseBoolParam(map[string]interface{}{"-i": "maybe"}, "-i"); err == nil {
		t.Fatalf("expected invalid bool string")
	}

	if _, err := parseHeadLimit(map[string]interface{}{"head_limit": -1}); err == nil {
		t.Fatalf("expected negative head_limit error")
	}
	if _, err := parseOffset(map[string]interface{}{"offset": -1}); err == nil {
		t.Fatalf("expected negative offset error")
	}

	if got := applyRegexFlags("a", true, true); got == "a" {
		t.Fatalf("expected flags applied")
	}
	if globs := resolveTypeGlobs("go"); len(globs) == 0 {
		t.Fatalf("expected go globs")
	}
}

func TestIntFromInt64Bounds(t *testing.T) {
	base := int64(maxIntValue)
	if got, err := intFromInt64(0); err != nil || got != 0 {
		t.Fatalf("expected int64 0 ok, got %d err=%v", got, err)
	}
	if strconv.IntSize == 32 {
		overflow := base + 1
		if _, err := intFromInt64(overflow); err == nil {
			t.Fatalf("expected int64 overflow error")
		}
		min := int64(minIntValue)
		underflow := min - 1
		if _, err := intFromInt64(underflow); err == nil {
			t.Fatalf("expected int64 underflow error")
		}
	}
	if strconv.IntSize == 32 {
		if _, err := intFromUint64(uint64(base) + 1); err == nil {
			t.Fatalf("expected uint64 overflow error")
		}
	} else {
		if got, err := intFromUint64(uint64(base)); err != nil || got != int(base) {
			t.Fatalf("expected uint64 max ok, got %d err=%v", got, err)
		}
	}
}
