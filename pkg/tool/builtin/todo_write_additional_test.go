package toolbuiltin

import "testing"

func TestTodoWriteMetadata(t *testing.T) {
	tool := NewTodoWriteTool()
	if tool.Name() == "" {
		t.Fatalf("expected name")
	}
	if tool.Description() == "" {
		t.Fatalf("expected description")
	}
	if tool.Schema() == nil {
		t.Fatalf("expected schema")
	}
}
