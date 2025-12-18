package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/model"
)

type blockingModel struct {
	started chan struct{}
	unblock chan struct{}
	once    sync.Once
}

func newBlockingModel() *blockingModel {
	return &blockingModel{
		started: make(chan struct{}, 1024),
		unblock: make(chan struct{}),
	}
}

func (m *blockingModel) Complete(ctx context.Context, _ model.Request) (*model.Response, error) {
	m.started <- struct{}{}
	select {
	case <-m.unblock:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &model.Response{Message: model.Message{Role: "assistant", Content: "ok"}}, nil
}

func (*blockingModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return errors.New("stream not supported")
}

func (m *blockingModel) Unblock() {
	if m == nil {
		return
	}
	m.once.Do(func() { close(m.unblock) })
}

func ptrBool(v bool) *bool { return &v }

func newConcurrentRuntime(t *testing.T, mdl model.Model) *Runtime {
	t.Helper()
	root := newClaudeProject(t)
	opts := Options{
		ProjectRoot:         root,
		Model:               mdl,
		EnabledBuiltinTools: []string{},
		RulesEnabled:        ptrBool(false),
	}
	rt, err := New(context.Background(), opts)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })
	return rt
}

func waitSignals(t *testing.T, ch <-chan struct{}, n int) {
	t.Helper()
	if n <= 0 {
		return
	}
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	for i := 0; i < n; i++ {
		select {
		case <-ch:
		case <-timer.C:
			t.Fatalf("timed out waiting for %d start signals (got %d)", n, i)
		}
	}
}

func drainStream(t *testing.T, stream <-chan StreamEvent) []StreamEvent {
	t.Helper()
	timer := time.NewTimer(5 * time.Second)
	defer timer.Stop()
	var events []StreamEvent
	for {
		select {
		case evt, ok := <-stream:
			if !ok {
				return events
			}
			events = append(events, evt)
		case <-timer.C:
			t.Fatalf("timed out draining stream (%d events)", len(events))
		}
	}
}

func findStreamError(events []StreamEvent) (string, bool) {
	for _, evt := range events {
		if evt.Type != EventError {
			continue
		}
		if text, ok := evt.Output.(string); ok {
			return text, true
		}
		if evt.Output == nil {
			return "", true
		}
		return "<non-string>", true
	}
	return "", false
}

type staticOKModel struct {
	content string
}

func (m staticOKModel) Complete(context.Context, model.Request) (*model.Response, error) {
	return &model.Response{Message: model.Message{Role: "assistant", Content: m.content}}, nil
}

func (staticOKModel) CompleteStream(context.Context, model.Request, model.StreamHandler) error {
	return errors.New("stream not supported")
}

