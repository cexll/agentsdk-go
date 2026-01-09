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

func TestMCPToolsChangedEventType(t *testing.T) {
	if MCPToolsChanged != "MCPToolsChanged" {
		t.Errorf("MCPToolsChanged = %q, want \"MCPToolsChanged\"", MCPToolsChanged)
	}
}

func TestMCPToolsChangedPayload(t *testing.T) {
	payload := MCPToolsChangedPayload{
		Server:    "http://example.com",
		SessionID: "session-1",
		Tools: []MCPToolDescriptor{
			{Name: "echo", Description: "echo tool", InputSchema: map[string]any{"type": "object"}},
		},
	}
	if payload.Server != "http://example.com" {
		t.Error("Server not set correctly")
	}
	if payload.SessionID != "session-1" {
		t.Error("SessionID not set correctly")
	}
	if len(payload.Tools) != 1 || payload.Tools[0].Name != "echo" {
		t.Errorf("unexpected tool payload: %#v", payload.Tools)
	}
}
