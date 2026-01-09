package mcp

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"time"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

type (
	Implementation              = mcpsdk.Implementation
	Client                      = mcpsdk.Client
	ClientOptions               = mcpsdk.ClientOptions
	ClientSession               = mcpsdk.ClientSession
	ClientSessionOptions        = mcpsdk.ClientSessionOptions
	Transport                   = mcpsdk.Transport
	Connection                  = mcpsdk.Connection
	CommandTransport            = mcpsdk.CommandTransport
	StdioTransport              = mcpsdk.StdioTransport
	IOTransport                 = mcpsdk.IOTransport
	InMemoryTransport           = mcpsdk.InMemoryTransport
	SSEClientTransport          = mcpsdk.SSEClientTransport
	SSEOptions                  = mcpsdk.SSEOptions
	SSEHandler                  = mcpsdk.SSEHandler
	StreamableClientTransport   = mcpsdk.StreamableClientTransport
	Tool                        = mcpsdk.Tool
	ToolAnnotations             = mcpsdk.ToolAnnotations
	ToolHandler                 = mcpsdk.ToolHandler
	ToolHandlerFor[In, Out any] = mcpsdk.ToolHandlerFor[In, Out]
	CallToolParams              = mcpsdk.CallToolParams
	CallToolResult              = mcpsdk.CallToolResult
	ToolDescriptor              = mcpsdk.Tool
	ToolCallResult              = mcpsdk.CallToolResult
	ListToolsParams             = mcpsdk.ListToolsParams
	ListToolsResult             = mcpsdk.ListToolsResult
	Content                     = mcpsdk.Content
	TextContent                 = mcpsdk.TextContent
	InitializeParams            = mcpsdk.InitializeParams
	InitializeResult            = mcpsdk.InitializeResult
	ServerCapabilities          = mcpsdk.ServerCapabilities
)

var (
	NewClient             = mcpsdk.NewClient
	NewServer             = mcpsdk.NewServer
	NewInMemoryTransports = mcpsdk.NewInMemoryTransports
)

const (
	mcpClientName    = "agentsdk-go"
	mcpClientVersion = "dev"

	stdioSchemePrefix = "stdio://"
	sseSchemePrefix   = "sse://"
	httpHintType      = "http"
	sseHintType       = "sse"
)

type connectConfig struct {
	bus *coreevents.Bus
}

type ConnectOption func(*connectConfig)

// WithEventBus publishes MCPToolsChanged events to the provided bus when the
// connected MCP server sends notifications/tools/list_changed.
func WithEventBus(bus *coreevents.Bus) ConnectOption {
	return func(cfg *connectConfig) {
		if cfg == nil {
			return
		}
		cfg.bus = bus
	}
}

// SpecClient is a backward-compatible client that dials an MCP server from a
// spec string (e.g., "stdio://cmd" or "https://server") and exposes a pared
// down API surface.
//
// Deprecated: this exists only for the legacy public API compatibility layer.
type SpecClient interface {
	ListTools(ctx context.Context) ([]ToolDescriptor, error)
	InvokeTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error)
	Close() error
}

// NewSpecClient connects to an MCP server described by spec and returns a
// compatibility wrapper.
//
// Deprecated: prefer using the full go-sdk ClientSession directly.
func NewSpecClient(spec string) (SpecClient, error) {
	connectCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	session, err := ConnectSession(connectCtx, spec)
	if err != nil {
		if ctxErr := connectCtx.Err(); ctxErr != nil {
			return nil, fmt.Errorf("connect MCP client: %w", ctxErr)
		}
		return nil, fmt.Errorf("connect MCP client: %w", err)
	}
	if session == nil {
		return nil, fmt.Errorf("connect MCP client: session is nil")
	}

	success := false
	defer func() {
		if !success {
			_ = session.Close()
		}
	}()

	if err := EnsureSessionInitialized(connectCtx, session); err != nil {
		return nil, fmt.Errorf("initialize MCP client: %w", err)
	}
	if err := connectCtx.Err(); err != nil {
		return nil, fmt.Errorf("connect MCP client: %w", err)
	}

	success = true
	return &specClientWrapper{session: session}, nil
}

// ConnectSession establishes a ClientSession using the same spec parsing logic
// as the tool registry.
func ConnectSession(ctx context.Context, spec string) (*ClientSession, error) {
	return ConnectSessionWithOptions(ctx, spec)
}

// ConnectSessionWithOptions establishes a ClientSession using the same spec
// parsing logic as the tool registry, optionally wiring MCP notifications into
// the SDK event bus.
func ConnectSessionWithOptions(ctx context.Context, spec string, opts ...ConnectOption) (*ClientSession, error) {
	ctx = nonNilContext(ctx)
	transport, err := buildSessionTransport(ctx, spec)
	if err != nil {
		return nil, fmt.Errorf("build transport: %w", err)
	}

	cfg := connectConfig{}
	for _, opt := range opts {
		if opt != nil {
			opt(&cfg)
		}
	}

	serverID := strings.TrimSpace(spec)
	client := NewClient(&Implementation{
		Name:    mcpClientName,
		Version: mcpClientVersion,
	}, &ClientOptions{
		ToolListChangedHandler: func(ctx context.Context, req *mcpsdk.ToolListChangedRequest) {
			if req == nil || req.Session == nil {
				return
			}
			publishToolsChanged(ctx, cfg.bus, serverID, req.Session)
		},
	})

	dialCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			cancel()
		case <-done:
		}
	}()

	session, err := client.Connect(dialCtx, transport, nil)
	close(done)
	if err != nil {
		cancel()
		return nil, err
	}
	return session, nil
}

