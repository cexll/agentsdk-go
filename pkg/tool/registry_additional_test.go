package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/mcp"
	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
)

func TestNonNilContext(t *testing.T) {
	t.Parallel()

	if got := nonNilContext(context.TODO()); got == nil {
		t.Fatalf("TODO context replaced unexpectedly")
	}
	if got := nonNilContext(nil); got == nil { //nolint:staticcheck
		t.Fatalf("nil context should be replaced")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if got := nonNilContext(ctx); got != ctx {
		t.Fatalf("expected original context returned")
	}
}

func TestRemoteToolDescription(t *testing.T) {
	rt := &remoteTool{name: "r", description: "remote", schema: &JSONSchema{Type: "object"}}
	if rt.Name() != "r" {
		t.Fatalf("unexpected name %q", rt.Name())
	}
	if rt.Description() == "" || rt.Schema() == nil {
		t.Fatalf("remote tool metadata missing")
	}
}

func TestRegisterMCPServerRejectsEmptyPath(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "   ", ""); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty server path error, got %v", err)
	}
}

func TestRegisterMCPServerInvalidTransportSpec(t *testing.T) {
	restore := withStubMCPClient(t, func(_ context.Context, spec string, _ mcpListChangedHandler) (*mcp.ClientSession, error) {
		if spec != "stdio://invalid" {
			t.Fatalf("unexpected spec %q", spec)
		}
		return nil, fmt.Errorf("dial failed")
	})
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "stdio://invalid", ""); err == nil || !strings.Contains(err.Error(), "connect MCP client") {
		t.Fatalf("expected connect error, got %v", err)
	}
}

func TestRegisterMCPServerUsesTimeoutContext(t *testing.T) {
	var captured context.Context
	restore := withStubMCPClient(t, func(ctx context.Context, spec string, _ mcpListChangedHandler) (*mcp.ClientSession, error) {
		captured = ctx
		return nil, fmt.Errorf("dial failed")
	})
	defer restore()

	err := NewRegistry().RegisterMCPServer(context.Background(), "stdio://fail", "")
	if err == nil || !strings.Contains(err.Error(), "connect MCP client") {
		t.Fatalf("expected connect error, got %v", err)
	}
	if captured == nil {
		t.Fatalf("connect context not passed")
	}
	deadline, ok := captured.Deadline()
	if !ok {
		t.Fatalf("connect context missing deadline")
	}
	if remaining := time.Until(deadline); remaining > 10*time.Second || remaining < 9*time.Second {
		t.Fatalf("deadline not ~10s, remaining %v", remaining)
	}
	select {
	case <-captured.Done():
	default:
		t.Fatalf("connect context not canceled after return")
	}
	if err := captured.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestRegisterMCPServerWithOptionsUsesCustomTimeoutContext(t *testing.T) {
	orig := newMCPClientWithOptions
	var captured context.Context
	newMCPClientWithOptions = func(ctx context.Context, spec string, opts MCPServerOptions, _ mcpListChangedHandler) (*mcp.ClientSession, error) {
		captured = ctx
		return nil, fmt.Errorf("dial failed")
	}
	defer func() { newMCPClientWithOptions = orig }()

	err := NewRegistry().RegisterMCPServerWithOptions(context.Background(), "stdio://fail", "", MCPServerOptions{
		Timeout: 2 * time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "connect MCP client") {
		t.Fatalf("expected connect error, got %v", err)
	}
	if captured == nil {
		t.Fatalf("connect context not passed")
	}
	deadline, ok := captured.Deadline()
	if !ok {
		t.Fatalf("connect context missing deadline")
	}
	if remaining := time.Until(deadline); remaining > 2*time.Second || remaining < time.Second {
		t.Fatalf("deadline not ~2s, remaining %v", remaining)
	}
	select {
	case <-captured.Done():
	default:
		t.Fatalf("connect context not canceled after return")
	}
	if err := captured.Err(); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected canceled context, got %v", err)
	}
}

func TestRegisterMCPServerTransportBuilderError(t *testing.T) {
	restore := withStubMCPClient(t, func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error) {
		return nil, errors.New("boom")
	})
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "spec", ""); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestRegisterMCPServerListToolsError(t *testing.T) {
	server := &stubMCPServer{listErr: errors.New("list failed")}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "list MCP tools") {
		t.Fatalf("expected list error, got %v", err)
	}
	if !server.Closed() {
		t.Fatalf("session should close on failure")
	}
}

