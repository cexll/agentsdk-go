# agentsdk-go 架构设计文档

> 早期调研笔记（含历史内容）
>
> 设计原则：KISS | YAGNI | Never Break Userspace | 大道至简

**文档状态**: 本文档包含早期调研内容；实现现状以代码与测试为准。

**实现范围（概览）**:
- Agent 核心循环 + Tool 执行
- Middleware（6 点拦截）
- Hooks（Shell）
- MCP（stdio/SSE/Streamable）
- Sandbox（FS/Network/Resource）
- Runtime 扩展：Skills / Commands / Subagents / Tasks
- 多模态支持 (text/image/document)
- 多模型分层 (ModelPool + SubagentModelMapping)
- 自动上下文压缩 (AutoCompact)
- OpenTelemetry 追踪 (可选 build tag)

---

## 目录

1. [项目调研总览](#一项目调研总览)
2. [横向对比分析](#二横向对比分析)
3. [核心架构设计](#三核心架构设计)
4. [技术选型](#四技术选型)
5. [API 设计](#五api-设计)
6. [实现路线图](#六实现路线图)

---

## 一、项目调研总览

### 1.1 调研范围

**总计 17 个项目，覆盖 3 种语言生态：**

#### TypeScript/JavaScript (6个)
1. **Kode-agent-sdk** - 企业级 Agent 框架
2. **Kode-cli** - CLI 包装器 + 热重载
3. **codex** - Rust 核心 + 多前端
4. **mastra** - DI 架构 + 工作流引擎
5. **micro-agent** - TDD 驱动 + 视觉测试
6. **opencode** - Bun + Hono + 多客户端

#### Python (8个)
1. **Mini-Agent** - MiniMax-M2 示教实现
2. **adk-python** - Google ADK (2600+ 单测)
3. **claude-agent-sdk-python** - Claude CLI 包装器
4. **kimi-cli** - Typer CLI + 时间回溯
5. **langchain** - Runnable 抽象 + LangGraph
6. **openai-agents-python** - 官方 SDK + Realtime
7. **agno** - 全家桶 (Agent/Team/Workflow/OS)
8. **deepagents** - LangGraph Middleware + HITL

#### Go (3个)
1. **anthropic-sdk-go** - 官方 Go SDK
2. **mini-claude-code-go** - 极简 800 行 REPL
3. **agentsdk** - 洋葱中间件 + 三层记忆 + CompositeBackend

---

## 二、横向对比分析

### 2.1 架构模式最佳实践

| 维度 | 最佳项目 | 核心亮点 | 可复用性 |
|------|---------|---------|---------|
| **事件架构** | Kode-agent-sdk | 三通道解耦 (Progress/Control/Monitor)<br>EventBus + bookmark 断点续播 | ⭐⭐⭐⭐⭐ |
| **持久化** | Kode-agent-sdk | WAL + 自动封口 + Event buffer<br>Resume/Fork 支持 | ⭐⭐⭐⭐⭐ |
| **工具治理** | Kode-agent-sdk<br>openai-agents | 权限模式 + 审批回调 + Hook<br>AJV 校验 + 限流 | ⭐⭐⭐⭐⭐ |
| **多代理** | mastra<br>agno | Team/handoff + shared session<br>递归 runnable | ⭐⭐⭐⭐ |
| **工作流** | mastra<br>agno<br>langchain | StateGraph + loop/parallel<br>time-travel 支持 | ⭐⭐⭐⭐ |
| **类型安全** | anthropic-sdk-go<br>openai-agents | 严格类型 + mypy strict<br>Zod/Pydantic schema | ⭐⭐⭐⭐⭐ |
| **测试** | adk-python<br>deepagents | 2600+ 单测 + mock fixture<br>标准测试基类 | ⭐⭐⭐⭐⭐ |
| **极简** | micro-agent<br>mini-claude-code-go | 单文件 <1000 行<br>零依赖 | ⭐⭐⭐⭐ |
| **安全** | deepagents<br>kimi-cli | 路径沙箱 + 符号链接解析<br>命令校验 + O_NOFOLLOW | ⭐⭐⭐⭐⭐ |
| **MCP** | Kode-cli<br>Mini-Agent | stdio/SSE 双协议<br>动态加载 | ⭐⭐⭐⭐ |
| **Backend 抽象** | agentsdk | CompositeBackend 路径路由<br>混搭内存/JSONStore/文件系统 | ⭐⭐⭐⭐⭐ |
| **三层记忆** | agentsdk | 文本记忆 + Working Memory(作用域/TTL/Schema)<br>语义记忆(向量+溯源+置信度) | ⭐⭐⭐⭐⭐ |
| **参数校验** | agentsdk | Schema 校验 + 类型检查<br>工具参数自动验证 | ⭐⭐⭐⭐ |
| **本地评估** | agentsdk | Evals 无需 LLM<br>关键词匹配 + 相似度打分 | ⭐⭐⭐⭐ |

### 2.2 共性优点（精华提取）

#### 🎯 架构设计
- **配置分层与 DI**: mastra/agno/openai-agents 通过依赖注入实现松耦合
- **Middleware Pipeline**: deepagents 的可插拔中间件 (TodoList/Summarization/SubAgent)
- **六段 Middleware**: agentsdk-go 将 before/after agent/model/tool 共 6 个拦截点串入 Chain，较 Claude Code 的单一 Hook 具有更强的治理粒度
- **三通道事件**: Kode-agent-sdk 的 Progress/Control/Monitor 解耦设计

#### 🎯 上下文管理
- **Checkpoint/Resume**: Kode-agent-sdk 的 WAL + Fork 机制
- **时间回溯**: kimi-cli 的 DenwaRenji (D-Mail) 机制
- **自动摘要**: kimi-cli/adk-python 的上下文压缩

#### 🎯 安全与治理
- **路径沙箱**: deepagents 的 O_NOFOLLOW + 符号链接解析
- **审批队列**: Kode-agent-sdk/kimi-cli 的 HITL (Human-in-the-Loop)
- **命令校验**: 危险命令检测 + 参数注入防御

#### 🎯 可观测性
- **OTEL Tracing**: mastra/adk-python/agno 的完整链路追踪
- **敏感数据过滤**: mastra 的自动脱敏
- **Metrics/Usage**: openai-agents 的 token 统计

#### 🎯 扩展性
- **Hook 系统**: 统一的生命周期钩子
- **MCP 集成**: Kode-cli/Mini-Agent 的 Model Context Protocol

### 2.3 共性缺陷（需规避）

| 缺陷类别 | 典型案例 | 影响 | 修复方向 |
|---------|---------|-----|---------|
| **巨型单文件** | `message.go` 5000+ 行 (anthropic-sdk-go)<br>`Agent.ts` 1800 行 (Kode-agent-sdk)<br>`server.ts` 1800 行 (opencode) | 可维护性极差<br>合并冲突频繁 | 强制 <500 行/文件<br>按职责拆分模块 |
| **测试不足** | micro-agent visual 覆写结果<br>Mini-Agent 未注册 RecallNoteTool<br>mini-claude-code-go 零测试 | 回归风险高<br>重构困难 | 单测覆盖 >90%<br>CI 强制检查 |
| **安全漏洞** | agno `eval()` 注入<br>deepagents 未转义 sandbox 命令<br>mini-claude-code-go 未解析符号链接 | 代码注入风险<br>路径穿越攻击 | 三层防御：<br>路径+命令+输出 |
| **依赖膨胀** | adk-python 十余个 google-cloud-*<br>mastra Agent 承担 10+ 职责 | 启动慢<br>镜像大 | 零依赖核心<br>按需扩展 |
| **状态一致性** | Kode-agent-sdk 模板累计污染<br>opencode 分享队列 silent drop<br>kimi-cli 审批未持久化 | 状态丢失<br>难以调试 | WAL + 事务语义<br>错误重试 |
| **Streaming bug** | mini-claude-code-go 流模式失效<br>anthropic-sdk-go SSE 大小写问题 | 功能不可用<br>线上故障 | 集成测试覆盖<br>Mock 验证 |

### 2.4 懒加载性能优化

#### 2.4.1 懒加载策略（Skills / Commands）
- **Skills**: 注册阶段只记录路径与 handler stub，不读取 SKILL.md；首个 `Execute` 前通过 `sync.Once` 读取文件并解析 frontmatter+body。
- **Commands**: 启动仅做元数据探测（1 次 meta read），命令体和 stat 在首次 `Handle` 时才触发；读取与解析同样由 `sync.Once` 包裹。

#### 2.4.2 性能说明（不固化指标）
- 懒加载的目标是减少启动阶段的文件读取，把正文读取推迟到首次执行。
- 具体耗时/分配随机器、仓库规模、系统缓存变化；需要量化时请运行 `test/benchmarks` 下的基准测试并以结果为准。

#### 2.4.4 实现要点
- `sync.Once` 包裹正文与 frontmatter 解析，确保并发下只读一次。
- frontmatter 解析与正文读取解耦：启动仅需要的 meta（命令），正文延迟到首次执行。
- body 延迟加载后立即复用已解析结构，避免重复磁盘 IO 与重复分配。

### 2.5 Middleware 系统设计（agentsdk-go 独有）

#### 2.5.1 设计动机（为何需要 6 个拦截点）
- **全链路治理**: 在 Agent→Model→Tool→回传的每个阶段暴露可插拔治理面，避免单点 Hook 无法覆盖工具调用与结果回填。
- **短路保护**: 任一环节发现违规（如越权工具、超时响应）立即中断，减少无效推理成本。
- **与 Claude Code 的关系**: Claude Code 以 hooks 为主要扩展点；本项目额外提供可选的 in-process middleware，用于更细粒度的治理/可观测。

#### 2.5.2 拦截点详解
- `before_agent` (`StageBeforeAgent`): 会话入口前做租户/速率/审计初始化。
- `before_model` (`StageBeforeModel`): Prompt 组装前做上下文裁剪、敏感字段遮蔽。
- `after_model` (`StageAfterModel`): 模型输出后做安全过滤、拒绝理由重写。
- `before_tool` (`StageBeforeTool`): 工具调用前校验白名单、参数 Schema、冷却时间。
- `after_tool` (`StageAfterTool`): 结果回填前做降噪、结构化封装、观测指标打点。
- `after_agent` (`StageAfterAgent`): 对最终回复做格式化、用量上报、持久化。

#### 2.5.3 Chain 执行器（串行 + 短路 + 超时）
- **串行执行**: `Chain.Execute` 逐个中间件调用，保持确定性顺序。
- **短路语义**: 首个返回 error 的中间件立即中断后续执行并让 Agent 失败收敛。
- **超时保护**: `WithTimeout` 为每个阶段包裹 `context.WithTimeout`，避免慢中间件拖垮会话。

```go
// pkg/middleware/chain.go
chain := middleware.NewChain(
    []middleware.Middleware{audit, limiter, tracer},
    middleware.WithTimeout(200*time.Millisecond),
)
if err := chain.Execute(ctx, middleware.StageBeforeAgent, state); err != nil {
    return err // 短路
}
```

```go
// pkg/agent/agent.go (节选)
state := &middleware.State{Agent: c, Values: map[string]any{}}
_ = a.mw.Execute(ctx, middleware.StageBeforeAgent, state)
_ = a.mw.Execute(ctx, middleware.StageBeforeModel, state)
out, _ := a.model.Generate(ctx, c) // agent.Model 接口
state.ModelOutput = out
_ = a.mw.Execute(ctx, middleware.StageAfterModel, state)
// 工具调用前后同理 (StageBeforeTool / StageAfterTool)
// 循环结束时 StageAfterAgent
```

#### 2.5.4 使用场景
- **日志/审计**: 统一入口收集 request/工具调用/最终回复三段日志。
- **限流/配额**: `before_agent` + `before_model` 组合做租户限流和 prompt token 预算。
- **安全检查**: `before_tool` 过滤危险命令，`after_tool` 做结果脱敏与防注入。
- **监控/告警**: `after_agent` 上报耗时、QPS、error rate，支持熔断/报警。

#### 2.5.5 实现细节（集成点）
- **Middleware 接口**: `middleware.Middleware` 是一个接口，定义 `Name()` + 6 个 Hook 方法 (`BeforeAgent`/`BeforeModel`/`AfterModel`/`BeforeTool`/`AfterTool`/`AfterAgent`)。
- **Funcs 辅助**: `middleware.Funcs` 结构体将函数指针转为 `Middleware` 接口实现，未指定的 Hook 默认 no-op。
- **状态传递**: `middleware.State` 贯穿 6 段，记录 `Agent`、`ModelInput`、`ModelOutput`、`ToolCall`、`ToolResult` 与 `Values` 扩展字段（均为 `any` 类型，由调用方类型断言）。
- **线程安全**: `Chain.Use` 内置写锁，运行时追加中间件不会破坏正在执行的链。
- **零依赖 & 可预测**: 不引入反射/泛型，保持核心简洁；相比 Claude Code 的多 Hook 抽象，agentsdk-go 更符合 KISS。

### 2.6 技术选型对比

| 语言 | 优势 | 劣势 | 适用场景 |
|-----|------|-----|---------|
| **TypeScript** | - 类型安全<br>- 生态丰富<br>- 前后端统一 | - 运行时性能<br>- 内存占用<br>- 冷启动慢 | Web/Desktop 应用<br>全栈开发 |
| **Python** | - 开发效率<br>- AI 生态<br>- 丰富库支持 | - 并发性能<br>- 类型安全弱<br>- 打包部署复杂 | 数据科学<br>原型开发<br>研究项目 |
| **Go** | - 性能优秀<br>- 并发原生<br>- 部署简单<br>- 零依赖 | - 泛型支持晚<br>- 生态较小 | CLI 工具<br>后端服务<br>云原生应用 |

**✅ 选择 Go 的理由**:
1. **性能**: 编译型语言，启动快，内存小
2. **并发**: goroutine 原生支持，适合 Agent 多工具并发
3. **部署**: 单二进制文件，无运行时依赖
4. **类型安全**: 编译期检查，减少运行时错误
5. **生态**: 云原生基础设施的标准语言

---

## 三、核心架构设计

### 3.1 设计原则

#### Linus 风格
- **KISS (Keep It Simple, Stupid)**: 单一职责，核心文件 <500 行
- **YAGNI (You Aren't Gonna Need It)**: 零依赖起步，按需扩展
- **Never Break Userspace**: API 稳定，向后兼容
- **大道至简**: 接口极简，实现精炼

#### Go 惯用法
- 接口优于实现
- 组合优于继承
- channel 传递数据
- context 控制生命周期

### 3.2 整体架构 (当前实现)

```
┌─────────────────────────────────────────────────────────────────┐
│                         agentsdk-go                              │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/api - 统一入口层 (Runtime)                              │ │
│  │  ├─ Runtime.Run(ctx, Request) -> Response                  │ │
│  │  ├─ Runtime.RunStream(ctx, Request) -> <-chan StreamEvent  │ │
│  │  ├─ Token 统计 & 自动 Compact                               │ │
│  │  ├─ OpenTelemetry 追踪 & UUID 标识                          │ │
│  │  ├─ Hooks 桥接 & 权限审批                                   │ │
│  │  └─ 会话历史持久化                                          │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/agent - Agent 核心循环 (190 行)                         │ │
│  │  ├─ agent.Model.Generate() → Tool Calls → Execute → Loop  │ │
│  │  ├─ MaxIterations 限制 & Timeout 保护                       │ │
│  │  └─ Context 状态管理 (Values/ToolResults/Iteration)         │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/middleware - 6 点拦截链                                 │ │
│  │  ├─ Middleware 接口 (Name + 6 个 Hook 方法)                 │ │
│  │  ├─ Funcs 辅助结构 (函数指针 → Middleware)                   │ │
│  │  ├─ Chain 串行执行器 (短路 + 超时)                           │ │
│  │  └─ State 跨中间件共享状态                                   │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/model - 模型适配层                                      │ │
│  │  ├─ Model 接口 (Complete / CompleteStream)                 │ │
│  │  ├─ Provider 接口 (Model 工厂 + 缓存)                       │ │
│  │  ├─ AnthropicProvider (Claude 系列)                        │ │
│  │  ├─ OpenAIProvider (OpenAI / Azure / 兼容层)               │ │
│  │  ├─ 多模态支持 (ContentBlock: text/image/document)          │ │
│  │  └─ reasoning_content 透传 (thinking models)               │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/tool - 工具系统                                         │ │
│  │  ├─ Registry (工具注册表 + MCP 会话管理)                     │ │
│  │  ├─ Executor (沙箱执行 + 权限解析 + 输出持久化)              │ │
│  │  ├─ builtin/ (内置工具)                                     │ │
│  │  │   ├─ bash (异步/流式)  ├─ grep/glob                     │ │
│  │  │   ├─ read/write/edit   ├─ webfetch/websearch            │ │
│  │  │   ├─ task/taskcreate   ├─ taskget/tasklist/taskupdate   │ │
│  │  │   ├─ skill/slashcmd    ├─ killtask/bashstatus           │ │
│  │  │   ├─ askuserquestion   └─ todo_write                    │ │
│  │  └─ MCP 集成 (stdio/SSE/Streamable + 动态刷新)             │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  支撑模块                                                    │ │
│  │  ├─ pkg/config      - 配置加载 & Rules & CLAUDE.md & FS 抽象│ │
│  │  ├─ pkg/message     - 消息历史 & LRU 会话缓存 & Token 裁剪  │ │
│  │  ├─ pkg/prompts     - 系统提示词组装 (skills/hooks/commands) │ │
│  │  ├─ pkg/core/hooks  - Shell Hook 执行器 & 生命周期管理       │ │
│  │  ├─ pkg/core/events - 事件总线 & 事件类型                    │ │
│  │  ├─ pkg/core/middleware - Hook 中间件链                      │ │
│  │  ├─ pkg/sandbox     - 文件系统 & 网络隔离                    │ │
│  │  ├─ pkg/security    - 命令校验 & 路径解析 & 权限审批队列     │ │
│  │  ├─ pkg/mcp         - MCP 客户端 (stdio/SSE/Streamable)    │ │
│  │  └─ pkg/gitignore   - .gitignore 匹配器                     │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/runtime - 运行时扩展                                    │ │
│  │  ├─ skills/     - Skills 管理 (懒加载 + Matcher)            │ │
│  │  ├─ subagents/  - Subagent 编排 & 模型分层                  │ │
│  │  ├─ commands/   - Slash Commands 解析 & 执行                │ │
│  │  └─ tasks/      - Task 跟踪 & 依赖管理                      │ │
│  └────────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 3.3 目录结构 (当前实际)

```
agentsdk-go/
├── pkg/                          # 核心包
│   ├── api/                      # 统一 API 入口
│   │   ├── agent.go              # Runtime 实现
│   │   ├── options.go            # Options & Request & Response
│   │   ├── stream.go             # StreamEvent 类型 (Anthropic 兼容 SSE)
│   │   ├── compact.go            # 自动上下文压缩
│   │   ├── compact_prompt.go     # 压缩提示词
│   │   ├── stats.go              # Token 统计
│   │   ├── progress.go           # 进度事件
│   │   ├── rollout.go            # 功能灰度
│   │   ├── otel.go / otel_*.go   # OpenTelemetry 集成 (build tag)
│   │   ├── helpers.go            # 工具函数
│   │   ├── request_helpers.go    # 请求辅助
│   │   ├── runtime_helpers.go    # 运行时辅助 (含平台特定)
│   │   ├── history_persistence.go # 会话历史持久化
│   │   ├── hooks_bridge.go       # Hooks 桥接
│   │   ├── mcp_bridge.go         # MCP 桥接
│   │   ├── sandbox_bridge.go     # Sandbox 桥接
│   │   ├── settings_bridge.go    # Settings 桥接
│   │   └── claude_embed_hooks.go # 嵌入式 Hooks 物化
│   │
│   ├── agent/                    # Agent 核心循环
│   │   ├── agent.go              # 核心循环 (190行)
│   │   ├── context.go            # RunContext (Iteration/Values/ToolResults)
│   │   └── options.go            # Agent 配置 (MaxIterations/Timeout/Middleware)
│   │
│   ├── middleware/               # 6 点拦截中间件
│   │   ├── chain.go              # 中间件链执行器 (串行+短路+超时)
│   │   └── types.go              # Stage & State & Middleware 接口 & Funcs 辅助
│   │
│   ├── model/                    # 模型抽象层
│   │   ├── interface.go          # Model 接口 (Complete/CompleteStream)
│   │   │                         # Message, ContentBlock, ToolCall, Request, Response
│   │   ├── anthropic.go          # Anthropic 适配器
│   │   ├── openai.go             # OpenAI 适配器
│   │   ├── openai_responses.go   # OpenAI Responses API
│   │   ├── provider.go           # Provider 接口 & AnthropicProvider & OpenAIProvider
│   │   ├── stream_wrapper.go     # 流式包装器
│   │   └── middleware_state.go   # 中间件状态上下文键
│   │
│   ├── tool/                     # 工具系统
│   │   ├── tool.go               # Tool 接口
│   │   ├── registry.go           # 工具注册表 + MCP 会话管理
│   │   ├── executor.go           # 工具执行器 (沙箱+权限+持久化)
│   │   ├── schema.go             # JSON Schema
│   │   └── builtin/              # 内置工具
│   │       ├── bash.go           # Bash (支持异步/流式)
│   │       ├── bash_stream.go    # Bash 流式输出
│   │       ├── bash_unix.go      # Unix 平台特定
│   │       ├── bash_windows.go   # Windows 平台特定
│   │       ├── bashoutput.go     # Bash 输出处理
│   │       ├── bashstatus.go     # Bash 状态查询
│   │       ├── async_manager.go  # 异步任务管理
│   │       ├── read.go           # 文件读取
│   │       ├── write.go          # 文件写入
│   │       ├── edit.go           # 文件编辑
│   │       ├── grep.go           # 内容搜索 (含上下文/分页)
│   │       ├── glob.go           # 文件匹配
│   │       ├── task.go           # Subagent 任务
│   │       ├── taskcreate.go     # 任务创建
│   │       ├── taskget.go        # 任务查询
│   │       ├── tasklist.go       # 任务列表
│   │       ├── taskupdate.go     # 任务更新
│   │       ├── killtask.go       # 终止任务
│   │       ├── skill.go          # Skills 执行
│   │       ├── slashcommand.go   # Slash 命令执行
│   │       ├── askuserquestion.go # 用户交互
│   │       ├── webfetch.go       # Web 内容获取
│   │       ├── websearch.go      # Web 搜索
│   │       ├── todo_write.go     # TodoWrite 工具
│   │       └── file_sandbox.go   # 文件沙箱辅助
│   │
│   ├── message/                  # 消息历史
│   │   ├── history.go            # History 管理
│   │   ├── converter.go          # Message 类型转换
│   │   └── trimmer.go            # Token 裁剪
│   │
│   ├── config/                   # 配置管理
│   │   ├── settings_loader.go    # 配置加载
│   │   ├── settings_types.go     # 配置类型定义
│   │   ├── settings_merge.go     # 配置合并
│   │   ├── hooks_unmarshal.go    # Hooks 反序列化
│   │   ├── rules.go              # .claude/rules/ 加载
│   │   ├── claude_md.go          # CLAUDE.md 加载
│   │   ├── fs.go                 # 文件系统抽象 (OS + EmbedFS)
│   │   └── validator.go          # 配置校验
│   │
│   ├── prompts/                  # 系统提示词
│   │   ├── prompts.go            # 提示词组装
│   │   ├── skills.go             # Skills 提示词
│   │   ├── hooks.go              # Hooks 提示词
│   │   ├── commands.go           # Commands 提示词
│   │   └── subagents.go          # Subagents 提示词
│   │
│   ├── core/                     # 核心扩展
│   │   ├── events/               # 事件总线
│   │   │   ├── bus.go            # EventBus
│   │   │   └── types.go          # Event 类型 & Payload 定义
│   │   ├── hooks/                # Hooks 系统
│   │   │   ├── executor.go       # Shell Hook 执行
│   │   │   └── lifecycle.go      # 生命周期管理
│   │   └── middleware/           # Hook 中间件链
│   │       └── chain.go          # 中间件链
│   │
│   ├── runtime/                  # 运行时扩展
│   │   ├── skills/               # Skills 管理
│   │   │   ├── registry.go       # Skills 注册表
│   │   │   ├── loader.go         # 懒加载器
│   │   │   └── matcher.go        # 激活匹配器
│   │   ├── subagents/            # Subagent 管理
│   │   │   ├── manager.go        # Subagent 管理器
│   │   │   ├── loader.go         # 定义加载
│   │   │   └── context.go        # Subagent 上下文
│   │   ├── commands/             # Slash Commands
│   │   │   ├── executor.go       # 命令执行器
│   │   │   ├── loader.go         # 命令加载
│   │   │   └── parser.go         # 命令解析
│   │   └── tasks/                # Task 系统
│   │       ├── task.go           # Task 定义
│   │       ├── store.go          # Task 存储
│   │       └── dependency.go     # 依赖管理
│   │
│   ├── mcp/                      # MCP 客户端
│   │   └── mcp.go                # stdio/SSE/Streamable 支持
│   │
│   ├── sandbox/                  # 沙箱隔离
│   │   ├── interface.go          # Manager 接口
│   │   ├── fs_policy.go          # 文件系统策略
│   │   └── net_policy.go         # 网络策略
│   │
│   ├── security/                 # 安全模块
│   │   ├── validator.go          # 命令校验
│   │   ├── resolver.go           # 路径解析
│   │   ├── resolver_unix.go      # Unix 路径解析
│   │   ├── resolver_windows.go   # Windows 路径解析
│   │   ├── permission_matcher.go # 权限匹配 (allow/deny/ask)
│   │   ├── sandbox.go            # 沙箱安全策略
│   │   └── approval.go           # 审批队列 & 会话白名单
│   │
│   └── gitignore/                # Gitignore 支持
│       └── matcher.go            # .gitignore 模式匹配
│
├── cmd/cli/                      # CLI 入口
│   └── main.go
│
├── examples/                     # 示例代码
│   ├── 01-basic/                 # 基础用法
│   ├── 02-cli/                   # CLI REPL
│   ├── 03-http/                  # HTTP 服务
│   ├── 04-advanced/              # 完整功能
│   ├── 05-custom-tools/          # 自定义工具
│   ├── 06-embed/                 # 嵌入式 FS
│   ├── 07-multimodel/            # 多模型
│   ├── 08-askuserquestion/       # 用户交互
│   ├── 09-task-system/           # Task 系统
│   ├── 10-hooks/                 # Hooks 示例
│   ├── 11-reasoning/             # 推理模型
│   └── 12-multimodal/            # 多模态
│
├── test/                         # 测试
│   ├── integration/              # 集成测试
│   ├── benchmarks/               # 性能测试
│   └── runtime/                  # 运行时测试
│
└── docs/                         # 文档
    ├── architecture.md           # 本文档
    ├── api-reference.md          # API 参考
    ├── getting-started.md        # 快速开始
    ├── security.md               # 安全指南
    ├── trace-system.md           # 追踪系统
    └── adr/                      # 架构决策记录
```

### 3.4 核心接口设计

#### 3.4.1 Agent 核心循环

Agent 核心循环位于 `pkg/agent/agent.go`，采用结构体而非接口设计：

```go
// pkg/agent/agent.go
package agent

// Model 是 agent 层的模型接口，由 api 层适配 model.Model
type Model interface {
    Generate(ctx context.Context, c *Context) (*ModelOutput, error)
}

// ToolExecutor 执行模型发出的工具调用
type ToolExecutor interface {
    Execute(ctx context.Context, call ToolCall, c *Context) (ToolResult, error)
}

// Agent 驱动核心循环，串联 middleware、model、tools
type Agent struct {
    model Model
    tools ToolExecutor
    opts  Options
    mw    *middleware.Chain
}

// New 构造 Agent
func New(model Model, tools ToolExecutor, opts Options) (*Agent, error)

// Run 执行 agent 循环，直到模型返回最终输出、context 取消或出错
func (a *Agent) Run(ctx context.Context, c *Context) (*ModelOutput, error)
```

**注意**: `agent.Model` 接口使用 `Generate` 方法，这是 agent 层的内部抽象。
外部 `model.Model` 接口使用 `Complete/CompleteStream`，由 `pkg/api` 层负责适配。

```go
// pkg/agent/context.go
type Context struct {
    Iteration       int
    StartedAt       time.Time
    Values          map[string]any
    ToolResults     []ToolResult
    LastModelOutput *ModelOutput
}

// pkg/agent/options.go
type Options struct {
    MaxIterations int
    Timeout       time.Duration
    Middleware    *middleware.Chain
}
```

#### 3.4.2 事件系统

```go
// pkg/core/events/types.go
package events

type EventType string

const (
    PreToolUse         EventType = "PreToolUse"
    PostToolUse        EventType = "PostToolUse"
    PostToolUseFailure EventType = "PostToolUseFailure"
    PreCompact         EventType = "PreCompact"
    ContextCompacted   EventType = "ContextCompacted"
    UserPromptSubmit   EventType = "UserPromptSubmit"
    SessionStart       EventType = "SessionStart"
    SessionEnd         EventType = "SessionEnd"
    Stop               EventType = "Stop"
    SubagentStart      EventType = "SubagentStart"
    SubagentStop       EventType = "SubagentStop"
    Notification       EventType = "Notification"
    TokenUsage         EventType = "TokenUsage"
    PermissionRequest  EventType = "PermissionRequest"
    ModelSelected      EventType = "ModelSelected"
    MCPToolsChanged    EventType = "MCPToolsChanged"
)

// Event 轻量级事件结构
type Event struct {
    ID        string
    Type      EventType
    Timestamp time.Time
    SessionID string
    RequestID string
    Payload   interface{} // 类型断言获取具体 Payload
}
```

```go
// pkg/core/events/bus.go
// Bus 基于 Pub/Sub 模式，单 dispatch loop 保序，per-subscriber 队列隔离
type Bus struct { ... }

func NewBus(opts ...BusOption) *Bus
func (b *Bus) Publish(evt Event) error
func (b *Bus) Subscribe(t EventType, handler Handler, opts ...SubscriptionOption) func()
func (b *Bus) Close()
```

**设计要点**:
- 单 dispatch loop 保证事件顺序
- per-subscriber 缓冲队列防止慢消费者阻塞
- LRU 去重窗口 (可选)
- panic 隔离：subscriber panic 不影响其他订阅者
- 支持 per-event 超时 (`WithSubscriptionTimeout`)

#### 3.4.3 工具系统

```go
// pkg/tool/tool.go
package tool

// Tool 工具接口
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)
}

// ToolResult 工具执行结果
type ToolResult struct {
    Success bool
    Output  string
    Data    interface{}
    Error   error
}
```

```go
// pkg/tool/registry.go
// Registry 线程安全的工具注册表，支持 MCP 会话管理
type Registry struct {
    tools       map[string]Tool
    mcpSessions []*mcpSessionInfo
    validator   Validator
}

func NewRegistry() *Registry
func (r *Registry) Register(tool Tool) error
func (r *Registry) Get(name string) (Tool, error)
func (r *Registry) List() []Tool
func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (*ToolResult, error)
func (r *Registry) RegisterMCPServer(ctx context.Context, serverPath, serverName string) error
func (r *Registry) RegisterMCPServerWithOptions(ctx context.Context, serverPath, serverName string, opts MCPServerOptions) error
func (r *Registry) Close()
```

```go
// pkg/tool/executor.go
// Executor 串联 Registry + Sandbox + 权限解析 + 输出持久化
type Executor struct {
    registry  *Registry
    sandbox   *sandbox.Manager
    persister *OutputPersister
    permCheck PermissionResolver
}

func NewExecutor(registry *Registry, sb *sandbox.Manager) *Executor
func (e *Executor) Execute(ctx context.Context, call Call) (*CallResult, error)
func (e *Executor) ExecuteAll(ctx context.Context, calls []Call) []CallResult
func (e *Executor) WithSandbox(sb *sandbox.Manager) *Executor
func (e *Executor) WithPermissionResolver(resolver PermissionResolver) *Executor
func (e *Executor) WithOutputPersister(persister *OutputPersister) *Executor
```

#### 3.4.4 消息历史与会话管理

```go
// pkg/message/history.go
// History 管理 per-session 消息历史，支持 LRU 淘汰
type History struct { ... }

func NewHistory(maxSessions int) *History
func (h *History) Append(sessionID string, msg model.Message)
func (h *History) List(sessionID string) []model.Message
func (h *History) Clear(sessionID string)
```

```go
// pkg/message/converter.go
// 在 model.Message 与 agent 内部格式之间转换

// pkg/message/trimmer.go
// Token 裁剪：当消息历史超过 token 预算时自动截断
```

```go
// pkg/api/history_persistence.go
// 磁盘持久化：将会话历史写入 .claude/ 目录
// 支持 session 恢复和跨进程共享
```

**设计要点**:
- LRU 淘汰策略 (通过 `MaxSessions` 配置)
- per-session 隔离，线程安全
- Token 裁剪防止上下文溢出
- 可选磁盘持久化 (api 层)

#### 3.4.5 安全系统

安全系统分布在 `pkg/security/` 和 `pkg/sandbox/` 两个包中：

```go
// pkg/security/validator.go - 命令校验器
// 阻止危险命令: dd, mkfs, fdisk, shutdown, reboot 等
// 模式检测: rm -rf, rmdir 等危险操作
// 可配置: CLI 场景可允许 shell 元字符

// pkg/security/resolver.go - 路径解析器
// 符号链接解析，防止路径穿越
// 平台特定实现 (resolver_unix.go / resolver_windows.go)

// pkg/security/permission_matcher.go - 权限匹配
// 支持 allow/deny/ask 三种决策
// 基于 glob 模式匹配工具名和参数

// pkg/security/approval.go - 审批队列
// ApprovalQueue: 持久化权限决策
// 会话白名单 (TTL 控制)
// ApprovalRecord: 记录审批历史

// pkg/security/sandbox.go - 沙箱安全策略
// 整合路径校验 + 命令校验 + 权限匹配
```

```go
// pkg/sandbox/ - 沙箱隔离
// interface.go - Manager 接口 (CheckToolPermission/Enforce)
// fs_policy.go - 文件系统策略 (路径白名单)
// net_policy.go - 网络策略 (域名白名单)
```

**三层防御**:
1. **路径白名单** - `sandbox.Manager` 限制文件系统访问范围
2. **符号链接解析** - `security.Resolver` 防止路径穿越
3. **命令校验** - `security.Validator` 阻止危险命令
4. **权限审批** - `security.ApprovalQueue` 支持 HITL 审批流程

#### 3.4.6 统一 API 层

`pkg/api` 是面向用户的统一入口，封装了所有底层模块：

```go
// pkg/api/agent.go
type Runtime struct { ... }

func New(ctx context.Context, opts Options) (*Runtime, error)
func (r *Runtime) Run(ctx context.Context, req Request) (*Response, error)
func (r *Runtime) RunStream(ctx context.Context, req Request) (<-chan StreamEvent, error)
func (r *Runtime) Close() error
```

```go
// pkg/api/options.go
type Options struct {
    EntryPoint        EntryPoint        // cli / ci / platform
    ProjectRoot       string
    EmbedFS           fs.FS             // 可选嵌入文件系统
    Model             model.Model
    ModelFactory      ModelFactory
    ModelPool         map[ModelTier]model.Model  // 分层模型池
    SystemPrompt      string
    Middleware        []middleware.Middleware
    Tools             []tool.Tool
    EnabledBuiltinTools []string        // 内置工具白名单
    DisallowedTools   []string          // 工具黑名单
    CustomTools       []tool.Tool       // 自定义工具
    TypedHooks        []corehooks.ShellHook
    Skills            []SkillRegistration
    Commands          []CommandRegistration
    Subagents         []SubagentRegistration
    Sandbox           SandboxOptions
    AutoCompact       CompactConfig
    OTEL              OTELConfig
    // ... 更多选项
}

type Request struct {
    Prompt            string
    ContentBlocks     []model.ContentBlock  // 多模态内容
    SessionID         string
    RequestID         string
    Model             ModelTier             // 可选模型层级覆盖
    EnablePromptCache *bool
    ToolWhitelist     []string
    TargetSubagent    string
    // ... 更多字段
}
```

**Runtime 初始化流程**:
1. 解析配置 (settings.json + settings.local.json + CLAUDE.md)
2. 解析模型 (Model / ModelFactory / ModelPool)
3. 构建沙箱 (文件系统 + 网络策略)
4. 注册工具 (内置 + 自定义 + MCP)
5. 初始化 Hooks 执行器
6. 初始化 Skills / Commands / Subagents
7. 构建消息历史存储

---

## 四、技术选型

### 4.1 核心原则：零依赖

```go
// go.mod
module github.com/cexll/agentsdk-go

go 1.24

// 核心包尽量减少外部依赖
// 全部使用 Go 标准库:
// - context
// - encoding/json
// - net/http
// - os/exec
// - io
// - sync
```

### 4.2 可选扩展（按需引入）

```go
// 仅在需要时引入以下依赖:

require (
    // 并发控制
    golang.org/x/sync v0.x.x    // errgroup, singleflight

    // 终端交互 (仅 CLI 工具需要)
    golang.org/x/term v0.x.x

    // Shell 命令解析
    github.com/kballard/go-shellquote v0.0.0
)
```

### 4.3 测试依赖

```go
// go.mod (仅测试)
require (
    github.com/stretchr/testify v1.8.4
    github.com/golang/mock v1.6.0
)
```

---

## 五、API 设计

### 5.1 基础用法

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
    // 1. 创建模型 Provider
    provider := &model.AnthropicProvider{
        APIKey:    os.Getenv("ANTHROPIC_API_KEY"),
        ModelName: "claude-sonnet-4-5",
    }

    // 2. 创建 Runtime
    runtime, err := api.New(context.Background(), api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
        MaxIterations: 20,
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // 3. 运行
    result, err := runtime.Run(context.Background(), api.Request{
        Prompt:    "帮我重构 main.go 的 handleRequest 函数",
        SessionID: "session-123",
    })
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println("Output:", result.Result.Output)
}
```

### 5.2 流式输出

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/cexll/agentsdk-go/pkg/api"
)

func main() {
    runtime := createRuntime() // ... 同上
    defer runtime.Close()

    // 流式执行
    events, err := runtime.RunStream(context.Background(), api.Request{
        Prompt:    "实现用户登录功能",
        SessionID: "stream-demo",
    })
    if err != nil {
        log.Fatal(err)
    }

    // 监听 SSE 事件 (Anthropic 兼容格式)
    for evt := range events {
        switch evt.Type {
        case api.EventContentBlockDelta:
            if evt.Delta != nil {
                fmt.Print(evt.Delta.Text)
            }
        case api.EventToolExecutionStart:
            fmt.Printf("\n[工具] %s\n", evt.Name)
        case api.EventToolExecutionResult:
            fmt.Printf("[结果] %v\n", evt.Output)
        case api.EventError:
            fmt.Printf("[错误] %v\n", evt.Output)
        case api.EventMessageStop:
            fmt.Println("\n[完成]")
        }
    }
}
```

### 5.3 多模型分层

```go
package main

import (
    "context"
    "os"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    apiKey := os.Getenv("ANTHROPIC_API_KEY")

    runtime, _ := api.New(context.Background(), api.Options{
        ProjectRoot: ".",
        ModelFactory: &model.AnthropicProvider{
            APIKey:    apiKey,
            ModelName: "claude-sonnet-4-5",
        },
        // 分层模型池：不同任务使用不同成本的模型
        ModelPool: map[api.ModelTier]model.Model{
            api.ModelTierLow:  model.MustProvider(&model.AnthropicProvider{APIKey: apiKey, ModelName: "claude-3-5-haiku-20241022"}),
            api.ModelTierMid:  model.MustProvider(&model.AnthropicProvider{APIKey: apiKey, ModelName: "claude-sonnet-4-5"}),
            api.ModelTierHigh: model.MustProvider(&model.AnthropicProvider{APIKey: apiKey, ModelName: "claude-opus-4"}),
        },
        // Subagent 类型到模型层级的映射
        SubagentModelMapping: map[string]api.ModelTier{
            "explore": api.ModelTierLow,
            "plan":    api.ModelTierHigh,
        },
    })
    defer runtime.Close()

    // 请求时可覆盖模型层级
    runtime.Run(context.Background(), api.Request{
        Prompt: "分析代码质量",
        Model:  api.ModelTierHigh, // 使用 Opus
    })
}
```

### 5.4 自定义工具

```go
package main

import (
    "context"
    "fmt"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/tool"
)

// DatabaseTool 自定义数据库工具
type DatabaseTool struct {
    db *sql.DB
}

func (t *DatabaseTool) Name() string        { return "database_query" }
func (t *DatabaseTool) Description() string { return "执行 SQL 查询并返回结果" }
func (t *DatabaseTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "SQL 查询语句",
            },
        },
        Required: []string{"query"},
    }
}

func (t *DatabaseTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    query := params["query"].(string)
    rows, err := t.db.QueryContext(ctx, query)
    if err != nil {
        return &tool.ToolResult{Success: false, Error: err}, nil
    }
    defer rows.Close()
    // ... 解析 rows
    return &tool.ToolResult{Success: true, Output: "查询完成"}, nil
}

func main() {
    db, _ := sql.Open("postgres", "...")

    runtime, _ := api.New(context.Background(), api.Options{
        ProjectRoot: ".",
        ModelFactory: provider,
        CustomTools: []tool.Tool{&DatabaseTool{db: db}},
    })
    defer runtime.Close()

    runtime.Run(context.Background(), api.Request{
        Prompt: "查询最近 24 小时的订单数据",
    })
}
```

### 5.5 Hooks 与 Middleware 扩展

**Shell Hooks** (通过配置或 `api.Options.TypedHooks`):

```go
runtime, _ := api.New(ctx, api.Options{
    // Shell Hooks: 外部进程拦截
    TypedHooks: []corehooks.ShellHook{
        {
            Matcher: corehooks.HookMatcher{EventName: "PreToolUse", ToolName: "Bash"},
            Command: "python3 validate_bash.py",
            Timeout: 10 * time.Second,
        },
    },
    // ...
})
```

**In-process Middleware** (通过 `api.Options.Middleware`):

```go
runtime, _ := api.New(ctx, api.Options{
    Middleware: []middleware.Middleware{
        middleware.Funcs{
            Identifier: "audit-logger",
            OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
                log.Printf("[审计] 会话开始")
                return nil
            },
            OnBeforeTool: func(ctx context.Context, st *middleware.State) error {
                log.Printf("[审计] 工具调用: %v", st.ToolCall)
                return nil
            },
            OnAfterAgent: func(ctx context.Context, st *middleware.State) error {
                log.Printf("[审计] 会话结束")
                return nil
            },
        },
    },
    // ...
})
```

---

## 六、实现路线图

### 6.1 v0.1 - MVP (2 周)

**目标**: 可用的最小核心

#### Week 1
- [x] 项目搭建
  - [ ] 目录结构
  - [ ] go.mod 初始化
  - [ ] Makefile
  - [ ] CI/CD (GitHub Actions)

- [x] Agent 核心
  - [ ] Agent 接口定义
  - [ ] 基础实现 (Run 方法)
  - [ ] RunContext 管理

- [x] 模型适配
  - [ ] Model 接口
  - [ ] Anthropic 适配器
  - [ ] OpenAI 适配器
  - [ ] 消息转换

#### Week 2
- [x] 工具系统
  - [ ] Tool 接口
  - [ ] Registry 实现
  - [ ] Bash 工具 (带沙箱)
  - [ ] File 工具 (read/write)

- [x] 会话管理
  - [ ] Session 接口
  - [ ] 内存存储实现
  - [ ] 消息追加/列表

- [x] 测试
  - [ ] 单元测试（风险驱动；覆盖率不在文档固化阈值）
  - [ ] 集成测试
  - [ ] 示例代码

**交付物**:
- 可工作的 Agent 核心
- 2 个模型适配器
- 2 个基础工具
- 文档 + 示例

---

### 6.2 v0.2 - 增强 (4 周)

**目标**: 生产级特性

#### Week 3-4
- [x] 三通道事件系统
  - [ ] EventBus 实现
  - [ ] Progress/Control/Monitor 通道
  - [ ] Bookmark 断点续播

- [x] 流式执行
  - [ ] RunStream 实现
  - [ ] SSE 流式输出
  - [ ] 事件分发

#### Week 5-6
- [x] WAL + Checkpoint
  - [ ] WAL 实现
  - [ ] FileSession
  - [ ] Checkpoint/Resume/Fork

- [x] MCP 集成
  - [ ] MCP 客户端
  - [ ] stdio 传输
  - [ ] SSE 传输
  - [ ] 工具自动注册

- [x] CLI 工具
  - [ ] agentctl run
  - [ ] agentctl serve
  - [ ] agentctl config

**交付物**:
- 事件系统
- 持久化会话
- MCP 支持
- CLI 工具

---

### 6.3 v0.3 - 企业级 (8 周)

**目标**: 企业生产就绪

#### Week 7-10
- [x] 审批系统
  - [ ] Approval Queue
  - [ ] 会话级白名单
  - [ ] 持久化审批记录

- [x] 工作流引擎
  - [ ] StateGraph 实现
  - [ ] Middleware 接口
  - [ ] 内置中间件
    - [ ] TodoListMiddleware
    - [ ] SummarizationMiddleware
    - [ ] SubAgentMiddleware
    - [ ] ApprovalMiddleware
  - [ ] Loop/Parallel/Condition

#### Week 11-14
- [x] 可观测性
  - [ ] OTEL Tracing
  - [ ] Metrics 上报
  - [ ] 敏感数据过滤

- [x] 多代理协作
  - [ ] SubAgent 支持
  - [ ] 共享会话
  - [ ] Team 模式

- [x] 生产部署
  - [ ] Docker 镜像
  - [ ] K8s 部署配置
  - [ ] 监控告警

**交付物**:
- 审批系统
- 工作流引擎
- 可观测性
- 部署文档

---

## 七、质量保证

### 7.1 测试策略

#### 单元测试
- 覆盖率：不在文档固化阈值；按改动风险补齐关键路径，并以 CI/本地 `go test` 结果为准。
- 所有公开接口必须有测试
- 使用 table-driven tests

```go
// 示例
func TestAgent_Run(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    string
        wantErr bool
    }{
        {
            name:  "simple query",
            input: "hello",
            want:  "hi there",
        },
        // ...
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // ...
        })
    }
}
```

#### 集成测试
- 真实模型调用 (可选)
- Mock 服务器验证
- 端到端流程

#### Benchmark
- 性能回归测试
- 内存占用监控

### 7.2 CI/CD

```yaml
# .github/workflows/ci.yml
name: CI

