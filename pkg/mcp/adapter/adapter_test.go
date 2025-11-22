package adapter

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync/atomic"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestTypeMappings(t *testing.T) {
	t.Helper()
	descriptor := toToolDescriptor(&mcpsdk.Tool{
		Name:        "echo",
		Description: "Echo tool",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"text": map[string]any{"type": "string"}}},
	})
	if descriptor.Name != "echo" || descriptor.Description != "Echo tool" {
		t.Fatalf("unexpected descriptor: %+v", descriptor)
	}
	var schema map[string]any
	if err := json.Unmarshal(descriptor.Schema, &schema); err != nil {
		t.Fatalf("schema should be valid JSON: %v", err)
	}
	if schema["type"] != "object" {
		t.Fatalf("unexpected schema: %+v", schema)
	}

	result := toToolCallResult(&mcpsdk.CallToolResult{
		Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "hello"}},
	})
	var content []map[string]any
	if err := json.Unmarshal(result.Content, &content); err != nil {
		t.Fatalf("content should be valid JSON: %v", err)
	}
	if got := content[0]["text"]; got != "hello" {
		t.Fatalf("unexpected content: %+v", content)
	}

	errMsg := (&Error{Code: -1, Message: "boom"}).Error()
	if errMsg != "mcp error -1: boom" {
		t.Fatalf("unexpected error string: %s", errMsg)
	}
	errWithData := (&Error{Code: 1, Message: "boom", Data: json.RawMessage(`"extra"`)}).Error()
	if errWithData != "mcp error 1: boom (\"extra\")" {
		t.Fatalf("unexpected error string with data: %s", errWithData)
	}
	if (&Error{}).Error() == "<nil>" {
		t.Fatalf("non-nil error should not print <nil>")
	}
	var nilErr *Error
	if nilErr.Error() != "<nil>" {
		t.Fatalf("nil error should print <nil>")
	}
	if desc := toToolDescriptor(nil); desc.Name != "" || desc.Description != "" || len(desc.Schema) != 0 {
		t.Fatalf("nil tool should return zero descriptor, got %+v", desc)
	}
	if res := toToolCallResult(nil); res == nil || len(res.Content) != 0 {
		t.Fatalf("nil CallToolResult should return empty content, got %#v", res)
	}
}

func TestConvertErrorFromWire(t *testing.T) {
	resourceErr := mcpsdk.ResourceNotFoundError("file://missing")
	converted := convertError(resourceErr)
	adapterErr, ok := converted.(*Error)
	if !ok {
		t.Fatalf("expected *Error, got %T", converted)
	}
	if adapterErr.Code != -32002 {
		t.Fatalf("unexpected code: %d", adapterErr.Code)
	}
	if string(adapterErr.Data) == "" {
		t.Fatalf("expected data payload, got empty")
	}

	joined := errors.Join(fmt.Errorf("wrapper: %w", resourceErr), errors.New("other"))
	joinedConverted := convertError(joined)
	if _, ok := joinedConverted.(*Error); !ok {
		t.Fatalf("expected conversion through joined errors, got %T", joinedConverted)
	}

	if convertError(nil) != nil {
		t.Fatalf("convertError should return nil for nil input")
	}
	custom := &Error{Code: 42, Message: "custom"}
	if convertError(custom) != custom {
		t.Fatalf("convertError should return existing adapter error unmodified")
	}
}

func TestClientListToolsAndInvoke(t *testing.T) {
	var builderCalls atomic.Int32
	client, cleanup := setupTestClient(t, &builderCalls)
	defer cleanup()

	tools, err := client.ListTools(context.Background())
	if err != nil {
		t.Fatalf("ListTools failed: %v", err)
	}
	if builderCalls.Load() != 1 {
		t.Fatalf("expected single connect, got %d", builderCalls.Load())
	}
	if len(tools) != 2 {
		t.Fatalf("expected 2 tools, got %d", len(tools))
	}
	names := map[string]ToolDescriptor{}
	for _, tool := range tools {
		names[tool.Name] = tool
	}
	if _, ok := names["echo"]; !ok {
		t.Fatalf("echo tool missing: %+v", tools)
	}
	if _, ok := names["ping"]; !ok {
		t.Fatalf("ping tool missing: %+v", tools)
	}

	// Ensure repeated calls do not reconnect.
	if _, err := client.ListTools(context.Background()); err != nil {
		t.Fatalf("ListTools second call failed: %v", err)
	}
	if builderCalls.Load() != 1 {
		t.Fatalf("expected lazy connect, got %d connects", builderCalls.Load())
	}

	// Invoke tool.
	res, err := client.InvokeTool(context.Background(), "echo", map[string]interface{}{"text": "hi"})
	if err != nil {
		t.Fatalf("InvokeTool failed: %v", err)
	}
	var payload []map[string]any
	if err := json.Unmarshal(res.Content, &payload); err != nil {
		t.Fatalf("result should be valid JSON: %v", err)
	}
	if payload[0]["text"] != "echo:hi" {
		t.Fatalf("unexpected payload: %+v", payload)
	}
}

