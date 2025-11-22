package mcp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestRetryTransportContextCanceled tests retry when context is already canceled.
func TestRetryTransportContextCanceled(t *testing.T) {
	inner := &stubTransport{err: errors.New("inner error")}
	policy := RetryPolicy{
		MaxAttempts: 3,
		Retryable: func(err error) bool {
			return true
		},
		Backoff: func(attempt int) time.Duration {
			return time.Millisecond
		},
		Sleep: func(d time.Duration) {
			time.Sleep(d)
		},
	}
	retry := NewRetryTransport(inner, policy)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := retry.Call(ctx, &Request{ID: "canceled"})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

// TestRetryTransportContextDuringRetry tests context canceled during retry.
func TestRetryTransportContextDuringRetry(t *testing.T) {
	attemptCount := 0
	var attemptMu sync.Mutex
	inner := &customTransport{
		callFunc: func(ctx context.Context, req *Request) (*Response, error) {
			attemptMu.Lock()
			attemptCount++
			attemptMu.Unlock()
			return nil, errors.New("retryable error")
		},
	}
	policy := RetryPolicy{
		MaxAttempts: 5,
		Retryable: func(err error) bool {
			return true
		},
		Backoff: func(attempt int) time.Duration {
			return 10 * time.Millisecond
		},
		Sleep: func(d time.Duration) {
			time.Sleep(d)
		},
	}
	retry := NewRetryTransport(inner, policy)

	ctx, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()

	_, err := retry.Call(ctx, &Request{ID: "timeout"})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("expected context.DeadlineExceeded, got %v", err)
	}
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if attemptCount > 4 {
		t.Fatalf("expected early abort, got %d attempts", attemptCount)
	}
}

// TestRetryTransportNonRetryableError tests that non-retryable errors abort immediately.
func TestRetryTransportNonRetryableError(t *testing.T) {
	attemptCount := 0
	var attemptMu sync.Mutex
	inner := &customTransport{
		callFunc: func(ctx context.Context, req *Request) (*Response, error) {
			attemptMu.Lock()
			attemptCount++
			attemptMu.Unlock()
			return nil, errors.New("fatal error")
		},
	}
	policy := RetryPolicy{
		MaxAttempts: 3,
		Retryable: func(err error) bool {
			return false // Never retryable
		},
		Backoff: func(attempt int) time.Duration {
			return time.Millisecond
		},
		Sleep: func(d time.Duration) {},
	}
	retry := NewRetryTransport(inner, policy)

	_, err := retry.Call(context.Background(), &Request{ID: "fatal"})
	if err == nil || !strings.Contains(err.Error(), "fatal error") {
		t.Fatalf("expected fatal error, got %v", err)
	}
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if attemptCount != 1 {
		t.Fatalf("expected exactly 1 attempt for non-retryable, got %d", attemptCount)
	}
}

// TestRetryTransportMaxAttemptsReached tests exhausting all retry attempts.
func TestRetryTransportMaxAttemptsReached(t *testing.T) {
	attemptCount := 0
	var attemptMu sync.Mutex
	inner := &customTransport{
		callFunc: func(ctx context.Context, req *Request) (*Response, error) {
			attemptMu.Lock()
			attemptCount++
			current := attemptCount
			attemptMu.Unlock()
			return nil, fmt.Errorf("attempt %d failed", current)
		},
	}
	policy := RetryPolicy{
		MaxAttempts: 3,
		Retryable: func(err error) bool {
			return true
		},
		Backoff: func(attempt int) time.Duration {
			return time.Millisecond
		},
		Sleep: func(d time.Duration) {},
	}
	retry := NewRetryTransport(inner, policy)

	_, err := retry.Call(context.Background(), &Request{ID: "exhaust"})
	if err == nil || !strings.Contains(err.Error(), "attempt 3 failed") {
		t.Fatalf("expected last attempt error, got %v", err)
	}
	attemptMu.Lock()
	defer attemptMu.Unlock()
	if attemptCount != 3 {
		t.Fatalf("expected exactly 3 attempts, got %d", attemptCount)
	}
}

