package tool

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

func TestRegistryRegister(t *testing.T) {
	tests := []struct {
		name        string
		tool        Tool
		preRegister []Tool
		wantErr     string
		verify      func(t *testing.T, r *Registry)
	}{
		{name: "nil tool", wantErr: "tool is nil"},
		{name: "empty name", tool: &spyTool{name: ""}, wantErr: "tool name is empty"},
		{
			name:        "duplicate name rejected",
			tool:        &spyTool{name: "echo"},
			preRegister: []Tool{&spyTool{name: "echo"}},
			wantErr:     "already registered",
		},
		{
			name: "successful registration available via get and list",
			tool: &spyTool{name: "sum", result: &ToolResult{Output: "ok"}},
			verify: func(t *testing.T, r *Registry) {
				t.Helper()
				got, err := r.Get("sum")
				if err != nil {
					t.Fatalf("get failed: %v", err)
				}
				if got.Name() != "sum" {
					t.Fatalf("unexpected tool returned: %s", got.Name())
				}
				if len(r.List()) != 1 {
					t.Fatalf("list length = %d", len(r.List()))
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			for _, pre := range tt.preRegister {
				if err := r.Register(pre); err != nil {
					t.Fatalf("setup register failed: %v", err)
				}
			}
			err := r.Register(tt.tool)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q got %v", tt.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("register failed: %v", err)
			}
			if tt.verify != nil {
				tt.verify(t, r)
			}
		})
	}
}

func TestRegistryExecute(t *testing.T) {
	ctx := context.Background()
	tests := []struct {
		name       string
		tool       *spyTool
		params     map[string]interface{}
		validator  Validator
		wantErr    string
		wantCalls  int
		wantParams map[string]interface{}
	}{
		{
			name:      "tool without schema bypasses validator",
			tool:      &spyTool{name: "echo", result: &ToolResult{Output: "ok"}},
			validator: &spyValidator{},
			wantCalls: 1,
		},
		{
			name:      "validation failure prevents execution",
			tool:      &spyTool{name: "calc", schema: &JSONSchema{Type: "object"}},
			validator: &spyValidator{err: errors.New("boom")},
			wantErr:   "validation failed",
			wantCalls: 0,
		},
		{
			name:       "validation success forwards params to tool",
			tool:       &spyTool{name: "calc", schema: &JSONSchema{Type: "object"}, result: &ToolResult{Output: "ok"}},
			validator:  &spyValidator{},
			params:     map[string]interface{}{"x": 1},
			wantCalls:  1,
			wantParams: map[string]interface{}{"x": 1},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewRegistry()
			if err := r.Register(tt.tool); err != nil {
				t.Fatalf("register: %v", err)
			}
			if tt.validator != nil {
				r.SetValidator(tt.validator)
			}
			res, err := r.Execute(ctx, tt.tool.Name(), tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error containing %q got %v", tt.wantErr, err)
				}
				if tt.tool.calls != tt.wantCalls {
					t.Fatalf("tool calls = %d", tt.tool.calls)
				}
				return
			}
			if err != nil {
				t.Fatalf("execute failed: %v", err)
			}
			if tt.tool.calls != tt.wantCalls {
				t.Fatalf("tool calls = %d want %d", tt.tool.calls, tt.wantCalls)
			}
			if tt.wantParams != nil {
				for k, v := range tt.wantParams {
					if tt.tool.params[k] != v {
						t.Fatalf("param %s mismatch", k)
					}
				}
			}
			if res == nil {
				t.Fatal("nil result returned")
			}
		})
	}

	t.Run("unknown tool name returns error", func(t *testing.T) {
		r := NewRegistry()
		if _, err := r.Execute(ctx, "missing", nil); err == nil || !strings.Contains(err.Error(), "not found") {
			t.Fatalf("expected not found error, got %v", err)
		}
	})
}

func TestRegisterMCPServerSSE(t *testing.T) {
	h := newRegistrySSEHarness(t)
	defer h.Close()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), h.URL(), ""); err != nil {
		t.Fatalf("register MCP: %v", err)
	}

	res, err := r.Execute(context.Background(), "echo", map[string]interface{}{"text": "ping"})
	if err != nil {
		t.Fatalf("execute remote tool: %v", err)
	}
	if !strings.Contains(res.Output, "echo:ping") {
		t.Fatalf("unexpected output: %s", res.Output)
	}
}

