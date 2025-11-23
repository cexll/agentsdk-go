中文 | [English](README.md)

# agentsdk-go

基于 Go 语言实现的 Agent SDK，提供完整的 Claude Code 核心功能和 Middleware 拦截机制。

## 概述

agentsdk-go 是一个模块化的 Agent 开发框架，实现了 Claude Code 的 7 项核心功能（Hooks、MCP、Sandbox、Skills、Subagents、Commands、Plugins），并在此基础上扩展了 6 点 Middleware 拦截机制。该 SDK 支持 CLI、CI/CD 和企业平台等多种部署场景。

### 技术指标

- 核心代码：约 20,300 行（生产代码，不含测试）
- Agent 核心循环：189 行
- 测试覆盖率：核心模块平均 90.5%（实际：subagents 91.7%，api 90.2%，mcp 90.3%，model 92.2%，sandbox 90.5%，security 90.4%）
- 模块数量：13 个独立包
- 外部依赖：anthropic-sdk-go、fsnotify、gopkg.in/yaml.v3、google/uuid、golang.org/x/mod、golang.org/x/net

## 系统架构

### 核心层（6 个模块）

- `pkg/agent` - Agent 执行循环，负责模型调用和工具执行的协调
- `pkg/middleware` - 6 点拦截机制，支持请求/响应生命周期的扩展
- `pkg/model` - 模型适配器，当前支持 Anthropic Claude
- `pkg/tool` - 工具注册与执行，包含内置工具和 MCP 工具支持
- `pkg/message` - 消息历史管理，基于 LRU 的会话缓存
- `pkg/api` - 统一 API 接口，对外暴露 SDK 功能

### 功能层（7 个模块）

- `pkg/core/hooks` - Hooks 执行器，覆盖 7 类生命周期事件，支持自定义扩展
- `pkg/mcp` - MCP（Model Context Protocol）客户端，桥接外部工具（stdio/SSE）并自动注册
- `pkg/sandbox` - 沙箱隔离层，控制文件系统与网络访问策略
- `pkg/runtime/skills` - Skills 管理，支持脚本化技能装载与热更新
- `pkg/runtime/subagents` - Subagents 管理，负责多智能体的编排与调度
- `pkg/runtime/commands` - Commands 解析器，处理 Slash 命令路由与参数校验
- `pkg/plugins` - 插件系统，支持签名验证与生命周期钩子

此外，功能层还包含 `pkg/config`（配置加载/热更新）、`pkg/core/events`（事件总线）和 `pkg/security`（命令与路径校验）等支撑包。

### Middleware 拦截点

SDK 在请求处理的关键节点提供拦截能力：

```
用户请求
  ↓
before_agent  ← 请求验证、审计日志
  ↓
Agent 循环
  ↓
before_model  ← Prompt 处理、上下文优化
  ↓
模型调用
  ↓
after_model   ← 结果过滤、内容检查
  ↓
before_tool   ← 工具参数验证
  ↓
工具执行
  ↓
after_tool    ← 结果后处理
  ↓
after_agent   ← 响应格式化、指标采集
  ↓
用户响应
```

## 安装

### 环境要求

- Go 1.24.0 或更高版本
- Anthropic API Key（运行示例需要）

### 获取 SDK

```bash
go get github.com/cexll/agentsdk-go
```

## 快速开始

### 基础示例

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

### 使用 Middleware

```go
import (
    "context"
    "log"
    "time"

    "github.com/cexll/agentsdk-go/pkg/middleware"
)

// 日志中间件
loggingMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        log.Printf("[REQUEST] %s", req.Input)
        req.Meta["start_time"] = time.Now()
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        duration := time.Since(resp.Meta["start_time"].(time.Time))
        log.Printf("[RESPONSE] %s (耗时: %v)", resp.Output, duration)
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
```

### 流式输出

```go
// 使用流式 API 获取实时进度
events := runtime.RunStream(ctx, api.Request{
    Prompt:    "分析代码库结构",
    SessionID: "analysis",
})

for event := range events {
    switch event.Type {
    case "content_block_delta":
        fmt.Print(event.Delta.Text)
    case "tool_execution_start":
        fmt.Printf("\n[工具执行] %s\n", event.ToolName)
    case "tool_execution_stop":
        fmt.Printf("[工具结果] %s\n", event.Output)
    }
}
```

