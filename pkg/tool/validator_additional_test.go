package tool

import (
	"encoding/json"
	"testing"
)

func TestValidatorTypeChecks(t *testing.T) {
	v := DefaultValidator{}
	schema := &JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"name":  map[string]any{"type": "string"},
			"count": map[string]any{"type": "integer"},
			"ratio": map[string]any{"type": "number"},
			"flag":  map[string]any{"type": "boolean"},
			"tags":  map[string]any{"type": "array"},
		},
		Required: []string{"name"},
	}

	if err := v.Validate(map[string]any{
		"name":  "ok",
		"count": int64(2),
		"ratio": 1.5,
		"flag":  true,
		"tags":  []any{"a"},
	}, schema); err != nil {
		t.Fatalf("validate failed: %v", err)
	}

	if err := v.Validate(map[string]any{"name": "ok", "flag": "no"}, schema); err == nil {
		t.Fatalf("expected type error")
	}
}

func TestValidatorRequiredAndUnsupported(t *testing.T) {
	v := DefaultValidator{}
	schema := &JSONSchema{Type: "object", Required: []string{"id"}, Properties: map[string]any{"id": map[string]any{"type": "weird"}}}

	if err := v.Validate(map[string]any{}, schema); err == nil {
		t.Fatalf("expected required error")
	}
	if err := v.Validate(map[string]any{"id": 1}, schema); err == nil {
		t.Fatalf("expected unsupported type error")
	}
}

func TestIsNumberAndIntegerBranches(t *testing.T) {
	v := DefaultValidator{}
	schema := &JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"num": map[string]any{"type": "number"},
			"int": map[string]any{"type": "integer"},
		},
	}

	// float32 branch
	if err := v.Validate(map[string]any{"num": float32(1.2)}, schema); err != nil {
		t.Fatalf("float32 should pass: %v", err)
	}
	// integer branch via float64 whole number
	if err := v.Validate(map[string]any{"int": float64(2)}, schema); err != nil {
		t.Fatalf("float64 integer should pass: %v", err)
	}
	if err := v.Validate(map[string]any{"num": uint(3)}, schema); err != nil {
		t.Fatalf("uint should be number: %v", err)
	}
	if err := v.Validate(map[string]any{"int": json.Number("4")}, schema); err != nil {
		t.Fatalf("json number should be integer: %v", err)
	}
	if err := v.Validate(map[string]any{"int": 1.3}, schema); err == nil {
		t.Fatalf("non integer should fail")
	}
}

func TestValidatorCoversAllNumberTypes(t *testing.T) {
	v := DefaultValidator{}
	schema := &JSONSchema{Type: "object", Properties: map[string]any{"num": map[string]any{"type": "number"}, "int": map[string]any{"type": "integer"}}}
	schema.Properties["str"] = &JSONSchema{Type: "string"}

	numberVals := []any{int(1), int32(2), uint16(3), json.Number("4.5")}
	for _, val := range numberVals {
		if err := v.Validate(map[string]any{"num": val}, schema); err != nil {
			t.Fatalf("number validation failed for %T: %v", val, err)
		}
	}

	if err := v.Validate(map[string]any{"int": uint64(9)}, schema); err != nil {
		t.Fatalf("integer validation failed: %v", err)
	}
	if err := v.Validate(map[string]any{"str": "ok"}, schema); err != nil {
		t.Fatalf("string type via JSONSchema failed: %v", err)
	}
	if err := v.Validate(map[string]any{"num": true}, schema); err == nil {
		t.Fatalf("expected failure for bool")
	}
	if err := v.Validate(map[string]any{"int": "12"}, schema); err == nil {
		t.Fatalf("expected failure for string integer")
	}
}