func TestRegisterMCPServerNoTools(t *testing.T) {
	server := &stubMCPServer{}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "returned no tools") {
		t.Fatalf("expected empty tool list error, got %v", err)
	}
	if !server.Closed() {
		t.Fatalf("session should close on failure")
	}
}

func TestRegisterMCPServerEmptyToolName(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "  "}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected empty tool name error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateLocalTool(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "dup"}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.Register(&spyTool{name: "dup"}); err != nil {
		t.Fatalf("setup register failed: %v", err)
	}
	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}

func TestRegisterMCPServerSchemaError(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "bad", InputSchema: json.RawMessage("123")}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "parse schema for bad") {
		t.Fatalf("expected schema parse error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateRemoteTools(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "dup"}, {Name: "dup"}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate remote error, got %v", err)
	}
	if !server.Closed() {
		t.Fatalf("session should close on failure")
	}
}

func TestRegisterMCPServerSuccessAddsClient(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote tool", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if len(r.mcpSessions) != 1 {
		t.Fatalf("expected client to be tracked, got %d", len(r.mcpSessions))
	}
	if server.Closed() {
		t.Fatalf("session should remain open after success")
	}
	for _, info := range r.mcpSessions {
		if info != nil && info.session != nil {
			_ = info.session.Close()
		}
	}
}

func TestRegisterMCPServerNamespacesRemoteTools(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote tool", InputSchema: map[string]any{"type": "object"}}}}
	server.callFn = func(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		if params.Name != "echo" {
			return nil, fmt.Errorf("unexpected tool %s", params.Name)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "fake", "svc"); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	t.Cleanup(r.Close)

	if _, err := r.Get("svc__echo"); err != nil {
		t.Fatalf("expected namespaced tool to be registered: %v", err)
	}
	if _, err := r.Get("echo"); err == nil {
		t.Fatalf("expected unnamespaced tool to be missing")
	}

	res, err := r.Execute(context.Background(), "svc__echo", nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(res.Output, "ok") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestRegistryCloseClosesSessions(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	r.Close()

	if !server.Closed() {
		t.Fatalf("expected session to be closed")
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	if len(r.mcpSessions) != 0 {
		t.Fatalf("expected session list cleared, got %d", len(r.mcpSessions))
	}
}

func TestRegistryRefreshMCPToolsReplacesSnapshot(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if _, err := r.Get("echo"); err != nil {
		t.Fatalf("expected echo tool: %v", err)
	}

	server.tools = []*mcp.Tool{{Name: "sum", Description: "remote", InputSchema: map[string]any{"type": "object"}}}
	if err := r.refreshMCPTools(context.Background(), "fake", ""); err != nil {
		t.Fatalf("refresh failed: %v", err)
	}
	if _, err := r.Get("sum"); err != nil {
		t.Fatalf("expected sum tool after refresh: %v", err)
	}
	if _, err := r.Get("echo"); err == nil {
		t.Fatalf("expected old tool to be removed")
	}
}

func TestRegisterMCPServerWithOptionsSuccess(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote", InputSchema: map[string]any{"type": "object"}}}}
	orig := newMCPClientWithOptions
	newMCPClientWithOptions = func(context.Context, string, MCPServerOptions, mcpListChangedHandler) (*mcp.ClientSession, error) {
		return server.newSession()
	}
	defer func() { newMCPClientWithOptions = orig }()

	r := NewRegistry()
	if err := r.RegisterMCPServerWithOptions(context.Background(), "fake", "svc", MCPServerOptions{Headers: map[string]string{"a": "b"}}); err != nil {
		t.Fatalf("register with options failed: %v", err)
	}
	if _, err := r.Get("svc__echo"); err != nil {
		t.Fatalf("expected namespaced tool: %v", err)
	}
	r.Close()
}

func TestConnectMCPClientWithOptions(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	transport := &stubTransport{server: server}

	restore := withStubMCPTransport(t, func(context.Context, string) (mcp.Transport, error) {
		return transport, nil
	})
	defer restore()

	session, err := connectMCPClientWithOptions(context.Background(), "fake", MCPServerOptions{}, func(context.Context, *mcp.ClientSession) {})
	if err != nil || session == nil {
		t.Fatalf("connect failed: %v", err)
	}
	_ = session.Close()
}

func TestConnectMCPClientWithOptionsTransportError(t *testing.T) {
	restore := withStubMCPTransport(t, func(context.Context, string) (mcp.Transport, error) {
		return nil, errors.New("boom")
	})
	defer restore()

	if _, err := connectMCPClientWithOptions(context.Background(), "fake", MCPServerOptions{}, nil); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestMCPToolsChangedHandlerRefreshes(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", Description: "remote", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	info := r.mcpSessions[0]
	server.tools = []*mcp.Tool{{Name: "sum", Description: "remote", InputSchema: map[string]any{"type": "object"}}}

	handler := r.mcpToolsChangedHandler("fake")
	handler(context.Background(), info.session)

	found := false
	for i := 0; i < 50; i++ {
		if _, err := r.Get("sum"); err == nil {
			found = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !found {
		t.Fatalf("expected tools refresh to register new tool")
	}
	r.Close()
}

func TestRemoteToolExecuteWithNilParams(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "remote", InputSchema: map[string]any{"type": "object"}}}}
	server.callFn = func(_ context.Context, params *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		var args map[string]any
		if params.Arguments != nil {
			var ok bool
			args, ok = params.Arguments.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("unexpected arguments type %T", params.Arguments)
			}
		}
		if params.Name != "remote" {
			return nil, fmt.Errorf("unexpected tool %s", params.Name)
		}
		if len(args) != 0 {
			return nil, fmt.Errorf("expected empty arguments, got %v", args)
		}
		return &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "ok"}}}, nil
	}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("stub session failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tool := &remoteTool{name: "remote", description: "desc", session: session}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Output == "" || !strings.Contains(res.Output, "ok") {
		t.Fatalf("unexpected result output %q", res.Output)
	}
}