## 示例

当前仓库包含 10 个可运行示例：`cli`、`http`、`mcp`、`middleware`、`hooks`、`sandbox`、`skills`、`subagents`、`commands`、`plugins`。

## 项目结构

```
agentsdk-go/
├── pkg/                        # 核心包
│   ├── agent/                  # Agent 核心循环
│   ├── middleware/             # Middleware 系统
│   ├── model/                  # 模型适配器
│   ├── tool/                   # 工具系统
│   │   └── builtin/            # 内置工具（bash、file、grep、glob）
│   ├── message/                # 消息历史管理
│   ├── api/                    # SDK 统一接口
│   ├── config/                 # 配置加载
│   ├── plugins/                # 插件系统
│   ├── core/
│   │   ├── events/             # 事件总线
│   │   └── hooks/              # Hooks 执行器
│   ├── sandbox/                # 沙箱隔离
│   ├── mcp/                    # MCP 客户端
│   ├── runtime/
│   │   ├── skills/             # Skills 管理
│   │   ├── subagents/          # Subagents 管理
│   │   └── commands/           # Commands 解析
│   └── security/               # 安全工具
├── cmd/cli/                    # CLI 入口
├── examples/                   # 示例代码
│   ├── cli/                    # CLI 示例
│   ├── http/                   # HTTP 服务器示例
│   ├── mcp/                    # MCP 客户端示例
│   ├── middleware/             # Middleware 示例
│   ├── commands/               # Commands 示例
│   ├── hooks/                  # Hooks 示例
│   ├── sandbox/                # Sandbox 示例
│   ├── skills/                 # Skills 示例
│   ├── subagents/              # Subagents 示例
│   └── plugins/                # Plugins 示例
├── test/integration/           # 集成测试
└── docs/                       # 文档
```

## 配置

SDK 使用 `.claude/` 目录进行配置，与 Claude Code 兼容：

```
.claude/
├── settings.json     # 项目配置
├── settings.local.json  # 本地覆盖（已加入 .gitignore）
├── skills/           # Skills 定义
├── commands/         # 斜杠命令定义
├── agents/           # Subagents 定义
└── plugins/          # 插件目录
```

### 配置示例

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

SDK 提供 HTTP 服务器实现，支持 SSE 流式推送。

### 启动服务器

```bash
export ANTHROPIC_API_KEY=sk-ant-...
cd examples/03-http
go run .
```

服务器默认监听 `:8080`，提供以下端点：

- `GET /health` - 健康检查
- `POST /v1/run` - 同步执行，返回完整结果
- `POST /v1/run/stream` - SSE 流式输出，实时返回进度

### 流式 API 示例

```bash
curl -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{
    "prompt": "列出当前目录",
    "session_id": "demo"
  }'
```

响应格式遵循 Anthropic Messages API 规范，包含以下事件类型：

- `agent_start` / `agent_stop` - Agent 执行边界
- `iteration_start` / `iteration_stop` - 迭代边界
- `message_start` / `message_stop` - 消息边界
- `content_block_delta` - 文本增量输出
- `tool_execution_start` / `tool_execution_stop` - 工具执行进度

## 测试

### 运行测试

```bash
# 所有测试
go test ./...

# 核心模块测试
go test ./pkg/agent/... ./pkg/middleware/... ./pkg/model/...

# 集成测试
go test ./test/integration/...

# 生成覆盖率报告
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out
```

### 测试覆盖率

#### 核心模块（全部 ≥90%）

| 模块 | 覆盖率 |
|------|--------|
| pkg/runtime/subagents | 91.7% |
| pkg/api | 90.2% |
| pkg/mcp | 90.3% |
| pkg/model | 92.2% |
| pkg/sandbox | 90.5% |
| pkg/security | 90.4% |
| 平均 | 90.5% |

## 构建

### Makefile 命令

```bash
# 运行测试
make test

# 生成覆盖率报告
make coverage

# 代码检查
make lint

# 构建 CLI 工具
make agentctl

# 安装到 GOPATH
make install

# 清理构建产物
make clean
```

## 内置工具

SDK 包含以下内置工具（位于 `pkg/tool/builtin/`）：

