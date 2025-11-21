# 入门指南

本文档介绍 agentsdk-go 的基本使用方法，包括环境配置、核心概念和常见场景的代码示例。

## 环境要求

### 必需组件

- Go 1.23 或更高版本
- Git（用于克隆仓库）
- Anthropic API Key

### 验证环境

```bash
go version  # 应显示 go1.23 或更高版本
```

## 安装

### 获取源码

```bash
git clone https://github.com/cexll/agentsdk-go.git
cd agentsdk-go
```

### 构建验证

```bash
# 构建项目
make build

# 运行核心模块测试
go test ./pkg/agent ./pkg/middleware
```

### 配置 API Key

```bash
export ANTHROPIC_API_KEY=sk-ant-...
```

## 基础示例

### 最小可运行示例

创建 `main.go` 文件：

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

运行示例：

```bash
go run main.go
```

### 使用 Middleware

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

### 流式输出

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

## 核心概念

### Agent

Agent 是 SDK 的核心组件，负责协调模型调用和工具执行。位于 `pkg/agent/agent.go`。

关键方法：

- `Run(ctx context.Context) (*ModelOutput, error)` - 执行一次完整的 Agent 循环

关键特性：

- 支持多轮迭代（模型 → 工具 → 模型）
- 通过 `MaxIterations` 选项限制循环次数
- 在 6 个拦截点执行 Middleware

### Model

Model 接口定义了模型提供者的行为。位于 `pkg/model/interface.go`。

```go
type Model interface {
    Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}
```

当前支持的提供者：

- Anthropic Claude（通过 `AnthropicProvider`）

### Tool

Tool 是 Agent 可以调用的外部功能。位于 `pkg/tool/tool.go`。

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

内置工具（位于 `pkg/tool/builtin/`）：

- `bash` - 执行 shell 命令
- `file_read` - 读取文件
- `file_write` - 写入文件
- `grep` - 内容搜索
- `glob` - 文件匹配

### Middleware

Middleware 提供 6 个拦截点，允许在请求处理的关键阶段注入自定义逻辑。位于 `pkg/middleware/`。

拦截点：

1. `BeforeAgent` - Agent 执行前
2. `BeforeModel` - 模型调用前
3. `AfterModel` - 模型调用后
4. `BeforeTool` - 工具执行前
5. `AfterTool` - 工具执行后
6. `AfterAgent` - Agent 执行后

### Context

Context 维护 Agent 执行过程中的状态信息。位于 `pkg/agent/context.go`。

包含信息：

- 消息历史
- 工具执行结果
- 会话元数据

## 配置管理

### 配置文件结构

SDK 使用 `.claude/` 目录管理配置：

```
.claude/
├── settings.json         # 主配置文件
├── settings.local.json   # 本地覆盖（已加入 .gitignore）
├── skills/               # Skills 定义
├── commands/             # 斜杠命令定义
├── agents/               # Subagents 定义
└── plugins/              # 插件目录
```

### settings.json 示例

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

### 配置加载

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

## Middleware 开发

### 基础 Middleware

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

### 状态共享

使用 `Meta` 字段在拦截点之间共享数据：

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

### 错误处理

Middleware 返回 error 会中断执行链：

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

### 复杂示例：限流 + 监控

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

## 运行示例程序

### CLI 示例

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/02-cli
go run . --session-id demo --settings-path .claude/settings.json
```

支持的参数：

- `--session-id` - 指定会话 ID（默认 `SESSION_ID` 环境变量或 `demo-session`）
- `--settings-path` - 指定 `.claude/settings.json`，启用沙箱/工具配置

### HTTP 服务器示例

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/03-http
go run .
```

启动后访问：

- 健康检查：`http://localhost:8080/health`
- 同步执行：`POST /v1/run`
- 流式输出：`POST /v1/run/stream`

### MCP 客户端示例

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/mcp
go run .
```

演示如何集成外部 MCP 服务器。

### Middleware 示例

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/middleware
go run .
```

展示完整的 Middleware 使用场景：日志、限流、监控、安全检查。

## 常见问题

### Agent 提前结束

原因：达到最大迭代次数限制。

解决方案：

```go
runtime, err := api.New(ctx, api.Options{
    MaxIterations: 10, // 增加迭代次数限制
    // ...
})
```

### 工具执行失败

原因：工具参数不符合 Schema 定义。

解决方案：

1. 检查工具的 `Schema()` 方法定义
2. 确保参数类型正确
3. 检查沙箱路径配置

### Middleware 顺序问题

Middleware 按注册顺序执行。如果存在依赖关系，确保正确的注册顺序：

```go
runtime, err := api.New(ctx, api.Options{
    Middleware: []middleware.Middleware{
        rateLimitMiddleware,  // 先限流
        loggingMiddleware,    // 后记录
        monitoringMiddleware, // 最后监控
    },
})
```

### 会话隔离

使用不同的 SessionID 确保会话隔离：

```go
result1, _ := runtime.Run(ctx, api.Request{
    Prompt:    "任务 1",
    SessionID: "session-1", // 独立会话
})

result2, _ := runtime.Run(ctx, api.Request{
    Prompt:    "任务 2",
    SessionID: "session-2", // 另一个独立会话
})
```

## 最佳实践

### 错误处理

始终检查错误并提供有用的错误信息：

```go
result, err := runtime.Run(ctx, api.Request{
    Prompt:    prompt,
    SessionID: sessionID,
})
if err != nil {
    log.Printf("执行失败: %v", err)
    return err
}
```

### 资源清理

使用 `defer` 确保资源正确释放：

```go
runtime, err := api.New(ctx, opts)
if err != nil {
    return err
}
defer runtime.Close() // 确保清理资源
```

### 上下文管理

使用带超时的 Context 防止长时间阻塞：

```go
ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
defer cancel()

result, err := runtime.Run(ctx, api.Request{
    Prompt:    prompt,
    SessionID: sessionID,
})
```

### 日志记录

在 Middleware 中添加结构化日志：

```go
loggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("[request_id=%s] input=%s session=%s",
            req.RequestID, req.Input, req.SessionID)
        return req, nil
    },
}
```

### 配置管理

将环境相关配置放入环境变量或配置文件：

```go
// 从环境变量读取
apiKey := os.Getenv("ANTHROPIC_API_KEY")
model := os.Getenv("MODEL_NAME")
if model == "" {
    model = "claude-sonnet-4-5" // 默认值
}

provider := model.NewAnthropicProvider(
    model.WithAPIKey(apiKey),
    model.WithModel(model),
)
```

## 下一步

- 阅读 [架构文档](architecture.md) 了解系统设计
- 阅读 [API 参考](api-reference.md) 了解详细 API
- 阅读 [安全文档](security.md) 了解安全配置
- 查看 [自定义工具指南](custom-tools-guide.md) 学习工具开发