func TestPendingTrackerDuplicateID(t *testing.T) {
	p := newPendingTracker()
	if _, err := p.add("dup"); err != nil {
		t.Fatalf("first add failed: %v", err)
	}
	if _, err := p.add("dup"); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

// TestSSEConsumeOnceRequestCreationError tests consumeOnce when request creation fails.
func TestSSEConsumeOnceRequestCreationError(t *testing.T) {
	tr := &SSETransport{
		events:  "://invalid-url",
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())

	_, err := tr.consumeOnce()
	if err == nil || !strings.Contains(err.Error(), "build events request") {
		t.Fatalf("expected request creation error, got %v", err)
	}
}

// TestSSEConsumeOnceBodyReadError tests consumeOnce when body read fails during error handling.
func TestSSEConsumeOnceBodyReadError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "100")
		w.WriteHeader(http.StatusBadRequest)
		// Close connection before sending full body
	}))
	defer srv.Close()

	tr := &SSETransport{
		client:  srv.Client(),
		events:  srv.URL,
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())

	_, err := tr.consumeOnce()
	if err == nil || !strings.Contains(err.Error(), "events status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

// TestSSEConsumeOnceHeartbeatEvent tests that heartbeat events are handled.
func TestSSEConsumeOnceHeartbeatEvent(t *testing.T) {
	sseData := "event: heartbeat\ndata: ping\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	tr := &SSETransport{
		client:  srv.Client(),
		events:  srv.URL,
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		tr.cancel()
	}()

	_, err := tr.consumeOnce()
	// heartbeat events should touch heartbeat and not cause errors, then EOF when canceled
	if err == nil {
		t.Fatal("expected error (EOF or canceled)")
	}
}

// TestSSEConsumeOnceCommentLine tests that SSE comment lines (starting with :) are handled.
func TestSSEConsumeOnceCommentLine(t *testing.T) {
	sseData := ": this is a comment\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	tr := &SSETransport{
		client:  srv.Client(),
		events:  srv.URL,
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		tr.cancel()
	}()

	_, err := tr.consumeOnce()
	// comment lines should just touch heartbeat, then EOF when canceled
	if err == nil {
		t.Fatal("expected error (EOF or canceled)")
	}
}

// TestSSEConsumeOnceDefaultDataLine tests data lines without "data:" prefix.
func TestSSEConsumeOnceDefaultDataLine(t *testing.T) {
	sseData := "raw line\n\n"
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		fmt.Fprint(w, sseData)
	}))
	defer srv.Close()

	tr := &SSETransport{
		client:  srv.Client(),
		events:  srv.URL,
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())

	go func() {
		time.Sleep(50 * time.Millisecond)
		tr.cancel()
	}()

	_, err := tr.consumeOnce()
	if err == nil {
		t.Fatal("expected error from invalid payload")
	}
}

// TestSSEDispatchEncodeError tests dispatch when JSON encoding fails.
func TestSSEDispatchEncodeError(t *testing.T) {
	tr := &SSETransport{
		client: &http.Client{},
		rpc:    "http://example.com/rpc",
	}

	req := &Request{
		ID:     "encode-error",
		Params: make(chan int), // Channels cannot be JSON-encoded
	}

	err := tr.dispatch(context.Background(), req)
	if err == nil || !strings.Contains(err.Error(), "encode request") {
		t.Fatalf("expected encode error, got %v", err)
	}
}

// TestSSEDispatchBodyDrainError tests dispatch when draining response body fails.
func TestSSEDispatchBodyDrainError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "1000")
		w.WriteHeader(http.StatusOK)
		// Close connection before sending full body
	}))
	defer srv.Close()

	tr := &SSETransport{
		client: srv.Client(),
		rpc:    srv.URL,
	}

	err := tr.dispatch(context.Background(), &Request{ID: "drain", JSONRPC: jsonRPCVersion})
	if err == nil || !strings.Contains(err.Error(), "drain rpc body") {
		t.Fatalf("expected drain error, got %v", err)
	}
}

// customTransport is a test helper that allows custom Call implementation.
type customTransport struct {
	callFunc func(context.Context, *Request) (*Response, error)
}

func (c *customTransport) Call(ctx context.Context, req *Request) (*Response, error) {
	if c.callFunc != nil {
		return c.callFunc(ctx, req)
	}
	return nil, errors.New("not implemented")
}

func (c *customTransport) Close() error {
	return nil
}
