# Getting Started

This guide walks through the basic usage of agentsdk-go, including environment setup, core concepts, and common code examples.

## Environment Requirements

### Required

- Go 1.24 or later
- Git (to clone the repo)
- Anthropic API Key

### Verify

```bash
go version  # should show go1.24 or later
```

## Installation

### Get the Source

```bash
git clone https://github.com/cexll/agentsdk-go.git
cd agentsdk-go
```

### Build Check

```bash
# Build the project
make build

# Run core module tests
go test ./pkg/agent ./pkg/middleware
```

### Configure API Key

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

## Basic Examples

### Minimal Runnable Example

Create `main.go`:

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

    // 创建模型提供者
    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    // 初始化运行时
    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // 执行任务
    result, err := runtime.Run(ctx, api.Request{
        Prompt:    "列出当前目录下的文件",
        SessionID: "demo",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("输出: %s", result.Output)
}
```

Run:

```bash
go run main.go
```

### Using Middleware

```go
package main

import (
    "context"
    "log"
    "os"
    "time"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/middleware"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    ctx := context.Background()

    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    // 定义日志 Middleware
    loggingMiddleware := middleware.Middleware{
        BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
            log.Printf("[请求] %s", req.Input)
            req.Meta["start_time"] = time.Now()
            return req, nil
        },
        AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
            duration := time.Since(resp.Meta["start_time"].(time.Time))
            log.Printf("[响应] 耗时: %v", duration)
            return resp, nil
        },
    }

    // 注入 Middleware
    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
        Middleware:    []middleware.Middleware{loggingMiddleware},
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    result, err := runtime.Run(ctx, api.Request{
        Prompt:    "计算 1+1",
        SessionID: "math",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("结果: %s", result.Output)
}
```

### Streaming Output

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    ctx := context.Background()

    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // 使用流式 API
    events := runtime.RunStream(ctx, api.Request{
        Prompt:    "分析当前项目结构",
        SessionID: "stream-demo",
    })

    for event := range events {
        switch event.Type {
        case "content_block_delta":
            fmt.Print(event.Delta.Text)
        case "tool_execution_start":
            fmt.Printf("\n[执行工具] %s\n", event.ToolName)
        case "tool_execution_stop":
            fmt.Printf("[工具输出] %s\n", event.Output)
        case "message_stop":
            fmt.Println("\n[完成]")
        }
    }
}
```

## Core Concepts

### Agent

The Agent is the core component that orchestrates model calls and tool execution (`pkg/agent/agent.go`).

Key method:

- `Run(ctx context.Context) (*ModelOutput, error)` — runs a full agent loop

Key traits:

- Supports multi-iteration (model → tools → model)
- `MaxIterations` limits loop count
- Middleware executes at 6 hook points

### Model

The Model interface defines provider behavior (`pkg/model/interface.go`):

```go
type Model interface {
    Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}
```

Currently supported provider:

- Anthropic Claude (via `AnthropicProvider`)

### Tool

Tools are external functions the Agent can invoke (`pkg/tool/tool.go`):

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

Built-ins (`pkg/tool/builtin/`):

- `bash` — execute shell commands
- `file_read` — read files
- `file_write` — write files
- `grep` — content search
- `glob` — file globbing

### Middleware

Middleware offers 6 interception points to inject custom logic (`pkg/middleware/`):

1. `BeforeAgent` — before Agent runs
2. `BeforeModel` — before model call
3. `AfterModel` — after model call
4. `BeforeTool` — before tool execution
5. `AfterTool` — after tool execution
6. `AfterAgent` — after Agent finishes

### Context

Context maintains state during Agent execution (`pkg/agent/context.go`):

- message history
- tool execution results
- session metadata

## Configuration

### Directory Layout

Configuration lives under `.claude/`:

```
.claude/
├── settings.json         # main config
├── settings.local.json   # local overrides (gitignored)
├── skills/               # skill definitions
├── commands/             # slash command definitions
└── agents/               # subagent definitions
```

### Precedence (high → low)

1. Runtime overrides (CLI flags / API `RuntimeOverrides`)
2. `.claude/settings.local.json`
3. `.claude/settings.json`
4. SDK defaults

`~/.claude/` is no longer read—keep config in the project.

### settings.json Example

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

### Load Config

```go
import "github.com/cexll/agentsdk-go/pkg/config"

loader, err := config.NewLoader(".", config.WithClaudeDir(".claude"))
if err != nil {
    log.Fatal(err)
}

cfg, err := loader.Load()
if err != nil {
    log.Fatal(err)
}
```

## Middleware Development

### Basic Middleware

```go
loggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("收到请求: %s", req.Input)
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        log.Printf("返回响应: %s", resp.Output)
        return resp, nil
    },
}
```

### Sharing State

Use `Meta` to share data across hooks:

```go
timingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        req.Meta["start_time"] = time.Now()
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        startTime := resp.Meta["start_time"].(time.Time)
        duration := time.Since(startTime)
        log.Printf("执行时间: %v", duration)
        return resp, nil
    },
}
```

### Error Handling

Returning an error stops the chain:

```go
validationMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        if req.Input == "" {
            return nil, errors.New("输入不能为空")
        }
        return req, nil
    },
}
```

### Complex Example: Rate Limiting + Monitoring

```go
package main

import (
    "context"
    "errors"
    "log"
    "time"

    "github.com/cexll/agentsdk-go/pkg/middleware"
)

// 令牌桶限流器
type rateLimiter struct {
    tokens    int
    maxTokens int
    lastTime  time.Time
}

func (r *rateLimiter) allow() bool {
    now := time.Now()
    elapsed := now.Sub(r.lastTime).Seconds()
    r.tokens = min(r.maxTokens, r.tokens+int(elapsed*5)) // 每秒补充 5 个令牌
    r.lastTime = now

    if r.tokens > 0 {
        r.tokens--
        return true
    }
    return false
}

func createRateLimitMiddleware(maxTokens int) middleware.Middleware {
    limiter := &rateLimiter{
        tokens:    maxTokens,
        maxTokens: maxTokens,
        lastTime:  time.Now(),
    }

    return middleware.Middleware{
        BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
            if !limiter.allow() {
                return nil, errors.New("请求过于频繁，请稍后再试")
            }
            return req, nil
        },
    }
}

func createMonitoringMiddleware() middleware.Middleware {
    return middleware.Middleware{
        BeforeModel: func(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
            // 记录模型调用
            log.Printf("[监控] 模型调用开始")
            return msgs, nil
        },
        AfterModel: func(ctx context.Context, output *agent.ModelOutput) (*agent.ModelOutput, error) {
            // 记录模型响应
            log.Printf("[监控] 模型调用结束，生成 %d 个工具调用", len(output.ToolCalls))
            return output, nil
        },
        BeforeTool: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
            // 记录工具调用
            log.Printf("[监控] 执行工具: %s", call.Name)
            return call, nil
        },
        AfterTool: func(ctx context.Context, result *middleware.ToolResult) (*middleware.ToolResult, error) {
            // 记录工具结果
            if result.Error != nil {
                log.Printf("[监控] 工具执行失败: %v", result.Error)
            }
            return result, nil
        },
    }
}

func min(a, b int) int {
    if a < b {
        return a
    }
    return b
}
```

## Running Examples

### CLI

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/02-cli
go run . --session-id demo --settings-path .claude/settings.json
```

Flags:

- `--session-id` — session ID (defaults to `SESSION_ID` env or `demo-session`)
- `--settings-path` — path to `.claude/settings.json` to enable sandbox/tool config

### HTTP Server

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/03-http
go run .
```

Endpoints:

- Health: `http://localhost:8080/health`
- Sync run: `POST /v1/run`
- Streaming: `POST /v1/run/stream`

### MCP Client

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/mcp
go run .
```
