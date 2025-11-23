package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/url"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/mcp"
)

// Registry keeps the mapping between tool names and implementations.
type Registry struct {
	mu          sync.RWMutex
	tools       map[string]Tool
	mcpSessions []*mcp.ClientSession
	validator   Validator
}

var newMCPClient = func(ctx context.Context, spec string) (*mcp.ClientSession, error) {
	return mcp.ConnectSession(ctx, spec)
}

const (
	httpHintType      = "http"
	sseHintType       = "sse"
	stdioSchemePrefix = "stdio://"
	sseSchemePrefix   = "sse://"
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
func (r *Registry) RegisterMCPServer(ctx context.Context, serverPath string) error {
	ctx = nonNilContext(ctx)
	if strings.TrimSpace(serverPath) == "" {
		return fmt.Errorf("server path is empty")
	}
	connectCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	session, err := newMCPClient(connectCtx, serverPath)
	if err != nil {
		if ctxErr := connectCtx.Err(); ctxErr != nil {
			return fmt.Errorf("connect MCP client: %w", ctxErr)
		}
		return fmt.Errorf("connect MCP client: %w", err)
	}
	if session == nil {
		return fmt.Errorf("connect MCP client: session is nil")
	}
	success := false
	defer func() {
		if !success {
			_ = session.Close()
		}
	}()

	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("initialize MCP client: connect context: %w", err)
	}
	if session.InitializeResult() == nil {
		return fmt.Errorf("initialize MCP client: mcp session missing initialize result")
	}
	if err := connectCtx.Err(); err != nil {
		return fmt.Errorf("connect MCP client: %w", err)
	}

	listCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	var tools []*mcp.Tool
	for tool, iterErr := range session.Tools(listCtx, nil) {
		if iterErr != nil {
			return fmt.Errorf("list MCP tools: %w", iterErr)
		}
		tools = append(tools, tool)
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
		schema, err := convertMCPSchema(desc.InputSchema)
		if err != nil {
			return fmt.Errorf("parse schema for %s: %w", desc.Name, err)
		}
		wrappers = append(wrappers, &remoteTool{
			name:        desc.Name,
			description: desc.Description,
			schema:      schema,
			session:     session,
		})
	}

	for _, tool := range wrappers {
		if err := r.Register(tool); err != nil {
			return err
		}
	}

	r.mu.Lock()
	r.mcpSessions = append(r.mcpSessions, session)
	r.mu.Unlock()

	success = true
	return nil
}

// Close terminates all tracked MCP sessions.
// Errors are logged and ignored to avoid masking shutdown flows.
func (r *Registry) Close() {
	r.mu.Lock()
	sessions := r.mcpSessions
	r.mcpSessions = nil
	r.mu.Unlock()

	for _, session := range sessions {
		if session == nil {
			continue
		}
		if err := session.Close(); err != nil {
			log.Printf("tool registry: close MCP session: %v", err)
		}
	}
}

func (r *Registry) hasTool(name string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	_, exists := r.tools[name]
	return exists
}

func convertMCPSchema(raw any) (*JSONSchema, error) {
	if raw == nil {
		return nil, nil
	}
	var (
		data []byte
		err  error
	)
	switch v := raw.(type) {
	case json.RawMessage:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	case []byte:
		if len(v) == 0 {
			return nil, nil
		}
		data = v
	default:
		data, err = json.Marshal(raw)
		if err != nil {
			return nil, err
		}
	}
	var schema JSONSchema
	if err := json.Unmarshal(data, &schema); err == nil && schema.Type != "" {
		return &schema, nil
	}
	var generic map[string]interface{}
	if err := json.Unmarshal(data, &generic); err != nil {
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

// Compatibility wrappers keep registry tests aligned with the shared MCP
// transport builders now hosted in the mcp package.
func buildMCPSessionTransport(ctx context.Context, spec string) (mcp.Transport, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("mcp transport spec is empty")
	}

	lowered := strings.ToLower(spec)
	switch {
	case strings.HasPrefix(lowered, stdioSchemePrefix):
		return buildStdioTransport(ctx, spec[len(stdioSchemePrefix):])
	case strings.HasPrefix(lowered, sseSchemePrefix):
		target := strings.TrimSpace(spec[len(sseSchemePrefix):])
		return buildSSETransport(target, true)
	}

	if kind, endpoint, matched, err := parseHTTPFamilySpec(spec); err != nil {
		return nil, err
	} else if matched {
		if kind == httpHintType {
			return buildStreamableTransport(endpoint)
		}
		return buildSSETransport(endpoint, false)
	}

	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return buildSSETransport(spec, false)
	}

	return buildStdioTransport(ctx, spec)
}

