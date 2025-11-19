package mcp

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSSEWaitReadyCtxCancel(t *testing.T) {
	transport := &SSETransport{
		ready:   make(chan struct{}),
		pending: newPendingTracker(),
	}
	transport.ctx, transport.cancel = context.WithCancel(context.Background())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Millisecond)
	cancel()
	if err := transport.waitReady(ctx); err == nil {
		t.Fatal("expected context cancellation")
	}
}

func TestSSEConsumeOnceStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "boom", http.StatusServiceUnavailable)
	}))
	defer srv.Close()

	tr := &SSETransport{
		client:  srv.Client(),
		events:  srv.URL,
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	if _, err := tr.consumeOnce(); err == nil || !strings.Contains(err.Error(), "events status") {
		t.Fatalf("expected status error, got %v", err)
	}
}

func TestSSEDispatchStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bad", http.StatusBadGateway)
	}))
	defer srv.Close()

	tr := &SSETransport{
		client: srv.Client(),
		rpc:    srv.URL,
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	err := tr.dispatch(ctx, &Request{ID: "1", JSONRPC: jsonRPCVersion})
	if err == nil || !strings.Contains(err.Error(), "rpc status") {
		t.Fatalf("expected rpc status error, got %v", err)
	}
}

func TestSSEHandlePayloadDecodeError(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	if err := tr.handlePayload("{"); err == nil {
		t.Fatal("expected decode error")
	}
	if err := tr.handlePayload(`{"id":"skip"}`); err != nil {
		t.Fatalf("handle valid payload: %v", err)
	}
}

func TestSSEWatchHeartbeatInterrupts(t *testing.T) {
	tr := &SSETransport{
		pending:    newPendingTracker(),
		hbInterval: 5 * time.Millisecond,
		hbTimeout:  5 * time.Millisecond,
		ready:      make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	tr.heartbeat.Store(time.Now().Add(-time.Minute).UnixNano())
	tr.wg.Add(1)
	go tr.watchHeartbeat()
	time.Sleep(15 * time.Millisecond)
	tr.cancel()
	tr.wg.Wait()
}

func TestSSEFailSignalsReady(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	tr.fail(fmt.Errorf("boom"))
	select {
	case <-tr.ready:
	default:
		t.Fatal("ready not closed after fail")
	}
}

func TestSSECallPendingError(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
	}
	close(tr.ready)
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	tr.pending.failAll(ErrTransportClosed)
	if _, err := tr.Call(context.Background(), &Request{ID: "x"}); err == nil {
		t.Fatal("expected add error")
	}
}

func TestSSECallContextCancel(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
		client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("ok")),
				Header:     make(http.Header),
			}, nil
		})},
		rpc: "http://example/rpc",
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	close(tr.ready)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if _, err := tr.Call(ctx, &Request{ID: "ctx"}); err == nil {
		t.Fatal("expected context cancellation")
	}
}

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}

func TestSSEWaitReadyTransportClosed(t *testing.T) {
	tr := &SSETransport{
		ready:   make(chan struct{}),
		pending: newPendingTracker(),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	tr.cancel()
	if err := tr.waitReady(context.Background()); err != ErrTransportClosed {
		t.Fatalf("expected transport closed, got %v", err)
	}
}

func TestSSERunStreamBackoff(t *testing.T) {
	tr := &SSETransport{
		pending:      newPendingTracker(),
		reconInitial: time.Millisecond,
		reconMax:     2 * time.Millisecond,
		ready:        make(chan struct{}),
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	callCount := 0
	tr.consume = func() (bool, error) {
		callCount++
		if callCount == 1 {
			return false, fmt.Errorf("first failure")
		}
		if callCount == 2 {
			return true, fmt.Errorf("second failure")
		}
		time.Sleep(2 * time.Millisecond)
		return true, io.EOF
	}
	tr.wg.Add(1)
	go tr.runStream()
	time.Sleep(10 * time.Millisecond)
	tr.cancel()
	tr.wg.Wait()
	if callCount < 3 {
		t.Fatalf("expected multiple consume attempts, got %d", callCount)
	}
}

func TestSSECallSuccess(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
		client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusOK,
				Body:       io.NopCloser(strings.NewReader("{}")),
				Header:     make(http.Header),
			}, nil
		})},
		rpc: "http://example/rpc",
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	close(tr.ready)
	req := &Request{ID: "success"}
	go func() {
		time.Sleep(5 * time.Millisecond)
		tr.pending.deliver(req.ID, callResult{resp: &Response{ID: req.ID}})
	}()
	if _, err := tr.Call(context.TODO(), req); err != nil {
		t.Fatalf("call failed: %v", err)
	}
}

func TestSSECallDispatchError(t *testing.T) {
	tr := &SSETransport{
		pending: newPendingTracker(),
		ready:   make(chan struct{}),
		client: &http.Client{Transport: roundTripperFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{
				StatusCode: http.StatusBadGateway,
				Body:       io.NopCloser(strings.NewReader("bad")),
				Header:     make(http.Header),
			}, nil
		})},
		rpc: "http://example/rpc",
	}
	tr.ctx, tr.cancel = context.WithCancel(context.Background())
	close(tr.ready)
	if _, err := tr.Call(context.Background(), &Request{ID: "dispatch"}); err == nil || !strings.Contains(err.Error(), "rpc status") {
		t.Fatalf("expected dispatch error, got %v", err)
	}
}

func TestSSEHandlePayloadVariants(t *testing.T) {
	tr := &SSETransport{pending: newPendingTracker()}
	if err := tr.handlePayload("   "); err != nil {
		t.Fatalf("empty payload should be ignored: %v", err)
	}
	if err := tr.handlePayload(`{"jsonrpc":"2.0"}`); err != nil {
		t.Fatalf("missing id should be ignored: %v", err)
	}
	ch, err := tr.pending.add("id")
	if err != nil {
		t.Fatalf("add: %v", err)
	}
	if err := tr.handlePayload(`{"jsonrpc":"2.0","id":"id"}`); err != nil {
		t.Fatalf("handle valid payload: %v", err)
	}
	select {
	case <-ch:
	case <-time.After(time.Second):
		t.Fatal("payload not delivered")
	}
}

func TestSSEClearConn(t *testing.T) {
	tr := &SSETransport{}
	body := io.NopCloser(strings.NewReader(""))
	tr.conn = body
	tr.clearConn(body)
	if tr.conn != nil {
		t.Fatal("clearConn should reset when matching")
	}
}
