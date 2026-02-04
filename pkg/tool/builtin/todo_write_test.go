package toolbuiltin

import (
	"context"
	"errors"
	"strings"
	"testing"
)

func TestTodoWriteToolExecuteAndSnapshot(t *testing.T) {
	t.Parallel()

	tool := NewTodoWriteTool()
	params := map[string]interface{}{
		"todos": []interface{}{
			map[string]interface{}{
				"content":    "Write tests",
				"status":     "pending",
				"activeForm": "Writing tests",
			},
			map[string]interface{}{
				"content": "Review",
				"status":  "completed",
			},
		},
	}

	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success result")
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if count, ok := data["count"].(int); !ok || count != 2 {
		t.Fatalf("unexpected count %v", data["count"])
	}

	snapshot := tool.Snapshot()
	if len(snapshot) != 2 {
		t.Fatalf("expected 2 todos, got %d", len(snapshot))
	}
	if snapshot[0].ActiveForm != "Writing tests" {
		t.Fatalf("expected activeForm override, got %q", snapshot[0].ActiveForm)
	}
	if snapshot[1].ActiveForm != "Review" {
		t.Fatalf("expected default activeForm, got %q", snapshot[1].ActiveForm)
	}

	snapshot[0].Content = "mutated"
	again := tool.Snapshot()
	if again[0].Content == "mutated" {
		t.Fatalf("snapshot should be a copy")
	}
}

func TestTodoWriteToolExecuteErrors(t *testing.T) {
	t.Parallel()

	params := map[string]interface{}{
		"todos": []interface{}{
			map[string]interface{}{
				"content": "X",
				"status":  "pending",
			},
		},
	}

	if _, err := (*TodoWriteTool)(nil).Execute(context.Background(), params); err == nil {
		t.Fatalf("expected nil tool error")
	}
	if _, err := NewTodoWriteTool().Execute(nil, params); err == nil {
		t.Fatalf("expected nil context error")
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NewTodoWriteTool().Execute(ctx, params); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestParseTodoWriteItemsErrors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name   string
		params map[string]interface{}
	}{
		{name: "nil params", params: nil},
		{name: "missing todos", params: map[string]interface{}{}},
		{name: "todos not array", params: map[string]interface{}{"todos": "bad"}},
		{name: "todos entry not map", params: map[string]interface{}{"todos": []interface{}{"bad"}}},
		{name: "content not string", params: map[string]interface{}{"todos": []interface{}{map[string]interface{}{"content": 1, "status": "pending"}}}},
		{name: "status not string", params: map[string]interface{}{"todos": []interface{}{map[string]interface{}{"content": "x", "status": 1}}}},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if _, err := parseTodoWriteItems(tc.params); err == nil {
				t.Fatalf("expected error")
			}
		})
	}

	if items, err := parseTodoWriteItems(map[string]interface{}{
		"todos": []map[string]interface{}{
			{"content": "ok", "status": "pending", "activeForm": ""},
		},
	}); err != nil || len(items) != 1 || !strings.EqualFold(items[0].ActiveForm, "ok") {
		t.Fatalf("expected activeForm fallback, got %v err=%v", items, err)
	}
}
