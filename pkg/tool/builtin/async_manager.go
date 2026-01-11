package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	maxAsyncTasks     = 50
	maxAsyncOutputLen = 1024 * 1024 // 1MB
)

// AsyncTask represents a single async bash invocation.
type AsyncTask struct {
	ID        string
	Command   string
	StartTime time.Time
	Done      chan struct{}
	Error     error

	mu       sync.Mutex
	output   *tool.SpoolWriter
	consumed int
	cancel   context.CancelFunc
	cmd      *exec.Cmd
}

// AsyncTaskInfo is a lightweight snapshot used by List().
type AsyncTaskInfo struct {
	ID        string    `json:"id"`
	Command   string    `json:"command"`
	Status    string    `json:"status"`
	StartTime time.Time `json:"start_time"`
	Error     string    `json:"error,omitempty"`
}

// AsyncTaskManager tracks and manages async bash tasks.
type AsyncTaskManager struct {
	mu           sync.RWMutex
	tasks        map[string]*AsyncTask
	maxOutputLen int
}

var defaultAsyncTaskManager = newAsyncTaskManager()

// DefaultAsyncTaskManager returns the global async task manager.
func DefaultAsyncTaskManager() *AsyncTaskManager {
	return defaultAsyncTaskManager
}

func newAsyncTaskManager() *AsyncTaskManager {
	return &AsyncTaskManager{
		tasks:        map[string]*AsyncTask{},
		maxOutputLen: maxAsyncOutputLen,
	}
}

func (m *AsyncTaskManager) SetMaxOutputLen(maxLen int) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if maxLen <= 0 {
		m.maxOutputLen = maxAsyncOutputLen
	} else {
		m.maxOutputLen = maxLen
	}
	m.mu.Unlock()
}

// Start launches a task in the background using a detached context.
func (m *AsyncTaskManager) Start(id, command string) error {
	return m.startWithContext(context.Background(), id, command, "", 0)
}

func (m *AsyncTaskManager) startWithContext(ctx context.Context, id, command, workdir string, timeout time.Duration) error {
	if m == nil {
		return errors.New("async task manager is nil")
	}
	trimmedID := strings.TrimSpace(id)
	if trimmedID == "" {
		return errors.New("task id cannot be empty")
	}
	trimmedCmd := strings.TrimSpace(command)
	if trimmedCmd == "" {
		return errors.New("command cannot be empty")
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.Lock()
	if m.tasks == nil {
		m.tasks = map[string]*AsyncTask{}
	}
	if _, exists := m.tasks[trimmedID]; exists {
		m.mu.Unlock()
		return fmt.Errorf("task %s already exists", trimmedID)
	}
	if m.runningCountLocked() >= maxAsyncTasks {
		m.mu.Unlock()
		return fmt.Errorf("async task limit reached (%d)", maxAsyncTasks)
	}
	task := &AsyncTask{
		ID:        trimmedID,
		Command:   trimmedCmd,
		StartTime: time.Now(),
		Done:      make(chan struct{}),
	}
	threshold := m.maxOutputLen
	if threshold <= 0 {
		threshold = maxAsyncOutputLen
	}
	task.output = tool.NewSpoolWriter(threshold, func() (io.WriteCloser, string, error) {
		sessionID := bashSessionID(ctx)
		dir := filepath.Join(bashOutputBaseDir(), sanitizePathComponent(sessionID))
		outputPath := filepath.Join(dir, bashOutputFilename())
		return openBashOutputFile(outputPath)
	})
	m.tasks[trimmedID] = task
	m.mu.Unlock()

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
	} else {
		execCtx, cancel = context.WithCancel(ctx)
	}

	task.mu.Lock()
	task.cancel = cancel
	task.mu.Unlock()

	cmd := exec.CommandContext(execCtx, "bash", "-c", trimmedCmd)
	cmd.Env = os.Environ()
	if strings.TrimSpace(workdir) != "" {
		cmd.Dir = workdir
	}
	cmd.Stdout = task.output
	cmd.Stderr = task.output

	if err := cmd.Start(); err != nil {
		cancel()
		_ = task.output.Close()
		m.mu.Lock()
		delete(m.tasks, trimmedID)
		m.mu.Unlock()
		return fmt.Errorf("start task: %w", err)
	}

	task.mu.Lock()
	task.cmd = cmd
	task.mu.Unlock()

	go func() {
		err := cmd.Wait()
		_ = task.output.Close()
		task.mu.Lock()
		task.Error = err
		task.mu.Unlock()
		cancel()
		close(task.Done)
	}()

	return nil
}