func buildSSETransport(endpoint string, allowSchemeGuess bool) (mcp.Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, allowSchemeGuess)
	if err != nil {
		return nil, fmt.Errorf("invalid SSE endpoint: %w", err)
	}
	return &mcp.SSEClientTransport{Endpoint: normalized}, nil
}

func buildStreamableTransport(endpoint string) (mcp.Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("invalid streamable endpoint: %w", err)
	}
	return &mcp.StreamableClientTransport{Endpoint: normalized}, nil
}

func buildStdioTransport(ctx context.Context, cmdSpec string) (mcp.Transport, error) {
	cmdSpec = strings.TrimSpace(cmdSpec)
	parts := strings.Fields(cmdSpec)
	if len(parts) == 0 {
		return nil, fmt.Errorf("mcp stdio command is empty")
	}
	command := exec.CommandContext(nonNilContext(ctx), parts[0], parts[1:]...) // #nosec G204
	return &mcp.CommandTransport{Command: command}, nil
}

func parseHTTPFamilySpec(spec string) (kind string, endpoint string, matched bool, err error) {
	u, parseErr := url.Parse(strings.TrimSpace(spec))
	if parseErr != nil || u.Scheme == "" {
		return "", "", false, nil
	}
	scheme := strings.ToLower(u.Scheme)
	base, hintRaw, hasHint := strings.Cut(scheme, "+")
	if !hasHint {
		return "", "", false, nil
	}
	if base != "http" && base != "https" {
		return "", "", false, nil
	}
	hint := hintRaw
	if idx := strings.IndexByte(hint, '+'); idx >= 0 {
		hint = hint[:idx]
	}
	var resolvedKind string
	switch hint {
	case "sse":
		resolvedKind = sseHintType
	case "stream", "streamable", "http", "json":
		resolvedKind = httpHintType
	default:
		return "", "", true, fmt.Errorf("unsupported HTTP transport hint %q", hint)
	}
	normalized := *u
	normalized.Scheme = base
	endpoint, normErr := normalizeHTTPURL(normalized.String(), false)
	if normErr != nil {
		return "", "", true, fmt.Errorf("invalid %s endpoint: %w", resolvedKind, normErr)
	}
	return resolvedKind, endpoint, true, nil
}

func normalizeHTTPURL(raw string, allowSchemeGuess bool) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("endpoint is empty")
	}
	if allowSchemeGuess && !strings.Contains(raw, "://") {
		raw = "https://" + raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", fmt.Errorf("unsupported scheme %q", parsed.Scheme)
	}
	if parsed.Host == "" {
		return "", fmt.Errorf("missing host")
	}
	parsed.Scheme = scheme
	return parsed.String(), nil
}

func nonNilContext(ctx context.Context) context.Context {
	if ctx != nil {
		return ctx
	}
	return context.Background()
}

type remoteTool struct {
	name        string
	description string
	schema      *JSONSchema
	session     *mcp.ClientSession
}

func (r *remoteTool) Name() string        { return r.name }
func (r *remoteTool) Description() string { return r.description }
func (r *remoteTool) Schema() *JSONSchema { return r.schema }

func (r *remoteTool) Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error) {
	if r.session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	if params == nil {
		params = map[string]interface{}{}
	}
	res, err := r.session.CallTool(ctx, &mcp.CallToolParams{
		Name:      r.name,
		Arguments: params,
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("MCP call returned nil result")
	}
	output := firstTextContent(res.Content)
	if output == "" {
		if payload, err := json.Marshal(res.Content); err == nil {
			output = string(payload)
		}
	}
	return &ToolResult{
		Success: true,
		Output:  output,
		Data:    res.Content,
	}, nil
}

func firstTextContent(content []mcp.Content) string {
	for _, part := range content {
		if txt, ok := part.(*mcp.TextContent); ok {
			return txt.Text
		}
	}
	return ""
}
