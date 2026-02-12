package tasks

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"
)

var (
	ErrEmptySubject      = errors.New("tasks: subject is required")
	ErrInvalidTaskID     = errors.New("tasks: invalid task id")
	ErrTaskNotFound      = errors.New("tasks: task not found")
	ErrInvalidTaskStatus = errors.New("tasks: invalid task status")
	ErrTaskBlocked       = errors.New("tasks: task is blocked by incomplete dependencies")
)

// Store defines the task storage contract used by builtin task tools.
type Store interface {
	Create(subject, description, activeForm string) (*Task, error)
	Get(id string) (*Task, error)
	Update(id string, updates TaskUpdate) (*Task, error)
	List() []*Task
	Delete(id string) error
	AddDependency(taskID, blockedByID string) error
	RemoveDependency(taskID, blockedByID string) error
	GetBlockedTasks(taskID string) []*Task
	GetBlockingTasks(taskID string) []*Task
	Close() error
}

type TaskUpdate struct {
	Subject     *string
	Description *string
	ActiveForm  *string
	Status      *TaskStatus
	Owner       *string
}

type TaskStore struct {
	mu    sync.RWMutex
	tasks map[string]*Task
	order []string // 保持插入顺序
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: map[string]*Task{},
	}
}

// NewTaskStoreFromSnapshot creates a store from an external snapshot.
// Invalid entries (empty IDs) are skipped.
func NewTaskStoreFromSnapshot(snapshot []*Task) *TaskStore {
	store := NewTaskStore()
	if len(snapshot) == 0 {
		return store
	}

	store.mu.Lock()
	defer store.mu.Unlock()
	store.initLocked()

	for _, task := range snapshot {
		if task == nil {
			continue
		}
		id := strings.TrimSpace(task.ID)
		if id == "" {
			continue
		}
		if _, exists := store.tasks[id]; exists {
			continue
		}
		dup := cloneTask(task)
		dup.ID = id
		store.tasks[id] = dup
		store.order = append(store.order, id)
	}
	return store
}

// Close releases persistence resources.
func (s *TaskStore) Close() error {
	return nil
}

func (s *TaskStore) Create(subject, description, activeForm string) (*Task, error) {
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return nil, ErrEmptySubject
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	s.initLocked()

	id, err := s.uniqueIDLocked()
	if err != nil {
		return nil, err
	}

	task := &Task{
		ID:          id,
		Subject:     subject,
		Description: strings.TrimSpace(description),
		ActiveForm:  strings.TrimSpace(activeForm),
		Status:      TaskPending,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	s.tasks[id] = task
	s.order = append(s.order, id)

	return cloneTask(task), nil
}

func (s *TaskStore) Get(id string) (*Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrInvalidTaskID
	}

	s.mu.RLock()
	defer s.mu.RUnlock()
	task := s.tasks[id]
	if task == nil {
		return nil, ErrTaskNotFound
	}
	return cloneTask(task), nil
}

func (s *TaskStore) Update(id string, updates TaskUpdate) (*Task, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return nil, ErrInvalidTaskID
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()
	task := s.tasks[id]
	if task == nil {
		return nil, ErrTaskNotFound
	}

	if updates.Subject != nil {
		subject := strings.TrimSpace(*updates.Subject)
		if subject == "" {
			return nil, ErrEmptySubject
		}
		task.Subject = subject
	}
	if updates.Description != nil {
		task.Description = strings.TrimSpace(*updates.Description)
	}
	if updates.ActiveForm != nil {
		task.ActiveForm = strings.TrimSpace(*updates.ActiveForm)
	}
	if updates.Owner != nil {
		task.Owner = strings.TrimSpace(*updates.Owner)
	}

	previousStatus := task.Status
	if updates.Status != nil {
		status := *updates.Status
		if !validStatus(status) {
			return nil, ErrInvalidTaskStatus
		}
		if (status == TaskInProgress || status == TaskCompleted) && s.hasIncompleteBlockersLocked(task) {
			return nil, ErrTaskBlocked
		}
		task.Status = status
		s.reconcileBlockedStatusLocked(task)
	}

	task.UpdatedAt = now

	if previousStatus != TaskCompleted && task.Status == TaskCompleted {
		s.onTaskCompleted(id)
	} else if previousStatus == TaskCompleted && task.Status != TaskCompleted {
		s.onTaskStatusChangedLocked(task.ID, now)
	}

	return cloneTask(task), nil
}

