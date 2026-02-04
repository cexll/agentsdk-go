package tool

import (
	"context"
	"net/http"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/mcp"
)

type roundTripperFunc func(*http.Request) (*http.Response, error)

func (f roundTripperFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNormalizeHeadersAndMergeEnv(t *testing.T) {
	t.Parallel()

	headers := normalizeHeaders(map[string]string{
		" x-test ": " value ",
		"":         "skip",
	})
	if headers.Get("X-Test") != "value" {
		t.Fatalf("unexpected header %v", headers)
	}

	env := mergeEnv([]string{"A=1", "B=2"}, map[string]string{"B": "3", "C": "4"})
	joined := strings.Join(env, ";")
	if !strings.Contains(joined, "A=1") || !strings.Contains(joined, "B=3") || !strings.Contains(joined, "C=4") {
		t.Fatalf("unexpected env %v", env)
	}
}

func TestHeaderRoundTripper(t *testing.T) {
	t.Parallel()

	base := roundTripperFunc(func(req *http.Request) (*http.Response, error) {
		if req.Header.Get("X-Test") != "new" {
			t.Fatalf("unexpected header %q", req.Header.Get("X-Test"))
		}
		return &http.Response{StatusCode: 200, Body: http.NoBody}, nil
	})
	rt := &headerRoundTripper{
		base:    base,
		headers: http.Header{"X-Test": []string{"new"}},
	}
	req, _ := http.NewRequest(http.MethodGet, "http://example.com", nil)
	req.Header.Set("X-Test", "old")
	if _, err := rt.RoundTrip(req); err != nil {
		t.Fatalf("round trip failed: %v", err)
	}
	if _, err := rt.RoundTrip(nil); err == nil {
		t.Fatalf("expected nil request error")
	}
}

func TestApplyMCPTransportOptions(t *testing.T) {
	t.Parallel()

	if err := applyMCPTransportOptions(nil, MCPServerOptions{}); err == nil {
		t.Fatalf("expected nil transport error")
	}

	cmdTransport := &mcp.CommandTransport{}
	if err := applyMCPTransportOptions(cmdTransport, MCPServerOptions{Env: map[string]string{"A": "1"}}); err == nil {
		t.Fatalf("expected missing command error")
	}

	command := &mcp.CommandTransport{Command: exec.Command("true")}
	if err := applyMCPTransportOptions(command, MCPServerOptions{Env: map[string]string{"A": "1"}}); err != nil {
		t.Fatalf("apply env failed: %v", err)
	}
	if len(command.Command.Env) == 0 {
		t.Fatalf("expected env to be set")
	}

	sse := &mcp.SSEClientTransport{}
	if err := applyMCPTransportOptions(sse, MCPServerOptions{Headers: map[string]string{"X-Test": "1"}}); err != nil {
		t.Fatalf("apply headers failed: %v", err)
	}
	if sse.HTTPClient == nil || sse.HTTPClient.Transport == nil {
		t.Fatalf("expected injected headers")
	}
}

func TestWithInjectedHeaders(t *testing.T) {
	t.Parallel()

	client := withInjectedHeaders(nil, map[string]string{"X-Test": "1"})
	if client == nil || client.Transport == nil {
		t.Fatalf("expected http client with transport")
	}
	if _, ok := client.Transport.(*headerRoundTripper); !ok {
		t.Fatalf("expected header round tripper")
	}
	if got := normalizeHeaders(nil); got != nil {
		t.Fatalf("expected nil headers")
	}

	if err := applyMCPTransportOptions(&mcp.StreamableClientTransport{}, MCPServerOptions{Headers: map[string]string{"X": "1"}}); err != nil {
		t.Fatalf("unexpected error %v", err)
	}
}

func TestMCPToolsChangedHandlerNilSession(t *testing.T) {
	t.Parallel()

	r := NewRegistry()
	handler := r.mcpToolsChangedHandler("server")
	if handler == nil {
		t.Fatalf("expected handler")
	}
	handler(context.Background(), nil)
	time.Sleep(10 * time.Millisecond)
}
