# agentsdk-go 架构设计文档

> 早期调研笔记（含历史内容）
>
> 设计原则：KISS | YAGNI | Never Break Userspace | 大道至简

**文档状态**: 本文档包含早期调研内容；实现现状以代码与测试为准。

**实现范围（概览）**:
- Agent 核心循环 + Tool 执行
- Middleware（6 点拦截）
- Hooks（Shell）
- MCP（stdio/SSE）
- Sandbox（FS/Network/Resource）
- Runtime 扩展：Skills / Commands / Subagents / Tasks

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
- `before_agent`: 会话入口前做租户/速率/审计初始化。
- `before_model`: Prompt 组装前做上下文裁剪、敏感字段遮蔽。
- `after_model`: 模型输出后做安全过滤、拒绝理由重写。
- `before_tool`: 工具调用前校验白名单、参数 Schema、冷却时间。
- `after_tool`: 结果回填前做降噪、结构化封装、观测指标打点。
- `after_agent`: 对最终回复做格式化、用量上报、持久化。

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
out, _ := a.model.Generate(ctx, c)
state.ModelOutput = out
_ = a.mw.Execute(ctx, middleware.StageAfterModel, state)
// 工具调用前后同理
```

#### 2.5.4 使用场景
- **日志/审计**: 统一入口收集 request/工具调用/最终回复三段日志。
- **限流/配额**: `before_agent` + `before_model` 组合做租户限流和 prompt token 预算。
- **安全检查**: `before_tool` 过滤危险命令，`after_tool` 做结果脱敏与防注入。
- **监控/告警**: `after_agent` 上报耗时、QPS、error rate，支持熔断/报警。

#### 2.5.5 实现细节（集成点）
- **状态传递**: `middleware.State` 贯穿 6 段，记录 `Agent Context`、`ModelOutput`、`ToolCall/Result` 与 `Values` 扩展字段。
- **线程安全**: `Chain.Use` 内置写锁，运行时追加中间件不会破坏正在执行的链。
- **零依赖 & 可预测**: 不引入反射/泛型，保持核心 <150 行；相比 Claude Code 的多 Hook 抽象，agentsdk-go 更符合 KISS。

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

### 3.2 整体架构 (v0.4.0 实现)

```
┌─────────────────────────────────────────────────────────────────┐
│                         agentsdk-go v0.4.0                       │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/api - 统一入口层                                        │ │
│  │  ├─ Runtime.Run(ctx, Request) -> Response                  │ │
│  │  ├─ Runtime.RunStream(ctx, Request) -> <-chan StreamEvent  │ │
│  │  ├─ Token 统计 & 自动 Compact                               │ │
│  │  └─ OpenTelemetry 追踪 & UUID 标识                          │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/agent - Agent 核心循环 (189 行)                         │ │
│  │  ├─ Model.Generate() → Tool Calls → Execute → Loop         │ │
│  │  ├─ MaxIterations 限制                                      │ │
│  │  └─ Context 状态管理                                        │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/middleware - 6 点拦截链                                 │ │
│  │  ├─ BeforeAgent  → 请求验证、审计                           │ │
│  │  ├─ BeforeModel  → Prompt 处理、上下文优化                   │ │
│  │  ├─ AfterModel   → 结果过滤、安全检查                        │ │
│  │  ├─ BeforeTool   → 工具参数校验                             │ │
│  │  ├─ AfterTool    → 结果后处理                               │ │
│  │  └─ AfterAgent   → 响应格式化、指标采集                      │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/model - 模型适配层                                      │ │
│  │  ├─ Model 接口 (Complete / CompleteStream)                 │ │
│  │  ├─ AnthropicProvider (Claude 系列)                        │ │
│  │  ├─ ModelFactory 多模型支持                                 │ │
│  │  └─ Token Usage 追踪                                        │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/tool - 工具系统                                         │ │
│  │  ├─ Registry (工具注册表)                                   │ │
│  │  ├─ Executor (沙箱执行)                                     │ │
│  │  ├─ builtin/ (20+ 内置工具)                                 │ │
│  │  │   ├─ bash (异步支持)    ├─ grep/glob                    │ │
│  │  │   ├─ read/write/edit   ├─ web_fetch/search              │ │
│  │  │   ├─ task/skill        └─ task_create                   │ │
│  │  └─ MCP 集成 (stdio/SSE)                                    │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  支撑模块                                                    │ │
│  │  ├─ pkg/config     - 配置加载 & Rules & 热重载              │ │
│  │  ├─ pkg/message    - 消息历史 & LRU 会话缓存                 │ │
│  │  ├─ pkg/core/hooks - Shell Hook 执行器                      │ │
│  │  ├─ pkg/core/events - 事件总线                              │ │
│  │  ├─ pkg/sandbox    - 文件系统隔离                           │ │
│  │  ├─ pkg/security   - 命令校验 & 路径解析                     │ │
│  │  ├─ pkg/mcp        - MCP 客户端                             │ │
│  └────────────────────────────────────────────────────────────┘ │
│                              ↓                                   │
│  ┌────────────────────────────────────────────────────────────┐ │
│  │  pkg/runtime - 运行时扩展                                    │ │
│  │  ├─ skills/     - Skills 管理 (懒加载)                      │ │
│  │  ├─ subagents/  - Subagent 编排                            │ │
│  │  └─ commands/   - Slash Commands 解析                       │ │
│  └────────────────────────────────────────────────────────────┘ │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

