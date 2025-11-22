# agentsdk-go v0.1.0 发布说明

## 版本信息
- 版本：v0.1.0（首个公开版本）
- 发布日期：2025-01-15
- 简介：Go Agent SDK，完整实现 Claude Code 的 7 项核心能力，并新增独创的 6 点 Middleware 拦截链，保持 KISS、YAGNI 原则。

## 核心特性 🚀
- 13 个独立包，模块化、解耦。
- Agent 核心循环 189 行，保持可审计的简单性。
- 平均测试覆盖率 90.5%。
- Claude Code 七大能力：Hooks、MCP、Sandbox、Skills、Subagents、Commands、Plugins。
- 6 点 Middleware 拦截：before/after agent、model、tool。
- 三层安全防御：路径白名单、符号链接解析、命令验证。

## 技术指标 📊
| 指标 | 数值 | 说明 |
| --- | --- | --- |
| 代码量 | ~20,300 行 | 生产代码（不含测试） |
| 包数量 | 13 | 核心层 6 + 功能层 7 |
| 核心循环 | 189 行 | `pkg/agent/agent.go` |
| 覆盖率 | 90.5% | 核心模块平均 |
| 部署 | CLI / CI/CD / 企业平台 | 三类入口 |
| 依赖 | anthropic-sdk-go, fsnotify, yaml.v3, uuid, x/mod, x/net | 外部依赖 |

## 主要模块
- 核心层（6）：agent、middleware、model、tool、message、api
- 功能层（7）：hooks、mcp、sandbox、skills、subagents、commands、plugins

## 内置工具
`bash`、`file_read`、`file_write`、`grep`、`glob`

## 示例
- 提供 10 个可运行示例（含 CLI、HTTP、进阶流水线等场景）

## 快速开始（摘自 README）
```go
ctx := context.Background()
provider := model.NewAnthropicProvider(
    model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    model.WithModel("claude-sonnet-4-5"),
)
runtime, err := api.New(ctx, api.Options{
    ProjectRoot:  ".",
    ModelFactory: provider,
})
if err != nil { log.Fatal(err) }
defer runtime.Close()

result, err := runtime.Run(ctx, api.Request{
    Prompt:    "List files in the current directory",
    SessionID: "demo",
})
if err != nil { log.Fatal(err) }
log.Printf("Output: %s", result.Output)
```

## 安装与环境
- 安装：`go get github.com/cexll/agentsdk-go`
- 环境：Go 1.24.0+，需设置 `ANTHROPIC_API_KEY`

## 已知问题
- Middleware 在多次工具执行时的错误处理：`AfterTool` 返回错误会导致后续 tool 结果丢失，下一次循环产生 tool_use/tool_result 不匹配，报 400。位置：`pkg/agent/agent.go:152-179`。临时做法：`AfterTool` 记录错误但返回 `nil`。

## 下一步计划（v0.2）
- 事件系统增强
- WAL 持久化
- 性能优化

---

# agentsdk-go v0.1.0 Release Notes

## Version
- Version: v0.1.0 (first public release)
- Release Date: 2025-01-15
- Summary: Go Agent SDK delivering Claude Code’s seven core capabilities plus a unique six-point middleware chain; keeps the surface small and practical.

## Highlights 🚀
- 13 independent packages for clean modularity.
- Agent core loop is 189 lines—intentionally small.
- 90.5% average test coverage.
- Claude Code feature set: Hooks, MCP, Sandbox, Skills, Subagents, Commands, Plugins.
- Six middleware interception points: before/after agent, model, tool.
- Triple-layer safety: path whitelist, symlink resolution, command validation.

## Technical Metrics 📊
| Metric | Value | Notes |
| --- | --- | --- |
| Code size | ~20,300 LOC | Production only |
| Packages | 13 | Core 6 + Feature 7 |
| Core loop | 189 lines | `pkg/agent/agent.go` |
| Coverage | 90.5% | Core modules avg |
| Deploy modes | CLI / CI/CD / Enterprise | Three entry points |
| Dependencies | anthropic-sdk-go, fsnotify, yaml.v3, uuid, x/mod, x/net | External deps |

## Modules
- Core (6): agent, middleware, model, tool, message, api
- Feature (7): hooks, mcp, sandbox, skills, subagents, commands, plugins

## Built-in Tools
`bash`, `file_read`, `file_write`, `grep`, `glob`

## Examples
- 10 runnable examples covering CLI, HTTP, and advanced pipelines.

## Quick Start (from README)
```go
ctx := context.Background()
provider := model.NewAnthropicProvider(
    model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    model.WithModel("claude-sonnet-4-5"),
)
runtime, err := api.New(ctx, api.Options{
    ProjectRoot:  ".",
    ModelFactory: provider,
})
if err != nil { log.Fatal(err) }
defer runtime.Close()

result, err := runtime.Run(ctx, api.Request{
    Prompt:    "List files in the current directory",
    SessionID: "demo",
})
if err != nil { log.Fatal(err) }
log.Printf("Output: %s", result.Output)
```

## Install & Requirements
- Install: `go get github.com/cexll/agentsdk-go`
- Runtime: Go 1.24.0+, `ANTHROPIC_API_KEY` set

## Known Issues
- Middleware error handling in multi-tool runs: when `AfterTool` returns an error, subsequent tool results are dropped, causing a tool_use/tool_result mismatch and a 400 on the next iteration. Location: `pkg/agent/agent.go:152-179`. Current workaround: log the error but return `nil` from `AfterTool`.

## What’s Next (v0.2)
- Event system improvements
- WAL persistence
- Performance tuning
