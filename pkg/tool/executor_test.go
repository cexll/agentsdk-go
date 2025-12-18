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