func (s *TaskStore) List() []*Task {
	s.mu.RLock()
	defer s.mu.RUnlock()

	list := make([]*Task, 0, len(s.order))
	for _, id := range s.order {
		task := s.tasks[id]
		if task == nil {
			continue
		}
		list = append(list, cloneTask(task))
	}
	return list
}

func (s *TaskStore) Delete(id string) error {
	id = strings.TrimSpace(id)
	if id == "" {
		return ErrInvalidTaskID
	}

	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	task := s.tasks[id]
	if task == nil {
		return ErrTaskNotFound
	}

	for _, blockerID := range task.BlockedBy {
		blocker := s.tasks[blockerID]
		if blocker == nil {
			continue
		}
		blocker.Blocks = removeString(blocker.Blocks, id)
		blocker.UpdatedAt = now
	}

	for _, blockedID := range task.Blocks {
		blocked := s.tasks[blockedID]
		if blocked == nil {
			continue
		}
		blocked.BlockedBy = removeString(blocked.BlockedBy, id)
		s.reconcileBlockedStatusLocked(blocked)
		blocked.UpdatedAt = now
	}

	delete(s.tasks, id)
	s.order = removeString(s.order, id)
	return nil
}

func (s *TaskStore) initLocked() {
	if s.tasks == nil {
		s.tasks = map[string]*Task{}
	}
}

func (s *TaskStore) uniqueIDLocked() (string, error) {
	for attempts := 0; attempts < 16; attempts++ {
		id, err := newTaskID()
		if err != nil {
			return "", err
		}
		if _, exists := s.tasks[id]; !exists {
			return id, nil
		}
	}
	return "", errors.New("tasks: failed to allocate unique id")
}

func newTaskID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("tasks: generate id: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

func cloneTask(task *Task) *Task {
	if task == nil {
		return nil
	}
	dup := *task
	dup.Blocks = cloneStrings(task.Blocks)
	dup.BlockedBy = cloneStrings(task.BlockedBy)
	return &dup
}

func cloneStrings(ids []string) []string {
	if len(ids) == 0 {
		return nil
	}
	dup := make([]string, len(ids))
	copy(dup, ids)
	return dup
}

func validStatus(status TaskStatus) bool {
	switch status {
	case TaskPending, TaskInProgress, TaskCompleted, TaskBlocked:
		return true
	default:
		return false
	}
}

func (s *TaskStore) hasIncompleteBlockersLocked(task *Task) bool {
	if task == nil || len(task.BlockedBy) == 0 {
		return false
	}
	for _, blockerID := range task.BlockedBy {
		blocker := s.tasks[blockerID]
		if blocker == nil {
			continue
		}
		if blocker.Status != TaskCompleted {
			return true
		}
	}
	return false
}

func (s *TaskStore) reconcileBlockedStatusLocked(task *Task) {
	if task == nil || task.Status == TaskCompleted {
		return
	}
	if s.hasIncompleteBlockersLocked(task) {
		task.Status = TaskBlocked
		return
	}
	if task.Status == TaskBlocked {
		task.Status = TaskPending
	}
}

func removeString(list []string, target string) []string {
	for i, value := range list {
		if value == target {
			return append(list[:i], list[i+1:]...)
		}
	}
	return list
}

// Snapshot returns a deep copy of all tasks preserving insertion order.
func (s *TaskStore) Snapshot() []*Task {
	return s.List()
}
