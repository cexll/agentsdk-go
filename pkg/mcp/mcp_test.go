package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestNewSpecClient(t *testing.T) {
	t.Parallel()

	server := newEchoServer(t)
	handler := mcpsdk.NewSSEHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	client, err := NewSpecClient(ts.URL)
	if err != nil {
		t.Fatalf("NewSpecClient failed: %v", err)
	}
	t.Cleanup(func() { _ = client.Close() })

	ctx := context.Background()
	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := client.InvokeTool(ctx, "echo", map[string]any{"text": "ping"})
	if err != nil {
		t.Fatalf("InvokeTool failed: %v", err)
	}
	if got := firstText(res); got != "ping" {
		t.Fatalf("tool output %q, want %q", got, "ping")
	}
}

func TestNewSpecClientInvalidSpec(t *testing.T) {
	t.Parallel()

	if _, err := NewSpecClient("   "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty spec error, got %v", err)
	}
}

func TestSpecClientListTools(t *testing.T) {
	t.Parallel()

	session, cleanup := newInMemorySession(t)
	defer cleanup()

	client := &specClientWrapper{session: session}
	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools error: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools %+v", tools)
	}

	var nilClient *specClientWrapper
	if _, err := nilClient.ListTools(context.Background()); err == nil || !strings.Contains(err.Error(), "session is nil") {
		t.Fatalf("expected nil session error, got %v", err)
	}
}

func TestSpecClientInvokeTool(t *testing.T) {
	t.Parallel()

	session, cleanup := newInMemorySession(t)
	defer cleanup()
	client := &specClientWrapper{session: session}

	ctx := context.Background()
	res, err := client.InvokeTool(ctx, "echo", map[string]any{"text": "pong"})
	if err != nil {
		t.Fatalf("InvokeTool error: %v", err)
	}
	if firstText(res) != "pong" {
		t.Fatalf("unexpected tool result: %+v", res)
	}

	if _, err := client.InvokeTool(ctx, "   ", nil); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty name error, got %v", err)
	}

	_ = session.Close()
	if _, err := client.InvokeTool(ctx, "echo", nil); err == nil || !strings.Contains(strings.ToLower(err.Error()), "closed") {
		t.Fatalf("expected closed session error, got %v", err)
	}
}

func TestSpecClientClose(t *testing.T) {
	t.Parallel()

	session, cleanup := newInMemorySession(t)
	defer cleanup()
	client := &specClientWrapper{session: session}

	if err := client.Close(); err != nil {
		t.Fatalf("first close failed: %v", err)
	}
	if err := client.Close(); err != nil {
		t.Fatalf("second close failed: %v", err)
	}

	var nilClient *specClientWrapper
	if err := nilClient.Close(); err != nil {
		t.Fatalf("nil client close error: %v", err)
	}
}

func TestConnectSession(t *testing.T) {
	t.Parallel()

	server := newEchoServer(t)
	handler := mcpsdk.NewSSEHandler(func(*http.Request) *mcpsdk.Server { return server }, nil)
	ts := httptest.NewServer(handler)
	t.Cleanup(ts.Close)

	session, err := ConnectSession(context.Background(), ts.URL)
	if err != nil {
		t.Fatalf("ConnectSession failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	if session.InitializeResult() == nil {
		t.Fatalf("session not initialized")
	}

	if _, err := ConnectSession(context.Background(), "   "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty spec error, got %v", err)
	}

	blockListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen failed: %v", err)
	}
	defer blockListener.Close()

	// Hold the TCP connection open until the client context is canceled so the
	// dial path is forced to honor the deadline instead of failing early with a
	// local socket error.
	timeoutCtx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	go func() {
		conn, err := blockListener.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		<-timeoutCtx.Done()
	}()

	if _, err := ConnectSession(timeoutCtx, "http://"+blockListener.Addr().String()); err == nil {
		t.Fatalf("expected timeout error")
	} else if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context timeout, got %v", err)
	}
}

