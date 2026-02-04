package tool

import (
	"context"
	"encoding/json"
	"net/http"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"unsafe"

	"github.com/cexll/agentsdk-go/pkg/mcp"
)

func TestNewMCPClientWithOptionsDefaultPath(t *testing.T) {
	t.Parallel()

	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPTransport(t, func(context.Context, string) (mcp.Transport, error) {
		return &stubTransport{server: server}, nil
	})
	defer restore()

	session, err := newMCPClientWithOptions(context.Background(), "fake", MCPServerOptions{}, nil)
	if err != nil || session == nil {
		t.Fatalf("expected session, got %v", err)
	}
	_ = session.Close()
}

func TestRegisterMCPServerInitializeResultMissing(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = session.Close() }()
	clearInitializeResult(session)

	restore := withStubMCPClient(t, func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error) {
		return session, nil
	})
	defer restore()

	if err := NewRegistry().RegisterMCPServer(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "initialize MCP client") {
		t.Fatalf("expected initialize error, got %v", err)
	}
}

func TestRegisterMCPServerConnectContextCanceled(t *testing.T) {
	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = session.Close() }()

	restore := withStubMCPClient(t, func(context.Context, string, mcpListChangedHandler) (*mcp.ClientSession, error) {
		return session, nil
	})
	defer restore()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := NewRegistry().RegisterMCPServer(ctx, "fake", ""); err == nil || !strings.Contains(err.Error(), "connect context") {
		t.Fatalf("expected connect context error, got %v", err)
	}
}

func TestRegisterMCPSessionErrors(t *testing.T) {
	r := NewRegistry()
	if err := r.registerMCPSession("srv", "name", nil, nil, nil); err == nil {
		t.Fatalf("expected nil session error")
	}

	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo"}}}
	session, err := server.newSession()
	if err != nil {
		t.Fatalf("new session: %v", err)
	}
	defer func() { _ = session.Close() }()

	if err := r.registerMCPSession("srv", "name", session, []Tool{}, []string{"x"}); err == nil {
		t.Fatalf("expected tool mismatch error")
	}

	if err := r.registerMCPSession("srv", "name", session, []Tool{&spyTool{name: "echo"}}, []string{"echo"}); err != nil {
		t.Fatalf("unexpected register error: %v", err)
	}
	if err := r.registerMCPSession("srv", "name", session, []Tool{&spyTool{name: "echo"}}, []string{"echo"}); err == nil {
		t.Fatalf("expected duplicate tool error")
	}
}

func TestRefreshMCPToolsErrorPaths(t *testing.T) {
	if err := (*Registry)(nil).refreshMCPTools(context.Background(), "srv", ""); err == nil {
		t.Fatalf("expected nil registry error")
	}

	r := NewRegistry()
	if err := r.refreshMCPTools(context.Background(), "srv", ""); err == nil || !strings.Contains(err.Error(), "session not found") {
		t.Fatalf("expected session not found error, got %v", err)
	}

	server := &stubMCPServer{tools: []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}}
	restore := withStubMCPClient(t, sessionFactory(server))
	defer restore()

	if err := r.RegisterMCPServer(context.Background(), "fake", ""); err != nil {
		t.Fatalf("register failed: %v", err)
	}

	info := r.mcpSessions[0]
	if info != nil {
		info.toolNames = nil
	}
	server.tools = []*mcp.Tool{{Name: "echo", InputSchema: map[string]any{"type": "object"}}}
	if err := r.refreshMCPTools(context.Background(), "fake", ""); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}

func TestRegistryHelpersCoverage(t *testing.T) {
	if got := toNameSet([]string{"", "  ", "a"}); len(got) != 1 {
		t.Fatalf("expected filtered name set, got %v", got)
	}

	client := withInjectedHeaders(&http.Client{}, nil)
	if client == nil || client.Transport != nil {
		t.Fatalf("expected no transport override for empty headers")
	}

	rt := &headerRoundTripper{}
	if _, err := rt.RoundTrip(&http.Request{}); err == nil {
		t.Fatalf("expected base round trip error")
	}

	headers := normalizeHeaders(map[string]string{"": "skip", "x": "1"})
	if headers.Get("X") != "1" {
		t.Fatalf("expected canonical header, got %v", headers)
	}

	if err := applyMCPTransportOptions(&mcp.CommandTransport{Command: exec.Command("true")}, MCPServerOptions{}); err != nil {
		t.Fatalf("unexpected no-op error: %v", err)
	}
	if err := applyMCPTransportOptions(&mcp.SSEClientTransport{}, MCPServerOptions{}); err != nil {
		t.Fatalf("unexpected no-op error: %v", err)
	}
	if err := applyMCPTransportOptions(&mcp.StreamableClientTransport{}, MCPServerOptions{}); err != nil {
		t.Fatalf("unexpected no-op error: %v", err)
	}
}

func TestMergeEnvCoverage(t *testing.T) {
	env := mergeEnv(nil, map[string]string{"A": "1", "": "skip"})
	if len(env) == 0 {
		t.Fatalf("expected env entries")
	}
	env = mergeEnv([]string{"A=1", "B=2", "BAD"}, map[string]string{"B": "3", "C": "4"})
	joined := strings.Join(env, ";")
	if strings.Contains(joined, "BAD") || !strings.Contains(joined, "B=3") || !strings.Contains(joined, "C=4") {
		t.Fatalf("unexpected merge result %v", env)
	}
}

func TestConvertMCPSchemaCoverage(t *testing.T) {
	if schema, err := convertMCPSchema(nil); err != nil || schema != nil {
		t.Fatalf("expected nil schema, got %v %v", schema, err)
	}
	if schema, err := convertMCPSchema(json.RawMessage("")); err != nil || schema != nil {
		t.Fatalf("expected nil schema for empty raw, got %v %v", schema, err)
	}
	if schema, err := convertMCPSchema([]byte("")); err != nil || schema != nil {
		t.Fatalf("expected nil schema for empty bytes, got %v %v", schema, err)
	}
	if _, err := convertMCPSchema([]byte("{")); err == nil {
		t.Fatalf("expected json error")
	}

	data := map[string]any{
		"type":       "object",
		"properties": map[string]any{"x": map[string]any{"type": "string"}},
		"required":   []any{"x", 1},
	}
	schema, err := convertMCPSchema(data)
	if err != nil || schema == nil || schema.Type != "object" || len(schema.Required) == 0 {
		t.Fatalf("unexpected schema %v err %v", schema, err)
	}
}

func TestRemoteToolExecuteNilSession(t *testing.T) {
	tool := &remoteTool{name: "remote"}
	if _, err := tool.Execute(context.Background(), map[string]any{}); err == nil || !strings.Contains(err.Error(), "session is nil") {
		t.Fatalf("expected nil session error, got %v", err)
	}
}

func clearInitializeResult(session *mcp.ClientSession) {
	if session == nil {
		return
	}
	stateField := reflect.ValueOf(session).Elem().FieldByName("state")
	initField := stateField.FieldByName("InitializeResult")
	ptr := reflect.NewAt(initField.Type(), unsafe.Pointer(initField.UnsafeAddr()))
	ptr.Elem().Set(reflect.Zero(initField.Type()))
}