func TestRegisterMCPServerSSERefreshesOnListChanged(t *testing.T) {
	h := newRegistrySSEHarness(t)
	defer h.Close()

	r := NewRegistry()
	if err := r.RegisterMCPServer(context.Background(), h.URL(), ""); err != nil {
		t.Fatalf("register MCP: %v", err)
	}

	h.server.AddTool(&mcpsdk.Tool{
		Name:        "sum",
		Description: "sum tool",
		InputSchema: map[string]any{"type": "object"},
	}, func(context.Context, *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return &mcpsdk.CallToolResult{Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "sum"}}}, nil
	})

	deadline := time.NewTimer(2 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-deadline.C:
			t.Fatal("timed out waiting for MCP tool refresh")
		case <-ticker.C:
			if _, err := r.Get("sum"); err == nil {
				return
			}
		}
	}
}

type spyTool struct {
	name   string
	schema *JSONSchema
	result *ToolResult
	err    error
	calls  int
	params map[string]interface{}
}

func (s *spyTool) Name() string        { return s.name }
func (s *spyTool) Description() string { return "spy" }
func (s *spyTool) Schema() *JSONSchema { return s.schema }
func (s *spyTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	s.calls++
	s.params = params
	if s.result == nil {
		s.result = &ToolResult{}
	}
	return s.result, s.err
}

type spyValidator struct {
	err    error
	calls  int
	schema *JSONSchema
}

func (v *spyValidator) Validate(params map[string]interface{}, schema *JSONSchema) error {
	v.calls++
	v.schema = schema
	return v.err
}

type registrySSEHarness struct {
	srv    *httptest.Server
	server *mcpsdk.Server
}

func newRegistrySSEHarness(t *testing.T) *registrySSEHarness {
	t.Helper()

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Skipf("httptest: listen not permitted: %v", err)
	}

	server := mcpsdk.NewServer(&mcpsdk.Implementation{Name: "registry-test", Version: "dev"}, nil)
	server.AddTool(&mcpsdk.Tool{
		Name:        "echo",
		Description: "echo tool",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"text": map[string]any{"type": "string"},
			},
		},
	}, func(ctx context.Context, req *mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		var payload map[string]string
		if err := json.Unmarshal(req.Params.Arguments, &payload); err != nil {
			return nil, err
		}
		return &mcpsdk.CallToolResult{
			Content: []mcpsdk.Content{&mcpsdk.TextContent{Text: "echo:" + payload["text"]}},
		}, nil
	})

	handler := mcpsdk.NewSSEHandler(func(*http.Request) *mcpsdk.Server {
		return server
	}, nil)

	srv := httptest.NewUnstartedServer(handler)
	srv.Listener = listener
	srv.Start()

	return &registrySSEHarness{
		srv:    srv,
		server: server,
	}
}

func (h *registrySSEHarness) URL() string {
	return h.srv.URL
}

func (h *registrySSEHarness) Close() {
	h.srv.CloseClientConnections()
	h.srv.Close()
}

func TestConvertMCPSchema(t *testing.T) {
	jsonInput := json.RawMessage(`{"type":"object","properties":{"x":{"type":"string"}},"required":["x"]}`)
	schema, err := convertMCPSchema(jsonInput)
	if err != nil {
		t.Fatalf("convert err: %v", err)
	}
	if schema.Type != "object" || len(schema.Required) != 1 {
		t.Fatalf("unexpected schema: %#v", schema)
	}

	if _, err := convertMCPSchema(json.RawMessage(``)); err != nil {
		t.Fatalf("empty raw should return nil, got %v", err)
	}
	if val, err := convertMCPSchema(nil); err != nil || val != nil {
		t.Fatalf("nil raw should return nil, got %v %v", val, err)
	}
	if alt, err := convertMCPSchema(json.RawMessage(`{"properties":{"x":{"type":"number"}},"required":["x"]}`)); err != nil || alt == nil {
		t.Fatalf("expected map-based schema, got %#v err=%v", alt, err)
	}
	if _, err := convertMCPSchema(json.RawMessage(`{`)); err == nil {
		t.Fatalf("expected unmarshal error")
	}
}