### 3.3 目录结构 (v0.4.0 实际)

```
agentsdk-go/
├── pkg/                          # 核心包
│   ├── api/                      # 统一 API 入口
│   │   ├── agent.go              # Runtime 实现 (1284行)
│   │   ├── options.go            # Options & Request & Response
│   │   ├── stream.go             # StreamEvent 类型
│   │   ├── compact.go            # 自动上下文压缩
│   │   ├── stats.go              # Token 统计
│   │   ├── otel.go               # OpenTelemetry 集成
│   │   └── *_bridge.go           # 各模块桥接
│   │
│   ├── agent/                    # Agent 核心循环
│   │   ├── agent.go              # 核心循环 (189行)
│   │   ├── context.go            # RunContext
│   │   └── options.go            # Agent 配置
│   │
│   ├── middleware/               # 6 点拦截中间件
│   │   ├── chain.go              # 中间件链执行器
│   │   └── types.go              # Stage & State & Middleware 接口
│   │
│   ├── model/                    # 模型抽象层
│   │   ├── interface.go          # Model 接口定义
│   │   ├── anthropic.go          # Anthropic 适配器
│   │   ├── provider.go           # ModelFactory & Provider
│   │   └── options.go            # 模型配置选项
│   │
│   ├── tool/                     # 工具系统
│   │   ├── tool.go               # Tool 接口
│   │   ├── registry.go           # 工具注册表
│   │   ├── executor.go           # 工具执行器
│   │   ├── schema.go             # JSON Schema
│   │   └── builtin/              # 内置工具 (20+)
│   │       ├── bash.go           # Bash (支持异步)
│   │       ├── async_manager.go  # 异步任务管理
│   │       ├── read.go           # 文件读取
│   │       ├── write.go          # 文件写入
│   │       ├── edit.go           # 文件编辑
│   │       ├── grep.go           # 内容搜索
│   │       ├── glob.go           # 文件匹配
│   │       ├── task.go           # Subagent 任务
│   │       ├── skill.go          # Skills 执行
│   │       ├── webfetch.go       # Web 内容获取
│   │       └── ...
│   │
│   ├── message/                  # 消息历史
│   │   ├── history.go            # History 管理
│   │   ├── converter.go          # Message 类型转换
│   │   └── trimmer.go            # Token 裁剪
│   │
│   ├── config/                   # 配置管理
│   │   ├── settings_loader.go    # 配置加载
│   │   ├── settings_types.go     # 配置类型定义
│   │   ├── rules.go              # .claude/rules/ 加载
│   │   └── validator.go          # 配置校验
│   │
│   ├── core/                     # 核心扩展
│   │   ├── events/               # 事件总线
│   │   │   ├── bus.go            # EventBus
│   │   │   └── types.go          # Event 类型
│   │   ├── hooks/                # Hooks 系统
│   │   │   ├── executor.go       # Shell Hook 执行
│   │   │   └── types.go          # Hook 类型
│   │   └── middleware/           # OpenTelemetry 中间件
│   │
│   ├── runtime/                  # 运行时扩展
│   │   ├── skills/               # Skills 管理
│   │   ├── subagents/            # Subagent 管理
│   │   └── commands/             # Slash Commands
│   │
│   ├── mcp/                      # MCP 客户端
│   │   └── mcp.go                # stdio/SSE 支持
│   │
│   ├── sandbox/                  # 沙箱隔离
│   │   └── manager.go            # 文件系统限制
│   │
│   └── security/                 # 安全模块
│       ├── validator.go          # 命令校验
│       └── resolver.go           # 路径解析
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
│   └── 05-multimodel/            # 多模型
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

#### 3.4.1 Agent 接口

```go
// pkg/agent/agent.go
package agent

import (
    "context"
    "time"
)

