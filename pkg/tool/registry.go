package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/mcp"
)

// Registry keeps the mapping between tool names and implementations.
type Registry struct {
	mu         sync.RWMutex
	tools      map[string]Tool
	mcpClients []mcpClient
	validator  Validator
}

type mcpClient interface {
	Call(ctx context.Context, method string, params interface{}, dest interface{}) error
	ListTools(ctx context.Context) ([]mcp.ToolDescriptor, error)
	InvokeTool(ctx context.Context, name string, params map[string]interface{}) (*mcp.ToolCallResult, error)
	Close() error
}

var newMCPClient = func(spec string) mcpClient {
	return mcp.NewClient(spec)
}

const (
	mcpDefaultProtocolVersion = "2025-06-18"
	mcpClientName             = "agentsdk-go"
	mcpClientVersion          = "dev"
)

// NewRegistry creates a registry backed by the default validator.
func NewRegistry() *Registry {
	return &Registry{
		tools:     make(map[string]Tool),
		validator: DefaultValidator{},
	}
}

// Register inserts a tool when its name is not in use.
func (r *Registry) Register(tool Tool) error {
	if tool == nil {
		return fmt.Errorf("tool is nil")
	}
	name := tool.Name()
	if name == "" {
		return fmt.Errorf("tool name is empty")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.tools[name]; exists {
		return fmt.Errorf("tool %s already registered", name)
	}

	r.tools[name] = tool
	return nil
}

// Get fetches a tool by name.
func (r *Registry) Get(name string) (Tool, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tool, exists := r.tools[name]
	if !exists {
		return nil, fmt.Errorf("tool %s not found", name)
	}
	return tool, nil
}

// List produces a snapshot of all registered tools.
func (r *Registry) List() []Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	tools := make([]Tool, 0, len(r.tools))
	for _, tool := range r.tools {
		tools = append(tools, tool)
	}
	return tools
}

// SetValidator swaps the validator instance used before execution.
func (r *Registry) SetValidator(v Validator) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.validator = v
}

// Execute runs a registered tool after optional schema validation.

func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (_ *ToolResult, err error) {
	tool, err := r.Get(name)
	if err != nil {
		return nil, err
	}

	if schema := tool.Schema(); schema != nil {
		r.mu.RLock()
		validator := r.validator
		r.mu.RUnlock()

		if validator != nil {
			if err := validator.Validate(params, schema); err != nil {
				return nil, fmt.Errorf("tool %s validation failed: %w", name, err)
			}
		}
	}

	result, execErr := tool.Execute(ctx, params)
	return result, execErr
}

// RegisterMCPServer discovers tools exposed by an MCP server and registers them.
// serverPath accepts either an http(s) URL (SSE transport) or a stdio command.
func (r *Registry) RegisterMCPServer(serverPath string) error {
	if strings.TrimSpace(serverPath) == "" {
		return fmt.Errorf("server path is empty")
	}
	// 建立 MCP 连接后需要保持事件流存活，因此初始化阶段使用独立的上下文。
	connectCtx := context.Background()

	client := newMCPClient(serverPath)
	success := false
	defer func() {
		if !success {
			_ = client.Close()
		}
	}()

	if err := initializeMCPClient(connectCtx, client); err != nil {
		return fmt.Errorf("initialize MCP client: %w", err)
	}

	listCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	tools, err := client.ListTools(listCtx)
	if err != nil {
		return fmt.Errorf("list MCP tools: %w", err)
	}
	if len(tools) == 0 {
		return fmt.Errorf("MCP server returned no tools")
	}

	wrappers := make([]Tool, 0, len(tools))
	for _, desc := range tools {
		if strings.TrimSpace(desc.Name) == "" {
			return fmt.Errorf("encountered MCP tool with empty name")
		}
		if r.hasTool(desc.Name) {
			return fmt.Errorf("tool %s already registered", desc.Name)
		}
		schema, err := convertMCPSchema(desc.Schema)
		if err != nil {
			return fmt.Errorf("parse schema for %s: %w", desc.Name, err)
		}
		wrappers = append(wrappers, &remoteTool{
			name:        desc.Name,
			description: desc.Description,
			schema:      schema,
			client:      client,
		})
	}

	for _, tool := range wrappers {
		if err := r.Register(tool); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.mcpClients = append(r.mcpClients, client)
	r.mu.Unlock()

	success = true
	return nil
}

func (r *Registry) hasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.tools[name]
	return exists
}

func convertMCPSchema(raw json.RawMessage) (*JSONSchema, error) {
	if len(raw) == 0 {
		return nil, nil
	}
	var schema JSONSchema
	if err := json.Unmarshal(raw, &schema); err == nil && schema.Type != "" {
		return &schema, nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(raw, &generic); err != nil {
		return nil, err
	}
	if t, ok := generic["type"].(string); ok {
		schema.Type = t
	}
	if props, ok := generic["properties"].(map[string]interface{}); ok {
		schema.Properties = props
	}
	if req, ok := generic["required"].([]interface{}); ok {
		for _, value := range req {
			if name, ok := value.(string); ok {
				schema.Required = append(schema.Required, name)
			}
		}
	}
	return &schema, nil
}

func initializeMCPClient(ctx context.Context, client mcpClient) error {
	if ctx == nil {
		ctx = context.Background()
	}

	params := map[string]interface{}{
		"protocolVersion": mcpDefaultProtocolVersion,
		"capabilities":    map[string]interface{}{},
		"clientInfo": map[string]interface{}{
			"name":    mcpClientName,
			"version": mcpClientVersion,
		},
	}

	// Some servers (older MCP drafts) do not require initialization. If the method
	// is missing, continue so the example remains backward compatible.
	var out map[string]interface{}
	if err := client.Call(ctx, "initialize", params, &out); err != nil {
		if mcpErr, ok := err.(*mcp.Error); ok && mcpErr.Code == -32601 {
			return nil
		}
		return err
	}

	return nil
}

type remoteTool struct {
	name        string
	description string
	schema      *JSONSchema
	client      mcpClient
}

func (r *remoteTool) Name() string        { return r.name }
func (r *remoteTool) Description() string { return r.description }
func (r *remoteTool) Schema() *JSONSchema { return r.schema }

func (r *remoteTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	if params == nil {
		params = map[string]interface{}{}
	}
	res, err := r.client.InvokeTool(ctx, r.name, params)
	if err != nil {
		return nil, err
	}
	return &ToolResult{
		Success: true,
		Output:  string(res.Content),
		Data:    res.Content,
	}, nil
}