// GetOutput returns incremental output since last read, whether the task is done, and any task error.
func (m *AsyncTaskManager) GetOutput(id string) (string, bool, error) {
	task, ok := m.lookup(strings.TrimSpace(id))
	if !ok {
		return "", false, fmt.Errorf("task %s not found", strings.TrimSpace(id))
	}
	done := isDone(task.Done)
	task.mu.Lock()
	defer task.mu.Unlock()
	if task.output == nil {
		return "", done, task.Error
	}
	if path := task.output.Path(); path != "" {
		return "", done, task.Error
	}
	data := task.output.String()
	if task.consumed >= len(data) {
		return "", done, task.Error
	}
	chunk := data[task.consumed:]
	task.consumed = len(data)
	return chunk, done, task.Error
}

func (m *AsyncTaskManager) OutputFile(id string) string {
	task, ok := m.lookup(strings.TrimSpace(id))
	if !ok {
		return ""
	}
	task.mu.Lock()
	writer := task.output
	task.mu.Unlock()
	if writer == nil {
		return ""
	}
	return writer.Path()
}

// Kill terminates a running task.
func (m *AsyncTaskManager) Kill(id string) error {
	task, ok := m.lookup(strings.TrimSpace(id))
	if !ok {
		return fmt.Errorf("task %s not found", strings.TrimSpace(id))
	}
	task.mu.Lock()
	cancel := task.cancel
	cmd := task.cmd
	done := isDone(task.Done)
	task.mu.Unlock()

	if done {
		return nil
	}
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		if err := cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
			log.Printf("async task %s kill: %v", id, err)
		}
	}
	return nil
}

// List reports all known tasks with their status.
func (m *AsyncTaskManager) List() []AsyncTaskInfo {
	if m == nil {
		return nil
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]AsyncTaskInfo, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task == nil {
			continue
		}
		done := isDone(task.Done)
		task.mu.Lock()
		err := task.Error
		cmd := task.Command
		start := task.StartTime
		id := task.ID
		task.mu.Unlock()
		status := "running"
		if done {
			if err != nil {
				status = "failed"
			} else {
				status = "completed"
			}
		}
		info := AsyncTaskInfo{
			ID:        id,
			Command:   cmd,
			Status:    status,
			StartTime: start,
		}
		if err != nil {
			info.Error = err.Error()
		}
		out = append(out, info)
	}
	return out
}

// Shutdown attempts to terminate all tasks and waits for completion.
// It is best-effort: any kill errors are logged, and the method blocks until
// every task signals Done or the context is cancelled.
func (m *AsyncTaskManager) Shutdown(ctx context.Context) error {
	if m == nil {
		return nil
	}
	if ctx == nil {
		ctx = context.Background()
	}

	m.mu.RLock()
	tasks := make([]*AsyncTask, 0, len(m.tasks))
	for _, task := range m.tasks {
		if task != nil {
			tasks = append(tasks, task)
		}
	}
	m.mu.RUnlock()

	for _, task := range tasks {
		if task == nil {
			continue
		}
		if err := m.Kill(task.ID); err != nil {
			log.Printf("async task %s shutdown kill: %v", task.ID, err)
		}
	}

	var errs []error
	for _, task := range tasks {
		if task == nil || task.Done == nil {
			continue
		}
		select {
		case <-task.Done:
		case <-ctx.Done():
			errs = append(errs, ctx.Err())
			return errors.Join(errs...)
		}
	}
	return errors.Join(errs...)
}

func (m *AsyncTaskManager) lookup(id string) (*AsyncTask, bool) {
	if m == nil || id == "" {
		return nil, false
	}
	m.mu.RLock()
	defer m.mu.RUnlock()
	task, ok := m.tasks[id]
	return task, ok
}

func (m *AsyncTaskManager) runningCountLocked() int {
	count := 0
	for _, task := range m.tasks {
		if task == nil {
			continue
		}
		if !isDone(task.Done) {
			count++
		}
	}
	return count
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
