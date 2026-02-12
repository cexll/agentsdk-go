package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const taskUpdateDescription = "Update a task's status, owner, and dependencies. Use delete=true to delete tasks."

var taskUpdateSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"taskId": map[string]interface{}{
			"type":        "string",
			"description": "ID of the task to update.",
		},
		"delete": map[string]interface{}{
			"type":        "boolean",
			"description": "If true, delete the task (and reconcile dependencies) instead of updating it.",
		},
		"status": map[string]interface{}{
			"type":        "string",
			"description": "New task status.",
			"enum": []string{
				TaskStatusPending,
				TaskStatusInProgress,
				TaskStatusCompleted,
			},
		},
		"owner": map[string]interface{}{
			"type":        "string",
			"description": "Optional task owner. Pass empty string to clear.",
		},
		"blocks": map[string]interface{}{
			"type":        "array",
			"description": "Replace the list of tasks blocked by this task.",
			"items": map[string]interface{}{
				"type": "string",
			},
		},
		"blockedBy": map[string]interface{}{
			"type":        "array",
			"description": "Replace the list of tasks that block this task.",
			"items": map[string]interface{}{
				"type": "string",
			},
		},
	},
	Required: []string{"taskId"},
}

type TaskUpdateTool struct {
	mu       sync.Mutex
	store    tasks.Store
	revision uint64
}

func NewTaskUpdateTool(store tasks.Store) *TaskUpdateTool {
	return &TaskUpdateTool{store: store}
}

func (t *TaskUpdateTool) Name() string { return "TaskUpdate" }

func (t *TaskUpdateTool) Description() string { return taskUpdateDescription }

func (t *TaskUpdateTool) Schema() *tool.JSONSchema { return taskUpdateSchema }

