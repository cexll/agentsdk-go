package middleware

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDecodeJSONMapEdgeCases(t *testing.T) {
	if res := decodeJSONMap(json.RawMessage(nil)); res != nil {
		t.Fatalf("expected nil for empty raw message, got %#v", res)
	}

	invalid := json.RawMessage(`{"broken":`)
	res := decodeJSONMap(invalid)
	if res["raw"] != string(invalid) {
		t.Fatalf("invalid json should echo raw content: %#v", res)
	}

	raw := json.RawMessage(`{"foo":1}`)
	first := decodeJSONMap(raw)
	first["foo"] = 99
	second := decodeJSONMap(raw)
	if second["foo"] != float64(1) {
		t.Fatalf("decodeJSONMap must not retain shared state: %#v", second)
	}
}

func TestCloneMapSanitizesValues(t *testing.T) {
	if cloneMap(map[string]any{}) != nil {
		t.Fatalf("empty map should return nil clone")
	}

	raw := json.RawMessage(`{"a":1}`)
	bytesVal := []byte("plain-bytes")
	src := map[string]any{
		"raw":   raw,
		"bytes": bytesVal,
		"err":   errors.New("boom"),
	}

	cloned := cloneMap(src)
	if cloned["err"] != "boom" {
		t.Fatalf("error values must be stringified: %#v", cloned)
	}
	if cloned["bytes"] != "plain-bytes" {
		t.Fatalf("byte slices should be converted to strings: %#v", cloned)
	}

	resultRaw, ok := cloned["raw"].(json.RawMessage)
	if !ok {
		t.Fatalf("raw payload missing: %#v", cloned)
	}
	resultRaw[0] = 'X'
	if raw[0] == 'X' {
		t.Fatalf("clone should copy raw message data")
	}
}

func TestValueErrorStringVariants(t *testing.T) {
	type wrapper struct {
		Err error
	}
	tests := []struct {
		name string
		val  any
		want string
	}{
		{"nil", nil, ""},
		{"direct-error", errors.New("direct"), "direct"},
		{"struct-field", wrapper{Err: errors.New("wrapped")}, "wrapped"},
		{"pointer-struct", &wrapper{Err: errors.New("ptr")}, "ptr"},
		{"nil-pointer", (*wrapper)(nil), ""},
		{"no-err-field", struct{ Msg string }{Msg: "noop"}, ""},
		{"non-struct", 123, ""},
	}
	for _, tc := range tests {
		if got := valueErrorString(tc.val); got != tc.want {
			t.Fatalf("%s: expected %q got %q", tc.name, tc.want, got)
		}
	}
}

func TestSanitizePayloadVariants(t *testing.T) {
	type serializable struct {
		Value int
	}
	type chanHolder struct {
		C chan int
	}

	raw := json.RawMessage(`{"foo":1}`)
	cloned := sanitizePayload(raw).(json.RawMessage)
	cloned[0] = 'x'
	if raw[0] == 'x' {
		t.Fatalf("raw message should be copied before returning")
	}

	jsonBytes := []byte(`{"bar":2}`)
	cb, ok := sanitizePayload(jsonBytes).(json.RawMessage)
	if !ok || string(cb) != string(jsonBytes) {
		t.Fatalf("json bytes should convert to raw message: %#v", cb)
	}
	cb[0] = 'y'
	if jsonBytes[0] == 'y' {
		t.Fatalf("json byte slice should be copied")
	}

	if got := sanitizePayload([]byte("plain")); got != "plain" {
		t.Fatalf("plain bytes should stringify, got %#v", got)
	}

	if got := sanitizePayload(errors.New("boom")); got != "boom" {
		t.Fatalf("errors should stringify, got %#v", got)
	}

	serial := serializable{Value: 7}
	if got, ok := sanitizePayload(serial).(serializable); !ok || got.Value != 7 {
		t.Fatalf("serializable structs should round-trip, got %#v", got)
	}

	fn := func() {}
	if got := sanitizePayload(fn); got != "<non-serializable:func()>" {
		t.Fatalf("functions should report non-serializable, got %v", got)
	}

	ch := make(chan int)
	if got := sanitizePayload(ch); got != "<non-serializable:chan int>" {
		t.Fatalf("channels should report non-serializable, got %v", got)
	}

	holder := chanHolder{C: make(chan int)}
	if got := sanitizePayload(holder); got != "middleware.chanHolder" {
		t.Fatalf("struct fallback should use type name, got %v", got)
	}
}
