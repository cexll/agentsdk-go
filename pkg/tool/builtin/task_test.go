package toolbuiltin

import (
	"context"
	"errors"
	"sort"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestTaskToolMetadata(t *testing.T) {
	task := NewTaskTool()
	if task.Name() != "Task" {
		t.Fatalf("unexpected name: %s", task.Name())
	}
	if desc := task.Description(); !strings.Contains(desc, "Launch a new agent") {
		t.Fatalf("description mismatch: %s", desc)
	}
	schema := task.Schema()
	if schema == nil || schema.Type != "object" {
		t.Fatalf("unexpected schema: %+v", schema)
	}
	if len(schema.Required) != 3 {
		t.Fatalf("expected three required fields, got %v", schema.Required)
	}
	props := schema.Properties
	for _, key := range []string{"description", "prompt", "subagent_type", "model", "resume"} {
		if _, ok := props[key]; !ok {
			t.Fatalf("missing property %s", key)
		}
	}
}

func TestTaskToolExecuteSuccess(t *testing.T) {
	task := NewTaskTool()
	var captured TaskRequest
	task.SetRunner(func(ctx context.Context, req TaskRequest) (*tool.ToolResult, error) {
		captured = req
		return &tool.ToolResult{Success: true, Output: "ok"}, nil
	})

	params := map[string]interface{}{
		"description":   "Plan release scope",
		"prompt":        "Outline the migration plan",
		"subagent_type": "Explore",
		"model":         "haiku",
		"resume":        "alpha",
	}

	res, err := task.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if res == nil || res.Output != "ok" {
		t.Fatalf("unexpected result: %+v", res)
	}
	if captured.SubagentType != subagents.TypeExplore {
		t.Fatalf("unexpected subagent: %+v", captured)
	}
	if captured.Model != modelAliasMap[taskModelHaiku] {
		t.Fatalf("expected model mapping, got %+v", captured)
	}
	if captured.Resume != "alpha" {
		t.Fatalf("expected resume propagated, got %+v", captured)
	}
}

func TestTaskToolExecuteValidation(t *testing.T) {
	task := NewTaskTool()
	task.SetRunner(func(ctx context.Context, req TaskRequest) (*tool.ToolResult, error) {
		t.Fatalf("runner should not be invoked for invalid params: %+v", req)
		return nil, nil
	})

	cases := []struct {
		name   string
		params map[string]interface{}
	}{
		{"nil params", nil},
		{"missing description", map[string]interface{}{"prompt": "x", "subagent_type": "general-purpose"}},
		{"short description", map[string]interface{}{"description": "foo bar", "prompt": "x", "subagent_type": "general-purpose"}},
		{"long description", map[string]interface{}{"description": "one two three four five six", "prompt": "x", "subagent_type": "general-purpose"}},
		{"empty prompt", map[string]interface{}{"description": "valid words here", "prompt": " ", "subagent_type": "general-purpose"}},
		{"missing subagent", map[string]interface{}{"description": "valid words here", "prompt": "x"}},
		{"invalid model", map[string]interface{}{"description": "valid words here", "prompt": "x", "subagent_type": "general-purpose", "model": "delta"}},
		{"empty resume", map[string]interface{}{"description": "valid words here", "prompt": "x", "subagent_type": "general-purpose", "resume": " "}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := task.Execute(context.Background(), tc.params); err == nil {
				t.Fatalf("expected error for %s", tc.name)
			}
		})
	}
}

func TestTaskToolExecuteRequiresRunner(t *testing.T) {
	task := NewTaskTool()
	params := map[string]interface{}{
		"description":   "Valid short text",
		"prompt":        "Investigate",
		"subagent_type": "plan",
	}
	if _, err := task.Execute(context.Background(), params); err == nil {
		t.Fatal("expected error when runner not configured")
	}
}

func TestTaskToolExecuteContextCanceled(t *testing.T) {
	task := NewTaskTool()
	ran := false
	task.SetRunner(func(ctx context.Context, req TaskRequest) (*tool.ToolResult, error) {
		ran = true
		return &tool.ToolResult{}, nil
	})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	params := map[string]interface{}{
		"description":   "Valid short text",
		"prompt":        "Investigate",
		"subagent_type": "plan",
	}
	if _, err := task.Execute(ctx, params); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
	if ran {
		t.Fatal("runner should not execute when context cancelled")
	}
}

func TestTaskSchemaShape(t *testing.T) {
	prop, ok := taskSchema.Properties["subagent_type"].(map[string]interface{})
	if !ok {
		t.Fatalf("subagent_type property missing: %#v", taskSchema.Properties["subagent_type"])
	}
	if _, ok := prop["enum"]; ok {
		t.Fatalf("subagent_type should not declare fixed enum: %#v", prop["enum"])
	}

	modelProp, ok := taskSchema.Properties["model"].(map[string]interface{})
	if !ok {
		t.Fatalf("model property missing: %#v", taskSchema.Properties["model"])
	}
	rawModels, ok := modelProp["enum"].([]string)
	if !ok {
		t.Fatalf("model enum missing: %#v", modelProp["enum"])
	}
	actualModels := append([]string(nil), rawModels...)
	sort.Strings(actualModels)
	expectedModels := make([]string, 0, len(modelAliasMap))
	for alias := range modelAliasMap {
		expectedModels = append(expectedModels, alias)
	}
	sort.Strings(expectedModels)
	if len(actualModels) != len(expectedModels) {
		t.Fatalf("unexpected model enum length: got %v want %v", actualModels, expectedModels)
	}
	for i := range actualModels {
		if actualModels[i] != expectedModels[i] {
			t.Fatalf("model enum mismatch at %d: got %s want %s", i, actualModels[i], expectedModels[i])
		}
	}
}

