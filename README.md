[中文](README_zh.md) | English

# agentsdk-go

An Agent SDK implemented in Go that provides the full Claude Code core capabilities and middleware interception.

## Overview

agentsdk-go is a modular agent development framework that implements Claude Code's seven core capabilities (Hooks, MCP, Sandbox, Skills, Subagents, Commands, Plugins) and extends them with a six-point middleware interception mechanism. The SDK supports deployment scenarios across CLI, CI/CD, and enterprise platforms.

### Technical Metrics

- Core code: ~20,300 lines (production code, excluding tests)
- Agent core loop: 189 lines
- Test coverage: 90–93% across six core modules; all ≥90% (subagents 91.7%, api 90.2%, mcp 90.3%, model 92.2%, sandbox 90.5%, security 90.4%)
- Modules: 13 independent packages
- External dependencies: anthropic-sdk-go, fsnotify, gopkg.in/yaml.v3, google/uuid, golang.org/x/mod, golang.org/x/net

## Features

- Four-layer example path: `examples/01-basic` (minimal request/response), `examples/02-cli` (interactive REPL), `examples/03-http` (REST + SSE server on :8080), `examples/04-advanced` (full pipeline with middleware, hooks, MCP, sandbox, skills, subagents)

## System Architecture

### Core Layer (6 modules)

- `pkg/agent` - Agent execution loop coordinating model calls and tool execution
- `pkg/middleware` - Six interception points for extending the request/response lifecycle
- `pkg/model` - Model adapters, currently supports Anthropic Claude
- `pkg/tool` - Tool registration and execution, including built-in tools and MCP tool support
- `pkg/message` - Message history management with an LRU-based session cache
- `pkg/api` - Unified API surface exposing SDK features

### Feature Layer (7 modules)

- `pkg/core/hooks` - Hooks executor covering seven lifecycle events with custom extensions
- `pkg/mcp` - MCP (Model Context Protocol) client bridging external tools (stdio/SSE) with automatic registration
- `pkg/sandbox` - Sandbox isolation layer controlling filesystem and network access policies
- `pkg/runtime/skills` - Skills management supporting scriptable loading and hot reload
- `pkg/runtime/subagents` - Subagent management for multi-agent orchestration and scheduling
- `pkg/runtime/commands` - Commands parser handling slash-command routing and parameter validation
- `pkg/plugins` - Plugin system with signature verification and lifecycle hooks

In addition, the feature layer includes supporting packages such as `pkg/config` (configuration loading/hot reload), `pkg/core/events` (event bus), and `pkg/security` (command and path validation).

### Middleware Interception Points

The SDK exposes interception at critical stages of request handling:

```
User request
  ↓
before_agent  ← Request validation, audit logging
  ↓
Agent loop
  ↓
before_model  ← Prompt processing, context optimization
  ↓
Model invocation
  ↓
after_model   ← Result filtering, content checks
  ↓
before_tool   ← Tool parameter validation
  ↓
Tool execution
  ↓
after_tool    ← Result post-processing
  ↓
after_agent   ← Response formatting, metrics collection
  ↓
User response
```

## Installation

### Requirements

- Go 1.24.0 or later
- Anthropic API Key (required to run examples)

### Get the SDK

```bash
go get github.com/cexll/agentsdk-go
```

## Quick Start

### Basic Example (examples/01-basic)

Run the minimal starter in `examples/01-basic`:

```bash
# 1. Set up environment
cp .env.example .env
# Edit .env: ANTHROPIC_API_KEY=sk-ant-your-key-here
source .env

# 2. Run the example
go run ./examples/01-basic
```

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    ctx := context.Background()

    // Create the model provider
    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    // Initialize the runtime
    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // Execute a task
    result, err := runtime.Run(ctx, api.Request{
        Prompt:    "List files in the current directory",
        SessionID: "demo",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("Output: %s", result.Output)
}
```

### Using Middleware

```go
import (
    "context"
    "log"
    "time"

    "github.com/cexll/agentsdk-go/pkg/middleware"
)

// Logging middleware
loggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("[REQUEST] %s", req.Input)
        req.Meta["start_time"] = time.Now()
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        duration := time.Since(resp.Meta["start_time"].(time.Time))
        log.Printf("[RESPONSE] %s (elapsed: %v)", resp.Output, duration)
        return resp, nil
    },
}

// Inject middleware
runtime, err := api.New(ctx, api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
    Middleware:    []middleware.Middleware{loggingMiddleware},
})
if err != nil {
    log.Fatal(err)
}
defer runtime.Close()
```

### Streaming Output

```go
// Use the streaming API to get real-time progress
events := runtime.RunStream(ctx, api.Request{
    Prompt:    "Analyze the repository structure",
    SessionID: "analysis",
})