// Agent 是核心接口，提供最小化 API
type Agent interface {
    // Run 执行单次对话，阻塞直到完成
    Run(ctx context.Context, input string) (*RunResult, error)

    // RunStream 流式执行，通过 channel 返回事件
    RunStream(ctx context.Context, input string) (<-chan Event, error)

    // AddTool 注册工具
    AddTool(tool Tool) error

    // WithHook 添加生命周期钩子
    WithHook(hook Hook) Agent
}

// RunContext 运行上下文配置
type RunContext struct {
    SessionID      string        // 会话 ID
    WorkDir        string        // 工作目录
    MaxIterations  int           // 最大迭代次数
    MaxTokens      int           // 最大 token 数
    Timeout        time.Duration // 超时时间
    ApprovalMode   ApprovalMode  // 审批模式
    Temperature    float64       // 模型温度
}

// RunResult 运行结果
type RunResult struct {
    Output     string      // 输出文本
    ToolCalls  []ToolCall  // 工具调用记录
    Usage      TokenUsage  // Token 使用情况
    StopReason string      // 停止原因
    Events     []Event     // 事件列表
}

// TokenUsage Token 使用统计
type TokenUsage struct {
    InputTokens  int
    OutputTokens int
    TotalTokens  int
    CacheTokens  int // Prompt Caching
}

// ApprovalMode 审批模式
type ApprovalMode int

const (
    ApprovalNone     ApprovalMode = iota // 无需审批
    ApprovalRequired                      // 全部需要审批
    ApprovalAuto                          // 会话内自动审批
)
```

#### 3.4.2 事件系统

```go
// pkg/event/event.go
package event

import "time"

// EventType 事件类型
type EventType string

const (
    // Progress 通道事件
    EventProgress      EventType = "progress"       // 进度更新
    EventThinking      EventType = "thinking"       // 思考过程
    EventToolCall      EventType = "tool_call"      // 工具调用
    EventToolResult    EventType = "tool_result"    // 工具结果

    // Control 通道事件
    EventApprovalReq   EventType = "approval_req"   // 审批请求
    EventApprovalResp  EventType = "approval_resp"  // 审批响应
    EventInterrupt     EventType = "interrupt"      // 中断请求
    EventResume        EventType = "resume"         // 恢复执行

    // Monitor 通道事件
    EventMetrics       EventType = "metrics"        // 指标上报
    EventAudit         EventType = "audit"          // 审计日志
    EventError         EventType = "error"          // 错误事件
)

// Event 事件定义
type Event struct {
    ID        string                 // 事件 ID
    Type      EventType              // 事件类型
    Timestamp time.Time              // 时间戳
    SessionID string                 // 会话 ID
    Data      interface{}            // 事件数据
    Bookmark  *Bookmark              // 断点标记
}

// Bookmark 断点续播标记
type Bookmark struct {
    ID       string    // 断点 ID
    Position int64     // WAL 位置
    State    []byte    // 状态快照
}

// EventBus 三通道事件总线
type EventBus struct {
    progress chan<- Event  // Progress 通道
    control  chan<- Event  // Control 通道
    monitor  chan<- Event  // Monitor 通道
}

// NewEventBus 创建事件总线
func NewEventBus(
    progress chan<- Event,
    control chan<- Event,
    monitor chan<- Event,
) *EventBus {
    return &EventBus{
        progress: progress,
        control:  control,
        monitor:  monitor,
    }
}

// Emit 发送事件到对应通道
func (b *EventBus) Emit(event Event) error {
    switch event.Type {
    case EventProgress, EventThinking, EventToolCall, EventToolResult:
        b.progress <- event
    case EventApprovalReq, EventApprovalResp, EventInterrupt, EventResume:
        b.control <- event
    case EventMetrics, EventAudit, EventError:
        b.monitor <- event
    default:
        return fmt.Errorf("unknown event type: %s", event.Type)
    }
    return nil
}
```

#### 3.4.3 工具系统

```go
// pkg/tool/tool.go
package tool

import (
    "context"
    "encoding/json"
    "fmt"
)

// Tool 工具接口
type Tool interface {
    // Name 返回工具名称
    Name() string

    // Description 返回工具描述
    Description() string

    // Schema 返回参数 JSON Schema
    Schema() *JSONSchema

    // Execute 执行工具
    Execute(ctx context.Context, params map[string]interface{}) (*ToolResult, error)
}

// JSONSchema 工具参数 schema
type JSONSchema struct {
    Type       string                 `json:"type"`
    Properties map[string]interface{} `json:"properties"`
    Required   []string               `json:"required"`
}

