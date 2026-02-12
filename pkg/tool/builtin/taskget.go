package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const taskGetDescription = "Retrieve a task by ID with full block/blocker details."

var taskGetSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"taskId": map[string]interface{}{
			"type":        "string",
			"description": "ID of task to retrieve",
		},
	},
	Required: []string{"taskId"},
}

type TaskGetTool struct {
	store tasks.Store
}

func NewTaskGetTool(store tasks.Store) *TaskGetTool {
	return &TaskGetTool{store: store}
}

func (t *TaskGetTool) Name() string { return "TaskGet" }

func (t *TaskGetTool) Description() string { return taskGetDescription }

func (t *TaskGetTool) Schema() *tool.JSONSchema { return taskGetSchema }

func (t *TaskGetTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || t.store == nil {
		return nil, errors.New("task store is not configured")
	}
	taskID, err := requiredString(params, "taskId")
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	target, err := t.store.Get(taskID)
	if err != nil {
		return nil, err
	}

	blockedBy := taskRefsFromTasks(t.store.GetBlockingTasks(taskID))
	blocks := taskRefsFromTasks(t.store.GetBlockedTasks(taskID))

	output := formatTaskGetOutput(*target, blockedBy, blocks)
	return &tool.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]interface{}{
			"task":      *target,
			"blockedBy": blockedBy,
			"blocks":    blocks,
		},
	}, nil
}

type TaskRef struct {
	ID      string `json:"id"`
	Subject string `json:"subject,omitempty"`
	Status  string `json:"status"`
	Owner   string `json:"owner,omitempty"`
}

func taskRefsFromTasks(tasksList []*tasks.Task) []TaskRef {
	if len(tasksList) == 0 {
		return nil
	}
	out := make([]TaskRef, 0, len(tasksList))
	for _, task := range tasksList {
		if task == nil {
			continue
		}
		out = append(out, TaskRef{
			ID:      task.ID,
			Subject: task.Subject,
			Status:  string(task.Status),
			Owner:   task.Owner,
		})
	}
	return out
}

func formatTaskGetOutput(task tasks.Task, blockedBy []TaskRef, blocks []TaskRef) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task %s\n", task.ID)
	if strings.TrimSpace(task.Subject) != "" {
		fmt.Fprintf(&b, "subject: %s\n", strings.TrimSpace(task.Subject))
	}
	fmt.Fprintf(&b, "status: %s\n", task.Status)
	if strings.TrimSpace(task.Owner) != "" {
		fmt.Fprintf(&b, "owner: %s\n", strings.TrimSpace(task.Owner))
	}
	if len(blockedBy) == 0 {
		b.WriteString("blockedBy: (none)\n")
	} else {
		b.WriteString("blockedBy:\n")
		for _, ref := range blockedBy {
			fmt.Fprintf(&b, "- %s", ref.ID)
			if ref.Status != "" {
				fmt.Fprintf(&b, " [%s]", ref.Status)
			}
			if strings.TrimSpace(ref.Subject) != "" {
				fmt.Fprintf(&b, " %s", strings.TrimSpace(ref.Subject))
			}
			if strings.TrimSpace(ref.Owner) != "" {
				fmt.Fprintf(&b, " (owner=%s)", strings.TrimSpace(ref.Owner))
			}
			b.WriteByte('\n')
		}
	}

	if len(blocks) == 0 {
		b.WriteString("blocks: (none)")
		return b.String()
	}
	b.WriteString("blocks:\n")
	for _, ref := range blocks {
		fmt.Fprintf(&b, "- %s [%s]", ref.ID, ref.Status)
		if strings.TrimSpace(ref.Subject) != "" {
			fmt.Fprintf(&b, " %s", strings.TrimSpace(ref.Subject))
		}
		if strings.TrimSpace(ref.Owner) != "" {
			fmt.Fprintf(&b, " (owner=%s)", strings.TrimSpace(ref.Owner))
		}
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}
