package events

import "testing"

func TestModelSelectedEventType(t *testing.T) {
	if ModelSelected != "ModelSelected" {
		t.Errorf("ModelSelected = %q, want \"ModelSelected\"", ModelSelected)
	}
}

func TestModelSelectedPayload(t *testing.T) {
	payload := ModelSelectedPayload{
		ToolName:  "grep",
		ModelTier: "low",
		Reason:    "tool mapping",
	}
	if payload.ToolName != "grep" {
		t.Error("ToolName not set correctly")
	}
	if payload.ModelTier != "low" {
		t.Error("ModelTier not set correctly")
	}
	if payload.Reason != "tool mapping" {
		t.Error("Reason not set correctly")
	}
}