func TestRemoteToolExecuteError(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "remote"}}}
	server.callFn = func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error) {
		return nil, fmt.Errorf("call failed")
	}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("stub session failed: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	tool := &remoteTool{name: "remote", session: session}
	if _, err := tool.Execute(context.Background(), map[string]any{"x": 1}); err == nil || !strings.Contains(err.Error(), "call failed") {
		t.Fatalf("expected call error, got %v", err)
	}
}

func TestFindMCPSessionLockedVariants(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })

	r := NewRegistry()
	info := &mcpSessionInfo{
		serverID:  "srv",
		sessionID: "",
		session:   session,
		toolNames: map[string]struct{}{"echo": {}},
	}
	r.mcpSessions = []*mcpSessionInfo{info}

	if got := r.findMCPSessionLocked("srv", ""); got == nil {
		t.Fatalf("expected match by server id")
	}
	info.sessionID = "sess-id"
	if got := r.findMCPSessionLocked("", "sess-id"); got == nil {
		t.Fatalf("expected match by session id")
	}
	if got := r.findMCPSessionLocked("missing", "missing"); got != nil {
		t.Fatalf("expected nil match for missing ids")
	}
}

func withStubMCPClient(t *testing.T, fn func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error)) func() {
	t.Helper()
	original := newMCPClient
	newMCPClient = fn
	return func() {
		newMCPClient = original
	}
}

func withStubMCPTransport(t *testing.T, fn func(context.Context, string) (mcp.Transport, error)) func() {
	t.Helper()
	original := buildMCPTransport
	buildMCPTransport = fn
	return func() {
		buildMCPTransport = original
	}
}

func equalStringSlices(a, b []string) bool {
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

func sessionFactory(server *stubMCPServer) func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error) {
	return func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error) {
		return server.newSession()
	}
}

type stubMCPServer struct {
	tools         []*mcp.Tool
	listErr       error
	initializeErr error
	callFn        func(context.Context, *mcp.CallToolParams) (*mcp.CallToolResult, error)

	mu     sync.Mutex
	closed bool
}

