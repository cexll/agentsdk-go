中文 | [English](README.md)

# agentsdk-go 示例

十二个示例，均可在仓库根目录运行。

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
- `01-basic`（~36 行）：单次 API 调用，最小用法，打印一次响应。
- `02-cli`（~93 行）：交互式 REPL，会话历史，可选读取 `.claude/settings.json`。
- `03-http`（~300 行）：REST + SSE 服务，监听 `:8080`，生产级组合。
- `04-advanced`（~1400 行）：全功能集成，包含 middleware、hooks、MCP、sandbox、skills、subagents。
- `05-custom-tools`（~58 行）：选择性内置工具和自定义工具注册。
- `06-embed`（~181 行）：通过 `go:embed` 嵌入 `.claude` 目录到二进制文件。
- `07-multimodel`（~130 行）：多模型池，分层路由和成本优化。
- `08-askuserquestion`（~474 行）：AskUserQuestion 工具集成，多种演示场景。
- `09-task-system`（~56 行）：任务跟踪与依赖管理。
- `10-hooks`（~85 行）：Hooks 系统，PreToolUse/PostToolUse shell 钩子。
- `11-reasoning`（~186 行）：推理模型支持（DeepSeek-R1 reasoning_content 透传）。
- `12-multimodal`（~135 行）：多模态内容块（文本 + 图片）。

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

## 06-embed — 嵌入式文件系统
- 关键特性：`EmbedFS` 将 `.claude` 目录嵌入二进制文件，嵌入配置与磁盘配置的优先级解析。
- 运行：
```bash
source .env
go run ./examples/06-embed
```

## 07-multimodel — 多模型支持
- 关键特性：模型池配置、分层模型路由（low/mid/high）、子代理-模型映射、成本优化。
- 运行：
```bash
source .env
go run ./examples/07-multimodel
```
- 配置示例和最佳实践见 [07-multimodel/README.md](07-multimodel/README.md)。

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

## 09-task-system — 任务跟踪
- 关键特性：任务创建、依赖管理、通过内置任务工具进行状态跟踪。
- 运行：
```bash
source .env
go run ./examples/09-task-system
```

## 10-hooks — Hooks 系统
- 关键特性：`PreToolUse`/`PostToolUse` shell 钩子、异步执行、单次去重。
- 运行：
```bash
source .env
go run ./examples/10-hooks
```

## 11-reasoning — 推理模型
- 关键特性：思维模型的 `reasoning_content` 透传（DeepSeek-R1）、流式支持、多轮对话。
- 运行：
```bash
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://api.deepseek.com/v1
go run ./examples/11-reasoning
```

## 12-multimodal — 多模态内容
- 关键特性：文本 + 图片内容块（base64 和 URL）、`api.Request` 中的 `ContentBlocks`。
- 运行：
```bash
source .env
go run ./examples/12-multimodal
```