// ToolResult 工具执行结果
type ToolResult struct {
    Success bool        // 是否成功
    Output  string      // 输出内容
    Data    interface{} // 结构化数据
    Error   error       // 错误信息
}

// Registry 工具注册表
type Registry struct {
    tools     map[string]Tool
    mcpClient *mcp.Client
    validator Validator  // 新增：参数校验器
}

// Validator 工具参数校验器 (借鉴 agentsdk)
type Validator interface {
    // Validate 校验参数是否符合 schema
    Validate(params map[string]interface{}, schema *JSONSchema) error
}

// DefaultValidator JSON Schema 校验器
type DefaultValidator struct{}

// Validate 实现参数校验
func (v *DefaultValidator) Validate(params map[string]interface{}, schema *JSONSchema) error {
    // 1. 检查 required 字段
    for _, field := range schema.Required {
        if _, exists := params[field]; !exists {
            return fmt.Errorf("missing required field: %s", field)
        }
    }

    // 2. 检查字段类型（简化版）
    for key, value := range params {
        propSchema, exists := schema.Properties[key]
        if !exists {
            continue // 允许额外字段
        }

        // 类型检查逻辑
        expectedType := propSchema.(map[string]interface{})["type"].(string)
        if err := validateType(value, expectedType); err != nil {
            return fmt.Errorf("field %s: %w", key, err)
        }
    }

    return nil
}

// NewRegistry 创建注册表
func NewRegistry() *Registry {
    return &Registry{
        tools:     make(map[string]Tool),
        validator: &DefaultValidator{},
    }
}

// Register 注册工具
func (r *Registry) Register(tool Tool) error {
    if _, exists := r.tools[tool.Name()]; exists {
        return fmt.Errorf("tool %s already registered", tool.Name())
    }
    r.tools[tool.Name()] = tool
    return nil
}

// Get 获取工具
func (r *Registry) Get(name string) (Tool, error) {
    tool, exists := r.tools[name]
    if !exists {
        return nil, fmt.Errorf("tool %s not found", name)
    }
    return tool, nil
}

// List 列出所有工具
func (r *Registry) List() []Tool {
    tools := make([]Tool, 0, len(r.tools))
    for _, tool := range r.tools {
        tools = append(tools, tool)
    }
    return tools
}

// Execute 执行工具前先做参数校验
func (r *Registry) Execute(ctx context.Context, name string, params map[string]interface{}) (*ToolResult, error) {
    tool, err := r.Get(name)
    if err != nil {
        return nil, err
    }

    schema := tool.Schema()
    if schema != nil && r.validator != nil {
        if err := r.validator.Validate(params, schema); err != nil {
            return nil, fmt.Errorf("tool %s validation failed: %w", name, err)
        }
    }

    return tool.Execute(ctx, params)
}
```

#### 3.4.4 会话持久化

```go
// pkg/session/session.go
package session

import (
    "context"
    "time"
)

// Session 会话接口
type Session interface {
    // Append 追加消息
    Append(msg Message) error

    // List 列出消息
    List(filter Filter) ([]Message, error)

    // Checkpoint 创建检查点
    Checkpoint(name string) error

    // Resume 恢复到检查点
    Resume(name string) (*Session, error)

    // Fork 从检查点创建分支
    Fork(name string) (*Session, error)

    // Close 关闭会话
    Close() error
}

// Message 消息定义
type Message struct {
    ID        string      // 消息 ID
    Role      string      // 角色 (user/assistant/system)
    Content   string      // 内容
    ToolCalls []ToolCall  // 工具调用
    Timestamp time.Time   // 时间戳
}

// Filter 消息过滤器
type Filter struct {
    StartTime *time.Time
    EndTime   *time.Time
    Role      string
    Limit     int
    Offset    int
}

// Backend 存储后端接口 (借鉴 agentsdk)
type Backend interface {
    // Read 读取数据
    Read(path string) ([]byte, error)

    // Write 写入数据
    Write(path string, data []byte) error

    // List 列出路径
    List(prefix string) ([]string, error)

    // Delete 删除数据
    Delete(path string) error
}

// CompositeBackend 组合后端 - 路径级路由
type CompositeBackend struct {
    routes map[string]Backend  // path prefix → backend
    mu     sync.RWMutex
}

// NewCompositeBackend 创建组合后端
func NewCompositeBackend() *CompositeBackend {
    return &CompositeBackend{
        routes: make(map[string]Backend),
    }
}

// AddRoute 添加路由规则
// 例如: AddRoute("/sessions", fileBackend)
//       AddRoute("/cache", memoryBackend)
func (b *CompositeBackend) AddRoute(prefix string, backend Backend) {
    b.mu.Lock()
    defer b.mu.Unlock()
    b.routes[prefix] = backend
}