func (t *TaskUpdateTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if t == nil || t.store == nil {
		return nil, errors.New("task store is not configured")
	}
	req, err := parseTaskUpdateParams(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	if req.Delete {
		if err := t.store.Delete(req.TaskID); err != nil {
			return nil, err
		}
		t.revision++
		revision := t.revision
		payload := map[string]interface{}{
			"taskId":    req.TaskID,
			"deleted":   true,
			"revision":  revision,
			"affected":  nil,
			"unblocked": nil,
		}
		return &tool.ToolResult{
			Success: true,
			Output:  formatTaskDeleteOutput(req.TaskID, revision),
			Data:    payload,
		}, nil
	}

	current, err := t.store.Get(req.TaskID)
	if err != nil {
		return nil, err
	}
	oldStatus := current.Status

	affected := map[string]struct{}{req.TaskID: {}}

	var beforeBlocked map[string]tasks.TaskStatus
	if req.Status != nil && *req.Status == tasks.TaskCompleted && oldStatus != tasks.TaskCompleted {
		beforeBlocked = blockedBySnapshot(t.store.List(), req.TaskID)
	}

	if req.HasBlockedBy {
		if err := replaceBlockedBy(t.store, req.TaskID, current.BlockedBy, req.BlockedBy, affected); err != nil {
			return nil, err
		}
	}
	if req.HasBlocks {
		refreshed, err := t.store.Get(req.TaskID)
		if err != nil {
			return nil, err
		}
		if err := replaceBlocks(t.store, req.TaskID, refreshed.Blocks, req.Blocks, affected); err != nil {
			return nil, err
		}
	}

	var updates tasks.TaskUpdate
	if req.Owner != nil {
		owner := strings.TrimSpace(*req.Owner)
		updates.Owner = &owner
	}
	if req.Status != nil {
		status := *req.Status
		updates.Status = &status
	}
	if updates.Owner != nil || updates.Status != nil {
		if _, err := t.store.Update(req.TaskID, updates); err != nil {
			return nil, err
		}
	}

	updated, err := t.store.Get(req.TaskID)
	if err != nil {
		return nil, err
	}

	var unblocked []string
	if beforeBlocked != nil {
		unblocked = newlyUnblocked(t.store.List(), req.TaskID, beforeBlocked)
		for _, id := range unblocked {
			affected[id] = struct{}{}
		}
	}

	t.revision++
	revision := t.revision

	payload := map[string]interface{}{
		"task":     *updated,
		"revision": revision,
	}
	if len(unblocked) > 0 {
		payload["unblocked"] = unblocked
	}
	if len(affected) > 1 {
		payload["affected"] = sortedKeys(affected, req.TaskID)
	}

	return &tool.ToolResult{
		Success: true,
		Output:  formatTaskUpdateOutput(*updated, unblocked, revision),
		Data:    payload,
	}, nil
}

func (t *TaskUpdateTool) Snapshot(taskID string) (tasks.Task, bool) {
	if t == nil || t.store == nil {
		return tasks.Task{}, false
	}
	task, err := t.store.Get(taskID)
	if err != nil || task == nil {
		return tasks.Task{}, false
	}
	return *task, true
}

type taskUpdateRequest struct {
	TaskID       string
	Delete       bool
	Status       *tasks.TaskStatus
	Owner        *string
	Blocks       []string
	HasBlocks    bool
	BlockedBy    []string
	HasBlockedBy bool
}

func parseTaskUpdateParams(params map[string]interface{}) (taskUpdateRequest, error) {
	if params == nil {
		return taskUpdateRequest{}, errors.New("params is nil")
	}
	taskID, err := requiredString(params, "taskId")
	if err != nil {
		return taskUpdateRequest{}, err
	}
	req := taskUpdateRequest{TaskID: taskID}

	if raw, ok := params["delete"]; ok && raw != nil {
		val, ok := raw.(bool)
		if !ok {
			return taskUpdateRequest{}, fmt.Errorf("delete must be boolean, got %T", raw)
		}
		req.Delete = val
	}

	if raw, ok := params["status"]; ok && raw != nil {
		value, err := coerceString(raw)
		if err != nil {
			return taskUpdateRequest{}, fmt.Errorf("status must be string: %w", err)
		}
		normalized, ok := normalizeUpdateStatus(value)
		if !ok {
			return taskUpdateRequest{}, fmt.Errorf("status %q is invalid", strings.TrimSpace(value))
		}
		req.Status = &normalized
	}

	if raw, ok := params["owner"]; ok {
		var owner string
		if raw != nil {
			value, err := coerceString(raw)
			if err != nil {
				return taskUpdateRequest{}, fmt.Errorf("owner must be string: %w", err)
			}
			owner = strings.TrimSpace(value)
		}
		req.Owner = &owner
	}

	if raw, ok := params["blocks"]; ok {
		req.HasBlocks = true
		list, err := parseTaskIDList(raw, "blocks", taskID)
		if err != nil {
			return taskUpdateRequest{}, err
		}
		req.Blocks = list
	}

	if raw, ok := params["blockedBy"]; ok {
		req.HasBlockedBy = true
		list, err := parseTaskIDList(raw, "blockedBy", taskID)
		if err != nil {
			return taskUpdateRequest{}, err
		}
		req.BlockedBy = list
	}

	return req, nil
}

func normalizeUpdateStatus(value string) (tasks.TaskStatus, bool) {
	trimmed := strings.ToLower(strings.TrimSpace(value))
	if trimmed == "" {
		return "", false
	}
	trimmed = strings.ReplaceAll(trimmed, "-", "_")
	switch trimmed {
	case TaskStatusPending:
		return tasks.TaskPending, true
	case TaskStatusInProgress:
		return tasks.TaskInProgress, true
	case TaskStatusCompleted:
		return tasks.TaskCompleted, true
	case "complete", "done":
		return tasks.TaskCompleted, true
	default:
		return "", false
	}
}

func parseTaskIDList(value interface{}, field, selfID string) ([]string, error) {
	if value == nil {
		return nil, nil
	}
	var rawList []interface{}
	switch v := value.(type) {
	case []interface{}:
		rawList = v
	case []string:
		rawList = make([]interface{}, len(v))
		for i := range v {
			rawList[i] = v[i]
		}
	default:
		return nil, fmt.Errorf("%s must be an array, got %T", field, value)
	}

	seen := make(map[string]struct{}, len(rawList))
	out := make([]string, 0, len(rawList))
	for i, raw := range rawList {
		id, err := coerceString(raw)
		if err != nil {
			return nil, fmt.Errorf("%s[%d] must be string: %w", field, i, err)
		}
		id = strings.TrimSpace(id)
		if id == "" {
			return nil, fmt.Errorf("%s[%d] cannot be empty", field, i)
		}
		if id == selfID {
			return nil, fmt.Errorf("%s[%d] cannot reference taskId", field, i)
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	sort.Strings(out)
	return out, nil
}

func replaceBlockedBy(store tasks.Store, taskID string, existing, desired []string, affected map[string]struct{}) error {
	existingSet := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		existingSet[id] = struct{}{}
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	for _, id := range existing {
		if _, ok := desiredSet[id]; ok {
			continue
		}
		if err := store.RemoveDependency(taskID, id); err != nil {
			return err
		}
		affected[taskID] = struct{}{}
		affected[id] = struct{}{}
	}

	for _, id := range desired {
		if _, ok := existingSet[id]; ok {
			continue
		}
		if err := store.AddDependency(taskID, id); err != nil {
			return err
		}
		affected[taskID] = struct{}{}
		affected[id] = struct{}{}
	}

	return nil
}

func replaceBlocks(store tasks.Store, blockerID string, existing, desired []string, affected map[string]struct{}) error {
	existingSet := make(map[string]struct{}, len(existing))
	for _, id := range existing {
		existingSet[id] = struct{}{}
	}
	desiredSet := make(map[string]struct{}, len(desired))
	for _, id := range desired {
		desiredSet[id] = struct{}{}
	}

	for _, id := range existing {
		if _, ok := desiredSet[id]; ok {
			continue
		}
		if err := store.RemoveDependency(id, blockerID); err != nil {
			return err
		}
		affected[blockerID] = struct{}{}
		affected[id] = struct{}{}
	}

	for _, id := range desired {
		if _, ok := existingSet[id]; ok {
			continue
		}
		if err := store.AddDependency(id, blockerID); err != nil {
			return err
		}
		affected[blockerID] = struct{}{}
		affected[id] = struct{}{}
	}
	return nil
}

func blockedBySnapshot(list []*tasks.Task, blockerID string) map[string]tasks.TaskStatus {
	snapshot := make(map[string]tasks.TaskStatus)
	for _, task := range list {
		if task == nil {
			continue
		}
		if slices.Contains(task.BlockedBy, blockerID) {
			snapshot[task.ID] = task.Status
		}
	}
	return snapshot
}

func newlyUnblocked(list []*tasks.Task, blockerID string, before map[string]tasks.TaskStatus) []string {
	if len(before) == 0 {
		return nil
	}
	var out []string
	for _, task := range list {
		if task == nil {
			continue
		}
		prev, ok := before[task.ID]
		if !ok {
			continue
		}
		if prev != tasks.TaskBlocked {
			continue
		}
		if !slices.Contains(task.BlockedBy, blockerID) {
			continue
		}
		if task.Status == tasks.TaskPending {
			out = append(out, task.ID)
		}
	}
	if len(out) == 0 {
		return nil
	}
	sort.Strings(out)
	return out
}

func sortedKeys(set map[string]struct{}, except string) []string {
	if len(set) == 0 {
		return nil
	}
	out := make([]string, 0, len(set))
	for key := range set {
		if key == except {
			continue
		}
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

func formatTaskDeleteOutput(taskID string, revision uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task %s\n", taskID)
	b.WriteString("deleted: true\n")
	fmt.Fprintf(&b, "revision: %d", revision)
	return b.String()
}

func formatTaskUpdateOutput(task tasks.Task, unblocked []string, revision uint64) string {
	var b strings.Builder
	fmt.Fprintf(&b, "task %s\n", task.ID)
	if strings.TrimSpace(task.Subject) != "" {
		fmt.Fprintf(&b, "subject: %s\n", strings.TrimSpace(task.Subject))
	}
	fmt.Fprintf(&b, "status: %s\n", task.Status)
	if strings.TrimSpace(task.Owner) != "" {
		fmt.Fprintf(&b, "owner: %s\n", strings.TrimSpace(task.Owner))
	}

	blockedBy := append([]string(nil), task.BlockedBy...)
	sort.Strings(blockedBy)
	if len(blockedBy) == 0 {
		b.WriteString("blockedBy: (none)\n")
	} else {
		fmt.Fprintf(&b, "blockedBy: %s\n", strings.Join(blockedBy, ", "))
	}

	blocks := append([]string(nil), task.Blocks...)
	sort.Strings(blocks)
	if len(blocks) == 0 {
		b.WriteString("blocks: (none)\n")
	} else {
		fmt.Fprintf(&b, "blocks: %s\n", strings.Join(blocks, ", "))
	}

	if len(unblocked) > 0 {
		fmt.Fprintf(&b, "unblocked: %s\n", strings.Join(unblocked, ", "))
	}
	fmt.Fprintf(&b, "revision: %d", revision)
	return b.String()
}