func publishToolsChanged(ctx context.Context, bus *coreevents.Bus, serverID string, session *ClientSession) {
	tools, err := snapshotTools(ctx, session)

	payload := coreevents.MCPToolsChangedPayload{
		Server:    serverID,
		SessionID: "",
		Tools:     tools,
	}
	if session != nil {
		payload.SessionID = session.ID()
	}
	if err != nil {
		payload.Error = err.Error()
	}

	if bus == nil {
		return
	}
	if err := bus.Publish(coreevents.Event{
		Type:    coreevents.MCPToolsChanged,
		Payload: payload,
	}); err != nil {
		return
	}
}

func snapshotTools(ctx context.Context, session *ClientSession) ([]coreevents.MCPToolDescriptor, error) {
	if session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	ctx = nonNilContext(ctx)

	var tools []coreevents.MCPToolDescriptor
	for tool, err := range session.Tools(ctx, nil) {
		if err != nil {
			return tools, err
		}
		if tool == nil {
			continue
		}
		tools = append(tools, coreevents.MCPToolDescriptor{
			Name:         tool.Name,
			Description:  tool.Description,
			InputSchema:  tool.InputSchema,
			OutputSchema: tool.OutputSchema,
			Title:        tool.Title,
		})
	}
	return tools, nil
}

// EnsureSessionInitialized validates that the session completed MCP
// initialization.
//
// Deprecated: internal compatibility helper; prefer handling initialization
// checks in callers directly.
func EnsureSessionInitialized(ctx context.Context, session *ClientSession) error {
	if ctx == nil {
		return fmt.Errorf("mcp init context is nil")
	}
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("connect context: %w", err)
	}
	if session == nil {
		return fmt.Errorf("mcp session is nil")
	}
	if session.InitializeResult() == nil {
		return fmt.Errorf("mcp session missing initialize result")
	}
	return nil
}

type specClientWrapper struct {
	session *ClientSession
}

func (s *specClientWrapper) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	if s == nil || s.session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	var tools []ToolDescriptor
	for tool, err := range s.session.Tools(ctx, nil) {
		if err != nil {
			return nil, err
		}
		if tool == nil {
			continue
		}
		tools = append(tools, *tool)
	}
	return tools, nil
}

func (s *specClientWrapper) InvokeTool(ctx context.Context, name string, args map[string]any) (*ToolCallResult, error) {
	if s == nil || s.session == nil {
		return nil, fmt.Errorf("mcp session is nil")
	}
	if strings.TrimSpace(name) == "" {
		return nil, fmt.Errorf("tool name is empty")
	}
	if args == nil {
		args = map[string]any{}
	}

	res, err := s.session.CallTool(ctx, &CallToolParams{
		Name:      name,
		Arguments: args,
	})
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, fmt.Errorf("MCP call returned nil result")
	}
	return res, nil
}

func (s *specClientWrapper) Close() error {
	if s == nil || s.session == nil {
		return nil
	}
	return s.session.Close()
}

func buildSessionTransport(ctx context.Context, spec string) (Transport, error) {
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

func buildStdioTransport(ctx context.Context, cmdSpec string) (Transport, error) {
	cmdSpec = strings.TrimSpace(cmdSpec)
	parts := strings.Fields(cmdSpec)
	if len(parts) == 0 {
		return nil, fmt.Errorf("mcp stdio command is empty")
	}
	command := exec.CommandContext(nonNilContext(ctx), parts[0], parts[1:]...) // #nosec G204
	return &CommandTransport{Command: command}, nil
}

func buildSSETransport(endpoint string, allowSchemeGuess bool) (Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, allowSchemeGuess)
	if err != nil {
		return nil, fmt.Errorf("invalid SSE endpoint: %w", err)
	}
	return &SSEClientTransport{Endpoint: normalized}, nil
}

func buildStreamableTransport(endpoint string) (Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("invalid streamable endpoint: %w", err)
	}
	return &StreamableClientTransport{Endpoint: normalized}, nil
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

// BuildSessionTransport exposes the spec parsing logic for compatibility
// testing helpers.
//
// Deprecated: use ConnectSession instead.
func BuildSessionTransport(ctx context.Context, spec string) (Transport, error) {
	return buildSessionTransport(ctx, spec)
}

// BuildSSETransport exposes SSE transport construction for compatibility.
//
// Deprecated: use ConnectSession instead.
func BuildSSETransport(endpoint string, allowSchemeGuess bool) (Transport, error) {
	return buildSSETransport(endpoint, allowSchemeGuess)
}

// BuildStreamableTransport exposes streamable transport construction for
// compatibility.
//
// Deprecated: use ConnectSession instead.
func BuildStreamableTransport(endpoint string) (Transport, error) {
	return buildStreamableTransport(endpoint)
}

// BuildStdioTransport exposes stdio transport construction for compatibility.
//
// Deprecated: use ConnectSession instead.
func BuildStdioTransport(ctx context.Context, cmdSpec string) (Transport, error) {
	return buildStdioTransport(ctx, cmdSpec)
}

// ParseHTTPFamilySpec exposes the HTTP family spec parsing logic for tests.
//
// Deprecated: internal compatibility helper.
func ParseHTTPFamilySpec(spec string) (kind string, endpoint string, matched bool, err error) {
	return parseHTTPFamilySpec(spec)
}

// NormalizeHTTPURL exposes URL normalization for compatibility tests.
//
// Deprecated: internal compatibility helper.
func NormalizeHTTPURL(raw string, allowSchemeGuess bool) (string, error) {
	return normalizeHTTPURL(raw, allowSchemeGuess)
}

// NonNilContext keeps backward compatibility for helpers that need a
// non-nil context.
//
// Deprecated: internal compatibility helper.
func NonNilContext(ctx context.Context) context.Context {
	return nonNilContext(ctx)
}
