package toolbuiltin

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestAsyncTaskManagerStartOutputAndList(t *testing.T) {
	t.Parallel()

	mgr := newAsyncTaskManager()
	if err := mgr.startWithContext(context.Background(), "task1", "printf 'hi'", "", 0); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	task, ok := mgr.lookup("task1")
	if !ok {
		t.Fatalf("task not found")
	}
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not complete")
	}

	out, done, err := mgr.GetOutput("task1")
	if err != nil {
		t.Fatalf("get output failed: %v", err)
	}
	if !done {
		t.Fatalf("expected done task")
	}
	if strings.TrimSpace(out) == "" {
		t.Fatalf("expected output")
	}

	list := mgr.List()
	if len(list) != 1 || list[0].ID != "task1" {
		t.Fatalf("unexpected list %v", list)
	}
}

func TestAsyncTaskManagerErrors(t *testing.T) {
	t.Parallel()

	mgr := newAsyncTaskManager()
	if err := mgr.startWithContext(context.Background(), "", "echo hi", "", 0); err == nil {
		t.Fatalf("expected empty id error")
	}
	if err := mgr.startWithContext(context.Background(), "x", "", "", 0); err == nil {
		t.Fatalf("expected empty command error")
	}
	if _, _, err := mgr.GetOutput("missing"); err == nil {
		t.Fatalf("expected missing task error")
	}
	if err := mgr.Kill("missing"); err == nil {
		t.Fatalf("expected missing kill error")
	}
	mgr.SetMaxOutputLen(0)
	if mgr.maxOutputLen == 0 {
		t.Fatalf("expected default max output len")
	}
}

func TestAsyncTaskManagerStartAndShutdown(t *testing.T) {
	t.Parallel()

	mgr := newAsyncTaskManager()
	if err := mgr.Start("task2", "printf 'ok'"); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	task, ok := mgr.lookup("task2")
	if !ok {
		t.Fatalf("task not found")
	}
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not complete")
	}
	if err := mgr.Shutdown(context.Background()); err != nil {
		t.Fatalf("shutdown failed: %v", err)
	}
}

func TestAsyncTaskManagerOutputFile(t *testing.T) {
	t.Parallel()

	mgr := newAsyncTaskManager()
	mgr.SetMaxOutputLen(1)
	if err := mgr.startWithContext(context.Background(), "task3", "printf 'long-output'", "", 0); err != nil {
		t.Fatalf("start failed: %v", err)
	}
	task, ok := mgr.lookup("task3")
	if !ok {
		t.Fatalf("task not found")
	}
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not complete")
	}
	if path := mgr.OutputFile("task3"); path == "" {
		t.Fatalf("expected output file path")
	}
}

func TestAsyncTaskManagerAdditionalErrors(t *testing.T) {
	var nilMgr *AsyncTaskManager
	if err := nilMgr.startWithContext(context.Background(), "x", "echo hi", "", 0); err == nil {
		t.Fatalf("expected nil manager error")
	}

	mgr := newAsyncTaskManager()
	if err := mgr.startWithContext(context.Background(), "dup", "printf 'hi'", "", 0); err != nil {
		t.Fatalf("start dup failed: %v", err)
	}
	if err := mgr.startWithContext(context.Background(), "dup", "printf 'hi'", "", 0); err == nil {
		t.Fatalf("expected duplicate task error")
	}
	if got := mgr.OutputFile("missing"); got != "" {
		t.Fatalf("expected empty output for missing task")
	}

	mgr2 := newAsyncTaskManager()
	for i := 0; i < maxAsyncTasks; i++ {
		mgr2.tasks[fmt.Sprintf("t%d", i)] = &AsyncTask{ID: fmt.Sprintf("t%d", i), Done: make(chan struct{})}
	}
	if err := mgr2.startWithContext(context.Background(), "overflow", "echo hi", "", 0); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("expected limit error, got %v", err)
	}
}
