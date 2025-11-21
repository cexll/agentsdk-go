package agent

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/middleware"
)

type scriptedModel struct {
	outputs []*ModelOutput
	idx     int
	delay   time.Duration
	err     error
}

func (m *scriptedModel) Generate(ctx context.Context, _ *Context) (*ModelOutput, error) {
	if m.delay > 0 {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(m.delay):
		}
	}
	if m.err != nil {
		return nil, m.err
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(m.outputs) == 0 {
		return &ModelOutput{}, nil
	}
	if m.idx >= len(m.outputs) {
		return m.outputs[len(m.outputs)-1], nil
	}
	out := m.outputs[m.idx]
	m.idx++
	return out, nil
}

type stubTools struct {
	calls []ToolCall
	err   error
	delay time.Duration
}

func (t *stubTools) Execute(ctx context.Context, call ToolCall, _ *Context) (ToolResult, error) {
	t.calls = append(t.calls, call)
	if t.delay > 0 {
		select {
		case <-ctx.Done():
			return ToolResult{Name: call.Name}, ctx.Err()
		case <-time.After(t.delay):
		}
	}
	if t.err != nil {
		return ToolResult{Name: call.Name}, t.err
	}
	return ToolResult{Name: call.Name, Output: "ok"}, nil
}

type errorAwareModel struct {
	calls int
}

func (m *errorAwareModel) Generate(_ context.Context, c *Context) (*ModelOutput, error) {
	m.calls++
	if len(c.ToolResults) == 0 {
		return &ModelOutput{ToolCalls: []ToolCall{{ID: "call-1", Name: "flaky-tool"}}}, nil
	}
	return &ModelOutput{Content: "done after error", Done: true}, nil
}

func middlewareRecorder(log *[]string) middleware.Middleware {
	return middleware.Funcs{
		Identifier: "recorder",
		OnBeforeAgent: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("before_agent:%d", st.Iteration))
			return nil
		},
		OnBeforeModel: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("before_model:%d", st.Iteration))
			return nil
		},
		OnAfterModel: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("after_model:%d", st.Iteration))
			return nil
		},
		OnBeforeTool: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("before_tool:%d", st.Iteration))
			return nil
		},
		OnAfterTool: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("after_tool:%d", st.Iteration))
			return nil
		},
		OnAfterAgent: func(_ context.Context, st *middleware.State) error {
			*log = append(*log, fmt.Sprintf("after_agent:%d", st.Iteration))
			return nil
		},
	}
}

func TestAgentRunsThroughToolThenFinal(t *testing.T) {
	model := &scriptedModel{
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "tool"}}},
			{Content: "done", Done: true},
		},
	}
	tools := &stubTools{}
	log := []string{}
	chain := middleware.NewChain([]middleware.Middleware{middlewareRecorder(&log)})

	ag, err := New(model, tools, Options{Middleware: chain})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	out, err := ag.Run(context.Background(), NewContext())
	if err != nil {
		t.Fatalf("run error: %v", err)
	}
	if out.Content != "done" {
		t.Fatalf("unexpected final content: %q", out.Content)
	}

	expected := []string{
		"before_agent:0",
		"before_model:0",
		"after_model:0",
		"before_tool:0",
		"after_tool:0",
		"before_model:1",
		"after_model:1",
		"after_agent:1",
	}
	if !reflect.DeepEqual(log, expected) {
		t.Fatalf("middleware order mismatch:\n got %v\nwant %v", log, expected)
	}
	if len(tools.calls) != 1 || tools.calls[0].Name != "tool" {
		t.Fatalf("tool calls unexpected: %v", tools.calls)
	}
}

func TestAgentShortCircuitsOnMiddlewareError(t *testing.T) {
	model := &scriptedModel{
		outputs: []*ModelOutput{
			{ToolCalls: nil},
		},
	}
	tools := &stubTools{}
	sentinel := errors.New("middleware failure")

	mw := middleware.Funcs{
		Identifier: "failer",
		OnAfterModel: func(_ context.Context, _ *middleware.State) error {
			return sentinel
		},
		OnAfterAgent: func(_ context.Context, _ *middleware.State) error {
			t.Fatalf("after_agent should not run after error")
			return nil
		},
	}

	chain := middleware.NewChain([]middleware.Middleware{mw})
	ag, err := New(model, tools, Options{Middleware: chain})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	_, err = ag.Run(context.Background(), NewContext())
	if !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel error, got %v", err)
	}
	if len(tools.calls) != 0 {
		t.Fatalf("tools should not be invoked on middleware error")
	}
}

