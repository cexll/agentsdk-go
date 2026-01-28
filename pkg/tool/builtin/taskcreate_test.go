package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/tasks"
)

func TestTaskCreateToolMetadataAndSchema(t *testing.T) {
	tool := NewTaskCreateTool(tasks.NewTaskStore())
	if tool.Name() != "TaskCreate" {
		t.Fatalf("unexpected name %q", tool.Name())
	}
	if strings.TrimSpace(tool.Description()) == "" {
		t.Fatalf("expected non-empty description")
	}
	schema := tool.Schema()
	if schema == nil || schema.Type != "object" {
		t.Fatalf("unexpected schema %+v", schema)
	}
	if len(schema.Required) != 2 {
		t.Fatalf("unexpected required %+v", schema.Required)
	}
	props := schema.Properties
	for _, key := range []string{"subject", "description", "activeForm"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing property %s", key)
		}
	}
}

func TestTaskCreateToolExecuteSuccessStoresTask(t *testing.T) {
	store := tasks.NewTaskStore()
	tool := NewTaskCreateTool(store)

	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"subject":     "  Fix flaky tests  ",
		"description": "  make CI stable  ",
		"activeForm":  "default",
	})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res == nil || !res.Success {
		t.Fatalf("unexpected result %+v", res)
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	taskID, ok := data["taskId"].(string)
	if !ok || strings.TrimSpace(taskID) == "" {
		t.Fatalf("expected taskId, got %+v", data["taskId"])
	}
	var decoded map[string]string
	if err := json.Unmarshal([]byte(res.Output), &decoded); err != nil {
		t.Fatalf("invalid JSON output: %v", err)
	}
	if decoded["taskId"] != taskID {
		t.Fatalf("output taskId mismatch: %q vs %q", decoded["taskId"], taskID)
	}
	task, ok := store.GetTask(taskID)
	if !ok {
		t.Fatalf("task not stored for id %q", taskID)
	}
	if task.Subject != "Fix flaky tests" {
		t.Fatalf("unexpected subject %q", task.Subject)
	}
	if task.Description != "make CI stable" {
		t.Fatalf("unexpected description %q", task.Description)
	}
	if task.ActiveForm != "default" {
		t.Fatalf("unexpected activeForm %q", task.ActiveForm)
	}
}

func TestTaskCreateToolGenerateUniqueIDs(t *testing.T) {
	store := tasks.NewTaskStore()
	tool := NewTaskCreateTool(store)
	ctx := context.Background()

	first, err := tool.Execute(ctx, map[string]interface{}{
		"subject":    "A",
		"activeForm": "f",
	})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	second, err := tool.Execute(ctx, map[string]interface{}{
		"subject":    "A",
		"activeForm": "f",
	})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	id1 := first.Data.(map[string]interface{})["taskId"].(string)
	id2 := second.Data.(map[string]interface{})["taskId"].(string)
	if id1 == id2 {
		t.Fatalf("expected unique task IDs, got %q", id1)
	}
	if store.Len() != 2 {
		t.Fatalf("expected store size 2, got %d", store.Len())
	}
}

func TestTaskCreateToolValidationErrors(t *testing.T) {
	store := tasks.NewTaskStore()
	tool := NewTaskCreateTool(store)
	ctx := context.Background()

	cases := []struct {
		name   string
		params map[string]interface{}
		want   string
	}{
		{name: "nil params", params: nil, want: "params is nil"},
		{name: "missing subject", params: map[string]interface{}{"activeForm": "f"}, want: "subject is required"},
		{name: "missing activeForm", params: map[string]interface{}{"subject": "x"}, want: "activeForm is required"},
		{name: "empty subject", params: map[string]interface{}{"subject": "   ", "activeForm": "f"}, want: "subject cannot be empty"},
		{name: "empty activeForm", params: map[string]interface{}{"subject": "x", "activeForm": " "}, want: "activeForm cannot be empty"},
		{name: "non-string subject", params: map[string]interface{}{"subject": 123, "activeForm": "f"}, want: "subject must be string"},
		{name: "non-string activeForm", params: map[string]interface{}{"subject": "x", "activeForm": 456}, want: "activeForm must be string"},
		{name: "non-string description", params: map[string]interface{}{"subject": "x", "description": 789, "activeForm": "f"}, want: "description must be string"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := tool.Execute(ctx, tc.params); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("expected error containing %q, got %v", tc.want, err)
			}
		})
	}
}

func TestTaskCreateToolNilContextHandling(t *testing.T) {
	tool := NewTaskCreateTool(tasks.NewTaskStore())
	if _, err := tool.Execute(nil, map[string]interface{}{"subject": "x", "activeForm": "f"}); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("expected context is nil error, got %v", err)
	}
}

func TestTaskCreateToolCancelledContextReturnsError(t *testing.T) {
	store := tasks.NewTaskStore()
	tool := NewTaskCreateTool(store)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := tool.Execute(ctx, map[string]interface{}{"subject": "x", "activeForm": "f"}); err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled error, got %v", err)
	}
	if store.Len() != 0 {
		t.Fatalf("expected store empty after cancelled context, got %d", store.Len())
	}
}

func TestTaskCreateToolRequiresStore(t *testing.T) {
	tool := NewTaskCreateTool(nil)
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"subject": "x", "activeForm": "f"}); err == nil || !strings.Contains(err.Error(), "task store is not configured") {
		t.Fatalf("expected store configured error, got %v", err)
	}
}