- `bash` - 执行 shell 命令，支持工作目录和超时配置
- `file_read` - 读取文件内容
- `file_write` - 写入文件内容
- `grep` - 正则搜索，支持递归和文件过滤
- `glob` - 文件模式匹配

所有内置工具遵循沙箱策略，受路径白名单和命令验证器约束。

## 安全机制

### 三层防御

1. **路径白名单**：限制文件系统访问范围
2. **符号链接解析**：防止路径遍历攻击
3. **命令验证**：阻止危险命令执行

### 命令验证器

位于 `pkg/security/validator.go`，默认阻止以下操作：

- 破坏性命令：`dd`、`mkfs`、`fdisk`、`shutdown`、`reboot`
- 危险删除模式：`rm -rf`、`rm -r`、`rmdir -p`
- Shell 元字符：`|`、`;`、`&`、`>`、`<`、`` ` ``（在 Platform 模式下）

## 开发指南

### 添加自定义工具

实现 `tool.Tool` 接口：

```go
type CustomTool struct{}

func (t *CustomTool) Name() string {
    return "custom_tool"
}

func (t *CustomTool) Description() string {
    return "工具描述"
}

func (t *CustomTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "param": map[string]interface{}{
                "type": "string",
                "description": "参数说明",
            },
        },
        Required: []string{"param"},
    }
}

func (t *CustomTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // 工具实现
    return &tool.ToolResult{
        Name:   t.Name(),
        Output: "执行结果",
    }, nil
}
```

### 添加 Middleware

```go
customMiddleware := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        // 请求前处理
        return req, nil
    },
    AfterAgent: func(ctx context.Context, resp *middleware.AgentResponse) (*middleware.AgentResponse, error) {
        // 响应后处理
        return resp, nil
    },
    BeforeModel: func(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
        // 模型调用前处理
        return msgs, nil
    },
    AfterModel: func(ctx context.Context, output *agent.ModelOutput) (*agent.ModelOutput, error) {
        // 模型调用后处理
        return output, nil
    },
    BeforeTool: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
        // 工具执行前处理
        return call, nil
    },
    AfterTool: func(ctx context.Context, result *middleware.ToolResult) (*middleware.ToolResult, error) {
        // 工具执行后处理
        return result, nil
    },
}
```

## 设计原则

### KISS（Keep It Simple, Stupid）

- Agent 核心循环保持在 171 行
- 单一职责，每个模块功能明确
- 避免过度设计和不必要的抽象

### 配置驱动

- 通过 `.claude/` 目录管理所有配置
- 支持热更新，无需重启服务
- 声明式配置优于命令式代码

### 模块化

- 13 个独立包，松耦合设计
- 清晰的接口边界
- 易于测试和维护

### 可扩展性

- Middleware 机制支持灵活扩展
- 工具系统支持自定义工具注册
- MCP 协议支持外部工具集成

## 文档

- [架构文档](docs/architecture.md) - 详细架构分析
- [入门指南](docs/getting-started.md) - 分步教程
- [API 参考](docs/api-reference.md) - API 文档
- [安全实践](docs/security.md) - 安全配置指南
- [HTTP API 指南](examples/03-http/README.md) - HTTP 服务器使用说明
- [开发计划](.claude/specs/claude-code-rewrite/dev-plan.md) - 架构设计计划
- [完成报告](.claude/specs/claude-code-rewrite/COMPLETION_REPORT.md) - 实现报告

## 技术栈

- Go 1.24.0+
- [anthropic-sdk-go](https://github.com/anthropics/anthropic-sdk-go) - Anthropic 官方 SDK
- [modelcontextprotocol/go-sdk](https://github.com/modelcontextprotocol/go-sdk) - 官方 MCP SDK
- [fsnotify](https://github.com/fsnotify/fsnotify) - 文件系统监控
- [yaml.v3](https://gopkg.in/yaml.v3) - YAML 解析
- [google/uuid](https://github.com/google/uuid) - UUID 工具
- [golang.org/x/mod](https://pkg.go.dev/golang.org/x/mod) - 模块工具
- [golang.org/x/net](https://pkg.go.dev/golang.org/x/net) - 扩展网络包

## 许可证

详见 [LICENSE](LICENSE) 文件。