func TestBuildMCPTransport(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	tests := []struct {
		name         string
		spec         string
		wantType     string
		wantEndpoint string
		wantArgs     []string
		wantErr      string
	}{
		{name: "stdio", spec: "stdio://echo hi", wantType: "stdio", wantArgs: []string{"echo", "hi"}},
		{name: "sse guess", spec: "sse://example.com", wantType: "sse", wantEndpoint: "https://example.com"},
		{name: "http implicit sse", spec: "http://example.com/path", wantType: "sse", wantEndpoint: "http://example.com/path"},
		{name: "http stream hint", spec: "https+stream://api.example.com", wantType: "stream", wantEndpoint: "https://api.example.com"},
		{name: "fallback stdio", spec: "printf hi", wantType: "stdio", wantArgs: []string{"printf", "hi"}},
		{name: "invalid empty", spec: "  ", wantErr: "empty"},
		{name: "invalid hint", spec: "http+foo://example.com", wantErr: "unsupported"},
		{name: "invalid sse scheme", spec: "sse://ftp://example.com", wantErr: "unsupported scheme"},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			tr, err := buildSessionTransport(ctx, tt.spec)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("want error containing %q, got %v", tt.wantErr, err)
				}
				if tr != nil {
					t.Fatalf("expected nil transport on error")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			switch v := tr.(type) {
			case *CommandTransport:
				if tt.wantType != "stdio" {
					t.Fatalf("unexpected transport %T", tr)
				}
				if len(tt.wantArgs) > 0 && !equalStrings(v.Command.Args, tt.wantArgs) {
					t.Fatalf("args = %v want %v", v.Command.Args, tt.wantArgs)
				}
			case *SSEClientTransport:
				if tt.wantType != "sse" {
					t.Fatalf("unexpected transport %T", tr)
				}
				if v.Endpoint != tt.wantEndpoint {
					t.Fatalf("endpoint = %s want %s", v.Endpoint, tt.wantEndpoint)
				}
			case *StreamableClientTransport:
				if tt.wantType != "stream" {
					t.Fatalf("unexpected transport %T", tr)
				}
				if v.Endpoint != tt.wantEndpoint {
					t.Fatalf("endpoint = %s want %s", v.Endpoint, tt.wantEndpoint)
				}
			default:
				t.Fatalf("unexpected transport type %T", tr)
			}
		})
	}
}

func TestInitializeSession(t *testing.T) {
	t.Parallel()

	session, cleanup := newInMemorySession(t)
	defer cleanup()

	if err := EnsureSessionInitialized(context.Background(), session); err != nil {
		t.Fatalf("EnsureSessionInitialized failed: %v", err)
	}

	if err := EnsureSessionInitialized(context.TODO(), session); err != nil {
		t.Fatalf("EnsureSessionInitialized with TODO context failed: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := EnsureSessionInitialized(ctx, session); err == nil || !strings.Contains(err.Error(), "connect context") {
		t.Fatalf("expected canceled context error, got %v", err)
	}

	if err := EnsureSessionInitialized(context.Background(), (*ClientSession)(nil)); err == nil || !strings.Contains(err.Error(), "session is nil") {
		t.Fatalf("expected nil session error, got %v", err)
	}

	if err := EnsureSessionInitialized(context.Background(), &ClientSession{}); err == nil || !strings.Contains(err.Error(), "missing initialize result") {
		t.Fatalf("expected missing init error, got %v", err)
	}
}

func TestNormalizeHTTPURL(t *testing.T) {
	t.Parallel()

	if got, err := normalizeHTTPURL("example.com", true); err != nil || got != "https://example.com" {
		t.Fatalf("guess normalize got %q err %v", got, err)
	}
	if _, err := normalizeHTTPURL("   ", false); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty error, got %v", err)
	}
	if _, err := normalizeHTTPURL("ftp://example.com", false); err == nil || !strings.Contains(err.Error(), "unsupported") {
		t.Fatalf("expected unsupported error, got %v", err)
	}
}

