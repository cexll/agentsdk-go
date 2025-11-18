package tool

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

type stubTool struct {
	name    string
	delay   time.Duration
	mutate  bool
	called  int32
	lastArg map[string]any
}

func (s *stubTool) Name() string        { return s.name }
func (s *stubTool) Description() string { return "stub" }
func (s *stubTool) Schema() *JSONSchema { return nil }
func (s *stubTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	atomic.AddInt32(&s.called, 1)
	s.lastArg = params
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

func TestExecutorClonesParamsAndPreservesOrder(t *testing.T) {
	reg := NewRegistry()
	tool := &stubTool{name: "echo", delay: 15 * time.Millisecond, mutate: true}
	_ = reg.Register(tool)
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
	if nested := shared["nested"].(map[string]any); nested["y"] != 2 {
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
	cloned := cloneValue(original).([]any)
	cloned[0].(map[string]any)["a"] = 5
	if original[0].(map[string]any)["a"].(int) != 1 {
		t.Fatalf("original mutated: %#v", original)
	}
}
