package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const taskCreateDescription = `Create a new task in the task store.

Use this when you want to persist a task with a required subject and activeForm (plus an optional description).`

var taskCreateSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"subject": map[string]interface{}{
			"type":        "string",
			"description": "Short title of the task.",
			"minLength":   1,
		},
		"description": map[string]interface{}{
			"type":        "string",
			"description": "Optional additional details for the task.",
		},
		"activeForm": map[string]interface{}{
			"type":        "string",
			"description": "Identifier for the active form associated with this task.",
			"minLength":   1,
		},
	},
	Required: []string{"subject", "activeForm"},
}

type TaskCreateTool struct {
	store tasks.Store
}

func NewTaskCreateTool(store tasks.Store) *TaskCreateTool {
	return &TaskCreateTool{store: store}
}

func (t *TaskCreateTool) Name() string { return "TaskCreate" }

func (t *TaskCreateTool) Description() string { return taskCreateDescription }

func (t *TaskCreateTool) Schema() *tool.JSONSchema { return taskCreateSchema }

func (t *TaskCreateTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || t.store == nil {
		return nil, errors.New("task store is not configured")
	}
	subject, description, activeForm, err := parseTaskCreateParams(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	created, err := t.store.Create(subject, description, activeForm)
	if err != nil {
		return nil, err
	}
	taskID := ""
	if created != nil {
		taskID = created.ID
	}
	payload := map[string]interface{}{
		"taskId": taskID,
	}
	out, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal task result: %w", err)
	}
	return &tool.ToolResult{
		Success: true,
		Output:  string(out),
		Data:    payload,
	}, nil
}

func parseTaskCreateParams(params map[string]interface{}) (subject, description, activeForm string, err error) {
	if params == nil {
		return "", "", "", errors.New("params is nil")
	}
	subject, err = requiredString(params, "subject")
	if err != nil {
		return "", "", "", err
	}
	activeForm, err = requiredString(params, "activeForm")
	if err != nil {
		return "", "", "", err
	}
	description, err = optionalTrimmedString(params, "description")
	if err != nil {
		return "", "", "", err
	}
	return subject, description, activeForm, nil
}

func optionalTrimmedString(params map[string]interface{}, key string) (string, error) {
	raw, ok := params[key]
	if !ok || raw == nil {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be string: %w", key, err)
	}
	return strings.TrimSpace(value), nil
}
