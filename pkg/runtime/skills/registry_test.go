package skills

import (
	"context"
	"errors"
	"testing"
)

func TestRegistryRegisterAndExecute(t *testing.T) {
	r := NewRegistry()
	handler := HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) {
		return Result{Output: ac.Prompt, Metadata: map[string]any{"ctx": ac.Prompt}}, nil
	})
	if err := r.Register(Definition{Name: "echo", Priority: 1}, handler); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if err := r.Register(Definition{Name: "echo"}, handler); !errors.Is(err, ErrDuplicateSkill) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
	if err := r.Register(Definition{Name: "fail"}, nil); err == nil {
		t.Fatalf("expected nil handler rejection")
	}

	res, err := r.Execute(context.Background(), "echo", ActivationContext{Prompt: "hi"})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	meta, ok := res.Metadata["ctx"].(string)
	if !ok {
		t.Fatalf("expected metadata string, got %T", res.Metadata["ctx"])
	}
	if res.Skill != "echo" || res.Output != "hi" || meta != "hi" {
		t.Fatalf("unexpected result: %+v", res)
	}

	if _, err := r.Execute(context.Background(), "missing", ActivationContext{}); !errors.Is(err, ErrUnknownSkill) {
		t.Fatalf("expected unknown error, got %v", err)
	}

	// handler error path
	if err := r.Register(Definition{Name: "err"}, HandlerFunc(func(context.Context, ActivationContext) (Result, error) {
		return Result{}, errors.New("boom")
	})); err != nil {
		t.Fatalf("register err: %v", err)
	}
	if _, err := r.Execute(context.Background(), "err", ActivationContext{}); err == nil {
		t.Fatalf("expected execution error")
	}
}

func TestRegistryMatchOrderingAndMutex(t *testing.T) {
	r := NewRegistry()
	ctx := ActivationContext{Prompt: "deploy payment service to prod and staging"}

	must := func(def Definition) {
		err := r.Register(def, HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) {
			return Result{Output: def.Name}, nil
		}))
		if err != nil {
			t.Fatalf("register %s: %v", def.Name, err)
		}
	}
	must(Definition{Name: "ops", Priority: 1, Matchers: []Matcher{KeywordMatcher{All: []string{"deploy"}}}})
	must(Definition{Name: "prod", Priority: 2, MutexKey: "env", Matchers: []Matcher{KeywordMatcher{Any: []string{"prod"}}}})
	must(Definition{Name: "staging", Priority: 3, MutexKey: "env", Matchers: []Matcher{KeywordMatcher{Any: []string{"staging"}}}})
	must(Definition{Name: "manual", DisableAutoActivation: true})

	matches := r.Match(ctx)
	if len(matches) != 2 {
		t.Fatalf("expected 2 matches (mutex filtered), got %d", len(matches))
	}
	if matches[0].Skill.definition.Name != "staging" {
		t.Fatalf("priority order broken: %+v", matches)
	}
	if matches[1].Skill.definition.Name != "ops" {
		t.Fatalf("ops should remain after mutex filter, got %+v", matches)
	}
}

func TestRegistryListSorted(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Definition{Name: "b", Priority: 1}, HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) { return Result{}, nil })); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if err := r.Register(Definition{Name: "a", Priority: 2}, HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) { return Result{}, nil })); err != nil {
		t.Fatalf("register a: %v", err)
	}
	defs := r.List()
	if len(defs) != 2 || defs[0].Name != "a" || defs[1].Name != "b" {
		t.Fatalf("unexpected list order: %+v", defs)
	}
}

func TestActivationDefinition(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(Definition{Name: "inspector", Priority: 5, Metadata: map[string]string{"k": "v"}}, HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) {
		return Result{Output: "ok"}, nil
	})); err != nil {
		t.Fatalf("register: %v", err)
	}
	match := r.Match(ActivationContext{})
	if len(match) != 1 {
		t.Fatalf("expected one activation, got %d", len(match))
	}
	def := match[0].Definition()
	if def.Name != "inspector" || def.Priority != 5 || def.Metadata["k"] != "v" {
		t.Fatalf("unexpected definition snapshot: %+v", def)
	}

	empty := (Activation{}).Definition()
	if empty.Name != "" || empty.Priority != 0 || len(empty.Metadata) != 0 {
		t.Fatalf("unexpected data for nil activation: %+v", empty)
	}
}

func TestRegistryMatchNoCandidates(t *testing.T) {
	r := NewRegistry()
	if matches := r.Match(ActivationContext{}); matches != nil {
		t.Fatalf("expected nil matches when registry empty")
	}
}

func TestRegistryDefaultsAndValidation(t *testing.T) {
	if err := (Definition{Name: ""}).Validate(); err == nil {
		t.Fatalf("expected validation to fail")
	}
	var fn HandlerFunc
	if _, err := fn.Execute(context.Background(), ActivationContext{}); err == nil {
		t.Fatalf("nil handler func should error")
	}

	r := NewRegistry()
	err := r.Register(Definition{Name: "auto-skill"}, HandlerFunc(func(ctx context.Context, ac ActivationContext) (Result, error) { return Result{Output: "ok"}, nil }))
	if err != nil {
		t.Fatalf("register failed: %v", err)
	}
	matches := r.Match(ActivationContext{})
	if len(matches) != 1 || matches[0].Skill.definition.Name != "auto-skill" {
		t.Fatalf("expected implicit match with no matchers, got %+v", matches)
	}

	// list clones metadata and matchers
	if err := r.Register(Definition{Name: "with-meta", Metadata: map[string]string{"k": "v"}, Matchers: []Matcher{KeywordMatcher{All: []string{"x"}}}}, HandlerFunc(func(context.Context, ActivationContext) (Result, error) {
		return Result{}, nil
	})); err != nil {
		t.Fatalf("register with-meta: %v", err)
	}
	defs := r.List()
	if len(defs) != 2 {
		t.Fatalf("expected two definitions")
	}
	for i := range defs {
		defs[i].Metadata = map[string]string{"mutated": "1"}
		defs[i].Matchers = nil
	}
	latest := r.List()
	for _, def := range latest {
		if def.Name == "with-meta" && (def.Metadata["k"] != "v" || len(def.Matchers) == 0) {
			t.Fatalf("metadata/matcher leak detected: %+v", def)
		}
	}
}
