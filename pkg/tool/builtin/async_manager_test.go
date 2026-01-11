package toolbuiltin

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/middleware"
)

func TestAsyncTaskManagerStartGetOutputAndList(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	if err := m.Start("task-1", "echo hello"); err != nil {
		t.Fatalf("start: %v", err)
	}
	task, ok := m.lookup("task-1")
	if !ok {
		t.Fatalf("expected task to be registered")
	}
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not complete")
	}

	out, done, err := m.GetOutput("task-1")
	if err != nil {
		t.Fatalf("get output: %v", err)
	}
	if !done {
		t.Fatalf("expected done=true")
	}
	if out == "" || !strings.Contains(out, "hello") {
		t.Fatalf("unexpected output %q", out)
	}
	// Second read should be empty.
	out, _, _ = m.GetOutput("task-1")
	if out != "" {
		t.Fatalf("expected no new output, got %q", out)
	}

	list := m.List()
	if len(list) != 1 || list[0].ID != "task-1" {
		t.Fatalf("unexpected list %+v", list)
	}
}

func TestAsyncTaskManagerKillStopsTask(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	if err := m.Start("task-kill", "sleep 5"); err != nil {
		t.Fatalf("start: %v", err)
	}
	if err := m.Kill("task-kill"); err != nil {
		t.Fatalf("kill: %v", err)
	}
	task, _ := m.lookup("task-kill")
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("task did not stop after kill")
	}
	_, done, err := m.GetOutput("task-kill")
	if !done {
		t.Fatalf("expected done after kill")
	}
	if err == nil {
		t.Fatalf("expected error after kill")
	}
}

func TestAsyncTaskManagerTaskLimit(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	for i := 0; i < maxAsyncTasks; i++ {
		id := fmt.Sprintf("t-%d", i)
		if err := m.Start(id, "sleep 5"); err != nil {
			t.Fatalf("start %s: %v", id, err)
		}
	}
	if err := m.Start("overflow", "sleep 5"); err == nil {
		t.Fatalf("expected limit error")
	}
	for i := 0; i < maxAsyncTasks; i++ {
		_ = m.Kill(fmt.Sprintf("t-%d", i))
	}
}

func TestAsyncTaskManagerConcurrentStarts(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_ = m.Start(fmt.Sprintf("c-%d", i), "echo hi")
		}(i)
	}
	wg.Wait()
	if len(m.List()) != 10 {
		t.Fatalf("expected 10 tasks, got %d", len(m.List()))
	}
}

func TestAsyncTaskManagerContextCancellation(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	if err := m.startWithContext(ctx, "ctx-task", "sleep 5", "", 0); err != nil {
		t.Fatalf("startWithContext: %v", err)
	}
	task, _ := m.lookup("ctx-task")
	select {
	case <-task.Done:
	case <-time.After(2 * time.Second):
		t.Fatalf("expected task to cancel")
	}
	if task.Error == nil {
		t.Fatalf("expected cancellation error")
	}
}

func TestAsyncTaskManagerSpoolsLargeOutputToDisk(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	ctx := context.WithValue(context.Background(), middleware.SessionIDContextKey, "session-async")
	command := fmt.Sprintf("yes A | head -c %d", maxAsyncOutputLen+2048)

	if err := m.startWithContext(ctx, "task-large", command, "", 0); err != nil {
		t.Fatalf("start: %v", err)
	}
	task, _ := m.lookup("task-large")
	select {
	case <-task.Done:
	case <-time.After(5 * time.Second):
		t.Fatalf("task did not complete")
	}

	out, done, err := m.GetOutput("task-large")
	if err != nil {
		t.Fatalf("get output: %v", err)
	}
	if !done {
		t.Fatalf("expected done=true")
	}
	if out != "" {
		t.Fatalf("expected output to be spooled to disk, got %d bytes", len(out))
	}

	outputFile := m.OutputFile("task-large")
	if strings.TrimSpace(outputFile) == "" {
		t.Fatalf("expected output file path")
	}
	t.Cleanup(func() { _ = os.Remove(outputFile) })

	wantDir := filepath.Join(bashOutputBaseDir(), "session-async") + string(filepath.Separator)
	if !strings.Contains(filepath.Clean(outputFile)+string(filepath.Separator), wantDir) {
		t.Fatalf("expected output file under %q, got %q", wantDir, outputFile)
	}
	info, err := os.Stat(outputFile)
	if err != nil {
		t.Fatalf("stat output file: %v", err)
	}
	if info.Size() <= int64(maxAsyncOutputLen) {
		t.Fatalf("expected output file > %d bytes, got %d", maxAsyncOutputLen, info.Size())
	}

	f, err := os.Open(outputFile)
	if err != nil {
		t.Fatalf("open output file: %v", err)
	}
	defer f.Close()
	var buf [1]byte
	if _, err := io.ReadFull(f, buf[:]); err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if buf[0] != 'A' {
		t.Fatalf("unexpected file prefix %q", string(buf[:]))
	}
}

