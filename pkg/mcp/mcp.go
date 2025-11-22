package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/mcp/adapter"
)

// 重新导出适配器核心类型
type (
	Client         = adapter.Client
	ToolDescriptor = adapter.ToolDescriptor
	ToolCallResult = adapter.ToolCallResult
	Error          = adapter.Error
)

// 重新导出核心函数
var (
	NewClient = adapter.NewClient
)

// ========== 兼容层：为 pkg/tool/registry.go 保留旧 API ==========

// Transport 接口（已废弃，仅为兼容测试代码）
type Transport interface {
	Call(ctx context.Context, req *Request) (*Response, error)
	Close() error
}

// 协议类型（测试用）
type Request struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params,omitempty"`
}

type Response struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      json.RawMessage `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *Error          `json:"error,omitempty"`
}

type ToolListResult struct {
	Tools []ToolDescriptor `json:"tools"`
}

type ToolCallParams struct {
	Name      string                 `json:"name"`
	Arguments map[string]interface{} `json:"arguments"`
}

// SSEOptions 已废弃
type SSEOptions struct {
	BaseURL string
}

// STDIOOptions 已废弃
type STDIOOptions struct {
	Args []string
}

// ServerConfig 用于 NewClientFromServerConfig
type ServerConfig struct {
	Type      string
	Command   string
	Args      []string
	URL       string
	Env       map[string]string
	Headers   map[string]string
	Timeout   time.Duration
	AuthToken string
}

// NewSSETransport 已废弃，返回错误引导用户使用新 API
func NewSSETransport(ctx context.Context, opts SSEOptions) (Transport, error) {
	return nil, errors.New("NewSSETransport is deprecated, use mcp.NewClient(\"http://\" + baseURL) instead")
}

// NewSTDIOTransport 已废弃，返回错误引导用户使用新 API
func NewSTDIOTransport(ctx context.Context, cmd string, opts STDIOOptions) (Transport, error) {
	return nil, errors.New("NewSTDIOTransport is deprecated, use mcp.NewClient(\"stdio://\" + cmd) instead")
}

// NewClientFromServerConfig 将 ServerConfig 转为 spec 字符串（已废弃但保留兼容）
func NewClientFromServerConfig(ctx context.Context, name string, cfg ServerConfig, opts ...interface{}) (*Client, error) {
	var spec string
	switch strings.ToLower(strings.TrimSpace(cfg.Type)) {
	case "stdio", "":
		spec = "stdio://" + cfg.Command
		if len(cfg.Args) > 0 {
			spec += " " + strings.Join(cfg.Args, " ")
		}
	case "sse":
		spec = "http+sse://" + cfg.URL
	case "http":
		spec = cfg.URL
	default:
		return nil, fmt.Errorf("unsupported server type: %s", cfg.Type)
	}
	return NewClient(spec), nil
}