// Route 根据路径选择后端 (最长前缀匹配)
func (b *CompositeBackend) Route(path string) Backend {
    b.mu.RLock()
    defer b.mu.RUnlock()

    var matched Backend
    var maxLen int

    for prefix, backend := range b.routes {
        if strings.HasPrefix(path, prefix) && len(prefix) > maxLen {
            matched = backend
            maxLen = len(prefix)
        }
    }

    return matched
}

// Read 读取数据 (自动路由)
func (b *CompositeBackend) Read(path string) ([]byte, error) {
    backend := b.Route(path)
    if backend == nil {
        return nil, fmt.Errorf("no backend for path: %s", path)
    }
    return backend.Read(path)
}

// Write 写入数据 (自动路由)
func (b *CompositeBackend) Write(path string, data []byte) error {
    backend := b.Route(path)
    if backend == nil {
        return fmt.Errorf("no backend for path: %s", path)
    }
    return backend.Write(path, data)
}

// 这样可以实现：
// - `/sessions/*` 走文件系统 (持久化)
// - `/cache/*` 走内存 (快速访问)
// - `/checkpoints/*` 走 S3/OSS (长期存档)

// FileSession 文件存储会话实现
type FileSession struct {
    id         string
    dir        string
    wal        *WAL            // Write-Ahead Log
    buffer     *EventBuffer    // 事件缓冲
    summarizer *Summarizer     // 自动摘要
}

// NewFileSession 创建文件会话
func NewFileSession(id string, dir string) (*FileSession, error) {
    wal, err := NewWAL(filepath.Join(dir, id, "wal.log"))
    if err != nil {
        return nil, err
    }

    return &FileSession{
        id:         id,
        dir:        dir,
        wal:        wal,
        buffer:     NewEventBuffer(1000),
        summarizer: NewSummarizer(50000), // 50k token 触发摘要
    }, nil
}
```

#### 3.4.5 安全系统

```go
// pkg/security/sandbox.go
package security

import (
    "path/filepath"
    "os"
)

// Sandbox 路径沙箱
type Sandbox struct {
    allowList []string      // 路径白名单
    validator *Validator    // 命令校验器
    resolver  *PathResolver // 路径解析器
}

// NewSandbox 创建沙箱
func NewSandbox(workDir string) *Sandbox {
    return &Sandbox{
        allowList: []string{workDir},
        validator: NewValidator(),
        resolver:  NewPathResolver(),
    }
}

// ValidatePath 验证路径安全性
func (s *Sandbox) ValidatePath(path string) error {
    // 1. 解析符号链接 (fix mini-claude-code-go bug)
    resolved, err := s.resolver.Resolve(path)
    if err != nil {
        return fmt.Errorf("resolve path failed: %w", err)
    }

    // 2. 规范化路径
    abs, err := filepath.Abs(resolved)
    if err != nil {
        return fmt.Errorf("abs path failed: %w", err)
    }

    // 3. 检查白名单前缀
    allowed := false
    for _, prefix := range s.allowList {
        if strings.HasPrefix(abs, prefix) {
            allowed = true
            break
        }
    }
    if !allowed {
        return fmt.Errorf("path %s not in allowlist", abs)
    }

    return nil
}

// ValidateCommand 验证命令安全性
func (s *Sandbox) ValidateCommand(cmd string) error {
    return s.validator.Validate(cmd)
}

// PathResolver 路径解析器 (处理符号链接)
type PathResolver struct{}

// Resolve 解析路径，跟随符号链接
func (r *PathResolver) Resolve(path string) (string, error) {
    // 使用 O_NOFOLLOW 检测符号链接
    info, err := os.Lstat(path)
    if err != nil {
        return "", err
    }

    if info.Mode()&os.ModeSymlink != 0 {
        // 是符号链接，读取目标
        target, err := os.Readlink(path)
        if err != nil {
            return "", err
        }
        return target, nil
    }

    return path, nil
}

// Validator 命令校验器
type Validator struct {
    dangerousCommands []string
    dangerousArgs     []string
}

// NewValidator 创建校验器
func NewValidator() *Validator {
    return &Validator{
        dangerousCommands: []string{
            "rm", "rmdir", "dd", "mkfs",
            "format", "fdisk", "parted",
        },
        dangerousArgs: []string{
            "-rf", "--no-preserve-root",
            "--force", "/dev/",
        },
    }
}

