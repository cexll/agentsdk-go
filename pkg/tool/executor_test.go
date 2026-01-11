package tool

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

type stubTool struct {
	name   string
	delay  time.Duration
	mutate bool
	called int32
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub" }
func (s *stubTool) Schema() *JSONSchema { return nil }
func (s *stubTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	atomic.AddInt32(&s.called, 1)
	if s.delay > 0 {
		time.Sleep(s.delay)
	}
	if s.mutate {
		params["patched"] = true
		if nested, ok := params["nested"].(map[string]any); ok {
			nested["z"] = 99
		}
	}
	return &ToolResult{Success: true, Output: "ok"}, nil
}

type streamingStubTool struct {
	name     string
	streamed int32
	executed int32
}

func (s *streamingStubTool) Name() string        { return s.name }
func (s *streamingStubTool) Description() string { return "stream stub" }
func (s *streamingStubTool) Schema() *JSONSchema { return nil }
func (s *streamingStubTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	atomic.AddInt32(&s.executed, 1)
	return &ToolResult{Success: true, Output: "execute"}, nil
}

func (s *streamingStubTool) StreamExecute(ctx context.Context, params map[string]interface{}, emit func(chunk string, isStderr bool)) (*ToolResult, error) {
	atomic.AddInt32(&s.streamed, 1)
	if emit != nil {
		emit("out", false)
		emit("err", true)
	}
	return &ToolResult{Success: true, Output: "stream"}, nil
}

type fixedOutputTool struct {
	name   string
	output string
	ref    *OutputRef
}

func (f *fixedOutputTool) Name() string        { return f.name }
func (f *fixedOutputTool) Description() string { return "fixed output" }
func (f *fixedOutputTool) Schema() *JSONSchema { return nil }
func (f *fixedOutputTool) Execute(context.Context, map[string]interface{}) (*ToolResult, error) {
	return &ToolResult{Success: true, Output: f.output, OutputRef: f.ref}, nil
}

type fakeFSPolicy struct {
	last string
	err  error
}

func (f *fakeFSPolicy) Allow(path string) {}
func (f *fakeFSPolicy) Roots() []string   { return nil }
func (f *fakeFSPolicy) Validate(path string) error {
	f.last = path
	return f.err
}

func TestExecutorEnforcesSandbox(t *testing.T) {
	reg := NewRegistry()
	tool := &stubTool{name: "safe"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	fsPolicy := &fakeFSPolicy{err: sandbox.ErrPathDenied}
	exec := NewExecutor(reg, sandbox.NewManager(fsPolicy, nil, nil))

	_, err := exec.Execute(context.Background(), Call{Name: "safe", Path: "/tmp/blocked"})
	if !errors.Is(err, sandbox.ErrPathDenied) {
		t.Fatalf("expected sandbox error, got %v", err)
	}
	if fsPolicy.last != "/tmp/blocked" {
		t.Fatalf("path not forwarded to sandbox: %s", fsPolicy.last)
	}
}

func TestExecutorUsesStreamExecuteWhenSinkProvided(t *testing.T) {
	reg := NewRegistry()
	tool := &streamingStubTool{name: "streamer"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	exec := NewExecutor(reg, nil)

	var chunks []string
	var errs []bool
	sink := func(chunk string, isStderr bool) {
		chunks = append(chunks, chunk)
		errs = append(errs, isStderr)
	}

	cr, err := exec.Execute(context.Background(), Call{Name: "streamer", StreamSink: sink})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cr.Result == nil || cr.Result.Output != "stream" {
		t.Fatalf("unexpected result: %+v", cr.Result)
	}
	if atomic.LoadInt32(&tool.streamed) != 1 {
		t.Fatalf("expected streaming path")
	}
	if atomic.LoadInt32(&tool.executed) != 0 {
		t.Fatalf("execute should not be used when streaming sink is set")
	}
	if len(chunks) != 2 || chunks[0] != "out" || chunks[1] != "err" {
		t.Fatalf("stream sink not invoked: %+v", chunks)
	}
	if len(errs) != 2 || errs[0] || !errs[1] {
		t.Fatalf("stderr flags incorrect: %+v", errs)
	}
}

func TestExecutorFallsBackToExecuteWithoutSink(t *testing.T) {
	reg := NewRegistry()
	tool := &streamingStubTool{name: "streamer"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	exec := NewExecutor(reg, nil)

	cr, err := exec.Execute(context.Background(), Call{Name: "streamer"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cr.Result == nil || cr.Result.Output != "execute" {
		t.Fatalf("unexpected result: %+v", cr.Result)
	}
	if atomic.LoadInt32(&tool.executed) != 1 {
		t.Fatalf("Execute should run when no sink present")
	}
	if atomic.LoadInt32(&tool.streamed) != 0 {
		t.Fatalf("StreamExecute should not run without sink")
	}
}

func TestExecutorClonesParamsAndPreservesOrder(t *testing.T) {
	reg := NewRegistry()
	tool := &stubTool{name: "echo", delay: 15 * time.Millisecond, mutate: true}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	exec := NewExecutor(reg, nil)

	shared := map[string]any{"x": 1, "nested": map[string]any{"y": 2}}
	calls := []Call{{Name: "echo", Params: shared}, {Name: "echo", Params: shared}}

	results := exec.ExecuteAll(context.Background(), calls)

	if len(results) != 2 {
		t.Fatalf("results len = %d", len(results))
	}
	if atomic.LoadInt32(&tool.called) != 2 {
		t.Fatalf("tool called %d times", tool.called)
	}
	if _, ok := shared["patched"]; ok {
		t.Fatalf("shared map mutated: %+v", shared)
	}
	nested, ok := shared["nested"].(map[string]any)
	if !ok {
		t.Fatalf("expected nested map, got %T", shared["nested"])
	}
	if nested["y"] != 2 {
		t.Fatalf("nested map mutated: %+v", nested)
	}
}

func TestExecutorRejectsEmptyName(t *testing.T) {
	exec := NewExecutor(NewRegistry(), nil)
	if _, err := exec.Execute(context.Background(), Call{}); err == nil {
		t.Fatalf("expected error for empty name")
	}
}

func TestNewExecutorInitialisesRegistry(t *testing.T) {
	exec := NewExecutor(nil, nil)
	if exec.Registry() == nil {
		t.Fatalf("registry should be initialised")
	}
}

func TestCallResultDuration(t *testing.T) {
	start := time.Now()
	cr := CallResult{StartedAt: start, CompletedAt: start.Add(time.Second)}
	if cr.Duration() != time.Second {
		t.Fatalf("unexpected duration %s", cr.Duration())
	}
	if (CallResult{}).Duration() != 0 {
		t.Fatalf("zero timestamps should yield zero duration")
	}
}

func TestWithSandboxReturnsCopy(t *testing.T) {
	exec := NewExecutor(NewRegistry(), nil)
	copy := exec.WithSandbox(sandbox.NewManager(nil, nil, nil))
	if copy == exec || copy.Registry() != exec.Registry() {
		t.Fatalf("expected shallow copy sharing registry")
	}
}

func TestWithSandboxNilReceiverInitialisesExecutor(t *testing.T) {
	var exec *Executor
	copy := exec.WithSandbox(sandbox.NewManager(nil, nil, nil))
	if copy == nil || copy.Registry() == nil {
		t.Fatalf("expected non-nil executor with registry")
	}
}

func TestExecutorExecuteAllRespectsContextCancel(t *testing.T) {
	reg := NewRegistry()
	tool := &stubTool{name: "echo"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}
	exec := NewExecutor(reg, nil)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	results := exec.ExecuteAll(ctx, []Call{{Name: "echo"}})
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if !errors.Is(results[0].Err, context.Canceled) {
		t.Fatalf("expected context cancellation error, got %v", results[0].Err)
	}
	if atomic.LoadInt32(&tool.called) != 0 {
		t.Fatalf("tool should not run when context is cancelled")
	}
}

func TestExecutorExecuteAllNilReceiverPropagatesError(t *testing.T) {
	var exec *Executor
	results := exec.ExecuteAll(context.Background(), []Call{{Name: "echo"}})
	if len(results) != 1 {
		t.Fatalf("expected one result, got %d", len(results))
	}
	if results[0].Err == nil || !strings.Contains(results[0].Err.Error(), "not initialised") {
		t.Fatalf("expected initialisation error, got %v", results[0].Err)
	}
}

func TestCloneValueDeepCopiesSlice(t *testing.T) {
	original := []any{map[string]any{"a": 1}}
	clonedAny := cloneValue(original)
	cloned, ok := clonedAny.([]any)
	if !ok {
		t.Fatalf("expected cloned slice, got %T", clonedAny)
	}
	elem, ok := cloned[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map element, got %T", cloned[0])
	}
	elem["a"] = 5
	origElem, ok := original[0].(map[string]any)
	if !ok {
		t.Fatalf("expected map element in original, got %T", original[0])
	}
	if v, ok := origElem["a"].(int); !ok || v != 1 {
		t.Fatalf("original mutated: %#v", original)
	}
}

func TestExecutorPersistsLargeToolOutput(t *testing.T) {
	reg := NewRegistry()
	impl := &fixedOutputTool{name: "echo", output: strings.Repeat("x", 11)}
	if err := reg.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	persister := &OutputPersister{BaseDir: t.TempDir(), DefaultThresholdBytes: 10}
	exec := NewExecutor(reg, nil).WithOutputPersister(persister)

	cr, err := exec.Execute(context.Background(), Call{Name: "echo", SessionID: "sess"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cr == nil || cr.Result == nil {
		t.Fatalf("expected tool result")
	}
	if cr.Result.OutputRef == nil || strings.TrimSpace(cr.Result.OutputRef.Path) == "" {
		t.Fatalf("expected output ref, got %+v", cr.Result.OutputRef)
	}
	if cr.Result.Output == impl.output {
		t.Fatalf("expected output to be replaced by reference")
	}
	data, err := os.ReadFile(cr.Result.OutputRef.Path)
	if err != nil {
		t.Fatalf("read persisted output: %v", err)
	}
	if string(data) != impl.output {
		t.Fatalf("unexpected persisted output %q", string(data))
	}
}

func TestExecutorPersisterSkipsWhenOutputRefAlreadySet(t *testing.T) {
	reg := NewRegistry()
	ref := &OutputRef{Path: "/tmp/already", SizeBytes: 1, Truncated: false}
	impl := &fixedOutputTool{name: "echo", output: strings.Repeat("x", 11), ref: ref}
	if err := reg.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	persister := &OutputPersister{BaseDir: t.TempDir(), DefaultThresholdBytes: 1}
	exec := NewExecutor(reg, nil).WithOutputPersister(persister)

	cr, err := exec.Execute(context.Background(), Call{Name: "echo", SessionID: "sess"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cr == nil || cr.Result == nil {
		t.Fatalf("expected tool result")
	}
	if cr.Result.OutputRef != ref {
		t.Fatalf("expected output ref to be preserved, got %+v", cr.Result.OutputRef)
	}
}

func TestExecutorIgnoresPersisterFailure(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(base, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file base: %v", err)
	}

	reg := NewRegistry()
	impl := &fixedOutputTool{name: "echo", output: strings.Repeat("x", 11)}
	if err := reg.Register(impl); err != nil {
		t.Fatalf("register: %v", err)
	}
	persister := &OutputPersister{BaseDir: base, DefaultThresholdBytes: 1}
	exec := NewExecutor(reg, nil).WithOutputPersister(persister)

	cr, err := exec.Execute(context.Background(), Call{Name: "echo", SessionID: "sess"})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if cr == nil || cr.Result == nil {
		t.Fatalf("expected tool result")
	}
	if cr.Result.Output != impl.output {
		t.Fatalf("expected output to remain inline, got %q", cr.Result.Output)
	}
	if cr.Result.OutputRef != nil {
		t.Fatalf("expected OutputRef nil on persister failure, got %+v", cr.Result.OutputRef)
	}
}

func TestExecutorPersisterIsolatesDirectoriesPerToolName(t *testing.T) {
	base := t.TempDir()
	persister := &OutputPersister{BaseDir: base, DefaultThresholdBytes: 1}

	reg := NewRegistry()
	echoTool := &fixedOutputTool{name: "echo", output: "echo-out"}
	grepTool := &fixedOutputTool{name: "grep", output: "grep-out"}
	if err := reg.Register(echoTool); err != nil {
		t.Fatalf("register echo: %v", err)
	}
	if err := reg.Register(grepTool); err != nil {
		t.Fatalf("register grep: %v", err)
	}

	exec := NewExecutor(reg, nil).WithOutputPersister(persister)
	echoRes, err := exec.Execute(context.Background(), Call{Name: "echo", SessionID: "sess"})
	if err != nil {
		t.Fatalf("execute echo: %v", err)
	}
	grepRes, err := exec.Execute(context.Background(), Call{Name: "grep", SessionID: "sess"})
	if err != nil {
		t.Fatalf("execute grep: %v", err)
	}
	if echoRes == nil || echoRes.Result == nil || echoRes.Result.OutputRef == nil {
		t.Fatalf("expected echo OutputRef, got %+v", echoRes)
	}
	if grepRes == nil || grepRes.Result == nil || grepRes.Result.OutputRef == nil {
		t.Fatalf("expected grep OutputRef, got %+v", grepRes)
	}
	echoDir := filepath.Dir(echoRes.Result.OutputRef.Path)
	grepDir := filepath.Dir(grepRes.Result.OutputRef.Path)
	if echoDir == grepDir {
		t.Fatalf("expected different tool directories, got %q", echoDir)
	}
	wantEchoPrefix := filepath.Join(base, "sess", "echo") + string(filepath.Separator)
	if !strings.HasPrefix(echoRes.Result.OutputRef.Path, wantEchoPrefix) {
		t.Fatalf("expected echo path under %q, got %q", wantEchoPrefix, echoRes.Result.OutputRef.Path)
	}
	wantGrepPrefix := filepath.Join(base, "sess", "grep") + string(filepath.Separator)
	if !strings.HasPrefix(grepRes.Result.OutputRef.Path, wantGrepPrefix) {
		t.Fatalf("expected grep path under %q, got %q", wantGrepPrefix, grepRes.Result.OutputRef.Path)
	}
}

func TestExecutorDeniesByPermissions(t *testing.T) {
	root := canonicalTempDir(t)
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := `{"permissions":{"deny":["Bash(ls:*)"]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	reg := NewRegistry()
	tool := &stubTool{name: "Bash"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	exec := NewExecutor(reg, sandbox.NewManager(sandbox.NewFileSystemAllowList(root), nil, nil))
	_, err := exec.Execute(context.Background(), Call{Name: "Bash", Params: map[string]any{"command": "ls -la"}, Path: root})
	if err == nil || !strings.Contains(err.Error(), "denied") {
		t.Fatalf("expected deny error, got %v", err)
	}
	if atomic.LoadInt32(&tool.called) != 0 {
		t.Fatalf("tool should not run when denied")
	}
}

func TestExecutorAsksWhenConfigured(t *testing.T) {
	root := canonicalTempDir(t)
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := `{"permissions":{"ask":["Bash(ls:*)"]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	reg := NewRegistry()
	tool := &stubTool{name: "Bash"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	exec := NewExecutor(reg, sandbox.NewManager(sandbox.NewFileSystemAllowList(root), nil, nil))
	_, err := exec.Execute(context.Background(), Call{Name: "Bash", Params: map[string]any{"command": "ls -la"}, Path: root})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "requires approval") {
		t.Fatalf("expected approval error, got %v", err)
	}
	if atomic.LoadInt32(&tool.called) != 0 {
		t.Fatalf("tool should not run when approval needed")
	}
}

func TestExecutorAllowsWhenPermissionMatches(t *testing.T) {
	root := canonicalTempDir(t)
	claude := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claude, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := `{"permissions":{"allow":["Bash(ls:*)"]}}`
	if err := os.WriteFile(filepath.Join(claude, "settings.json"), []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	reg := NewRegistry()
	tool := &stubTool{name: "Bash"}
	if err := reg.Register(tool); err != nil {
		t.Fatalf("register: %v", err)
	}

	exec := NewExecutor(reg, sandbox.NewManager(sandbox.NewFileSystemAllowList(root), nil, nil))
	_, err := exec.Execute(context.Background(), Call{Name: "Bash", Params: map[string]any{"command": "ls"}, Path: root})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if atomic.LoadInt32(&tool.called) != 1 {
		t.Fatalf("tool should execute when allowed, got %d", tool.called)
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != "" {
		return resolved
	}
	return dir
}
