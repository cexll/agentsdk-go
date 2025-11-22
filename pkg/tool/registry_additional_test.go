package tool

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/mcp"
)

func TestRemoteToolDescription(t *testing.T) {
	rt := &remoteTool{name: "r", description: "remote", schema: &JSONSchema{Type: "object"}}
	if rt.Description() == "" || rt.Schema() == nil {
		t.Fatalf("remote tool metadata missing")
	}
}

func TestRegisterMCPServerRejectsEmptyPath(t *testing.T) {
	r := NewRegistry()
	if err := r.RegisterMCPServer("   "); err == nil || !strings.Contains(err.Error(), "empty") {
		t.Fatalf("expected empty server path error, got %v", err)
	}
}

func TestRegisterMCPServerInvalidTransportSpec(t *testing.T) {
	client := newFakeMCPClient()
	client.callFn = func(context.Context, string, interface{}, interface{}) error {
		return fmt.Errorf("build transport: invalid stdio server path")
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	err := r.RegisterMCPServer("stdio://")
	if err == nil || !strings.Contains(err.Error(), "initialize MCP client") {
		t.Fatalf("expected initialize error, got %v", err)
	}
	if client.spec != "stdio://" {
		t.Fatalf("client factory should receive spec, got %q", client.spec)
	}
	if !client.isClosed() {
		t.Fatalf("client should be closed after failure")
	}
}

func TestRegisterMCPServerTransportBuilderError(t *testing.T) {
	errBoom := errors.New("boom")
	client := newFakeMCPClient()
	client.callFn = func(context.Context, string, interface{}, interface{}) error {
		return errBoom
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); !errors.Is(err, errBoom) {
		t.Fatalf("expected builder error, got %v", err)
	}
	if !client.isClosed() {
		t.Fatalf("client should close on init failure")
	}
}

func TestRegisterMCPServerInitializeFailureClosesTransport(t *testing.T) {
	client := newFakeMCPClient()
	client.callFn = func(context.Context, string, interface{}, interface{}) error {
		return errors.New("dial failed")
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "initialize MCP client") {
		t.Fatalf("expected initialize failure error, got %v", err)
	}
	if !client.isClosed() {
		t.Fatalf("client should be closed on failure")
	}
	if got := client.callCount("call:initialize"); got != 1 {
		t.Fatalf("expected single initialize call, got %d", got)
	}
}

func TestRegisterMCPServerListToolsError(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return nil, errors.New("list failed")
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "list MCP tools") {
		t.Fatalf("expected list error, got %v", err)
	}
	if !client.isClosed() {
		t.Fatalf("client should close when listing fails")
	}
}

func TestRegisterMCPServerNoTools(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return nil, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "returned no tools") {
		t.Fatalf("expected empty tool list error, got %v", err)
	}
}

func TestRegisterMCPServerEmptyToolName(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return []mcp.ToolDescriptor{{Name: " "}}, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected empty tool name error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateLocalTool(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return []mcp.ToolDescriptor{{Name: "dup"}}, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	if err := r.Register(&spyTool{name: "dup"}); err != nil {
		t.Fatalf("setup register failed: %v", err)
	}
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}

func TestRegisterMCPServerSchemaError(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return []mcp.ToolDescriptor{{Name: "bad", Schema: json.RawMessage(`"oops"`)}}, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "parse schema for bad") {
		t.Fatalf("expected schema parse error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateRemoteTools(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return []mcp.ToolDescriptor{{Name: "dup"}, {Name: "dup"}}, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	err := r.RegisterMCPServer("fake")
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected register failure error, got %v", err)
	}
	if !client.isClosed() {
		t.Fatalf("client should be closed when registration fails")
	}
}

func TestRegisterMCPServerSuccessAddsClient(t *testing.T) {
	client := newFakeMCPClient()
	client.listFn = func(context.Context) ([]mcp.ToolDescriptor, error) {
		return []mcp.ToolDescriptor{{Name: "echo", Description: "remote tool", Schema: json.RawMessage(`{"type":"object"}`)}}, nil
	}
	restore := withStubMCPClient(t, client)
	defer restore()

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if len(r.mcpClients) != 1 {
		t.Fatalf("expected client to be tracked, got %d", len(r.mcpClients))
	}
	if client.isClosed() {
		t.Fatalf("client should remain open after success")
	}
}

func TestInitializeMCPClientSuccess(t *testing.T) {
	client := newFakeMCPClient()
	var ctxSeen context.Context
	client.callFn = func(ctx context.Context, method string, params interface{}, dest interface{}) error {
		if method != "initialize" {
			t.Fatalf("unexpected method %s", method)
		}
		ctxSeen = ctx
		return nil
	}
	if err := initializeMCPClient(context.TODO(), client); err != nil {
		t.Fatalf("initialize should succeed: %v", err)
	}
	if ctxSeen == nil {
		t.Fatalf("expected context to be forwarded")
	}
	if client.callCount("call:initialize") != 1 {
		t.Fatalf("expected initialize call, got %d", client.callCount("call:initialize"))
	}
}

func TestInitializeMCPClientPropagatesMCPErrors(t *testing.T) {
	client := newFakeMCPClient()
	client.callFn = func(context.Context, string, interface{}, interface{}) error {
		return &mcp.Error{Code: -32000, Message: "nope"}
	}
	if err := initializeMCPClient(context.Background(), client); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected MCP error, got %v", err)
	}
}