// Validate 校验命令
func (v *Validator) Validate(cmd string) error {
    // 解析命令 (fix Kode-cli BashTool bug)
    parts, err := shellquote.Split(cmd)
    if err != nil {
        return fmt.Errorf("parse command failed: %w", err)
    }

    if len(parts) == 0 {
        return fmt.Errorf("empty command")
    }

    // 检查危险命令
    baseCmd := filepath.Base(parts[0])
    for _, dangerous := range v.dangerousCommands {
        if baseCmd == dangerous {
            return fmt.Errorf("dangerous command: %s", dangerous)
        }
    }

    // 检查危险参数
    cmdStr := strings.Join(parts, " ")
    for _, dangerous := range v.dangerousArgs {
        if strings.Contains(cmdStr, dangerous) {
            return fmt.Errorf("dangerous argument: %s", dangerous)
        }
    }

    return nil
}
```

#### 3.4.6 评估系统

```go
// pkg/evals/evals.go
package evals

import (
    "fmt"
    "strings"
)

// Evaluator 评估器接口 (借鉴 agentsdk)
type Evaluator interface {
    // EvaluateKeyword 关键词匹配评估
    EvaluateKeyword(output, expected string) float64

    // EvaluateSimilarity 相似度评估
    EvaluateSimilarity(output, expected string) float64

    // Evaluate 综合评估
    Evaluate(output string, criteria *EvalCriteria) (*EvalResult, error)
}

// EvalCriteria 评估标准
type EvalCriteria struct {
    Keywords   []string  // 必须包含的关键词
    Exclude    []string  // 必须排除的关键词
    MinLength  int       // 最小长度
    MaxLength  int       // 最大长度
    Similarity float64   // 相似度阈值 (0-1)
    Reference  string    // 参考答案
}

// EvalResult 评估结果
type EvalResult struct {
    Score      float64            // 综合得分 (0-1)
    Passed     bool               // 是否通过
    Details    map[string]float64 // 各项得分
    Reason     string             // 未通过原因
}

// LocalEvaluator 本地评估器 (无需 LLM)
type LocalEvaluator struct{}

// NewLocalEvaluator 创建本地评估器
func NewLocalEvaluator() *LocalEvaluator {
    return &LocalEvaluator{}
}

// EvaluateKeyword 关键词匹配评估
func (e *LocalEvaluator) EvaluateKeyword(output, expected string) float64 {
    keywords := strings.Fields(expected)
    matched := 0

    outputLower := strings.ToLower(output)
    for _, kw := range keywords {
        if strings.Contains(outputLower, strings.ToLower(kw)) {
            matched++
        }
    }

    if len(keywords) == 0 {
        return 1.0
    }
    return float64(matched) / float64(len(keywords))
}

// EvaluateSimilarity 相似度评估 (Jaccard 系数)
func (e *LocalEvaluator) EvaluateSimilarity(output, expected string) float64 {
    outputWords := tokenize(output)
    expectedWords := tokenize(expected)

    intersection := intersect(outputWords, expectedWords)
    union := union(outputWords, expectedWords)

    if len(union) == 0 {
        return 1.0
    }
    return float64(len(intersection)) / float64(len(union))
}

// Evaluate 综合评估
func (e *LocalEvaluator) Evaluate(output string, criteria *EvalCriteria) (*EvalResult, error) {
    if criteria == nil {
        return nil, fmt.Errorf("criteria is nil")
    }

    result := &EvalResult{
        Details: make(map[string]float64),
    }

    // 1. 长度检查
    length := len(output)
    if criteria.MinLength > 0 && length < criteria.MinLength {
        result.Passed = false
        result.Reason = fmt.Sprintf("output too short: %d < %d", length, criteria.MinLength)
        return result, nil
    }
    if criteria.MaxLength > 0 && length > criteria.MaxLength {
        result.Passed = false
        result.Reason = fmt.Sprintf("output too long: %d > %d", length, criteria.MaxLength)
        return result, nil
    }
    result.Details["length"] = 1.0

    // 2. 关键词检查
    for _, kw := range criteria.Keywords {
        if !strings.Contains(strings.ToLower(output), strings.ToLower(kw)) {
            result.Passed = false
            result.Reason = fmt.Sprintf("missing keyword: %s", kw)
            return result, nil
        }
    }
    result.Details["keywords"] = 1.0

    // 3. 排除词检查
    for _, ex := range criteria.Exclude {
        if strings.Contains(strings.ToLower(output), strings.ToLower(ex)) {
            result.Passed = false
            result.Reason = fmt.Sprintf("contains excluded word: %s", ex)
            return result, nil
        }
    }
    result.Details["exclude"] = 1.0

    // 4. 相似度检查
    if criteria.Reference != "" {
        similarity := e.EvaluateSimilarity(output, criteria.Reference)
        result.Details["similarity"] = similarity

        if similarity < criteria.Similarity {
            result.Passed = false
            result.Reason = fmt.Sprintf("similarity too low: %.2f < %.2f", similarity, criteria.Similarity)
            return result, nil
        }
    }

    // 计算综合得分
    var total float64
    for _, score := range result.Details {
        total += score
    }
    if len(result.Details) > 0 {
        result.Score = total / float64(len(result.Details))
    }
    result.Passed = true

    return result, nil
}