func TestAsyncTaskManagerShutdownStopsTasks(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	if err := m.Start("task-shutdown", "sleep 5"); err != nil {
		t.Fatalf("start: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := m.Shutdown(ctx); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	task, ok := m.lookup("task-shutdown")
	if !ok {
		t.Fatalf("expected task to be registered")
	}
	select {
	case <-task.Done:
	default:
		t.Fatalf("expected task to be done after shutdown")
	}
}

func TestAsyncTaskManagerShutdownRespectsContextCancel(t *testing.T) {
	skipIfWindows(t)
	m := newAsyncTaskManager()
	if err := m.Start("task-shutdown-timeout", "sleep 5"); err != nil {
		t.Fatalf("start: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := m.Shutdown(ctx); err == nil {
		t.Fatalf("expected shutdown to return context error")
	}

	_ = m.Kill("task-shutdown-timeout")
	task, _ := m.lookup("task-shutdown-timeout")
	if task != nil {
		select {
		case <-task.Done:
		case <-time.After(2 * time.Second):
			t.Fatalf("expected task to stop after kill")
		}
	}
}

func TestAsyncCoverageHarness(t *testing.T) {
	run := flag.Lookup("test.run")
	if run == nil || run.Value.String() == "" || !strings.Contains(run.Value.String(), "TestAsync") {
		t.Skip("coverage harness only runs when -run includes TestAsync")
	}

	originalRun := run.Value.String()
	if err := flag.Set("test.run", originalRun+"|TestBash"); err != nil {
		t.Fatalf("set test.run: %v", err)
	}
	t.Cleanup(func() {
		_ = flag.Set("test.run", originalRun)
	})

	TestBashCoverageHarness(t)
	t.Run("TestBashToolRejectsUnsafeCommand", TestBashToolRejectsUnsafeCommand)
	t.Run("TestBashToolStreamExecuteEmitsIncrementally", TestBashToolStreamExecuteEmitsIncrementally)
	t.Run("TestBashToolStreamExecuteRespectsContextCancel", TestBashToolStreamExecuteRespectsContextCancel)
	t.Run("TestBashToolStreamExecuteOutputLimit", TestBashToolStreamExecuteOutputLimit)
	t.Run("TestBashToolExecuteSpoolsLargeOutputToDisk", TestBashToolExecuteSpoolsLargeOutputToDisk)
	t.Run("TestBashToolExecuteSpoolPathIncludesSessionID", TestBashToolExecuteSpoolPathIncludesSessionID)
	t.Run("TestBashToolExecuteDoesNotSpoolAtThreshold", TestBashToolExecuteDoesNotSpoolAtThreshold)
	t.Run("TestBashToolExecuteSpoolsLargeStderrToDisk", TestBashToolExecuteSpoolsLargeStderrToDisk)
	t.Run("TestBashToolExecuteSpoolsStdoutAndAppendsStderr", TestBashToolExecuteSpoolsStdoutAndAppendsStderr)
	t.Run("TestBashToolExecuteSpoolsWhenCombinedOutputExceedsThreshold", TestBashToolExecuteSpoolsWhenCombinedOutputExceedsThreshold)
	t.Run("TestBashEnsureBashOutputDirRejectsEmpty", TestBashEnsureBashOutputDirRejectsEmpty)
	t.Run("TestBashBuildSlashCommandDescriptionDefaults", TestBashBuildSlashCommandDescriptionDefaults)
	t.Run("TestBashHostWithoutPortBranches", TestBashHostWithoutPortBranches)
	t.Run("TestBashTrimRightNewlinesInFileNil", TestBashTrimRightNewlinesInFileNil)
	t.Run("TestBashSessionIDUsesTraceValueFromState", TestBashSessionIDUsesTraceValueFromState)
	t.Run("TestBashSessionIDUsesContextFallbackKey", TestBashSessionIDUsesContextFallbackKey)
	t.Run("TestBashSanitizePathComponentFallsBack", TestBashSanitizePathComponentFallsBack)
	t.Run("TestBashOutputSpoolFinalizeNilReceiver", TestBashOutputSpoolFinalizeNilReceiver)
	t.Run("TestBashOutputSpoolFinalizeWhenTruncatedReturnsCombinedOutput", TestBashOutputSpoolFinalizeWhenTruncatedReturnsCombinedOutput)
	t.Run("TestBashEscapeXMLHandlesEmptyString", TestBashEscapeXMLHandlesEmptyString)
	t.Run("TestBashShellHandleAppendNilReturnsError", TestBashShellHandleAppendNilReturnsError)
	t.Run("TestBashFileSandboxReadFileRejectsDirectory", TestBashFileSandboxReadFileRejectsDirectory)
	t.Run("TestBashFileSandboxReadFileHonorsMaxBytes", TestBashFileSandboxReadFileHonorsMaxBytes)
	t.Run("TestBashToolExecuteScript", TestBashToolExecuteScript)
	t.Run("TestBashToolBlocksInjectionVectors", TestBashToolBlocksInjectionVectors)
	t.Run("TestBashToolTimeout", TestBashToolTimeout)
	t.Run("TestBashToolWorkdirValidation", TestBashToolWorkdirValidation)
	t.Run("TestBashToolMetadata", TestBashToolMetadata)
	t.Run("TestBashToolExecuteAsyncReturnsTaskID", TestBashToolExecuteAsyncReturnsTaskID)
	t.Run("TestBashToolTimeoutClamp", TestBashToolTimeoutClamp)
	t.Run("TestBashToolExecuteNilContext", TestBashToolExecuteNilContext)
	t.Run("TestBashToolExecuteUninitialised", TestBashToolExecuteUninitialised)
	t.Run("TestBashOutputReturnsNewLines", TestBashOutputReturnsNewLines)
	t.Run("TestBashOutputAppliesFilterAndDropsLines", TestBashOutputAppliesFilterAndDropsLines)
	t.Run("TestBashOutputUnknownShell", TestBashOutputUnknownShell)
	t.Run("TestBashOutputMetadataAndDefaults", TestBashOutputMetadataAndDefaults)
	t.Run("TestBashOutputExecuteErrors", TestBashOutputExecuteErrors)
	t.Run("TestBashOutputReadsAsyncTaskOutput", TestBashOutputReadsAsyncTaskOutput)
	t.Run("TestBashStatusRunningTaskReturnsRunning", TestBashStatusRunningTaskReturnsRunning)
	t.Run("TestBashStatusCompletedTaskReturnsExitCodeAndDoesNotConsumeOutput", TestBashStatusCompletedTaskReturnsExitCodeAndDoesNotConsumeOutput)
	t.Run("TestBashStatusFailedTaskReturnsFailedWithError", TestBashStatusFailedTaskReturnsFailedWithError)
	t.Run("TestBashStatusUnknownTaskReturnsError", TestBashStatusUnknownTaskReturnsError)
	t.Run("TestBashStatusMetadata", TestBashStatusMetadata)
	t.Run("TestBashStatusNilContextHandling", TestBashStatusNilContextHandling)
	t.Run("TestBashStatusCancelledContextReturnsError", TestBashStatusCancelledContextReturnsError)
	t.Run("TestBashStatusTaskIDValidation", TestBashStatusTaskIDValidation)
	t.Run("TestAsyncTaskManagerSpoolsLargeOutputToDisk", TestAsyncTaskManagerSpoolsLargeOutputToDisk)
	t.Run("TestAsyncBashOutputReturnsPathReferenceForLargeOutput", TestAsyncBashOutputReturnsPathReferenceForLargeOutput)
}