on: [push, pull_request]

jobs:
  test:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v3
      - uses: actions/setup-go@v4
        with:
          go-version: '1.24'

      - name: Run tests
        run: make test

      - name: Check coverage
        run: make coverage

      - name: Lint
        run: make lint

      - name: Security scan
        run: make security
```

### 7.3 代码规范

#### Linting
```makefile
lint:
    golangci-lint run --config .golangci.yml

# .golangci.yml
linters:
  enable:
    - gofmt
    - govet
    - staticcheck
    - errcheck
    - gosec
    - goconst
```

#### 提交规范
```
feat: 新功能
fix: 修复
docs: 文档
test: 测试
refactor: 重构
```

---

## 八、文档体系

### 8.1 用户文档

- **README.md**: 项目简介 + 快速开始
- **docs/getting-started.md**: 详细教程
- **docs/api-reference.md**: API 参考
- **docs/security.md**: 安全指南
- **docs/trace-system.md**: 追踪系统文档

### 8.2 开发者文档

- **docs/architecture.md**: 本文档
- **docs/contributing.md**: 贡献指南
- **docs/adr/**: 架构决策记录
- **docs/development.md**: 开发环境搭建

### 8.3 代码文档

- 所有公开接口必须有 GoDoc 注释
- 关键算法/逻辑添加注释
- 示例代码演示用法

---

## 九、总结

### 9.1 核心优势

1. **简洁** - 4 个核心接口，零学习曲线
2. **可靠** - WAL + Checkpoint + 自动封口
3. **安全** - 三层防御 + 持久化审批
4. **高效** - 零依赖，编译快，运行快
5. **可扩展** - Middleware + Hook + MCP

### 9.2 吸取的精华

| 来源项目 | 借鉴特性 |
|---------|---------|
| Kode-agent-sdk | 三通道事件、WAL 持久化、自动封口 |
| deepagents | Middleware Pipeline、路径沙箱、HITL |
| anthropic-sdk-go | 类型安全、RequestOption 模式 |
| kimi-cli | DenwaRenji 时间回溯、审批队列 |
| mastra | DI 架构、工作流引擎 |
| langchain | Runnable 抽象、StateGraph |
| openai-agents | 严格类型、工具治理 |
| agno | Team/Workflow 统一接口 |
| agentsdk | CompositeBackend 路径路由、Working Memory Schema/TTL、语义记忆溯源、本地 Evals |

### 9.3 规避的缺陷

- ✅ 拆分巨型文件 (<500 行/文件)
- ✅ 单测覆盖 >90%
- ✅ 修复所有安全漏洞
- ✅ 零依赖核心
- ✅ WAL + 事务语义

### 9.3.1 额外规避（来自 agentsdk）

基于第 17 个项目 agentsdk 的分析，我们还需要规避以下问题：

- ✅ **中间件 Tools 传递** - 确保 tool schema 正确传递到 LLM，不留空
- ✅ **作用域自动注入** - Working Memory 的 thread_id/resource_id 自动从上下文注入
- ✅ **真实的自动总结** - 使用 LLM 进行真正的总结，而非简单字符串拼接
- ✅ **工具参数校验** - 在执行前校验 JSON Schema，而非运行期崩溃
- ✅ **示例代码测试** - 所有 examples/ 目录的代码必须能编译和运行

### 9.4 下一步行动

1. **立即开始** v0.1 MVP 开发
2. **2 周目标** 完成核心 Agent + 2 个模型 + 2 个工具
3. **持续迭代** 每 2 周一个版本
4. **社区建设** 开源后积极响应 Issue/PR

---

## 附录

### A. 参考资料

- [Kode-agent-sdk 分析报告](./analysis/kode-agent-sdk.md)
- [deepagents 分析报告](./analysis/deepagents.md)
- [anthropic-sdk-go 分析报告](./analysis/anthropic-sdk-go.md)
- [完整对比矩阵](./comparison-matrix.xlsx)

### B. 术语表

- **WAL**: Write-Ahead Log，写前日志
- **HITL**: Human-in-the-Loop，人在环中
- **MCP**: Model Context Protocol，模型上下文协议
- **SSE**: Server-Sent Events，服务器推送事件
- **OTEL**: OpenTelemetry，开放遥测标准

### C. 版本历史

- 2026-02-11: 与代码同步更新，修正接口签名、目录结构、API 示例
- 2025-01-15: v1.0 初版发布
- 2025-01-15: 完成 16 个项目横向对比
- 2025-01-15: 确定核心架构设计

---

**文档维护者**: 架构组
**最后更新**: 2026-02-11
**状态**: ✅ 已更新（与代码同步）
