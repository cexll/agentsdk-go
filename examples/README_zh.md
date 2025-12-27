中文 | [English](README.md)

# agentsdk-go 示例

八个示例，均可在仓库根目录运行。

**环境配置**

1. 复制 `.env.example` 为 `.env` 并设置 API 密钥：
```bash
cp .env.example .env
# 编辑 .env 文件，设置 ANTHROPIC_API_KEY=sk-ant-your-key-here
```

2. 加载环境变量：
```bash
source .env
```

或者直接导出：
```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

**学习路径**
- `01-basic`（32 行）：单次 API 调用，最小用法，打印一次响应。
- `02-cli`（73 行）：交互式 REPL，会话历史，可选读取 `.claude/settings.json`。
- `03-http`（约 300 行）：REST + SSE 服务，监听 `:8080`，生产级组合。
- `04-advanced`（约 1400 行）：全功能集成，包含 middleware、hooks、MCP、sandbox、skills、subagents。
- `05-custom-tools`（约 150 行）：选择性内置工具和自定义工具注册。
- `05-multimodel`（约 120 行）：多模型支持，模型池和分层路由。
- `06-v0.4.0-features`（约 280 行）：v0.4.0 新特性导览（离线可运行）。
- `08-askuserquestion`（约 150 行）：AskUserQuestion 工具集成，多种演示场景。

## 01-basic — 最小入门
- 目标：最快看到 SDK 核心循环，一次请求一次响应。
- 运行：
```bash
source .env
go run ./examples/01-basic
```

## 02-cli — 交互式 REPL
- 关键特性：交互输入、按会话保留历史、可选 `.claude/settings.json` 配置。
- 运行：
```bash
source .env
go run ./examples/02-cli --session-id demo --settings-path .claude/settings.json
```

## 03-http — REST + SSE
- 关键特性：`/health`、`/v1/run`（阻塞）、`/v1/run/stream`（SSE，15s 心跳）；默认端口 `:8080`。完全线程安全的 Runtime 自动处理并发请求。
- 运行：
```bash
source .env
go run ./examples/03-http
```

## 04-advanced — 全功能集成
- 关键特性：完整链路，涵盖 middleware 链、hooks、MCP 客户端、sandbox 控制、skills、subagents、流式输出。
- 运行：
```bash
source .env
go run ./examples/04-advanced --prompt "安全巡检" --enable-mcp=false
```

## 05-custom-tools — 自定义工具注册
- 关键特性：选择性内置工具（`EnabledBuiltinTools`）、自定义工具实现（`CustomTools`）、演示工具过滤与注册。
- 运行：
```bash
source .env
go run ./examples/05-custom-tools
```
- 详细用法和自定义工具实现指南见 [05-custom-tools/README.md](05-custom-tools/README.md)。

## 05-multimodel — 多模型支持
- 关键特性：模型池配置、分层模型路由（low/mid/high）、子代理-模型映射、成本优化。
- 运行：
```bash
source .env
go run ./examples/05-multimodel
```
- 配置示例和最佳实践见 [05-multimodel/README.md](05-multimodel/README.md)。

## 06-v0.4.0-features — v0.4.0 新特性导览（离线可运行）
- 关键特性：rules loader、token 统计、auto-compact、async bash、禁用工具、OTEL（需 build tag）。
- 运行：
```bash
go run ./examples/06-v0.4.0-features
```
- 详见 [06-v0.4.0-features/README.md](06-v0.4.0-features/README.md)（含在线模式说明）。

## 08-askuserquestion — AskUserQuestion 工具
- 关键特性：通过 build tag 选择三种演示模式。
- 运行：
```bash
source .env
(cd examples/08-askuserquestion && go run .)                  # 完整 agent 场景
(cd examples/08-askuserquestion && go run -tags demo_llm .)   # LLM 集成测试
(cd examples/08-askuserquestion && go run -tags demo_simple .) # 纯工具测试（无需 API key）
```
- 详细用法和实现模式见 [08-askuserquestion/README.md](08-askuserquestion/README.md)。