func TestClientInvokeToolErrorConversion(t *testing.T) {
	client, cleanup := setupTestClient(t, nil)
	defer cleanup()

	if _, err := client.InvokeTool(context.Background(), "missing", nil); err != nil {
		adapterErr, ok := err.(*Error)
		if !ok {
			t.Fatalf("expected *Error, got %T", err)
		}
		if adapterErr.Code != -32602 {
			t.Fatalf("unexpected error code: %d", adapterErr.Code)
		}
	} else {
		t.Fatalf("expected failure for missing tool")
	}
}

func TestClientCallInitializeAndUnsupported(t *testing.T) {
	client, cleanup := setupTestClient(t, nil)
	defer cleanup()

	if err := client.Call(context.Background(), "initialize", nil, nil); err != nil {
		t.Fatalf("initialize should connect: %v", err)
	}
	if err := client.Call(context.Background(), "shutdown", nil, nil); err == nil {
		t.Fatalf("unsupported method should fail")
	}
}

func TestClientEnsureConnectedError(t *testing.T) {
	originalBuilder := transportBuilder
	defer func() { transportBuilder = originalBuilder }()

	var calls atomic.Int32
	transportBuilder = func(context.Context, string) (mcpsdk.Transport, error) {
		calls.Add(1)
		return nil, fmt.Errorf("boom")
	}

	client := NewClient("bad://spec")

	if _, err := client.ListTools(context.Background()); err == nil {
		t.Fatalf("expected connection error")
	}
	if _, err := client.InvokeTool(context.Background(), "echo", nil); err == nil {
		t.Fatalf("expected cached connection error")
	}
	if calls.Load() != 1 {
		t.Fatalf("ensureConnected should only execute once, got %d", calls.Load())
	}
}

func TestClientCloseSafe(t *testing.T) {
	client := NewClient("noop")
	if err := client.Close(); err != nil {
		t.Fatalf("Close without session should be nil: %v", err)
	}
}

func TestBuildTransportInvalidSpec(t *testing.T) {
	if _, err := buildTransport(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty transport spec")
	}
	if _, err := buildTransport(context.Background(), "stdio://"); err == nil {
		t.Fatalf("expected error for empty stdio command")
	}
}

func TestEnsureConnectedNilImplementation(t *testing.T) {
	client := &Client{}
	if err := client.ensureConnected(context.Background()); err == nil {
		t.Fatalf("expected error when impl client is nil")
	}
}

func TestEnsureConnectedTransportConnectFailure(t *testing.T) {
	originalBuilder := transportBuilder
	defer func() { transportBuilder = originalBuilder }()

	transportBuilder = func(context.Context, string) (mcpsdk.Transport, error) {
		return failingTransport{}, nil
	}

	client := NewClient("bad-connect")
	if err := client.ensureConnected(context.Background()); err == nil {
		t.Fatalf("expected connect failure error")
	}
}

func setupTestClient(t *testing.T, callCounter *atomic.Int32) (*Client, func()) {
	t.Helper()
	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "test-server", Version: "test"}, nil)
	registerTestTools(server)

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan error, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		session, err := server.Connect(ctx, serverTransport, nil)
		if err != nil {
			ready <- err
			return
		}
		ready <- nil
		<-ctx.Done()
		_ = session.Close()
	}()

	originalBuilder := transportBuilder
	transportBuilder = func(ctx context.Context, spec string) (mcpsdk.Transport, error) {
		if callCounter != nil {
			callCounter.Add(1)
		}
		return clientTransport, nil
	}
	t.Cleanup(func() { transportBuilder = originalBuilder })

	client := NewClient("inmemory")
	cleanup := func() {
		_ = client.Close()
		cancel()
		<-done
		if err := <-ready; err != nil {
			t.Fatalf("server connect failed: %v", err)
		}
	}
	return client, cleanup
}

func registerTestTools(server *mcpsdk.Server) {
	server.AddTool(&mcpsdk.Tool{
		Name:        "echo",
		Description: "Echo input",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
			"required": []any{"text"},
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(req.Params.Arguments, &payload); err != nil {
			return nil, err
		}
		text := payload["text"]
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo:" + text}},
		}, nil
	})

	server.AddTool(&mcpsdk.Tool{
		Name:        "ping",
		Description: "Health check",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "pong"}},
		}, nil
	})
}

type failingTransport struct{}

func (failingTransport) Connect(context.Context) (mcpsdk.Connection, error) {
	return nil, fmt.Errorf("connect failed")
}
