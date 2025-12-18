package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/model"
)

type staticModel struct {
	content string
}

func (m staticModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: m.content}}, nil
}

func (staticModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return errors.New("stream not supported")
}

func boolPtr(v bool) *bool { return &v }

func newTestRuntime(t *testing.T, mdl model.Model, auto CompactConfig) *Runtime {
	t.Helper()
	root := t.TempDir()
	opts := Options{
		ProjectRoot:         root,
		Model:               mdl,
		EnabledBuiltinTools: []string{},
		RulesEnabled:        boolPtr(false),
		AutoCompact:         auto,
		TokenLimit:          50,
	}
	rt, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	// Assert that the runtime-wide recorder is no longer consulted anywhere.
	rt.recorder = nil
	return rt
}

func promptEvents(t *testing.T, events []coreevents.Event) []string {
	t.Helper()
	out := make([]string, 0, len(events))
	for _, evt := range events {
		if evt.Type != coreevents.UserPromptSubmit {
			continue
		}
		payload, ok := evt.Payload.(coreevents.UserPromptPayload)
		if !ok {
			t.Fatalf("UserPromptSubmit payload type = %T", evt.Payload)
		}
		out = append(out, payload.Prompt)
	}
	return out
}

func mustContainEventType(t *testing.T, events []coreevents.Event, typ coreevents.EventType) {
	t.Helper()
	for _, evt := range events {
		if evt.Type == typ {
			return
		}
	}
	t.Fatalf("expected event type %s, got %+v", typ, events)
}

func TestHookRecorder_SingleRequestCollectsHookEvents(t *testing.T) {
	rt := newTestRuntime(t, staticModel{content: "ok"}, CompactConfig{})

	resp, err := rt.Run(context.Background(), Request{Prompt: "hello", SessionID: "s-1"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp == nil {
		t.Fatal("Run returned nil response")
	}

	prompts := promptEvents(t, resp.HookEvents)
	if len(prompts) != 1 || prompts[0] != "hello" {
		t.Fatalf("expected prompt event for %q, got %+v", "hello", prompts)
	}
}

func TestHookRecorder_ConcurrentRequestsAreIsolated(t *testing.T) {
	rt := newTestRuntime(t, staticModel{content: "ok"}, CompactConfig{})

	const workers = 32
	results := make(chan struct {
		prompt string
		resp   *Response
		err    error
	}, workers)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			prompt := fmt.Sprintf("prompt-%d", i)
			sessionID := fmt.Sprintf("sess-%d", i)
			resp, err := rt.Run(context.Background(), Request{Prompt: prompt, SessionID: sessionID})
			results <- struct {
				prompt string
				resp   *Response
				err    error
			}{prompt: prompt, resp: resp, err: err}
		}()
	}
	wg.Wait()
	close(results)

	for res := range results {
		if res.err != nil {
			t.Fatalf("Run(%q): %v", res.prompt, res.err)
		}
		if res.resp == nil {
			t.Fatalf("Run(%q): nil response", res.prompt)
		}
		prompts := promptEvents(t, res.resp.HookEvents)
		if len(prompts) != 1 || prompts[0] != res.prompt {
			t.Fatalf("expected isolated prompt event for %q, got %+v", res.prompt, prompts)
		}
	}
}

func TestHookRecorder_CompactorUsesRequestScopedRecorder(t *testing.T) {
	auto := CompactConfig{Enabled: true, Threshold: 0.1, PreserveCount: 1}
	rt := newTestRuntime(t, staticModel{content: "SUM"}, auto)

	long := strings.Repeat("a", 400)

	// First run seeds history; should not compact (len(history)==1 before model completion).
	if _, err := rt.Run(context.Background(), Request{Prompt: long, SessionID: "sess"}); err != nil {
		t.Fatalf("seed Run: %v", err)
	}

	// Second run should trigger compaction and record compactor events into this request's recorder.
	resp, err := rt.Run(context.Background(), Request{Prompt: long, SessionID: "sess"})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if resp == nil {
		t.Fatal("Run returned nil response")
	}

	mustContainEventType(t, resp.HookEvents, coreevents.PreCompact)
	mustContainEventType(t, resp.HookEvents, coreevents.ContextCompacted)
}
