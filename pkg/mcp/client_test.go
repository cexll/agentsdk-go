package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

func TestClientCallDecodesResult(t *testing.T) {
	raw := mustMarshal(t, map[string]string{"status": "ok"})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: raw}}}
	client := NewClient(st)

	var out map[string]string
	if err := client.Call(context.Background(), "ping", nil, &out); err != nil {
		t.Fatalf("call failed: %v", err)
	}
	if out["status"] != "ok" {
		t.Fatalf("unexpected payload: %+v", out)
	}
}

func TestClientCallPropagatesError(t *testing.T) {
	st := &stubTransport{responses: []*Response{{ID: "1", Error: &Error{Code: 1, Message: "bad"}}}}
	client := NewClient(st)
	if err := client.Call(context.Background(), "ping", nil, nil); err == nil || err.Error() != "mcp error 1: bad" {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientListTools(t *testing.T) {
	payload := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "echo", Description: "Echo"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: payload}}}
	client := NewClient(st)
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected list: %+v", tools)
	}
}

func TestClientInvokeTool(t *testing.T) {
	payload := mustMarshal(t, ToolCallResult{Content: json.RawMessage(`"done"`)})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: payload}}}
	client := NewClient(st)
	res, err := client.InvokeTool(context.Background(), "echo", map[string]interface{}{"text": "hi"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if string(res.Content) != "\"done\"" {
		t.Fatalf("unexpected content: %s", string(res.Content))
	}
}

func TestClientClose(t *testing.T) {
	st := &stubTransport{}
	client := NewClient(st)
	if err := client.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	if !st.closed {
		t.Fatal("transport not closed")
	}
}

func TestClientListToolsCache(t *testing.T) {
	raw := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "cache"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: raw}, {ID: "2", Result: raw}}}
	now := time.Now()
	client := NewClient(st, WithToolCacheTTL(time.Minute), withClientClock(func() time.Time { return now }))

	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(st.calls) != 1 {
		t.Fatalf("expected single transport call, got %d", len(st.calls))
	}

	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("cached list: %v", err)
	}
	if len(st.calls) != 1 {
		t.Fatalf("cache miss triggered transport")
	}

	now = now.Add(2 * time.Minute)
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("post-expiry list: %v", err)
	}
	if len(st.calls) != 2 {
		t.Fatalf("expected cache refresh, calls=%d", len(st.calls))
	}
}

func TestClientSandboxGuard(t *testing.T) {
	raw := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "safe"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: raw}}}
	manager := sandbox.NewManager(nil, sandbox.NewDomainAllowList("allowed.dev"), nil)
	client := NewClient(st, WithSandboxHostGuard(manager, "allowed.dev"))
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("guard allowed host: %v", err)
	}

	denyClient := NewClient(st, WithSandboxHostGuard(manager, "blocked.dev"))
	if err := denyClient.Call(context.Background(), "tools/list", nil, nil); err == nil {
		t.Fatal("expected guard failure")
	}
}

func TestClientCloseNil(t *testing.T) {
	var client *Client
	if err := client.Close(); err != nil {
		t.Fatalf("nil close failed: %v", err)
	}
}

func TestClientWithRetryPolicyOption(t *testing.T) {
	stub := &retryStubTransport{
		errs:      []error{context.DeadlineExceeded},
		responses: []*Response{{ID: "1"}},
	}
	client := NewClient(stub, WithRetryPolicy(RetryPolicy{MaxAttempts: 2, Sleep: func(time.Duration) {}}))
	if err := client.Call(context.Background(), "ping", nil, nil); err != nil {
		t.Fatalf("call should retry: %v", err)
	}
}

