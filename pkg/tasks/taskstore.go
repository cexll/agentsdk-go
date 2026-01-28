package tasks

import (
	"errors"
	"fmt"
	"strings"
	"sync"
)

// Task is a lightweight record representing a unit of work tracked by the task system.
type Task struct {
	ID          string
	Subject     string
	Description string
	ActiveForm  string
}

// TaskStore is a minimal in-memory task store.
//
// It is intentionally small: enough to support TaskCreate and unblock further task-system work.
type TaskStore struct {
	mu     sync.Mutex
	tasks  map[string]Task
	nextID uint64
}

func NewTaskStore() *TaskStore {
	return &TaskStore{
		tasks: make(map[string]Task),
	}
}

func (s *TaskStore) CreateTask(subject, description, activeForm string) (string, error) {
	if s == nil {
		return "", errors.New("task store is nil")
	}
	subject = strings.TrimSpace(subject)
	if subject == "" {
		return "", errors.New("subject cannot be empty")
	}
	activeForm = strings.TrimSpace(activeForm)
	if activeForm == "" {
		return "", errors.New("activeForm cannot be empty")
	}
	description = strings.TrimSpace(description)

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.tasks == nil {
		s.tasks = make(map[string]Task)
	}

	s.nextID++
	id := fmt.Sprintf("task-%d", s.nextID)
	s.tasks[id] = Task{
		ID:          id,
		Subject:     subject,
		Description: description,
		ActiveForm:  activeForm,
	}
	return id, nil
}

func (s *TaskStore) GetTask(id string) (Task, bool) {
	if s == nil {
		return Task{}, false
	}
	id = strings.TrimSpace(id)
	if id == "" {
		return Task{}, false
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	task, ok := s.tasks[id]
	return task, ok
}

func (s *TaskStore) Len() int {
	if s == nil {
		return 0
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.tasks)
}
