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

func TestBuildMCPTransportVariants(t *testing.T) {
	if _, err := buildMCPTransport(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty spec")
	}
	if tr, err := buildMCPTransport(context.Background(), "http://example.com"); err != nil || tr == nil {
		t.Fatalf("http transport failed: %v %v", tr, err)
	}
	if _, err := buildMCPTransport(context.Background(), "stdio://"); err == nil {
		t.Fatalf("expected error for missing command")
	}
	if tr, err := buildMCPTransport(context.Background(), "stdio://echo"); err != nil || tr == nil {
		t.Fatalf("stdio transport failed: %v %v", tr, err)
	}
}

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
	r := NewRegistry()
	if err := r.RegisterMCPServer("stdio://"); err == nil || !strings.Contains(err.Error(), "invalid stdio server path") {
		t.Fatalf("expected invalid stdio path error, got %v", err)
	}
}

func TestRegisterMCPServerTransportBuilderError(t *testing.T) {
	restore := restoreTransportBuilder(t)
	defer restore()
	errBoom := errors.New("boom")
	mcpTransportBuilder = func(ctx context.Context, spec string) (mcp.Transport, error) {
		return nil, errBoom
	}

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); !errors.Is(err, errBoom) {
		t.Fatalf("expected builder error, got %v", err)
	}
}

func TestRegisterMCPServerInitializeFailureClosesTransport(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return nil, errors.New("dial failed")
	})
	withFakeTransport(t, tr)

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "initialize MCP client") {
		t.Fatalf("expected initialize failure error, got %v", err)
	}
	if !tr.isClosed() {
		t.Fatalf("transport should be closed on failure")
	}
	if count := tr.callCount("initialize"); count != 1 {
		t.Fatalf("expected single initialize call, got %d", count)
	}
}

func TestRegisterMCPServerListToolsError(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return nil, errors.New("list failed")
	})
	withFakeTransport(t, tr)

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "list MCP tools") {
		t.Fatalf("expected list error, got %v", err)
	}
}

func TestRegisterMCPServerNoTools(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return &mcp.Response{JSONRPC: "2.0", ID: req.ID, Result: marshalMust(mcp.ToolListResult{})}, nil
	})
	withFakeTransport(t, tr)

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "returned no tools") {
		t.Fatalf("expected empty tool list error, got %v", err)
	}
}

func TestRegisterMCPServerEmptyToolName(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return listResponse(req.ID, []mcp.ToolDescriptor{{Name: " "}}), nil
	})
	withFakeTransport(t, tr)

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "empty name") {
		t.Fatalf("expected empty tool name error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateLocalTool(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return listResponse(req.ID, []mcp.ToolDescriptor{{Name: "dup"}}), nil
	})
	withFakeTransport(t, tr)

	r := NewRegistry()
	if err := r.Register(&spyTool{name: "dup"}); err != nil {
		t.Fatalf("setup register failed: %v", err)
	}
	if err := r.RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected duplicate tool error, got %v", err)
	}
}

func TestRegisterMCPServerSchemaError(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return listResponse(req.ID, []mcp.ToolDescriptor{{
			Name:   "bad",
			Schema: json.RawMessage(`"oops"`),
		}}), nil
	})
	withFakeTransport(t, tr)

	if err := NewRegistry().RegisterMCPServer("fake"); err == nil || !strings.Contains(err.Error(), "parse schema for bad") {
		t.Fatalf("expected schema parse error, got %v", err)
	}
}

func TestRegisterMCPServerDuplicateRemoteTools(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return listResponse(req.ID, []mcp.ToolDescriptor{
			{Name: "dup"},
			{Name: "dup"},
		}), nil
	})
	withFakeTransport(t, tr)

	r := NewRegistry()
	err := r.RegisterMCPServer("fake")
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Fatalf("expected register failure error, got %v", err)
	}
	if !tr.isClosed() {
		t.Fatalf("transport should be closed when registration fails")
	}
}

func TestRegisterMCPServerSuccessAddsClient(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return methodNotFoundResponse(), nil
	})
	tr.handle("tools/list", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return listResponse(req.ID, []mcp.ToolDescriptor{{Name: "echo", Description: "remote tool"}}), nil
	})
	withFakeTransport(t, tr)

	r := NewRegistry()
	if err := r.RegisterMCPServer("fake"); err != nil {
		t.Fatalf("register failed: %v", err)
	}
	if len(r.mcpClients) != 1 {
		t.Fatalf("expected client to be tracked, got %d", len(r.mcpClients))
	}
	if tr.isClosed() {
		t.Fatalf("transport should remain open after success")
	}
}