func TestParseTaskParamsModelAliases(t *testing.T) {
	for alias, expected := range modelAliasMap {
		req, err := parseTaskParams(map[string]interface{}{
			"description":   "valid words here",
			"prompt":        "Investigate failure",
			"subagent_type": strings.ToUpper(subagents.TypePlan),
			"model":         alias,
			"resume":        " resume-42 ",
		})
		if err != nil {
			t.Fatalf("parseTaskParams(%s): %v", alias, err)
		}
		if req.SubagentType != subagents.TypePlan {
			t.Fatalf("expected plan normalization, got %s", req.SubagentType)
		}
		if req.Model != expected {
			t.Fatalf("expected model %s for alias %s, got %s", expected, alias, req.Model)
		}
		if req.Resume != "resume-42" {
			t.Fatalf("expected resume trimming, got %q", req.Resume)
		}
	}

	req, err := parseTaskParams(map[string]interface{}{
		"description":   "valid words here",
		"prompt":        "Investigate failure",
		"subagent_type": subagents.TypeGeneralPurpose,
	})
	if err != nil {
		t.Fatalf("parseTaskParams without optional fields: %v", err)
	}
	if req.Model != "" || req.Resume != "" {
		t.Fatalf("expected optional fields empty, got model=%q resume=%q", req.Model, req.Resume)
	}

	custom, err := parseTaskParams(map[string]interface{}{
		"description":   "valid words here",
		"prompt":        "Investigate failure",
		"subagent_type": "Code-Reviewer",
	})
	if err != nil {
		t.Fatalf("parseTaskParams custom subagent: %v", err)
	}
	if custom.SubagentType != "code-reviewer" {
		t.Fatalf("expected normalized custom subagent, got %q", custom.SubagentType)
	}
}

// TestTaskToolsShareSameStore verifies that TaskCreate, TaskList, TaskGet, and TaskUpdate
// all operate on the same TaskStore instance.
func TestTaskToolsShareSameStore(t *testing.T) {
	store := tasks.NewTaskStore()

	createTool := NewTaskCreateTool(store)
	listTool := NewTaskListTool(store)
	getTool := NewTaskGetTool(store)
	updateTool := NewTaskUpdateTool(store)

	ctx := context.Background()

	// Create a task using TaskCreate
	createRes, err := createTool.Execute(ctx, map[string]interface{}{
		"subject":     "Test Task",
		"description": "Integration test task",
		"activeForm":  "testing",
	})
	if err != nil {
		t.Fatalf("TaskCreate failed: %v", err)
	}
	taskID := createRes.Data.(map[string]interface{})["taskId"].(string)

	// Verify TaskList can see the created task
	listRes, err := listTool.Execute(ctx, map[string]interface{}{})
	if err != nil {
		t.Fatalf("TaskList failed: %v", err)
	}
	listData := listRes.Data.(map[string]interface{})
	if listData["total"].(int) != 1 {
		t.Fatalf("expected 1 task in list, got %v", listData["total"])
	}

	// Verify TaskGet can retrieve the created task
	getRes, err := getTool.Execute(ctx, map[string]interface{}{
		"taskId": taskID,
	})
	if err != nil {
		t.Fatalf("TaskGet failed: %v", err)
	}
	getTask := getRes.Data.(map[string]interface{})["task"].(tasks.Task)
	if getTask.Subject != "Test Task" {
		t.Fatalf("expected subject 'Test Task', got %q", getTask.Subject)
	}
	if getTask.Status != tasks.TaskPending {
		t.Fatalf("expected status pending, got %q", getTask.Status)
	}

	// Update the task using TaskUpdate
	_, err = updateTool.Execute(ctx, map[string]interface{}{
		"taskId": taskID,
		"status": "in_progress",
	})
	if err != nil {
		t.Fatalf("TaskUpdate failed: %v", err)
	}

	// Verify TaskGet sees the updated status
	getRes2, err := getTool.Execute(ctx, map[string]interface{}{
		"taskId": taskID,
	})
	if err != nil {
		t.Fatalf("TaskGet after update failed: %v", err)
	}
	getTask2 := getRes2.Data.(map[string]interface{})["task"].(tasks.Task)
	if getTask2.Status != tasks.TaskInProgress {
		t.Fatalf("expected status in_progress after update, got %q", getTask2.Status)
	}

	// Complete the task
	_, err = updateTool.Execute(ctx, map[string]interface{}{
		"taskId": taskID,
		"status": "completed",
	})
	if err != nil {
		t.Fatalf("TaskUpdate to completed failed: %v", err)
	}

	// Verify final state
	getRes3, err := getTool.Execute(ctx, map[string]interface{}{
		"taskId": taskID,
	})
	if err != nil {
		t.Fatalf("TaskGet final failed: %v", err)
	}
	getTask3 := getRes3.Data.(map[string]interface{})["task"].(tasks.Task)
	if getTask3.Status != tasks.TaskCompleted {
		t.Fatalf("expected status completed, got %q", getTask3.Status)
	}
}