func TestConcurrentExecution(t *testing.T) {
	t.Run("Run rejects concurrent on same session", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		sessionID := "sess"
		firstDone := make(chan error, 1)
		go func() {
			_, err := rt.Run(context.Background(), Request{Prompt: "first", SessionID: sessionID})
			firstDone <- err
		}()
		waitSignals(t, mdl.started, 1)

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		if _, err := rt.Run(ctx, Request{Prompt: "second", SessionID: sessionID}); !errors.Is(err, ErrConcurrentExecution) {
			t.Fatalf("expected ErrConcurrentExecution, got %v", err)
		}

		mdl.Unblock()
		if err := <-firstDone; err != nil {
			t.Fatalf("first Run failed: %v", err)
		}
	})

	t.Run("Run allows concurrent on different sessions", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		errs := make(chan error, 2)
		go func() {
			_, err := rt.Run(context.Background(), Request{Prompt: "a", SessionID: "sess-a"})
			errs <- err
		}()
		go func() {
			_, err := rt.Run(context.Background(), Request{Prompt: "b", SessionID: "sess-b"})
			errs <- err
		}()

		waitSignals(t, mdl.started, 2)
		mdl.Unblock()

		for i := 0; i < 2; i++ {
			if err := <-errs; err != nil {
				t.Fatalf("Run(%d) failed: %v", i, err)
			}
		}
	})

	t.Run("Run: 200 goroutines across 10 sessions", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		const sessions = 10
		const perSession = 20
		const contendersPerSession = perSession - 1

		type runResult struct {
			session int
			err     error
		}

		primaryDone := make(chan runResult, sessions)
		var primaryWG sync.WaitGroup
		primaryWG.Add(sessions)
		for s := 0; s < sessions; s++ {
			s := s
			sessionID := fmt.Sprintf("sess-%d", s)
			go func() {
				defer primaryWG.Done()
				_, err := rt.Run(context.Background(), Request{Prompt: "primary", SessionID: sessionID})
				primaryDone <- runResult{session: s, err: err}
			}()
		}

		waitSignals(t, mdl.started, sessions)

		start := make(chan struct{})
		totalContenders := sessions * contendersPerSession
		contenderDone := make(chan runResult, totalContenders)
		var contenderWG sync.WaitGroup
		contenderWG.Add(totalContenders)
		for s := 0; s < sessions; s++ {
			s := s
			sessionID := fmt.Sprintf("sess-%d", s)
			for i := 0; i < contendersPerSession; i++ {
				i := i
				go func() {
					defer contenderWG.Done()
					<-start
					ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
					defer cancel()
					_, err := rt.Run(ctx, Request{Prompt: fmt.Sprintf("contender-%d-%d", s, i), SessionID: sessionID})
					contenderDone <- runResult{session: s, err: err}
				}()
			}
		}
		close(start)

		contenderWG.Wait()
		close(contenderDone)

		var concurrentErrs [sessions]atomic.Int32
		var unexpected int32
		var unexpectedErr error
		for res := range contenderDone {
			if errors.Is(res.err, ErrConcurrentExecution) {
				concurrentErrs[res.session].Add(1)
				continue
			}
			atomic.AddInt32(&unexpected, 1)
			if unexpectedErr == nil {
				unexpectedErr = res.err
			}
		}
		if unexpected != 0 {
			t.Fatalf("unexpected contender results: %d (first=%v)", unexpected, unexpectedErr)
		}
		for s := 0; s < sessions; s++ {
			if got := concurrentErrs[s].Load(); got != contendersPerSession {
				t.Fatalf("session %d: expected %d ErrConcurrentExecution, got %d", s, contendersPerSession, got)
			}
		}

		select {
		case <-mdl.started:
			t.Fatal("unexpected model start signal from contenders")
		default:
		}

		mdl.Unblock()
		primaryWG.Wait()

		var successes [sessions]atomic.Int32
		for i := 0; i < sessions; i++ {
			res := <-primaryDone
			if res.err != nil {
				t.Fatalf("primary session %d failed: %v", res.session, res.err)
			}
			successes[res.session].Add(1)
		}
		for s := 0; s < sessions; s++ {
			if got := successes[s].Load(); got != 1 {
				t.Fatalf("session %d: expected 1 success, got %d", s, got)
			}
		}
		for s := 0; s < sessions; s++ {
			sessionID := fmt.Sprintf("sess-%d", s)
			if _, ok := rt.sessionGate.gates.Load(sessionID); ok {
				t.Fatalf("gate entry leaked for %q", sessionID)
			}
		}
	})

	t.Run("RunStream rejects concurrent on same session", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		sessionID := "sess"
		stream1, err := rt.RunStream(context.Background(), Request{Prompt: "first", SessionID: sessionID})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}
		waitSignals(t, mdl.started, 1)

		ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
		defer cancel()
		stream2, err := rt.RunStream(ctx, Request{Prompt: "second", SessionID: sessionID})
		if err != nil {
			t.Fatalf("RunStream: %v", err)
		}

		events2 := drainStream(t, stream2)
		got, ok := findStreamError(events2)
		if !ok || got != ErrConcurrentExecution.Error() {
			t.Fatalf("expected ErrConcurrentExecution error event, got %+v", events2)
		}

		mdl.Unblock()
		_ = drainStream(t, stream1)
	})

	t.Run("RunStream allows concurrent on different sessions", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		streamA, err := rt.RunStream(context.Background(), Request{Prompt: "a", SessionID: "sess-a"})
		if err != nil {
			t.Fatalf("RunStream(sess-a): %v", err)
		}
		streamB, err := rt.RunStream(context.Background(), Request{Prompt: "b", SessionID: "sess-b"})
		if err != nil {
			t.Fatalf("RunStream(sess-b): %v", err)
		}

		waitSignals(t, mdl.started, 2)
		mdl.Unblock()

		for _, evt := range drainStream(t, streamA) {
			if evt.Type == EventError {
				t.Fatalf("unexpected error event: %+v", evt)
			}
		}
		for _, evt := range drainStream(t, streamB) {
			if evt.Type == EventError {
				t.Fatalf("unexpected error event: %+v", evt)
			}
		}
	})

	t.Run("RunStream: 50 goroutines across 5 sessions", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		const sessions = 5
		const perSession = 10
		const total = sessions * perSession

		type callResult struct {
			stream <-chan StreamEvent
			err    error
		}

		start := make(chan struct{})
		results := make(chan callResult, total)
		var wg sync.WaitGroup
		wg.Add(total)
		for s := 0; s < sessions; s++ {
			sessionID := fmt.Sprintf("sess-%d", s)
			for i := 0; i < perSession; i++ {
				i := i
				go func() {
					defer wg.Done()
					<-start
					stream, err := rt.RunStream(context.Background(), Request{
						Prompt:    fmt.Sprintf("stream-%s-%d", sessionID, i),
						SessionID: sessionID,
					})
					results <- callResult{stream: stream, err: err}
				}()
			}
		}
		close(start)
		wg.Wait()

		streams := make([]<-chan StreamEvent, 0, total)
		for i := 0; i < total; i++ {
			res := <-results
			if res.err != nil {
				t.Fatalf("RunStream(%d) failed: %v", i, res.err)
			}
			streams = append(streams, res.stream)
		}

		waitSignals(t, mdl.started, sessions)
		select {
		case <-mdl.started:
			t.Fatal("unexpected extra model start signal before unblock")
		case <-time.After(200 * time.Millisecond):
		}

		mdl.Unblock()

		for i, stream := range streams {
			for _, evt := range drainStream(t, stream) {
				if evt.Type == EventError {
					t.Fatalf("stream %d unexpected error event: %+v", i, evt)
				}
			}
		}
	})

	t.Run("context cancel releases gate", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		sessionID := "sess"
		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan error, 1)
		go func() {
			_, err := rt.Run(ctx, Request{Prompt: "cancel", SessionID: sessionID})
			done <- err
		}()

		waitSignals(t, mdl.started, 1)
		cancel()

		timer := time.NewTimer(5 * time.Second)
		defer timer.Stop()
		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled, got %v", err)
			}
		case <-timer.C:
			t.Fatal("timed out waiting for canceled Run to return")
		}

		mdl.Unblock()
		resp, err := rt.Run(context.Background(), Request{Prompt: "ok", SessionID: sessionID})
		if err != nil {
			t.Fatalf("Run after cancel: %v", err)
		}
		if resp.Result == nil || resp.Result.Output != "ok" {
			t.Fatalf("unexpected response: %+v", resp)
		}
	})

	t.Run("cancellation releases gate (10 goroutines, 5 canceled)", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		const sessions = 5
		type runResult struct {
			sessionID string
			err       error
		}

		cancels := make([]context.CancelFunc, 0, sessions)
		holderDone := make(chan runResult, sessions)
		for s := 0; s < sessions; s++ {
			sessionID := fmt.Sprintf("sess-%d", s)
			ctx, cancel := context.WithCancel(context.Background())
			cancels = append(cancels, cancel)
			go func(sessionID string, ctx context.Context) {
				_, err := rt.Run(ctx, Request{Prompt: "hold", SessionID: sessionID})
				holderDone <- runResult{sessionID: sessionID, err: err}
			}(sessionID, ctx)
		}

		waitSignals(t, mdl.started, sessions)

		waiterDone := make(chan runResult, sessions)
		for s := 0; s < sessions; s++ {
			sessionID := fmt.Sprintf("sess-%d", s)
			go func(sessionID string) {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				_, err := rt.Run(ctx, Request{Prompt: "wait", SessionID: sessionID})
				waiterDone <- runResult{sessionID: sessionID, err: err}
			}(sessionID)
		}

		for _, cancel := range cancels {
			cancel()
		}

		for i := 0; i < sessions; i++ {
			res := <-holderDone
			if !errors.Is(res.err, context.Canceled) {
				t.Fatalf("session %q: expected context.Canceled, got %v", res.sessionID, res.err)
			}
		}

		waitSignals(t, mdl.started, sessions)
		mdl.Unblock()

		for i := 0; i < sessions; i++ {
			res := <-waiterDone
			if res.err != nil {
				t.Fatalf("waiter session %q failed: %v", res.sessionID, res.err)
			}
		}

		for s := 0; s < sessions; s++ {
			sessionID := fmt.Sprintf("sess-%d", s)
			if _, ok := rt.sessionGate.gates.Load(sessionID); ok {
				t.Fatalf("gate entry leaked for %q", sessionID)
			}
		}
	})

	t.Run("hook events isolated across 100 concurrent runs", func(t *testing.T) {
		rt := newConcurrentRuntime(t, staticOKModel{content: "ok"})

		const workers = 100
		type result struct {
			prompt string
			resp   *Response
			err    error
		}
		start := make(chan struct{})
		results := make(chan result, workers)
		var wg sync.WaitGroup
		wg.Add(workers)
		for i := 0; i < workers; i++ {
			i := i
			go func() {
				defer wg.Done()
				<-start
				prompt := fmt.Sprintf("prompt-%03d", i)
				sessionID := fmt.Sprintf("sess-%03d", i)
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				resp, err := rt.Run(ctx, Request{Prompt: prompt, SessionID: sessionID})
				results <- result{prompt: prompt, resp: resp, err: err}
			}()
		}
		close(start)
		wg.Wait()
		close(results)

		seen := make(map[string]struct{}, workers)
		for res := range results {
			if res.err != nil {
				t.Fatalf("Run failed: %v", res.err)
			}
			if res.resp == nil {
				t.Fatal("Run returned nil response")
			}
			var prompts []string
			for _, evt := range res.resp.HookEvents {
				if evt.Type != coreevents.UserPromptSubmit {
					continue
				}
				payload, ok := evt.Payload.(coreevents.UserPromptPayload)
				if !ok {
					t.Fatalf("UserPromptSubmit payload type = %T", evt.Payload)
				}
				prompts = append(prompts, payload.Prompt)
			}
			if len(prompts) != 1 || prompts[0] != res.prompt {
				t.Fatalf("expected 1 prompt event for %q, got %+v (events=%+v)", res.prompt, prompts, res.resp.HookEvents)
			}
			if _, ok := seen[res.prompt]; ok {
				t.Fatalf("duplicate prompt result for %q", res.prompt)
			}
			seen[res.prompt] = struct{}{}
		}
		if len(seen) != workers {
			t.Fatalf("expected %d unique prompts, got %d", workers, len(seen))
		}
	})

	t.Run("HTTP server scenario: concurrent sessions, serial per session", func(t *testing.T) {
		mdl := newBlockingModel()
		rt := newConcurrentRuntime(t, mdl)

		type runRequest struct {
			Prompt    string `json:"prompt"`
			SessionID string `json:"session_id"`
		}
		type runResponse struct {
			Output string `json:"output,omitempty"`
			Error  string `json:"error,omitempty"`
		}

		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer r.Body.Close()
			var in runRequest
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				w.WriteHeader(http.StatusBadRequest)
				//nolint:errcheck // test HTTP handler, error is already logged in response
				_ = json.NewEncoder(w).Encode(runResponse{Error: err.Error()})
				return
			}
			out, err := rt.Run(r.Context(), Request{Prompt: in.Prompt, SessionID: in.SessionID})
			if err != nil {
				w.WriteHeader(http.StatusConflict)
				//nolint:errcheck // test HTTP handler, error is already logged in response
				_ = json.NewEncoder(w).Encode(runResponse{Error: err.Error()})
				return
			}
			//nolint:errcheck // test HTTP handler, final response write failure is not actionable
			_ = json.NewEncoder(w).Encode(runResponse{Output: out.Result.Output})
		}))
		t.Cleanup(srv.Close)

		client := srv.Client()
		client.Timeout = 5 * time.Second

		sessions := []string{"s-1", "s-2", "s-3", "s-4", "s-5"}
		const perSession = 3

		start := make(chan struct{})
		var wg sync.WaitGroup
		errs := make(chan error, len(sessions)*perSession)

		for _, sessionID := range sessions {
			sessionID := sessionID
			wg.Add(1)
			go func() {
				defer wg.Done()
				<-start
				for i := 0; i < perSession; i++ {
					//nolint:errcheck // test code, json.Marshal with simple struct never fails
					payload, _ := json.Marshal(runRequest{Prompt: "ok", SessionID: sessionID})
					resp, err := client.Post(srv.URL, "application/json", bytes.NewReader(payload))
					if err != nil {
						errs <- err
						return
					}
					var out runResponse
					decodeErr := json.NewDecoder(resp.Body).Decode(&out)
					resp.Body.Close()
					if decodeErr != nil {
						errs <- decodeErr
						return
					}
					if out.Error != "" {
						errs <- errors.New(out.Error)
						return
					}
					if out.Output != "ok" {
						errs <- errors.New("unexpected output")
						return
					}
				}
			}()
		}

		close(start)
		waitSignals(t, mdl.started, len(sessions))
		mdl.Unblock()

		wg.Wait()
		close(errs)
		for err := range errs {
			if err != nil {
				t.Fatalf("http scenario: %v", err)
			}
		}
	})
}

func TestRuntimeConcurrent(t *testing.T) {
	TestConcurrentExecution(t)
}