func (s *stubMCPServer) newSession() (*mcp.ClientSession, error) {
	transport := &stubTransport{server: s}
	client := mcp.NewClient(&mcp.Implementation{Name: "stub-client", Version: "test"}, nil)
	return client.Connect(context.Background(), transport, nil)
}

func (s *stubMCPServer) markClosed() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.closed = true
}

func (s *stubMCPServer) Closed() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.closed
}

type stubTransport struct {
	server *stubMCPServer
}

func (t *stubTransport) Connect(context.Context) (mcp.Connection, error) {
	return newStubConnection(t.server), nil
}

type stubConnection struct {
	server   *stubMCPServer
	incoming chan jsonrpc.Message
	outgoing chan jsonrpc.Message
	closed   chan struct{}
	once     sync.Once
}

func newStubConnection(server *stubMCPServer) *stubConnection {
	conn := &stubConnection{
		server:   server,
		incoming: make(chan jsonrpc.Message, 16),
		outgoing: make(chan jsonrpc.Message, 16),
		closed:   make(chan struct{}),
	}
	go server.serve(conn.incoming, conn.outgoing, conn.closed)
	return conn
}

func (c *stubConnection) Read(ctx context.Context) (jsonrpc.Message, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-c.closed:
		return nil, io.EOF
	case msg, ok := <-c.outgoing:
		if !ok {
			return nil, io.EOF
		}
		return msg, nil
	}
}

func (c *stubConnection) Write(ctx context.Context, msg jsonrpc.Message) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-c.closed:
		return fmt.Errorf("connection closed")
	case c.incoming <- msg:
		return nil
	}
}

func (c *stubConnection) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.server.markClosed()
	})
	return nil
}

func (c *stubConnection) SessionID() string { return "" }

func (s *stubMCPServer) serve(in <-chan jsonrpc.Message, out chan<- jsonrpc.Message, closed <-chan struct{}) {
	for {
		select {
		case <-closed:
			close(out)
			return
		case msg := <-in:
			req, ok := msg.(*jsonrpc.Request)
			if !ok {
				continue
			}
			if !req.IsCall() {
				continue
			}
			resp := s.handleCall(req)
			if resp == nil {
				continue
			}
			select {
			case out <- resp:
			case <-closed:
				return
			}
		}
	}
}

func (s *stubMCPServer) handleCall(req *jsonrpc.Request) jsonrpc.Message {
	switch req.Method {
	case "initialize":
		if s.initializeErr != nil {
			return toResponse(req.ID, nil, s.initializeErr)
		}
		result := &mcp.InitializeResult{
			ProtocolVersion: "2025-06-18",
			ServerInfo:      &mcp.Implementation{Name: "stub", Version: "test"},
			Capabilities:    &mcp.ServerCapabilities{},
		}
		return toResponse(req.ID, result, nil)
	case "tools/list":
		if s.listErr != nil {
			return toResponse(req.ID, nil, s.listErr)
		}
		res := &mcp.ListToolsResult{Tools: append([]*mcp.Tool(nil), s.tools...)}
		return toResponse(req.ID, res, nil)
	case "tools/call":
		if s.callFn == nil {
			return toResponse(req.ID, nil, fmt.Errorf("call not configured"))
		}
		var params mcp.CallToolParams
		if len(req.Params) > 0 {
			if err := json.Unmarshal(req.Params, &params); err != nil {
				return toResponse(req.ID, nil, fmt.Errorf("decode call params: %w", err))
			}
		}
		result, err := s.callFn(context.Background(), &params)
		return toResponse(req.ID, result, err)
	default:
		return toResponse(req.ID, nil, fmt.Errorf("method %s not supported", req.Method))
	}
}

func toResponse(id jsonrpc.ID, value any, err error) jsonrpc.Message {
	var result json.RawMessage
	if value != nil {
		data, marshalErr := json.Marshal(value)
		if marshalErr != nil {
			err = fmt.Errorf("marshal response: %w", marshalErr)
		} else {
			result = data
		}
	}
	return &jsonrpc.Response{ID: id, Result: result, Error: err}
}