func TestAgentMaxIterations(t *testing.T) {
	model := &scriptedModel{
		outputs: []*ModelOutput{
			{ToolCalls: []ToolCall{{Name: "loop"}}},
		},
	}
	tools := &stubTools{}
	chain := middleware.NewChain(nil)

	ag, err := New(model, tools, Options{Middleware: chain, MaxIterations: 1})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	_, err = ag.Run(context.Background(), NewContext())
	if !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected max iteration error, got %v", err)
	}
}

func TestAgentTimeout(t *testing.T) {
	model := &scriptedModel{
		outputs: []*ModelOutput{{}},
		delay:   200 * time.Millisecond,
	}
	chain := middleware.NewChain(nil)
	ag, err := New(model, &stubTools{}, Options{Middleware: chain, Timeout: 50 * time.Millisecond})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	_, err = ag.Run(context.Background(), NewContext())
	if err == nil {
		t.Fatalf("expected timeout error")
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context deadline exceeded, got %v", err)
	}
}

func TestNewRequiresModel(t *testing.T) {
	if _, err := New(nil, nil, Options{}); !errors.Is(err, ErrNilModel) {
		t.Fatalf("expected ErrNilModel, got %v", err)
	}
}

func TestWithDefaultsCreatesChain(t *testing.T) {
	model := &scriptedModel{outputs: []*ModelOutput{{Done: true}}}
	ag, err := New(model, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if ag.mw == nil {
		t.Fatalf("default middleware chain should not be nil")
	}
	if err := ag.mw.Execute(context.Background(), middleware.StageAfterAgent, &middleware.State{}); err != nil {
		t.Fatalf("default chain should no-op: %v", err)
	}
	customChain := middleware.NewChain(nil)
	ag2, err := New(model, nil, Options{Middleware: customChain})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if ag2.mw != customChain {
		t.Fatalf("expected custom middleware to be used")
	}
}

func TestRunNilAgent(t *testing.T) {
	var a *Agent
	if _, err := a.Run(context.Background(), nil); err == nil {
		t.Fatalf("expected error for nil receiver")
	}
}

type nilModel struct{}

func (nilModel) Generate(context.Context, *Context) (*ModelOutput, error) {
	return nil, nil
}

func TestRunNilModelOutput(t *testing.T) {
	ag, err := New(nilModel{}, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); err == nil {
		t.Fatalf("expected error on nil model output")
	}
}

func TestRunRequiresToolExecutorWhenToolCallsPresent(t *testing.T) {
	model := &scriptedModel{
		outputs: []*ModelOutput{{ToolCalls: []ToolCall{{Name: "needs-tools"}}}},
	}
	ag, err := New(model, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); err == nil {
		t.Fatalf("expected error due to nil tool executor")
	}
}

func TestRunBeforeAgentMiddlewareError(t *testing.T) {
	sentinel := errors.New("before agent failed")
	mw := middleware.Funcs{
		OnBeforeAgent: func(context.Context, *middleware.State) error {
			return sentinel
		},
	}
	ag, err := New(&scriptedModel{outputs: []*ModelOutput{{Done: true}}}, nil, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestRunContextCanceledBeforeLoop(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	ag, err := New(&scriptedModel{outputs: []*ModelOutput{{Done: true}}}, nil, Options{})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(ctx, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled error, got %v", err)
	}
}

func TestRunBeforeModelMiddlewareError(t *testing.T) {
	sentinel := errors.New("before model fail")
	mw := middleware.Funcs{
		OnBeforeModel: func(context.Context, *middleware.State) error {
			return sentinel
		},
	}
	ag, err := New(&scriptedModel{outputs: []*ModelOutput{{Done: true}}}, nil, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestRunAfterAgentMiddlewareError(t *testing.T) {
	sentinel := errors.New("after agent fail")
	mw := middleware.Funcs{
		OnAfterAgent: func(context.Context, *middleware.State) error {
			return sentinel
		},
	}
	ag, err := New(&scriptedModel{outputs: []*ModelOutput{{Done: true}}}, nil, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestRunBeforeToolMiddlewareError(t *testing.T) {
	sentinel := errors.New("before tool fail")
	mw := middleware.Funcs{
		OnBeforeTool: func(context.Context, *middleware.State) error {
			return sentinel
		},
	}
	model := &scriptedModel{outputs: []*ModelOutput{{ToolCalls: []ToolCall{{Name: "call"}}}}}
	ag, err := New(model, &stubTools{}, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestRunToolExecutionError(t *testing.T) {
	sentinel := errors.New("tool exec fail")
	model := &scriptedModel{outputs: []*ModelOutput{{ToolCalls: []ToolCall{{Name: "call"}}}}}
	tools := &stubTools{err: sentinel}
	ag, err := New(model, tools, Options{MaxIterations: 1})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, ErrMaxIterations) {
		t.Fatalf("expected max iteration error, got %v", err)
	}
	if len(tools.calls) != 1 {
		t.Fatalf("expected tool to be invoked once, got %d", len(tools.calls))
	}
}

func TestRunAfterToolMiddlewareError(t *testing.T) {
	sentinel := errors.New("after tool fail")
	mw := middleware.Funcs{
		OnAfterTool: func(context.Context, *middleware.State) error {
			return sentinel
		},
	}
	model := &scriptedModel{outputs: []*ModelOutput{{ToolCalls: []ToolCall{{Name: "call"}}}}}
	ag, err := New(model, &stubTools{}, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
	if err != nil {
		t.Fatalf("new agent: %v", err)
	}
	if _, err := ag.Run(context.Background(), nil); !errors.Is(err, sentinel) {
		t.Fatalf("expected sentinel, got %v", err)
	}
}

func TestAgent_Run_MultipleToolCallsWithMiddlewareError(t *testing.T) {
	sentinel := errors.New("after tool middleware error")
	cases := []struct {
		name string
	}{
		{name: "returns middleware error after executing all tools"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			ctx := NewContext()
			model := &scriptedModel{
				outputs: []*ModelOutput{
					{ToolCalls: []ToolCall{
						{ID: "call-1", Name: "first"},
						{ID: "call-2", Name: "second"},
					}},
				},
			}
			tools := &stubTools{}

			afterToolCount := 0
			mw := middleware.Funcs{
				OnAfterTool: func(context.Context, *middleware.State) error {
					afterToolCount++
					if afterToolCount == 2 {
						return sentinel
					}
					return nil
				},
			}
			ag, err := New(model, tools, Options{Middleware: middleware.NewChain([]middleware.Middleware{mw})})
			if err != nil {
				t.Fatalf("new agent: %v", err)
			}

			_, runErr := ag.Run(context.Background(), ctx)
			if runErr == nil || !errors.Is(runErr, sentinel) {
				t.Fatalf("expected middleware error, got %v", runErr)
			}

			if len(tools.calls) != 2 {
				t.Fatalf("expected two tool calls, got %d", len(tools.calls))
			}
			if len(ctx.ToolResults) != 2 {
				t.Fatalf("expected two tool results, got %d", len(ctx.ToolResults))
			}
			if ctx.ToolResults[0].Name != "first" || ctx.ToolResults[1].Name != "second" {
				t.Fatalf("tool results in wrong order: %+v", ctx.ToolResults)
			}
		})
	}
}

func TestAgent_Run_ToolExecutionError(t *testing.T) {
	cases := []struct {
		name    string
		toolErr error
	}{
		{name: "continues after tool failure", toolErr: errors.New("tool execution failed")},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			model := &errorAwareModel{}
			tools := &stubTools{err: tc.toolErr}
			ctx := NewContext()

			ag, err := New(model, tools, Options{})
			if err != nil {
				t.Fatalf("new agent: %v", err)
			}

			out, runErr := ag.Run(context.Background(), ctx)
			if runErr != nil {
				t.Fatalf("run returned error: %v", runErr)
			}
			if out == nil || !out.Done {
				t.Fatalf("expected final output with Done=true, got %+v", out)
			}
			if model.calls != 2 {
				t.Fatalf("expected model to run twice, got %d", model.calls)
			}

			if len(ctx.ToolResults) != 1 {
				t.Fatalf("tool results length mismatch: %d", len(ctx.ToolResults))
			}
			result := ctx.ToolResults[0]
			if result.Metadata == nil || result.Metadata["is_error"] != true {
				t.Fatalf("expected is_error metadata flag, got %+v", result.Metadata)
			}
			if !strings.Contains(result.Output, tc.toolErr.Error()) {
				t.Fatalf("expected error message in output, got %q", result.Output)
			}
		})
	}
}