func TestInitializeMCPClientPropagatesTransportErrors(t *testing.T) {
	client := newFakeMCPClient()
	client.callFn = func(context.Context, string, interface{}, interface{}) error {
		return errors.New("rpc down")
	}
	if err := initializeMCPClient(context.Background(), client); err == nil || !strings.Contains(err.Error(), "rpc down") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestRemoteToolExecuteWithNilParams(t *testing.T) {
	client := newFakeMCPClient()
	client.invokeFn = func(_ context.Context, name string, params map[string]interface{}) (*mcp.ToolCallResult, error) {
		if name != "remote" {
			return nil, fmt.Errorf("unexpected tool %s", name)
		}
		if len(params) != 0 {
			return nil, fmt.Errorf("expected empty arguments, got %v", params)
		}
		return &mcp.ToolCallResult{Content: json.RawMessage(`{"ok":true}`)}, nil
	}
	tool := &remoteTool{name: "remote", description: "desc", client: client}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Output == "" || !strings.Contains(res.Output, `"ok":true`) {
		t.Fatalf("unexpected result output %q", res.Output)
	}
}

func TestRemoteToolExecuteError(t *testing.T) {
	client := newFakeMCPClient()
	client.invokeFn = func(context.Context, string, map[string]interface{}) (*mcp.ToolCallResult, error) {
		return nil, &mcp.Error{Code: -32001, Message: "call failed"}
	}
	tool := &remoteTool{name: "remote", client: client}
	if _, err := tool.Execute(context.Background(), map[string]any{"x": 1}); err == nil || !strings.Contains(err.Error(), "call failed") {
		t.Fatalf("expected call error, got %v", err)
	}
}

type fakeMCPClient struct {
	mu       sync.Mutex
	listFn   func(context.Context) ([]mcp.ToolDescriptor, error)
	callFn   func(context.Context, string, interface{}, interface{}) error
	invokeFn func(context.Context, string, map[string]interface{}) (*mcp.ToolCallResult, error)
	closeFn  func() error
	calls    map[string]int
	closed   bool
	spec     string
}

func newFakeMCPClient() *fakeMCPClient {
	return &fakeMCPClient{
		listFn: func(context.Context) ([]mcp.ToolDescriptor, error) {
			return nil, fmt.Errorf("list not configured")
		},
		callFn: func(context.Context, string, interface{}, interface{}) error { return nil },
		invokeFn: func(context.Context, string, map[string]interface{}) (*mcp.ToolCallResult, error) {
			return &mcp.ToolCallResult{Content: json.RawMessage(`""`)}, nil
		},
		closeFn: func() error { return nil },
		calls:   make(map[string]int),
	}
}

func (f *fakeMCPClient) ListTools(ctx context.Context) ([]mcp.ToolDescriptor, error) {
	f.mu.Lock()
	f.calls["list"]++
	f.mu.Unlock()
	return f.listFn(ctx)
}

func (f *fakeMCPClient) Call(ctx context.Context, method string, params interface{}, dest interface{}) error {
	f.mu.Lock()
	f.calls[fmt.Sprintf("call:%s", method)]++
	f.mu.Unlock()
	return f.callFn(ctx, method, params, dest)
}

func (f *fakeMCPClient) InvokeTool(ctx context.Context, name string, params map[string]interface{}) (*mcp.ToolCallResult, error) {
	f.mu.Lock()
	f.calls[fmt.Sprintf("invoke:%s", name)]++
	f.mu.Unlock()
	return f.invokeFn(ctx, name, params)
}

func (f *fakeMCPClient) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return f.closeFn()
}

func (f *fakeMCPClient) callCount(key string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[key]
}

func (f *fakeMCPClient) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func withStubMCPClient(t *testing.T, client *fakeMCPClient) func() {
	t.Helper()
	original := newMCPClient
	newMCPClient = func(spec string) mcpClient {
		client.mu.Lock()
		client.spec = spec
		client.mu.Unlock()
		return client
	}
	return func() {
		newMCPClient = original
	}
}