// 辅助函数
func tokenize(s string) map[string]bool {
    words := strings.Fields(strings.ToLower(s))
    set := make(map[string]bool)
    for _, w := range words {
        set[w] = true
    }
    return set
}

func intersect(a, b map[string]bool) map[string]bool {
    result := make(map[string]bool)
    for k := range a {
        if b[k] {
            result[k] = true
        }
    }
    return result
}

func union(a, b map[string]bool) map[string]bool {
    result := make(map[string]bool)
    for k := range a {
        result[k] = true
    }
    for k := range b {
        result[k] = true
    }
    return result
}
```

**使用示例**:

```go
evaluator := evals.NewLocalEvaluator()

criteria := &evals.EvalCriteria{
    Keywords:   []string{"function", "refactor"},
    Exclude:    []string{"error", "failed"},
    MinLength:  100,
    Reference:  "重构了 handleRequest 函数，提升了性能",
    Similarity: 0.5,
}

result, err := evaluator.Evaluate(agentOutput, criteria)
if err != nil {
    log.Fatal(err)
}

if result.Passed {
    fmt.Printf("评估通过，得分: %.2f\n", result.Score)
} else {
    fmt.Printf("评估失败: %s\n", result.Reason)
}
```

**优势**:
- ✅ 无需 LLM，本地运行
- ✅ 快速反馈，毫秒级
- ✅ 确定性结果，可重现
- ✅ 适合 CI/CD 自动化测试

---

## 四、技术选型

### 4.1 核心原则：零依赖

```go
// go.mod
module github.com/你的组织/agentsdk-go

go 1.24

// 核心包完全零外部依赖
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

    "github.com/你的组织/agentsdk-go/pkg/agent"
    "github.com/你的组织/agentsdk-go/pkg/model/anthropic"
    "github.com/你的组织/agentsdk-go/pkg/tool/builtin"
)

func main() {
    // 1. 创建模型
    model := anthropic.NewModel(
        "claude-sonnet-4.5",
        anthropic.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    )

    // 2. 创建 Agent
    ag := agent.New(
        agent.WithModel(model),
        agent.WithWorkDir("/path/to/project"),
        agent.WithMaxIterations(20),
        agent.WithApproval(agent.ApprovalRequired),
    )

    // 3. 添加工具
    if err := ag.AddTool(builtin.NewBashTool()); err != nil {
        log.Fatal(err)
    }
    if err := ag.AddTool(builtin.NewReadTool()); err != nil {
        log.Fatal(err)
    }
    if err := ag.AddTool(builtin.NewWriteTool()); err != nil {
        log.Fatal(err)
    }
    if err := ag.AddTool(builtin.NewGrepTool()); err != nil {
        log.Fatal(err)
    }

    // 4. 运行
    result, err := ag.Run(context.Background(), "帮我重构 main.go 的 handleRequest 函数")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println("Output:", result.Output)
    fmt.Printf("Token Usage: %+v\n", result.Usage)
}
```

### 5.2 流式输出 + 事件监听

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/你的组织/agentsdk-go/pkg/agent"
    "github.com/你的组织/agentsdk-go/pkg/event"
)

func main() {
    ag := createAgent() // ... 同上

    // 流式执行
    events, err := ag.RunStream(context.Background(), "实现用户登录功能")
    if err != nil {
        log.Fatal(err)
    }

    // 监听事件
    for evt := range events {
        switch evt.Type {
        case event.EventProgress:
            fmt.Println("[进度]", evt.Data)

        case event.EventThinking:
            fmt.Println("[思考]", evt.Data)

        case event.EventToolCall:
            toolCall := evt.Data.(event.ToolCallData)
            fmt.Printf("[工具] %s(%+v)\n", toolCall.Name, toolCall.Params)

        case event.EventToolResult:
            result := evt.Data.(event.ToolResultData)
            fmt.Printf("[结果] %s\n", result.Output)

        case event.EventApprovalReq:
            // 处理审批请求
            approval := evt.Data.(event.ApprovalRequest)
            fmt.Printf("[审批] 工具: %s, 参数: %+v\n", approval.ToolName, approval.Params)

            // 用户确认
            approved := askUser(approval)
            ag.Approve(evt.ID, approved)

        case event.EventError:
            fmt.Println("[错误]", evt.Data)
        }
    }
}

func askUser(req event.ApprovalRequest) bool {
    fmt.Printf("是否允许执行 %s? (y/n): ", req.ToolName)
    var answer string
    fmt.Scanln(&answer)
    return answer == "y" || answer == "Y"
}
```

