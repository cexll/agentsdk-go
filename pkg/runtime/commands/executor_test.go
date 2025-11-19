package commands

import (
	"context"
	"errors"
	"testing"
)

func TestExecutorRunAndMutex(t *testing.T) {
	exec := NewExecutor()
	runOrder := []string{}
	must := func(def Definition) {
		if err := exec.Register(def, HandlerFunc(func(ctx context.Context, inv Invocation) (Result, error) {
			runOrder = append(runOrder, inv.Name)
			return Result{Output: inv.Name}, nil
		})); err != nil {
			t.Fatalf("register %s failed: %v", def.Name, err)
		}
	}
	must(Definition{Name: "deploy", Priority: 1, MutexKey: "env"})
	must(Definition{Name: "build", Priority: 2, MutexKey: "env"})
	must(Definition{Name: "test", Priority: 0})

	results, err := exec.Run(context.Background(), "/deploy\n/build\n/test")
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if len(results) != 2 { // build suppresses deploy due to higher priority mutex
		t.Fatalf("expected 2 results, got %d", len(results))
	}
	if results[0].Command != "build" || results[1].Command != "test" {
		t.Fatalf("unexpected execution order: %+v", results)
	}
	if len(runOrder) != 2 || runOrder[0] != "build" {
		t.Fatalf("mutex filtering failed: %v", runOrder)
	}
}

func TestExecutorErrorPropagation(t *testing.T) {
	exec := NewExecutor()
	if err := exec.Register(Definition{Name: "fail"}, HandlerFunc(func(ctx context.Context, inv Invocation) (Result, error) {
		return Result{}, errors.New("boom")
	})); err != nil {
		t.Fatalf("register fail: %v", err)
	}
	inv := []Invocation{{Name: "fail"}}
	results, err := exec.Execute(context.Background(), inv)
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected handler error, got %v", err)
	}
	if len(results) != 1 || results[0].Error != "boom" {
		t.Fatalf("expected error result, got %+v", results)
	}
}

func TestExecutorUnknownCommand(t *testing.T) {
	exec := NewExecutor()
	if _, err := exec.Execute(context.Background(), []Invocation{{Name: "missing"}}); !errors.Is(err, ErrUnknownCommand) {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestExecutorListAndValidation(t *testing.T) {
	if err := (Definition{Name: "bad name"}).Validate(); err == nil {
		t.Fatalf("expected validation error for space")
	}
	var fn HandlerFunc
	if _, err := fn.Handle(context.Background(), Invocation{}); err == nil {
		t.Fatalf("expected nil handler func to error")
	}

	exec := NewExecutor()
	if err := exec.Register(Definition{Name: "beta", Priority: 0}, HandlerFunc(func(context.Context, Invocation) (Result, error) {
		return Result{Metadata: map[string]any{"k": "v"}}, nil
	})); err != nil {
		t.Fatalf("register beta: %v", err)
	}
	if err := exec.Register(Definition{Name: "alpha", Priority: 1}, HandlerFunc(func(context.Context, Invocation) (Result, error) {
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	list := exec.List()
	if len(list) != 2 || list[0].Name != "alpha" {
		t.Fatalf("unexpected list order: %+v", list)
	}

	// ensure Register rejects duplicate and nil handler
	if err := exec.Register(Definition{Name: "alpha"}, HandlerFunc(func(context.Context, Invocation) (Result, error) { return Result{}, nil })); err == nil {
		t.Fatalf("expected duplicate registration error")
	}
	if err := exec.Register(Definition{Name: "gamma"}, nil); err == nil {
		t.Fatalf("expected nil handler rejection")
	}

	// clone coverage
	out, err := exec.Execute(context.Background(), []Invocation{{Name: "beta"}})
	if err != nil {
		t.Fatalf("execute beta: %v", err)
	}
	out[0].Metadata["k"] = "mutated"
	refreshed, err := exec.Execute(context.Background(), []Invocation{{Name: "beta"}})
	if err != nil {
		t.Fatalf("execute beta second: %v", err)
	}
	if refreshed[0].Metadata["k"] != "v" {
		t.Fatalf("metadata clone failed")
	}
}
