package middleware

import (
	"bytes"
	"context"
	"log"
	"slices"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

type stubBodyHandler struct {
	length int
	loaded bool
}

func (h *stubBodyHandler) Execute(ctx context.Context, ac skills.ActivationContext) (skills.Result, error) {
	return skills.Result{}, nil
}

func (h *stubBodyHandler) BodyLength() (int, bool) {
	if h == nil {
		return 0, false
	}
	return h.length, h.loaded
}

func TestWithSkillTracingOption(t *testing.T) {
	mw := &TraceMiddleware{}
	WithSkillTracing(true)(mw)
	if !mw.traceSkills {
		t.Fatalf("expected skill tracing to be enabled")
	}
	WithSkillTracing(false)(mw)
	if mw.traceSkills {
		t.Fatalf("expected skill tracing to be disabled")
	}
}

func TestTraceSkillsSnapshotBeforeAfter(t *testing.T) {
	reg := skills.NewRegistry()
	alpha := &stubBodyHandler{length: 10, loaded: true}
	beta := &stubBodyHandler{length: 20, loaded: true}
	if err := reg.Register(skills.Definition{Name: "alpha"}, alpha); err != nil {
		t.Fatalf("register alpha: %v", err)
	}
	if err := reg.Register(skills.Definition{Name: "beta"}, beta); err != nil {
		t.Fatalf("register beta: %v", err)
	}

	state := &State{Values: map[string]any{
		forceSkillsValue:    []any{"Alpha ", []byte("beta"), stubStringer("ALPHA")},
		skillsRegistryValue: reg,
	}}

	mw := &TraceMiddleware{traceSkills: true}
	mw.traceSkillsSnapshot(context.Background(), state, true)

	names, ok := state.Values[traceSkillNamesKey].([]string)
	if !ok {
		t.Fatalf("trace skill names missing: %#v", state.Values[traceSkillNamesKey])
	}
	if !slices.Equal(names, []string{"alpha", "beta"}) {
		t.Fatalf("unexpected normalized names: %v", names)
	}

	beforeSnapshot, ok := state.Values[traceSkillBeforeKey].(map[string]int)
	if !ok {
		t.Fatalf("trace skill snapshot missing: %#v", state.Values[traceSkillBeforeKey])
	}
	if beforeSnapshot["alpha"] != 10 || beforeSnapshot["beta"] != 20 {
		t.Fatalf("unexpected before snapshot values: %#v", beforeSnapshot)
	}

	delete(state.Values, forceSkillsValue)
	beforeSnapshot["gamma"] = 5
	alpha.length = 12
	beta.length = 21

	origWriter := log.Writer()
	origFlags := log.Flags()
	var buf bytes.Buffer
	log.SetOutput(&buf)
	log.SetFlags(0)
	defer func() {
		log.SetOutput(origWriter)
		log.SetFlags(origFlags)
	}()

	mw.traceSkillsSnapshot(context.Background(), state, false)

	output := buf.String()
	expect := []string{
		"skill=alpha body_before=10 body_after=12",
		"skill=beta body_before=20 body_after=21",
		"skill=gamma body_before=5 body_after=0",
	}
	for _, needle := range expect {
		if !strings.Contains(output, needle) {
			t.Fatalf("log output missing %q: %s", needle, output)
		}
	}
}

func TestForceSkillsFromState(t *testing.T) {
	tests := []struct {
		name   string
		values map[string]any
		want   []string
	}{
		{
			name: "request list preferred",
			values: map[string]any{
				forceSkillsValue: []any{"Alpha ", []byte("BETA"), stubStringer("alpha")},
				traceSkillNamesKey: []string{
					"ignored",
				},
			},
			want: []string{"alpha", "beta"},
		},
		{
			name: "fallback to stored names",
			values: map[string]any{
				traceSkillNamesKey: []string{"First", "second", "FIRST"},
			},
			want: []string{"first", "second"},
		},
		{
			name:   "missing values",
			values: map[string]any{},
			want:   nil,
		},
	}

	for _, tc := range tests {
		got := forceSkillsFromState(tc.values)
		if tc.want == nil {
			if got != nil {
				t.Fatalf("%s: expected nil got %v", tc.name, got)
			}
			continue
		}
		if !slices.Equal(got, tc.want) {
			t.Fatalf("%s: expected %v got %v", tc.name, tc.want, got)
		}
	}
}

func TestRegistryFromState(t *testing.T) {
	reg := skills.NewRegistry()
	values := map[string]any{skillsRegistryValue: reg}
	if got := registryFromState(values); got != reg {
		t.Fatalf("expected registry pointer, got %v", got)
	}
	if registryFromState(map[string]any{skillsRegistryValue: "bad"}) != nil {
		t.Fatalf("non-registry value should yield nil")
	}
	if registryFromState(nil) != nil {
		t.Fatalf("nil map should yield nil registry")
	}
}

func TestSkillBodiesAndBodySize(t *testing.T) {
	reg := skills.NewRegistry()
	handler := &stubBodyHandler{length: 9, loaded: true}
	if err := reg.Register(skills.Definition{Name: "example"}, handler); err != nil {
		t.Fatalf("register example: %v", err)
	}
	bodies := skillBodies(reg, []string{"Example", "missing", "EXAMPLE"})
	if len(bodies) != 1 || bodies["example"] != 9 {
		t.Fatalf("unexpected bodies map: %#v", bodies)
	}
	if skillBodies(nil, []string{"Example"}) != nil {
		t.Fatalf("nil registry should return nil bodies")
	}
	if skillBodies(reg, nil) != nil {
		t.Fatalf("nil names should return nil bodies")
	}

	if size := skillBodySize((*stubBodyHandler)(nil)); size != 0 {
		t.Fatalf("nil handler should return zero, got %d", size)
	}
	if size := skillBodySize(skills.HandlerFunc(func(context.Context, skills.ActivationContext) (skills.Result, error) {
		return skills.Result{}, nil
	})); size != 0 {
		t.Fatalf("handler without BodyLength should return zero")
	}
	handler.loaded = false
	if size := skillBodySize(handler); size != 0 {
		t.Fatalf("size should be zero when body not loaded, got %d", size)
	}
	handler.loaded = true
	handler.length = 11
	if size := skillBodySize(handler); size != 11 {
		t.Fatalf("size mismatch: got %d want 11", size)
	}
}

func TestStringListAndDedupe(t *testing.T) {
	if stringList(nil) != nil {
		t.Fatalf("nil input should return nil slice")
	}
	if dedupeStrings(nil) != nil {
		t.Fatalf("nil dedupe input should return nil")
	}

	if got := stringList([]string{"Alpha", "alpha", "  "}); !slices.Equal(got, []string{"alpha"}) {
		t.Fatalf("[]string input mismatch: %v", got)
	}

	anyList := []any{"Alpha ", []byte("BETA"), stubStringer("gamma")}
	if got := stringList(anyList); !slices.Equal(got, []string{"alpha", "beta", "gamma"}) {
		t.Fatalf("[]any input mismatch: %v", got)
	}

	if got := stringList("Single Skill"); !slices.Equal(got, []string{"Single Skill"}) {
		t.Fatalf("string input should preserve case, got %v", got)
	}

	if got := dedupeStrings([]string{"Alpha", " alpha ", "BETA"}); !slices.Equal(got, []string{"alpha", "beta"}) {
		t.Fatalf("dedupe mismatch: %v", got)
	}
}

func TestOrderedSkillNames(t *testing.T) {
	names := []string{" Beta ", "Alpha", "beta", ""}
	before := map[string]int{"gamma": 1}
	after := map[string]int{"delta": 1, "epsilon": 1}
	want := []string{"Beta", "Alpha", "beta", "delta", "epsilon", "gamma"}
	if got := orderedSkillNames(names, before, after); !slices.Equal(got, want) {
		t.Fatalf("ordered skills mismatch: want %v got %v", want, got)
	}
}