func TestNonNilContext(t *testing.T) {
	t.Parallel()

	if nonNilContext(context.TODO()) == nil {
		t.Fatalf("TODO context replaced unexpectedly")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if nonNilContext(ctx) != ctx {
		t.Fatalf("existing context replaced unexpectedly")
	}
}

func TestCompatibilityWrappers(t *testing.T) {
	t.Parallel()

	if tr, err := BuildSessionTransport(context.Background(), "printf ok"); err != nil {
		t.Fatalf("BuildSessionTransport failed: %v", err)
	} else if _, ok := tr.(*CommandTransport); !ok {
		t.Fatalf("expected CommandTransport, got %T", tr)
	}

	if _, err := BuildSSETransport("ftp://example.com", false); err == nil {
		t.Fatalf("expected BuildSSETransport error for bad scheme")
	}
	if tr, err := BuildSSETransport("example.com", true); err != nil || tr == nil {
		t.Fatalf("BuildSSETransport guess failed: %v", err)
	}

	if _, err := BuildStreamableTransport("ftp://example.com"); err == nil {
		t.Fatalf("expected streamable transport error")
	}
	if tr, err := BuildStreamableTransport("https://example.com"); err != nil || tr == nil {
		t.Fatalf("valid streamable transport failed: %v", err)
	}

	if _, err := BuildStdioTransport(context.Background(), "   "); err == nil {
		t.Fatalf("expected stdio empty error")
	}
	if tr, err := BuildStdioTransport(context.Background(), "echo hi"); err != nil || tr == nil {
		t.Fatalf("valid stdio transport failed: %v", err)
	}

	if kind, endpoint, matched, err := ParseHTTPFamilySpec("http+sse://example.com"); err != nil || !matched || kind != sseHintType || endpoint == "" {
		t.Fatalf("ParseHTTPFamilySpec unexpected result: kind=%s matched=%v endpoint=%s err=%v", kind, matched, endpoint, err)
	}
	if _, _, matched, err := ParseHTTPFamilySpec("::::"); err != nil || matched {
		t.Fatalf("ParseHTTPFamilySpec should ignore parse failure, matched=%v err=%v", matched, err)
	}

	if _, err := NormalizeHTTPURL("https://", false); err == nil {
		t.Fatalf("expected missing host error")
	}
	if got, err := NormalizeHTTPURL("HTTP://Example.com/path", false); err != nil || got != "http://Example.com/path" {
		t.Fatalf("NormalizeHTTPURL unexpected result %q err=%v", got, err)
	}

	if NonNilContext(context.TODO()) == nil {
		t.Fatalf("NonNilContext returned nil")
	}
}

func newEchoServer(t *testing.T) *mcpsdk.Server {
	t.Helper()

	server := NewServer(&Implementation{Name: "echo-server", Version: "test"}, nil)
	server.AddTool(&Tool{
		Name:        "echo",
		Description: "echo text",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}, func(_ context.Context, req *mcpsdk.CallToolRequest) (*CallToolResult, error) {
		var args map[string]any
		if err := json.Unmarshal(req.Params.Arguments, &args); err != nil {
			return nil, err
		}
		text, ok := args["text"].(string)
		if !ok {
			return nil, errors.New("text argument missing or not string")
		}
		return &CallToolResult{Content: []Content{&TextContent{Text: text}}}, nil
	})
	return server
}

func newInMemorySession(t *testing.T) (*ClientSession, func()) {
	t.Helper()

	clientTransport, serverTransport := NewInMemoryTransports()
	server := newEchoServer(t)

	serverCtx, serverCancel := context.WithCancel(context.Background())
	serverSession, err := server.Connect(serverCtx, serverTransport, nil)
	if err != nil {
		t.Fatalf("server connect failed: %v", err)
	}

	client := NewClient(&Implementation{Name: "client", Version: "test"}, nil)
	clientSession, err := client.Connect(context.Background(), clientTransport, nil)
	if err != nil {
		t.Fatalf("client connect failed: %v", err)
	}

	cleanup := func() {
		_ = clientSession.Close()
		_ = serverSession.Close()
		serverCancel()
	}
	return clientSession, cleanup
}

func firstText(res *CallToolResult) string {
	if res == nil {
		return ""
	}
	for _, c := range res.Content {
		if txt, ok := c.(*TextContent); ok {
			return txt.Text
		}
	}
	return ""
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
