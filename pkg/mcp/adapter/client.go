package adapter

import (
	"context"
	"fmt"
	"net/url"
	"os/exec"
	"strings"
	"sync"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// transportBuilder is overridden in tests to stub the transport factory.
var transportBuilder = buildTransport

// Client wraps the official MCP SDK client with the legacy agentsdk-go surface.
type Client struct {
	implClient    *mcpsdk.Client
	session       *mcpsdk.ClientSession
	transportSpec string
	once          sync.Once
	connectErr    error
}

// NewClient constructs a new adapter client using the provided transport specification string.
func NewClient(spec string) *Client {
	impl := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "agentsdk-go", Version: "dev"}, nil)
	return &Client{implClient: impl, transportSpec: spec}
}

func (c *Client) ensureConnected(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	c.once.Do(func() {
		if c.implClient == nil {
			c.connectErr = fmt.Errorf("mcp adapter: nil client implementation")
			return
		}
		transport, err := transportBuilder(ctx, c.transportSpec)
		if err != nil {
			c.connectErr = fmt.Errorf("build transport: %w", err)
			return
		}
		session, err := c.implClient.Connect(ctx, transport, nil)
		if err != nil {
			c.connectErr = err
			return
		}
		c.session = session
	})
	return convertError(c.connectErr)
}

// ListTools fetches the full tool list and returns it as a slice.
func (c *Client) ListTools(ctx context.Context) ([]ToolDescriptor, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	seq := c.session.Tools(ctx, nil)
	var tools []ToolDescriptor
	for tool, err := range seq {
		if err != nil {
			return nil, convertError(err)
		}
		tools = append(tools, toToolDescriptor(tool))
	}
	return tools, nil
}

// InvokeTool adapts arguments to CallTool and returns the normalized result.
func (c *Client) InvokeTool(ctx context.Context, name string, args map[string]interface{}) (*ToolCallResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := c.ensureConnected(ctx); err != nil {
		return nil, err
	}
	params := &mcpsdk.CallToolParams{Name: name, Arguments: args}
	result, err := c.session.CallTool(ctx, params)
	if err != nil {
		return nil, convertError(err)
	}
	return toToolCallResult(result), nil
}

// Call exposes a minimal JSON-RPC compatible surface used by legacy callers.
func (c *Client) Call(ctx context.Context, method string, params interface{}, dest interface{}) error {
	if ctx == nil {
		ctx = context.Background()
	}
	if method == "initialize" {
		return c.ensureConnected(ctx)
	}
	return fmt.Errorf("mcp adapter: method %s not supported", method)
}

// Close shuts down the underlying session, if any.
func (c *Client) Close() error {
	if c == nil || c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}

func buildTransport(ctx context.Context, spec string) (mcpsdk.Transport, error) {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil, fmt.Errorf("mcp adapter: transport spec is empty")
	}

	lowered := strings.ToLower(spec)
	switch {
	case strings.HasPrefix(lowered, stdioSchemePrefix):
		return buildStdioTransport(ctx, spec[len(stdioSchemePrefix):])
	case strings.HasPrefix(lowered, sseSchemePrefix):
		target := strings.TrimSpace(spec[len(sseSchemePrefix):])
		endpoint, err := normalizeHTTPURL(target, true)
		if err != nil {
			return nil, fmt.Errorf("mcp adapter: invalid SSE endpoint: %w", err)
		}
		return buildSSETransport(endpoint)
	}

	if kind, endpoint, matched, err := parseHTTPFamilySpec(spec); err != nil {
		return nil, err
	} else if matched {
		if kind == httpHintType {
			return buildHTTPTransport(endpoint)
		}
		return buildSSETransport(endpoint)
	}

	if strings.HasPrefix(lowered, "http://") || strings.HasPrefix(lowered, "https://") {
		return buildSSETransport(spec)
	}

	return buildStdioTransport(ctx, spec)
}

func buildStdioTransport(ctx context.Context, cmdSpec string) (mcpsdk.Transport, error) {
	cmdSpec = strings.TrimSpace(cmdSpec)
	parts := strings.Fields(cmdSpec)
	if len(parts) == 0 {
		return nil, fmt.Errorf("mcp adapter: stdio command is empty")
	}
	// #nosec G204 -- cmdSpec originates from trusted MCP server config, not arbitrary user input
	command := exec.CommandContext(nonNilContext(ctx), parts[0], parts[1:]...)
	return &mcpsdk.CommandTransport{Command: command}, nil
}

func buildSSETransport(endpoint string) (mcpsdk.Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("mcp adapter: invalid SSE endpoint: %w", err)
	}
	return &mcpsdk.SSEClientTransport{Endpoint: normalized}, nil
}

func buildHTTPTransport(endpoint string) (mcpsdk.Transport, error) {
	normalized, err := normalizeHTTPURL(endpoint, false)
	if err != nil {
		return nil, fmt.Errorf("mcp adapter: invalid HTTP endpoint: %w", err)
	}
	return &mcpsdk.StreamableClientTransport{Endpoint: normalized}, nil
}

const (
	stdioSchemePrefix = "stdio://"
	sseSchemePrefix   = "sse://"
	httpHintType      = "http"
	sseHintType       = "sse"
)

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
		return "", "", true, fmt.Errorf("mcp adapter: unsupported HTTP transport hint %q", hint)
	}
	normalized := *u
	normalized.Scheme = base
	endpoint, normErr := normalizeHTTPURL(normalized.String(), false)
	if normErr != nil {
		return "", "", true, fmt.Errorf("mcp adapter: invalid %s endpoint: %w", resolvedKind, normErr)
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
