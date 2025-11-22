package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHTTPTransportCallSuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "1" {
			t.Errorf("missing header on request: %#v", r.Header)
		}
		if r.Header.Get("Authorization") != "Bearer token" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":"1","result":{"ok":true}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{
		URL:       server.URL,
		Headers:   map[string]string{"X-Test": "1"},
		AuthToken: "token",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	resp, err := transport.Call(context.Background(), &Request{ID: "1", Method: "ping"})
	if err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if resp == nil || resp.ID != "1" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	_ = transport.Close()
}

func TestHTTPTransportRetryOnStatus(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) < 3 {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":"99","result":{}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{
		URL:         server.URL,
		RetryPolicy: RetryPolicy{MaxAttempts: 4, Sleep: func(time.Duration) {}},
	})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	resp, err := transport.Call(context.Background(), &Request{ID: "99", Method: "retry"})
	if err != nil {
		t.Fatalf("call should succeed after retries: %v", err)
	}
	if resp == nil || resp.ID != "99" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if attempts.Load() < 3 {
		t.Fatalf("expected retries, got %d attempts", attempts.Load())
	}
}

func TestHTTPTransportTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		w.WriteHeader(http.StatusOK)
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":"t","result":{}}`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{URL: server.URL, Timeout: 10 * time.Millisecond, RetryPolicy: RetryPolicy{MaxAttempts: 1}})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	if _, err := transport.Call(context.Background(), &Request{ID: "t", Method: "timeout"}); err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestHTTPTransportConcurrentCalls(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req Request
		require.NoError(t, json.NewDecoder(r.Body).Decode(&req))
		w.Header().Set("Content-Type", "application/json")
		out := Response{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`"ok"`)}
		payload, err := json.Marshal(out)
		require.NoError(t, err)
		if _, err := w.Write(payload); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{URL: server.URL, Timeout: time.Second})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			id := fmt.Sprintf("%d", idx)
			resp, err := transport.Call(context.Background(), &Request{ID: id, Method: "ping"})
			if err != nil {
				t.Errorf("call %d failed: %v", idx, err)
				return
			}
			if resp.ID != id {
				t.Errorf("unexpected id: %s", resp.ID)
			}
		}(i)
	}
	wg.Wait()
}

func TestHTTPTransportStatusError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		if _, err := w.Write([]byte("bad upstream")); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{URL: server.URL, RetryPolicy: RetryPolicy{MaxAttempts: 1}})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	_, err = transport.Call(context.Background(), &Request{ID: "e", Method: "boom"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "502") || !strings.Contains(err.Error(), "bad upstream") {
		t.Fatalf("unexpected error: %v", err)
	}
	if statusErr, ok := err.(interface{ StatusCode() int }); !ok || statusErr.StatusCode() != http.StatusBadGateway {
		t.Fatalf("status code not exposed: %v", err)
	}
}

func TestHTTPTransportStatusErrorEmptyBody(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()
	transport, err := NewHTTPTransport(HTTPOptions{URL: server.URL, RetryPolicy: RetryPolicy{MaxAttempts: 1}})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	_, err = transport.Call(context.Background(), &Request{ID: "nobody", Method: "ping"})
	if err == nil {
		t.Fatal("expected error")
	}
	if !strings.Contains(err.Error(), "401") {
		t.Fatalf("unexpected error text: %v", err)
	}
}

func TestHTTPTransportContextCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	transport, err := NewHTTPTransport(HTTPOptions{URL: "http://127.0.0.1"})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	if _, err := transport.Call(ctx, &Request{ID: "ctx", Method: "ping"}); err == nil {
		t.Fatal("expected context cancellation")
	}
}

func TestHTTPTransportClose(t *testing.T) {
	rt := &closableRoundTripper{}
	client := &http.Client{Transport: rt}
	transport, err := NewHTTPTransport(HTTPOptions{URL: "http://example", Client: client})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !rt.closed {
		t.Fatal("expected round tripper to be closed")
	}
}

type closableRoundTripper struct {
	closed bool
}

func (c *closableRoundTripper) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, errors.New("noop")
}

func (c *closableRoundTripper) CloseIdleConnections() { c.closed = true }

func TestHTTPTransportDecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// malformed JSON to exercise decode failure path
		if _, err := w.Write([]byte(`{"jsonrpc":"2.0","id":"x"`)); err != nil {
			t.Fatal(err)
		}
	}))
	defer server.Close()

	transport, err := NewHTTPTransport(HTTPOptions{URL: server.URL, RetryPolicy: RetryPolicy{MaxAttempts: 1}})
	if err != nil {
		t.Fatalf("build transport: %v", err)
	}
	if _, err := transport.Call(context.Background(), &Request{ID: "x", Method: "ping"}); err == nil || !strings.Contains(err.Error(), "decode response") {
		t.Fatalf("expected decode error, got %v", err)
	}
}

func TestNewHTTPTransportRequiresURL(t *testing.T) {
	if _, err := NewHTTPTransport(HTTPOptions{}); err == nil {
		t.Fatal("expected url validation error")
	}
}
