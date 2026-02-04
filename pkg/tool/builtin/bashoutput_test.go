package toolbuiltin

import (
	"context"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestBashOutputToolAsync(t *testing.T) {
	t.Parallel()

	manager := DefaultAsyncTaskManager()
	taskID := "bash-output-test"
	_ = manager.Kill(taskID)

	if err := manager.startWithContext(context.Background(), taskID, "printf 'hello'", "", 0); err != nil {
		t.Fatalf("start async task: %v", err)
	}
	task, ok := manager.lookup(taskID)
	if !ok {
		t.Fatalf("task not found")
	}
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not complete")
	}

	tool := NewBashOutputTool(nil)
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"task_id": taskID,
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success || !strings.Contains(res.Output, "hello") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestBashOutputParsing(t *testing.T) {
	t.Parallel()

	if _, _, err := parseOutputID(nil); err == nil {
		t.Fatalf("expected params error")
	}
	if _, _, err := parseOutputID(map[string]interface{}{"bash_id": ""}); err == nil {
		t.Fatalf("expected empty bash_id error")
	}
	if _, err := parseFilter(map[string]interface{}{"filter": "["}); err == nil {
		t.Fatalf("expected invalid regex error")
	}
	re, err := parseFilter(map[string]interface{}{"filter": "foo"})
	if err != nil || re == nil || re.String() != regexp.MustCompile("foo").String() {
		t.Fatalf("unexpected filter %v err=%v", re, err)
	}
}

func TestShellStoreConsume(t *testing.T) {
	t.Parallel()

	store := newShellStore()
	handle, err := store.Register("shell1")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := handle.Append(ShellStreamStdout, "line1\nline2\n"); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := store.Close("shell1", 0); err != nil {
		t.Fatalf("close failed: %v", err)
	}

	read, err := store.Consume("shell1", nil)
	if err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	if read.Status != ShellStatusCompleted || len(read.Lines) != 2 {
		t.Fatalf("unexpected read %+v", read)
	}

	tool := NewBashOutputTool(store)
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"bash_id": "shell1",
	})
	if err != nil {
		t.Fatalf("execute shell read failed: %v", err)
	}
	if !res.Success || !strings.Contains(res.Output, "shell shell1") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestShellStoreErrorsAndFilter(t *testing.T) {
	t.Parallel()

	store := newShellStore()
	if _, err := store.Register(" "); err == nil {
		t.Fatalf("expected empty id error")
	}
	handle, err := store.Register("shell2")
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, err := store.Register("shell2"); err == nil {
		t.Fatalf("expected duplicate error")
	}
	if err := handle.Append(ShellStreamStdout, "keep\nskip\n"); err != nil {
		t.Fatalf("append failed: %v", err)
	}
	if err := store.Fail("shell2", nil); err != nil {
		t.Fatalf("fail failed: %v", err)
	}
	read, err := store.Consume("shell2", regexp.MustCompile("^keep$"))
	if err != nil {
		t.Fatalf("consume failed: %v", err)
	}
	if read.Dropped == 0 || len(read.Lines) != 1 {
		t.Fatalf("unexpected filtered read %+v", read)
	}
}

func TestShellHandleNilErrors(t *testing.T) {
	var handle *ShellHandle
	if err := handle.Append(ShellStreamStdout, "x"); err == nil {
		t.Fatalf("expected append error for nil handle")
	}
	if err := handle.Close(0); err == nil {
		t.Fatalf("expected close error for nil handle")
	}
	if err := handle.Fail(nil); err == nil {
		t.Fatalf("expected fail error for nil handle")
	}
}

func TestSplitLinesVariants(t *testing.T) {
	if got := splitLines(""); got != nil {
		t.Fatalf("expected nil for empty input")
	}
	lines := splitLines("a\r\nb\r\n")
	if len(lines) != 2 || lines[0] != "a" || lines[1] != "b" {
		t.Fatalf("unexpected lines %v", lines)
	}
	lines = splitLines("x\n")
	if len(lines) != 1 || lines[0] != "x" {
		t.Fatalf("unexpected lines %v", lines)
	}
}

func TestShellStoreAppendResetsStatus(t *testing.T) {
	store := newShellStore()
	handle, err := store.Register("shell-reset")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := handle.Close(7); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := store.Append("shell-reset", ShellStreamStdout, "line"); err != nil {
		t.Fatalf("append: %v", err)
	}
	read, err := store.Consume("shell-reset", nil)
	if err != nil {
		t.Fatalf("consume: %v", err)
	}
	if read.Status != ShellStatusRunning || read.ExitCode != 0 {
		t.Fatalf("expected status running reset, got %+v", read)
	}
}
