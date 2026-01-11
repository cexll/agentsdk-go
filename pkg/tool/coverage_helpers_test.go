package tool

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/mcp"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

func TestExtractStringSlice(t *testing.T) {
	t.Parallel()

	original := []string{"a", "b"}
	cloned := extractStringSlice(original)
	if !reflect.DeepEqual(cloned, []string{"a", "b"}) {
		t.Fatalf("unexpected clone: %+v", cloned)
	}
	original[0] = "z"
	if cloned[0] != "a" {
		t.Fatalf("expected clone to be independent, got %+v", cloned)
	}

	mixed := extractStringSlice([]any{"x", 1, "y"})
	if !reflect.DeepEqual(mixed, []string{"x", "y"}) {
		t.Fatalf("unexpected mixed extraction: %+v", mixed)
	}

	if got := extractStringSlice("nope"); got != nil {
		t.Fatalf("expected nil for unsupported input, got %+v", got)
	}
}

func TestExtractFloatPointer(t *testing.T) {
	t.Parallel()

	if v, ok := extractFloatPointer(nil); ok || v != nil {
		t.Fatalf("expected nil pointer for nil input, got ok=%v v=%v", ok, v)
	}
	if v, ok := extractFloatPointer("nope"); ok || v != nil {
		t.Fatalf("expected nil pointer for invalid input, got ok=%v v=%v", ok, v)
	}
	v, ok := extractFloatPointer(int64(3))
	if !ok || v == nil || *v != 3 {
		t.Fatalf("unexpected float pointer: ok=%v v=%v", ok, v)
	}
}

func TestToFloat64CoversNumericBranches(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		in   any
		want float64
		ok   bool
	}{
		{name: "float32", in: float32(1.5), want: 1.5, ok: true},
		{name: "float64", in: float64(2.5), want: 2.5, ok: true},
		{name: "int", in: int(-2), want: -2, ok: true},
		{name: "int8", in: int8(-3), want: -3, ok: true},
		{name: "int16", in: int16(-4), want: -4, ok: true},
		{name: "int32", in: int32(-5), want: -5, ok: true},
		{name: "int64", in: int64(-6), want: -6, ok: true},
		{name: "uint", in: uint(7), want: 7, ok: true},
		{name: "uint8", in: uint8(8), want: 8, ok: true},
		{name: "uint16", in: uint16(9), want: 9, ok: true},
		{name: "uint32", in: uint32(10), want: 10, ok: true},
		{name: "uint64", in: uint64(11), want: 11, ok: true},
		{name: "jsonNumberValid", in: json.Number("3.25"), want: 3.25, ok: true},
		{name: "jsonNumberInvalid", in: json.Number("nope"), want: 0, ok: false},
		{name: "default", in: struct{}{}, want: 0, ok: false},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			got, ok := toFloat64(tc.in)
			if ok != tc.ok {
				t.Fatalf("ok=%v, want %v", ok, tc.ok)
			}
			if tc.ok && got != tc.want {
				t.Fatalf("got=%v, want %v", got, tc.want)
			}
		})
	}
}

func TestIndexPathAndWrapFieldError(t *testing.T) {
	t.Parallel()

	if got := indexPath("", 2); got != "[2]" {
		t.Fatalf("unexpected index path: %q", got)
	}
	if got := indexPath("root", 2); got != "root[2]" {
		t.Fatalf("unexpected index path: %q", got)
	}

	sentinel := errors.New("boom")
	if got := wrapFieldError("", sentinel); got != sentinel {
		t.Fatalf("expected error passthrough, got %v", got)
	}
	wrapped := wrapFieldError("root", sentinel)
	if !errors.Is(wrapped, sentinel) {
		t.Fatalf("expected wrapped error to match sentinel, got %v", wrapped)
	}
}

func TestNonNilContextBranches(t *testing.T) {
	t.Parallel()

	ctx := context.TODO()
	if got := nonNilContext(ctx); got != ctx {
		t.Fatalf("expected context passthrough")
	}
	if got := nonNilContext(nil); got == nil {
		t.Fatalf("expected non-nil context")
	}
}

func TestFirstTextContent(t *testing.T) {
	t.Parallel()

	if got := firstTextContent(nil); got != "" {
		t.Fatalf("expected empty output, got %q", got)
	}
	content := []mcp.Content{
		&mcp.TextContent{Text: "ok"},
	}
	if got := firstTextContent(content); got != "ok" {
		t.Fatalf("expected first text output, got %q", got)
	}
}

func TestExecutorWithSandboxNilReceiver(t *testing.T) {
	t.Parallel()

	sb := sandbox.NewManager(nil, nil, nil)
	var exec *Executor
	got := exec.WithSandbox(sb)
	if got == nil || got.sandbox != sb {
		t.Fatalf("expected sandbox to be assigned")
	}
}
