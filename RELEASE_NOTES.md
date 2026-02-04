# agentsdk-go Release Notes

## 版本信息
- 版本：v0.1.0（历史版本）
- 发布日期：2025-01-15
- 简介：Go Agent SDK，对齐 Claude Code 的核心工作流能力（不包含 Plugins / LSP）。

## 核心特性 🚀
- Claude Code 主要能力：Hooks、MCP、Sandbox、Skills、Subagents、Commands、Tasks。
- 6 点 Middleware 拦截：before/after agent、model、tool。
- 三层安全防御：路径白名单、符号链接解析、命令验证。

## 主要模块
- 核心层（6）：agent、middleware、model、tool、message、api
- 功能层（7）：hooks、mcp、sandbox、skills、subagents、commands、tasks

## 内置工具
`bash`、`file_read`、`file_write`、`file_edit`、`grep`、`glob`、`web_fetch`、`web_search`、`task_*`

## 示例
- 提供多个可运行示例（含 CLI、HTTP、进阶流水线等场景）

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
- 如需严格的“不中断但记录”策略，请确保 `AfterTool` 中间件在记录错误后返回 `nil`，避免影响后续工具执行与结果回填。

## 下一步计划（v0.2）
- 事件系统增强
- WAL 持久化
- 性能优化

---

# agentsdk-go v0.1.0 Release Notes

## Version
- Version: v0.1.0 (historical)
- Release Date: 2025-01-15
- Summary: Go Agent SDK aligned with Claude Code’s core workflow surface (no Plugins / LSP).

## Highlights 🚀
- Claude Code feature set: Hooks, MCP, Sandbox, Skills, Subagents, Commands, Tasks.
- Six middleware interception points: before/after agent, model, tool.
- Triple-layer safety: path whitelist, symlink resolution, command validation.

## Modules
- Core (6): agent, middleware, model, tool, message, api
- Feature (7): hooks, mcp, sandbox, skills, subagents, commands, tasks

## Built-in Tools
`bash`, `file_read`, `file_write`, `file_edit`, `grep`, `glob`, `web_fetch`, `web_search`, `task_*`

## Examples
- Multiple runnable examples covering CLI, HTTP, and advanced pipelines.

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
- If you want “record errors but continue”, ensure `AfterTool` middleware returns `nil` after logging/recording.

## What’s Next (v0.2)
- Event system improvements
- WAL persistence
- Performance tuning