func TestInitializeMCPClientSuccess(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(ctx context.Context, req *mcp.Request) (*mcp.Response, error) {
		if ctx == nil {
			t.Fatalf("initialize received nil context")
		}
		return &mcp.Response{JSONRPC: "2.0", ID: req.ID, Result: marshalMust(map[string]any{"ok": true})}, nil
	})
	client := mcp.NewClient(tr)
	if err := initializeMCPClient(context.TODO(), client); err != nil {
		t.Fatalf("initialize should succeed: %v", err)
	}
	if tr.callCount("initialize") != 1 {
		t.Fatalf("expected initialize call, got %d", tr.callCount("initialize"))
	}
}

func TestInitializeMCPClientPropagatesMCPErrors(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return &mcp.Response{JSONRPC: "2.0", ID: req.ID, Error: &mcp.Error{Code: -32000, Message: "nope"}}, nil
	})
	client := mcp.NewClient(tr)
	if err := initializeMCPClient(context.Background(), client); err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("expected MCP error, got %v", err)
	}
}

func TestInitializeMCPClientPropagatesTransportErrors(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("initialize", func(context.Context, *mcp.Request) (*mcp.Response, error) {
		return nil, errors.New("rpc down")
	})
	client := mcp.NewClient(tr)
	if err := initializeMCPClient(context.Background(), client); err == nil || !strings.Contains(err.Error(), "rpc down") {
		t.Fatalf("expected transport error, got %v", err)
	}
}

func TestRemoteToolExecuteWithNilParams(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("tools/call", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		params, ok := req.Params.(mcp.ToolCallParams)
		if !ok {
			return nil, fmt.Errorf("unexpected params type %T", req.Params)
		}
		if len(params.Arguments) != 0 {
			return nil, fmt.Errorf("expected empty arguments, got %v", params.Arguments)
		}
		return &mcp.Response{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result:  marshalMust(mcp.ToolCallResult{Content: json.RawMessage(`{"ok":true}`)}),
		}, nil
	})
	tool := &remoteTool{name: "remote", description: "desc", client: mcp.NewClient(tr)}
	res, err := tool.Execute(context.Background(), nil)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if res.Output == "" || !strings.Contains(res.Output, `"ok":true`) {
		t.Fatalf("unexpected result output %q", res.Output)
	}
}

func TestRemoteToolExecuteError(t *testing.T) {
	tr := newFakeMCPTransport()
	tr.handle("tools/call", func(_ context.Context, req *mcp.Request) (*mcp.Response, error) {
		return &mcp.Response{JSONRPC: "2.0", ID: req.ID, Error: &mcp.Error{Code: -32001, Message: "call failed"}}, nil
	})
	tool := &remoteTool{name: "remote", client: mcp.NewClient(tr)}
	if _, err := tool.Execute(context.Background(), map[string]any{"x": 1}); err == nil || !strings.Contains(err.Error(), "call failed") {
		t.Fatalf("expected call error, got %v", err)
	}
}

type fakeMCPTransport struct {
	mu       sync.Mutex
	handlers map[string]func(context.Context, *mcp.Request) (*mcp.Response, error)
	calls    map[string]int
	ctxs     []context.Context
	closed   bool
}

func newFakeMCPTransport() *fakeMCPTransport {
	return &fakeMCPTransport{
		handlers: make(map[string]func(context.Context, *mcp.Request) (*mcp.Response, error)),
		calls:    make(map[string]int),
	}
}

func (f *fakeMCPTransport) handle(method string, fn func(context.Context, *mcp.Request) (*mcp.Response, error)) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.handlers[method] = fn
}

func (f *fakeMCPTransport) Call(ctx context.Context, req *mcp.Request) (*mcp.Response, error) {
	f.mu.Lock()
	handler := f.handlers[req.Method]
	f.calls[req.Method]++
	f.ctxs = append(f.ctxs, ctx)
	f.mu.Unlock()
	if handler == nil {
		return nil, fmt.Errorf("unexpected method %s", req.Method)
	}
	return handler(ctx, req)
}

func (f *fakeMCPTransport) Close() error {
	f.mu.Lock()
	f.closed = true
	f.mu.Unlock()
	return nil
}

func (f *fakeMCPTransport) callCount(method string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[method]
}

func (f *fakeMCPTransport) isClosed() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func withFakeTransport(t *testing.T, tr *fakeMCPTransport) {
	t.Helper()
	restore := restoreTransportBuilder(t)
	mcpTransportBuilder = func(ctx context.Context, spec string) (mcp.Transport, error) {
		return tr, nil
	}
	t.Cleanup(func() {
		restore()
	})
}

func restoreTransportBuilder(t *testing.T) func() {
	t.Helper()
	original := mcpTransportBuilder
	return func() {
		mcpTransportBuilder = original
	}
}

func methodNotFoundResponse() *mcp.Response {
	return &mcp.Response{JSONRPC: "2.0", Error: &mcp.Error{Code: -32601, Message: "method not found"}}
}

func listResponse(id string, tools []mcp.ToolDescriptor) *mcp.Response {
	return &mcp.Response{JSONRPC: "2.0", ID: id, Result: marshalMust(mcp.ToolListResult{Tools: tools})}
}