for event := range events {
    switch event.Type {
    case "content_block_delta":
        fmt.Print(event.Delta.Text)
    case "tool_execution_start":
        fmt.Printf("\n[Tool Execution] %s\n", event.ToolName)
    case "tool_execution_stop":
        fmt.Printf("[Tool Result] %s\n", event.Output)
    }
}
```

### Customize Tool Registration

Choose which built-ins to load and append your own tools:

```go
rt, err := api.New(ctx, api.Options{
    ProjectRoot:         ".",
    ModelFactory:        provider,
    EnabledBuiltinTools: []string{"bash", "file_read"}, // nil = all, empty = none
    CustomTools:         []tool.Tool{&EchoTool{}},      // appended when Tools is empty
})
if err != nil {
    log.Fatal(err)
}
defer rt.Close()
```

- `EnabledBuiltinTools`: nil→全部内置；空切片→禁用内置；非空→只启用列出的内置（大小写不敏感，下划线命名）。  
- `CustomTools`: 追加自定义工具；当 `Tools` 非空时被忽略。  
- `Tools`: 旧字段，非空时完全接管工具集（保持向后兼容）。

See a runnable demo in `examples/05-custom-tools`.

## Examples

The repository includes four progressive examples aligned to the new four-layer path:
- `01-basic` – minimal single request/response.
- `02-cli` – interactive REPL with session history and optional config load.
- `03-http` – REST + SSE server on `:8080`.
- `04-advanced` – full pipeline exercising middleware, hooks, MCP, sandbox, skills, and subagents.
- `05-custom-tools` – selective built-ins plus custom tool registration.

## Project Structure

```
agentsdk-go/
├── pkg/                        # Core packages
│   ├── agent/                  # Agent core loop
│   ├── middleware/             # Middleware system
│   ├── model/                  # Model adapters
│   ├── tool/                   # Tool system
│   │   └── builtin/            # Built-in tools (bash, file, grep, glob)
│   ├── message/                # Message history management
│   ├── api/                    # Unified SDK interface
│   ├── config/                 # Configuration loading
│   ├── plugins/                # Plugin system
│   ├── core/
│   │   ├── events/             # Event bus
│   │   └── hooks/              # Hooks executor
│   ├── sandbox/                # Sandbox isolation
│   ├── mcp/                    # MCP client
│   ├── runtime/
│   │   ├── skills/             # Skills management
│   │   ├── subagents/          # Subagents management
│   │   └── commands/           # Commands parsing
│   └── security/               # Security utilities
├── cmd/cli/                    # CLI entrypoint
├── examples/                   # Example code
│   ├── 01-basic/               # Minimal single request/response
│   ├── 02-cli/                 # CLI REPL with session history
│   ├── 03-http/                # HTTP server (REST + SSE)
│   └── 04-advanced/            # Full pipeline (middleware, hooks, MCP, sandbox, skills, subagents)
├── test/integration/           # Integration tests
└── docs/                       # Documentation
```

## Configuration

The SDK uses the `.claude/` directory for configuration, compatible with Claude Code:

```
.claude/
├── settings.json     # Project configuration
├── settings.local.json  # Local overrides (gitignored)
├── skills/           # Skills definitions
├── commands/         # Slash command definitions
├── agents/           # Subagents definitions
└── plugins/          # Plugin directory
```

Configuration precedence (high → low):
- Managed policies (`/etc/claude-code/managed-settings.json` or platform-specific equivalents)
- Runtime overrides (CLI/API-provided)
- `.claude/settings.local.json`
- `.claude/settings.json`
- Built-in defaults (shipped with the SDK)

`~/.claude` is no longer read; use project-scoped files for all configuration.

### Configuration Example

```json
{
  "permissions": {
    "allow": ["Bash(ls:*)", "Bash(pwd:*)"],
    "deny": ["Read(.env)", "Read(secrets/**)"]
  },
  "env": {
    "MY_VAR": "value"
  },
  "sandbox": {
    "enabled": false
  }
}
```

## HTTP API

The SDK provides an HTTP server implementation with SSE streaming.

### Start the Server

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/03-http
go run .
```

The server listens on `:8080` by default and exposes these endpoints:

- `GET /health` - Liveness probe
- `POST /v1/run` - Synchronous execution returning the full result
- `POST /v1/run/stream` - SSE streaming with real-time progress

### Streaming API Example

```bash
curl -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "List the current directory",
    "session_id": "demo"
  }'
```

The response format follows the Anthropic Messages API and includes these event types:

- `agent_start` / `agent_stop` - Agent execution boundaries
- `iteration_start` / `iteration_stop` - Iteration boundaries
- `message_start` / `message_stop` - Message boundaries
- `content_block_delta` - Incremental text output
- `tool_execution_start` / `tool_execution_stop` - Tool execution progress

