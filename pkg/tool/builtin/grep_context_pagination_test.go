package toolbuiltin

import (
	"context"
	"testing"
)

func TestGrepContextA(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name   string
		file   string
		ctx    map[string]any
		after  []string
		before []string
	}{
		{
			name:   "after_only",
			file:   "zero\none\ntwo target\nthree",
			ctx:    map[string]any{"-A": 1},
			after:  []string{"three"},
			before: nil,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "ctx.txt", tc.file)
			tool := NewGrepToolWithRoot(dir)

			params := map[string]any{"pattern": "target", "path": file, "output_mode": "content"}
			for k, v := range tc.ctx {
				params[k] = v
			}

			res, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			matches := data["matches"].([]GrepMatch)
			if len(matches) != 1 || !sameSet(matches[0].After, tc.after) || len(matches[0].Before) != len(tc.before) {
				t.Fatalf("after context mismatch: %#v", matches)
			}
			if data["after_context"] != tc.ctx["-A"] || data["before_context"] != 0 {
				t.Fatalf("context counters mismatch: %#v", data)
			}
		})
	}
}

func TestGrepContextB(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name    string
		content string
		ctx     int
		before  []string
	}{
		{"before_only", "zero\none\ntwo target\nthree", 1, []string{"one"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "ctx.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{"pattern": "target", "path": file, "-B": tc.ctx, "output_mode": "content"})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			matches := grepData(t, res)["matches"].([]GrepMatch)
			if len(matches) != 1 || !sameSet(matches[0].Before, tc.before) {
				t.Fatalf("before context mismatch: %#v", matches)
			}
		})
	}
}

func TestGrepContextC(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name    string
		content string
		ctx     int
		before  []string
		after   []string
	}{
		{"symmetric", "a\nb\nc target\nd\ne", 1, []string{"b"}, []string{"d"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "ctx.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{"pattern": "target", "path": file, "-C": tc.ctx, "output_mode": "content"})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			matches := grepData(t, res)["matches"].([]GrepMatch)
			if len(matches) != 1 || !sameSet(matches[0].Before, tc.before) || !sameSet(matches[0].After, tc.after) {
				t.Fatalf("symmetric context missing: %#v", matches)
			}
		})
	}
}

func TestGrepContextPrecedence(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name    string
		content string
		params  map[string]any
		expect  int
	}{
		{
			name:    "c_overrides",
			content: "a\nb\nc target\nd\ne",
			params: map[string]any{
				"-A": 3,
				"-B": 3,
				"-C": 1,
			},
			expect: 1,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "ctx.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			params := map[string]any{"pattern": "target", "path": file, "output_mode": "content"}
			for k, v := range tc.params {
				params[k] = v
			}

			res, err := tool.Execute(context.Background(), params)
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			if data["before_context"] != tc.expect || data["after_context"] != tc.expect {
				t.Fatalf("-C should override -A/-B: %#v", data)
			}
		})
	}
}

func TestGrepHeadLimit(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name      string
		content   string
		headLimit int
		total     int
	}{
		{"limit_two", "one\ntwo\nthree", 2, 3},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "many.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     "^.*$",
				"path":        file,
				"output_mode": "content",
				"head_limit":  tc.headLimit,
			})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			matches := data["matches"].([]GrepMatch)
			if len(matches) != tc.headLimit || data["display_count"] != tc.headLimit || data["total_matches"] != tc.total {
				t.Fatalf("head_limit not applied: %#v", data)
			}
			if data["truncated"] != true {
				t.Fatalf("expected truncated flag with head_limit: %#v", data["truncated"])
			}
		})
	}
}

func TestGrepOffset(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name    string
		content string
		offset  int
		first   string
	}{
		{"skip_one", "one\ntwo\nthree", 1, "two"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "many.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     "^.*$",
				"path":        file,
				"output_mode": "content",
				"offset":      tc.offset,
			})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			matches := data["matches"].([]GrepMatch)
			if len(matches) != 2 || matches[0].Match != tc.first {
				t.Fatalf("offset did not skip expected results: %#v", matches)
			}
			if data["truncated"] != true {
				t.Fatalf("expected truncated due to offset: %#v", data["truncated"])
			}
		})
	}
}

func TestGrepHeadLimitAndOffset(t *testing.T) {
	skipIfWindows(t)
	cases := []struct {
		name      string
		content   string
		offset    int
		headLimit int
		first     string
		total     int
	}{
		{"window", "one\ntwo\nthree\nfour", 2, 1, "three", 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := cleanTempDir(t)
			file := writeGrepFixture(t, dir, "many.txt", tc.content)
			tool := NewGrepToolWithRoot(dir)

			res, err := tool.Execute(context.Background(), map[string]any{
				"pattern":     "^.*$",
				"path":        file,
				"output_mode": "content",
				"head_limit":  tc.headLimit,
				"offset":      tc.offset,
			})
			if err != nil {
				t.Fatalf("grep execute: %v", err)
			}
			data := grepData(t, res)
			matches := data["matches"].([]GrepMatch)
			if len(matches) != tc.headLimit || matches[0].Match != tc.first {
				t.Fatalf("windowing incorrect: %#v", matches)
			}
			if data["display_count"] != tc.headLimit || data["total_matches"] != tc.total {
				t.Fatalf("counts incorrect: %#v", data)
			}
			if data["truncated"] != true {
				t.Fatalf("expected truncated flag: %#v", data["truncated"])
			}
		})
	}
}