### 5.3 会话恢复 (Checkpoint/Resume)

```go
package main

import (
    "context"
    "fmt"
    "log"

    "github.com/你的组织/agentsdk-go/pkg/agent"
    "github.com/你的组织/agentsdk-go/pkg/session"
)

func main() {
    ag := createAgent()

    // 获取会话
    sess, err := ag.GetSession()
    if err != nil {
        log.Fatal(err)
    }

    // 保存 checkpoint
    if err := sess.Checkpoint("before-refactor"); err != nil {
        log.Fatal(err)
    }

    // 执行操作
    result, err := ag.Run(context.Background(), "重构整个项目")
    if err != nil {
        // 出错了，恢复到 checkpoint
        fmt.Println("发生错误，恢复到之前状态...")
        if err := sess.Resume("before-refactor"); err != nil {
            log.Fatal(err)
        }
        return
    }

    fmt.Println("重构完成:", result.Output)

    // 也可以 Fork 创建分支探索
    forkSess, err := sess.Fork("experiment-branch")
    if err != nil {
        log.Fatal(err)
    }

    // 在分支中尝试不同方案
    // ...
}
```

### 5.4 自定义工具

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"

    "github.com/你的组织/agentsdk-go/pkg/tool"
)

// DatabaseTool 自定义数据库工具
type DatabaseTool struct {
    db *sql.DB
}

func NewDatabaseTool(db *sql.DB) *DatabaseTool {
    return &DatabaseTool{db: db}
}

func (t *DatabaseTool) Name() string {
    return "database_query"
}

func (t *DatabaseTool) Description() string {
    return "执行 SQL 查询并返回结果"
}

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
    query, ok := params["query"].(string)
    if !ok {
        return nil, fmt.Errorf("invalid query parameter")
    }

    // 执行查询
    rows, err := t.db.QueryContext(ctx, query)
    if err != nil {
        return &tool.ToolResult{
            Success: false,
            Error:   err,
        }, nil
    }
    defer rows.Close()

    // 构造结果
    var results []map[string]interface{}
    // ... 解析 rows

    return &tool.ToolResult{
        Success: true,
        Output:  fmt.Sprintf("查询返回 %d 行", len(results)),
        Data:    results,
    }, nil
}

func main() {
    db, _ := sql.Open("postgres", "...")

    ag := createAgent()
    ag.AddTool(NewDatabaseTool(db))

    ag.Run(context.Background(), "查询最近 24 小时的订单数据")
}
```

### 5.5 Hook 扩展

```go
package main

import (
    "context"
    "fmt"
    "time"

    "github.com/你的组织/agentsdk-go/pkg/agent"
)

// LoggingHook 日志钩子
type LoggingHook struct{}

func (h *LoggingHook) PreRun(ctx context.Context, input string) error {
    fmt.Printf("[%s] 开始执行: %s\n", time.Now().Format("15:04:05"), input)
    return nil
}

func (h *LoggingHook) PostRun(ctx context.Context, result *agent.RunResult) error {
    fmt.Printf("[%s] 执行完成，Token: %d\n",
        time.Now().Format("15:04:05"),
        result.Usage.TotalTokens,
    )
    return nil
}

func (h *LoggingHook) PreToolCall(ctx context.Context, toolName string, params map[string]interface{}) error {
    fmt.Printf("[%s] 调用工具: %s\n", time.Now().Format("15:04:05"), toolName)
    return nil
}

func (h *LoggingHook) PostToolCall(ctx context.Context, toolName string, result *tool.ToolResult) error {
    fmt.Printf("[%s] 工具返回: %s\n", time.Now().Format("15:04:05"), result.Output)
    return nil
}

func main() {
    ag := createAgent()

    // 添加 Hook
    ag = ag.WithHook(&LoggingHook{})

    ag.Run(context.Background(), "分析代码质量")
}
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

- 2025-01-15: v1.0 初版发布
- 2025-01-15: 完成 16 个项目横向对比
- 2025-01-15: 确定核心架构设计

---

**文档维护者**: 架构组
**最后更新**: 2025-01-15
**状态**: ✅ 已定稿