## Testing

### Run Tests

```bash
# All tests
go test ./...

# Core module tests
go test ./pkg/agent/... ./pkg/middleware/... ./pkg/model/...

# Integration tests
go test ./test/integration/...

# Generate coverage report
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### Test Coverage

#### Core Modules (all ≥90%)

| Module | Coverage |
|--------|----------|
| pkg/runtime/subagents | 91.7% |
| pkg/api | 90.2% |
| pkg/mcp | 90.3% |
| pkg/model | 92.2% |
| pkg/sandbox | 90.5% |
| pkg/security | 90.4% |
| Average | 90.5% |

## Build

### Makefile Commands

```bash
# Run tests
make test

# Generate coverage report
make coverage

# Lint code
make lint

# Build CLI tool
make agentctl

# Install into GOPATH
make install

# Clean build artifacts
make clean
```

## Built-in Tools

The SDK ships with the following built-in tools (under `pkg/tool/builtin/`):

- `bash` - Execute shell commands with working directory and timeout configuration
- `file_read` - Read file contents
- `file_write` - Write file contents
- `grep` - Regex search with recursion and file filtering
- `glob` - File pattern matching

All built-in tools obey sandbox policies and are constrained by the path whitelist and command validator.

## Security Mechanisms

### Three Layers of Defense

1. **Path whitelist**: Restricts filesystem access scope
2. **Symlink resolution**: Prevents path traversal attacks
3. **Command validation**: Blocks execution of dangerous commands

### Command Validator

Located at `pkg/security/validator.go`, it blocks the following by default:

- Destructive commands: `dd`, `mkfs`, `fdisk`, `shutdown`, `reboot`
- Hazardous delete patterns: `rm -rf`, `rm -r`, `rmdir -p`
- Shell metacharacters: `|`, `;`, `&`, `>`, `<`, `` ` `` (in Platform mode)

## Development Guide

### Add a Custom Tool

Implement the `tool.Tool` interface:

```go
type CustomTool struct{}

func (t *CustomTool) Name() string {
    return "custom_tool"
}

func (t *CustomTool) Description() string {
    return "Tool description"
}

func (t *CustomTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "param": map[string]interface{}{
                "type": "string",
                "description": "Parameter description",
            },
        },
        Required: []string{"param"},
    }
}

func (t *CustomTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // Tool implementation
    return &tool.ToolResult{
        Name:   t.Name(),
        Output: "Execution result",
    }, nil
}
```

### Add Middleware

```go
customMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        // Pre-request handling
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        // Post-response handling
        return resp, nil
    },
    BeforeModel: func(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
        // Before model call
        return msgs, nil
    },
    AfterModel: func(ctx context.Context, output *agent.ModelOutput) (*agent.ModelOutput, error) {
        // After model call
        return output, nil
    },
    BeforeTool: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
        // Before tool execution
        return call, nil
    },
    AfterTool: func(ctx context.Context, result *middleware.ToolResult) (*middleware.ToolResult, error) {
        // After tool execution
        return result, nil
    },
}
```

## Design Principles

### KISS (Keep It Simple, Stupid)

- Agent core loop stays at 171 lines
- Single responsibility; each module has a clear role
- Avoid overdesign and unnecessary abstractions

### Configuration-Driven

- Manage all configuration under `.claude/`
- Supports hot reload without restarting the service
- Declarative configuration preferred over imperative code

### Modularity

- 13 independent packages with loose coupling
- Clear interface boundaries
- Easy to test and maintain

### Extensibility

- Middleware mechanism for flexible extensions
- Tool system supports custom tool registration
- MCP protocol integrates external tools

## Documentation

- [Architecture](docs/architecture.md) - Detailed architecture analysis
- [Getting Started](docs/getting-started.md) - Step-by-step tutorial
- [API Reference](docs/api-reference.md) - API documentation
- [Security](docs/security.md) - Security configuration guide
- [HTTP API Guide](examples/03-http/README.md) - HTTP server instructions
- [Development Plan](.claude/specs/claude-code-rewrite/dev-plan.md) - Architecture plan
- [Completion Report](.claude/specs/claude-code-rewrite/COMPLETION_REPORT.md) - Implementation report

## Tech Stack

- Go 1.24.0+
- [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) - Anthropic official SDK
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) - Official MCP SDK
- [fsnotify](https://github.com/fsnotify/fsnotify) - Filesystem watchers
- [yaml.v3](https://gopkg.in/yaml.v3) - YAML parser
- [google/uuid](https://github.com/google/uuid) - UUID utilities
- [golang.org/x/mod](https://pkg.go.dev/golang.org/x/mod) - Module utilities
- [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) - Extended net packages

## License

See [LICENSE](LICENSE).
