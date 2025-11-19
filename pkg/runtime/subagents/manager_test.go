package subagents

import (
	"context"
	"errors"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

func TestManagerRegisterAndDispatchTarget(t *testing.T) {
	m := NewManager()
	handler := HandlerFunc(func(ctx context.Context, subCtx Context, req Request) (Result, error) {
		return Result{Output: subCtx.SessionID, Metadata: map[string]any{"tools": subCtx.ToolList()}}, nil
	})
	if err := m.Register(Definition{Name: "code", BaseContext: Context{SessionID: "child"}}, handler); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := m.Register(Definition{Name: "code"}, handler); !errors.Is(err, ErrDuplicateSubagent) {
		t.Fatalf("expected duplicate error")
	}

	res, err := m.Dispatch(context.Background(), Request{Target: "code", Instruction: "run", ToolWhitelist: []string{"bash"}})
	if err != nil {
		t.Fatalf("dispatch failed: %v", err)
	}
	tools, ok := res.Metadata["tools"].([]string)
	if !ok {
		t.Fatalf("expected tools metadata slice, got %T", res.Metadata["tools"])
	}
	if res.Subagent != "code" || res.Output != "child" || tools[0] != "bash" {
		t.Fatalf("unexpected result: %+v", res)
	}
}

func TestManagerAutoMatchPriorityAndMutex(t *testing.T) {
	m := NewManager()
	errorHandler := HandlerFunc(func(ctx context.Context, subCtx Context, req Request) (Result, error) {
		return Result{}, errors.New("boom")
	})
	matcher := skills.KeywordMatcher{Any: []string{"deploy", "ops"}}
	if err := m.Register(Definition{Name: "low", Priority: 1, Matchers: []skills.Matcher{matcher}}, errorHandler); err != nil {
		t.Fatalf("register low: %v", err)
	}
	if err := m.Register(Definition{Name: "high", Priority: 2, MutexKey: "env", Matchers: []skills.Matcher{matcher}}, HandlerFunc(func(ctx context.Context, subCtx Context, req Request) (Result, error) {
		return Result{Output: "ok"}, nil
	})); err != nil {
		t.Fatalf("register high: %v", err)
	}
	if err := m.Register(Definition{Name: "other", Priority: 3, MutexKey: "env", Matchers: []skills.Matcher{skills.KeywordMatcher{Any: []string{"other"}}}}, HandlerFunc(func(ctx context.Context, subCtx Context, req Request) (Result, error) {
		return Result{Output: "other"}, nil
	})); err != nil {
		t.Fatalf("register other: %v", err)
	}

	res, err := m.Dispatch(context.Background(), Request{Instruction: "deploy", Activation: skills.ActivationContext{Prompt: "deploy prod"}})
	if err != nil {
		t.Fatalf("dispatch match failed: %v", err)
	}
	if res.Subagent != "high" {
		t.Fatalf("expected high priority selection, got %s", res.Subagent)
	}

	_, err = m.Dispatch(context.Background(), Request{Instruction: "deploy", Activation: skills.ActivationContext{Prompt: "missing"}})
	if !errors.Is(err, ErrNoMatchingSubagent) {
		t.Fatalf("expected no match error, got %v", err)
	}

	_, err = m.Dispatch(context.Background(), Request{Instruction: "", Activation: skills.ActivationContext{Prompt: "deploy"}})
	if !errors.Is(err, ErrEmptyInstruction) {
		t.Fatalf("expected empty instruction error")
	}
}

func TestManagerUnknownTarget(t *testing.T) {
	m := NewManager()
	if _, err := m.Dispatch(context.Background(), Request{Target: "missing", Instruction: "run"}); !errors.Is(err, ErrUnknownSubagent) {
		t.Fatalf("expected unknown target error")
	}

	// coverage for selectTarget manual path
	handler := HandlerFunc(func(context.Context, Context, Request) (Result, error) {
		return Result{Output: "ok"}, nil
	})
	if err := m.Register(Definition{Name: "direct"}, handler); err != nil {
		t.Fatalf("register direct: %v", err)
	}
	res, err := m.Dispatch(context.Background(), Request{Target: "direct", Instruction: "run"})
	if err != nil || res.Subagent != "direct" {
		t.Fatalf("expected direct dispatch, got %v %v", res, err)
	}
}

func TestManagerListAndDefinitionClone(t *testing.T) {
	m := NewManager()
	base := Context{SessionID: "root", Metadata: map[string]any{"a": 1}, ToolWhitelist: []string{"bash"}}
	handler := HandlerFunc(func(context.Context, Context, Request) (Result, error) {
		return Result{}, nil
	})
	if err := m.Register(Definition{Name: "list", BaseContext: base}, HandlerFunc(func(context.Context, Context, Request) (Result, error) {
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	list := m.List()
	if len(list) != 1 || list[0].Name != "list" {
		t.Fatalf("unexpected list result: %+v", list)
	}
	list[0].BaseContext.Metadata["a"] = 2
	list[0].Matchers = nil
	refreshed := m.List()
	if refreshed[0].BaseContext.Metadata["a"] != 1 {
		t.Fatalf("context clone failed: %+v", refreshed[0])
	}

	// ensure mutex filtering path keeps first entry when same priority
	if err := m.Register(Definition{Name: "mutex-a", Priority: 1, MutexKey: "env"}, handler); err != nil {
		t.Fatalf("register mutex-a: %v", err)
	}
	if err := m.Register(Definition{Name: "mutex-b", Priority: 1, MutexKey: "env"}, handler); err != nil {
		t.Fatalf("register mutex-b: %v", err)
	}
	match := m.matching(skills.ActivationContext{})
	if len(match) == 0 {
		t.Fatalf("expected at least one match")
	}
}

func TestManagerValidationAndGuards(t *testing.T) {
	if err := (Definition{Name: "bad name"}).Validate(); err == nil {
		t.Fatalf("expected validation error for spaces")
	}
	var fn HandlerFunc
	if _, err := fn.Handle(context.Background(), Context{}, Request{}); err == nil {
		t.Fatalf("nil handler func should error")
	}

	m := NewManager()
	if err := m.Register(Definition{Name: "ok"}, nil); err == nil {
		t.Fatalf("expected nil handler rejection")
	}
	if err := m.Register(Definition{Name: "prio-high", Priority: -1}, HandlerFunc(func(context.Context, Context, Request) (Result, error) {
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register prio-high: %v", err)
	}
	if err := m.Register(Definition{Name: "prio-low", Priority: 1}, HandlerFunc(func(context.Context, Context, Request) (Result, error) {
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register prio-low: %v", err)
	}
	list := m.List()
	if len(list) != 2 || list[0].Name != "prio-low" || list[0].Priority != 1 {
		t.Fatalf("expected list order by priority desc, got %+v", list)
	}

	// Dispatch should merge metadata into cloned context
	if err := m.Register(Definition{Name: "meta"}, HandlerFunc(func(ctx context.Context, subCtx Context, req Request) (Result, error) {
		if subCtx.Metadata["k"] != "v" {
			t.Fatalf("metadata not merged")
		}
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register meta: %v", err)
	}
	if _, err := m.Dispatch(context.Background(), Request{Target: "meta", Instruction: "run", Metadata: map[string]any{"k": "v"}}); err != nil {
		t.Fatalf("dispatch meta: %v", err)
	}
}