func TestClientPreflightChain(t *testing.T) {
	raw := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "chain"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: raw}}}

	var order []string
	client := NewClient(st,
		WithPreflight(func(ctx context.Context, req *Request) error {
			order = append(order, "one")
			return nil
		}),
		WithPreflight(func(ctx context.Context, req *Request) error {
			order = append(order, "two")
			return nil
		}))
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if strings.Join(order, ",") != "one,two" {
		t.Fatalf("preflight order unexpected: %v", order)
	}

	failClient := NewClient(st, WithPreflight(func(ctx context.Context, req *Request) error {
		return errors.New("block")
	}))
	if err := failClient.Call(context.Background(), "ping", nil, nil); err == nil || err.Error() != "block" {
		t.Fatalf("expected preflight error, got %v", err)
	}
}

func TestWithRetryPolicyNilTransport(t *testing.T) {
	client := NewClient(nil, WithRetryPolicy(RetryPolicy{MaxAttempts: 1}))
	if client.transport != nil {
		t.Fatal("nil transport should remain nil")
	}
}

func TestWithSandboxHostGuardNoHost(t *testing.T) {
	client := NewClient(&stubTransport{}, WithSandboxHostGuard(sandbox.NewManager(nil, nil, nil), ""))
	if err := client.Call(context.Background(), "ping", nil, nil); err == nil {
		t.Fatalf("expected error due to missing response")
	}
}

func TestClientNextIDWrap(t *testing.T) {
	client := NewClient(&stubTransport{})
	client.seq.Store(^uint64(0))
	if got := client.nextID(); got != "1" {
		t.Fatalf("expected id wrap to 1, got %s", got)
	}
}

func TestCopyToolDescriptors(t *testing.T) {
	if out := copyToolDescriptors(nil); out != nil {
		t.Fatalf("expected nil copy, got %v", out)
	}
	source := []ToolDescriptor{{Name: "a"}}
	out := copyToolDescriptors(source)
	out[0].Name = "b"
	if source[0].Name != "a" {
		t.Fatal("copy should not mutate source")
	}
}

func TestClientInvokeToolError(t *testing.T) {
	st := &stubTransport{err: errors.New("rpc down")}
	client := NewClient(st)
	if _, err := client.InvokeTool(context.Background(), "echo", nil); err == nil || !strings.Contains(err.Error(), "rpc down") {
		t.Fatalf("expected error, got %v", err)
	}
}

func TestClientListToolsNoCache(t *testing.T) {
	payload := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "fresh"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: payload}, {ID: "2", Result: payload}}}
	client := NewClient(st, WithToolCacheTTL(0))
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools second: %v", err)
	}
	if len(st.calls) != 2 {
		t.Fatalf("expected no caching when ttl=0, calls=%d", len(st.calls))
	}
}

func TestWithPreflightHandlesNilClient(t *testing.T) {
	opt := WithPreflight(func(context.Context, *Request) error { return nil })
	opt(nil) // should be a no-op without panicking
}

func TestServerNameFallback(t *testing.T) {
	if got := serverName(""); got != "<unnamed>" {
		t.Fatalf("unexpected fallback: %s", got)
	}
	if got := serverName("named"); got != "named" {
		t.Fatalf("unexpected name: %s", got)
	}
}

func TestWithToolCacheTTLNegative(t *testing.T) {
	payload := mustMarshal(t, ToolListResult{Tools: []ToolDescriptor{{Name: "fresh"}}})
	st := &stubTransport{responses: []*Response{{ID: "1", Result: payload}, {ID: "2", Result: payload}}}
	client := NewClient(st, WithToolCacheTTL(-1))
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("list tools second: %v", err)
	}
	if len(st.calls) != 2 {
		t.Fatalf("negative ttl should disable cache, calls=%d", len(st.calls))
	}
}

func mustMarshal(t *testing.T, v any) []byte {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return data
}

type stubTransport struct {
	mu        sync.Mutex
	responses []*Response
	calls     []*Request
	closed    bool
	err       error
}

func (s *stubTransport) Call(ctx context.Context, req *Request) (*Response, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.calls = append(s.calls, req)
	if s.err != nil {
		return nil, s.err
	}
	if len(s.responses) == 0 {
		return nil, errors.New("no response queued")
	}
	resp := s.responses[0]
	s.responses = s.responses[1:]
	return resp, nil
}

func (s *stubTransport) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
	return nil
}
