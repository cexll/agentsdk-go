# P0-2: 洋葱中间件架构实现方案

## 1. 设计原理

### 1.1 什么是洋葱中间件？

洋葱中间件（Onion Middleware）是一种请求处理模式，请求从外层依次穿过各层中间件到达核心处理器，响应再从核心依次返回到外层。每层中间件可以：
- 在请求到达核心前**预处理**（如日志、鉴权、参数校验）
- 在响应返回时**后处理**（如记录耗时、脱敏、缓存）

```
请求流：
用户输入 → [日志中间件] → [审批中间件] → [摘要中间件] → 核心处理器
                ↓                ↓                ↓              ↓
响应流：
最终输出 ← [记录日志]   ← [记录审批]   ← [保存摘要]   ← 模型/工具结果
```

### 1.2 为什么需要中间件？

**当前问题**（`agentsdk-go`）：
- 扩展功能需修改核心 `Agent` 代码（违反开闭原则）
- PII 脱敏、审计、摘要等横切关注点散落各处
- 无法动态插拔能力

**参考实现**（`agentsdk`）：
```go
// agentsdk/pkg/middleware/stack.go:16-76
type Middleware interface {
    Priority() int  // 优先级（越大越靠外层）
    ExecuteModelCall(next ModelCallFunc) ModelCallFunc
    ExecuteToolCall(next ToolCallFunc) ToolCallFunc
}

// 洋葱包裹逻辑
func (s *Stack) ExecuteModelCall(ctx, req, finalHandler) {
    handler := finalHandler
    for i := len(s.middlewares) - 1; i >= 0; i-- {  // 降序包裹
        mw := s.middlewares[i]
        handler = func(ctx, req) {
            return mw.ExecuteModelCall(ctx, req, handler)
        }
    }
    return handler(ctx, req)
}
```

**优势**：
- ✅ 统一扩展面（ModelCall + ToolCall）
- ✅ 优先级排序（高优先级越靠内核）
- ✅ 可插拔（注册/注销无需修改核心）

---

## 2. 接口设计

### 2.1 核心接口

**文件**：`pkg/middleware/middleware.go`（新建）

```go
package middleware

import (
    "context"
    "time"

    "github.com/cexll/agentsdk-go/pkg/model"
    "github.com/cexll/agentsdk-go/pkg/tool"
)

// Middleware 中间件接口
type Middleware interface {
    // Priority 返回优先级（0-100，越大越靠外层）
    // 推荐范围：
    //   10-20: 基础设施（日志、Telemetry）
    //   30-40: 安全层（审批、PII 脱敏）
    //   50-60: 功能层（摘要、记忆）
    //   70-80: 业务层（自定义逻辑）
    Priority() int

    // Name 返回中间件名称
    Name() string

    // ExecuteModelCall 拦截模型调用
    ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error)

    // ExecuteToolCall 拦截工具调用
    ExecuteToolCall(ctx context.Context, call *ToolCallRequest, next ToolCallFunc) (*ToolCallResponse, error)

    // OnStart 在 Agent 启动时调用（可选）
    OnStart(ctx context.Context) error

    // OnStop 在 Agent 停止时调用（可选）
    OnStop(ctx context.Context) error
}

// ModelRequest 模型调用请求
type ModelRequest struct {
    Messages     []model.Message       // 消息历史
    Tools        []map[string]any      // 工具定义
    SessionID    string                // 会话 ID
    Metadata     map[string]any        // 元数据（供中间件传递上下文）
}

// ModelResponse 模型调用响应
type ModelResponse struct {
    Message      model.Message         // 模型响应
    Usage        model.TokenUsage      // Token 使用情况
    Metadata     map[string]any        // 元数据
}

// ToolCallRequest 工具调用请求
type ToolCallRequest struct {
    Name         string                // 工具名称
    Arguments    map[string]any        // 工具参数
    SessionID    string                // 会话 ID
    Metadata     map[string]any        // 元数据
}

// ToolCallResponse 工具调用响应
type ToolCallResponse struct {
    Output       string                // 工具输出
    Data         any                   // 结构化数据
    Error        error                 // 错误（如有）
    Metadata     map[string]any        // 元数据
}

// ModelCallFunc 模型调用函数类型
type ModelCallFunc func(ctx context.Context, req *ModelRequest) (*ModelResponse, error)

// ToolCallFunc 工具调用函数类型
type ToolCallFunc func(ctx context.Context, req *ToolCallRequest) (*ToolCallResponse, error)
```

### 2.2 基础中间件抽象

**文件**：`pkg/middleware/base.go`（新建）

```go
package middleware

import "context"

// BaseMiddleware 提供默认实现（空操作）
type BaseMiddleware struct {
    priority int
    name     string
}

func NewBaseMiddleware(name string, priority int) *BaseMiddleware {
    return &BaseMiddleware{
        name:     name,
        priority: priority,
    }
}

func (m *BaseMiddleware) Name() string {
    return m.name
}

func (m *BaseMiddleware) Priority() int {
    return m.priority
}

// 默认直接透传
func (m *BaseMiddleware) ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error) {
    return next(ctx, req)
}

func (m *BaseMiddleware) ExecuteToolCall(ctx context.Context, req *ToolCallRequest, next ToolCallFunc) (*ToolCallResponse, error) {
    return next(ctx, req)
}

func (m *BaseMiddleware) OnStart(ctx context.Context) error {
    return nil
}

func (m *BaseMiddleware) OnStop(ctx context.Context) error {
    return nil
}
```

---

## 3. Stack 实现

### 3.1 中间件栈

**文件**：`pkg/middleware/stack.go`（新建）

```go
package middleware

import (
    "context"
    "sort"
    "sync"
)

// Stack 中间件栈
type Stack struct {
    middlewares []Middleware
    mu          sync.RWMutex
}

// NewStack 创建中间件栈
func NewStack() *Stack {
    return &Stack{
        middlewares: make([]Middleware, 0),
    }
}

// Use 注册中间件
func (s *Stack) Use(mw Middleware) {
    s.mu.Lock()
    defer s.mu.Unlock()

    s.middlewares = append(s.middlewares, mw)

    // 按优先级升序排序（高优先级在后）
    sort.Slice(s.middlewares, func(i, j int) bool {
        return s.middlewares[i].Priority() < s.middlewares[j].Priority()
    })
}

// Remove 移除中间件
func (s *Stack) Remove(name string) bool {
    s.mu.Lock()
    defer s.mu.Unlock()

    for i, mw := range s.middlewares {
        if mw.Name() == name {
            s.middlewares = append(s.middlewares[:i], s.middlewares[i+1:]...)
            return true
        }
    }
    return false
}

// List 列出所有中间件（按执行顺序）
func (s *Stack) List() []Middleware {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // 返回副本，防止外部修改
    result := make([]Middleware, len(s.middlewares))
    copy(result, s.middlewares)

    // 反转顺序（执行时从高优先级开始）
    for i := len(result)/2 - 1; i >= 0; i-- {
        opp := len(result) - 1 - i
        result[i], result[opp] = result[opp], result[i]
    }

    return result
}

// ExecuteModelCall 执行模型调用（带中间件链）
func (s *Stack) ExecuteModelCall(ctx context.Context, req *ModelRequest, finalHandler ModelCallFunc) (*ModelResponse, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // 从内层（finalHandler）向外层包裹
    handler := finalHandler

    // 降序遍历（高优先级先包裹，执行时先触发）
    for i := len(s.middlewares) - 1; i >= 0; i-- {
        mw := s.middlewares[i]

        // 闭包捕获当前 handler
        currentHandler := handler
        handler = func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
            return mw.ExecuteModelCall(ctx, req, currentHandler)
        }
    }

    return handler(ctx, req)
}

// ExecuteToolCall 执行工具调用（带中间件链）
func (s *Stack) ExecuteToolCall(ctx context.Context, req *ToolCallRequest, finalHandler ToolCallFunc) (*ToolCallResponse, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()

    handler := finalHandler

    for i := len(s.middlewares) - 1; i >= 0; i-- {
        mw := s.middlewares[i]

        currentHandler := handler
        handler = func(ctx context.Context, req *ToolCallRequest) (*ToolCallResponse, error) {
            return mw.ExecuteToolCall(ctx, req, currentHandler)
        }
    }

    return handler(ctx, req)
}

// Start 启动所有中间件（LIFO 顺序）
func (s *Stack) Start(ctx context.Context) error {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // 正序启动（低优先级先启动）
    for _, mw := range s.middlewares {
        if err := mw.OnStart(ctx); err != nil {
            return err
        }
    }
    return nil
}

// Stop 停止所有中间件（LIFO 顺序）
func (s *Stack) Stop(ctx context.Context) error {
    s.mu.RLock()
    defer s.mu.RUnlock()

    // 逆序停止（高优先级先停止）
    for i := len(s.middlewares) - 1; i >= 0; i-- {
        if err := s.middlewares[i].OnStop(ctx); err != nil {
            return err
        }
    }
    return nil
}
```

---

## 4. 集成到 Agent

### 4.1 修改 Agent 结构

**文件**：`pkg/agent/agent_impl.go`

**修改位置**：第 51-88 行（结构体定义）

```go
type basicAgent struct {
    id           string
    config       Config
    model        model.Model
    tools        map[string]tool.Tool
    toolMu       sync.RWMutex
    session      session.Session
    hooks        []Hook
    approvalQ    *approval.Queue
    middlewareStack *middleware.Stack  // ✅ 新增
}
```

### 4.2 修改构造函数

**文件**：`pkg/agent/agent_impl.go`

**修改位置**：第 98-111 行（`newBasicAgent` 函数）

```go
func newBasicAgent(cfg Config, sess session.Session, approvalQ *approval.Queue) *basicAgent {
    return &basicAgent{
        id:              generateID(),
        config:          cfg,
        tools:           make(map[string]tool.Tool),
        session:         sess,
        hooks:           make([]Hook, 0),
        approvalQ:       approvalQ,
        middlewareStack: middleware.NewStack(),  // ✅ 新增
    }
}
```

### 4.3 修改模型调用路径

**文件**：`pkg/agent/agent_impl.go`

**修改位置**：第 287-307 行（模型调用逻辑）

**修改前**：
```go
// 直接调用模型
if modelWithTools, ok := a.model.(model.ModelWithTools); ok && len(a.tools) > 0 {
    toolSchemas := a.buildToolSchemas()
    resp, err = modelWithTools.GenerateWithTools(ctx, messages, toolSchemas)
} else {
    resp, err = a.model.Generate(ctx, messages)
}
```

**修改后**：
```go
// 通过中间件栈调用模型
req := &middleware.ModelRequest{
    Messages:  messages,
    Tools:     a.buildToolSchemas(),
    SessionID: runCtx.SessionID,
    Metadata:  make(map[string]any),
}

// 定义最终处理器
finalHandler := func(ctx context.Context, req *middleware.ModelRequest) (*middleware.ModelResponse, error) {
    var resp model.Message
    var err error

    if modelWithTools, ok := a.model.(model.ModelWithTools); ok && len(req.Tools) > 0 {
        resp, err = modelWithTools.GenerateWithTools(ctx, req.Messages, req.Tools)
    } else {
        resp, err = a.model.Generate(ctx, req.Messages)
    }

    if err != nil {
        return nil, err
    }

    return &middleware.ModelResponse{
        Message: resp,
        Usage: model.TokenUsage{
            InputTokens:  0, // TODO: 从模型响应提取
            OutputTokens: 0,
            TotalTokens:  0,
        },
        Metadata: make(map[string]any),
    }, nil
}

// 执行中间件链
modelResp, err := a.middlewareStack.ExecuteModelCall(ctx, req, finalHandler)
if err != nil {
    result.StopReason = "model_error"
    // ... 错误处理
}

resp = modelResp.Message
```

### 4.4 修改工具调用路径

**文件**：`pkg/agent/agent_impl.go`

**修改位置**：第 360-390 行（`executeTool` 方法）

**修改后**：
```go
func (a *basicAgent) executeTool(ctx context.Context, name string, args map[string]any) (*ToolCall, error) {
    // 构造中间件请求
    req := &middleware.ToolCallRequest{
        Name:      name,
        Arguments: args,
        SessionID: "", // TODO: 从上下文获取
        Metadata:  make(map[string]any),
    }

    // 定义最终处理器
    finalHandler := func(ctx context.Context, req *middleware.ToolCallRequest) (*middleware.ToolCallResponse, error) {
        a.toolMu.RLock()
        t, exists := a.tools[req.Name]
        a.toolMu.RUnlock()

        if !exists {
            return &middleware.ToolCallResponse{
                Error: fmt.Errorf("tool %s not found", req.Name),
            }, nil
        }

        result, err := t.Execute(ctx, req.Arguments)
        if err != nil {
            return &middleware.ToolCallResponse{
                Error: err,
            }, nil
        }

        output := fmt.Sprintf("%v", result)
        return &middleware.ToolCallResponse{
            Output: output,
            Data:   result,
        }, nil
    }

    // 执行中间件链
    toolResp, err := a.middlewareStack.ExecuteToolCall(ctx, req, finalHandler)
    if err != nil {
        return nil, err
    }

    // 转换为 ToolCall
    return &ToolCall{
        ID:        generateToolCallID(),
        Name:      name,
        Arguments: args,
        Output:    toolResp.Output,
        Error:     toolResp.Error,
    }, nil
}
```

### 4.5 暴露中间件管理接口

**文件**：`pkg/agent/agent.go`

**修改位置**：第 11-33 行（`Agent` 接口定义）

```go
type Agent interface {
    Run(ctx context.Context, input string) (*RunResult, error)
    RunWithContext(ctx context.Context, input string, runCtx RunContext) (*RunResult, error)
    RunStream(ctx context.Context, input string) (<-chan event.Event, error)
    AddTool(tool tool.Tool) error
    Fork(opts ...ForkOption) (Agent, error)
    GetSession() (session.Session, error)

    // ✅ 新增：中间件管理
    UseMiddleware(mw middleware.Middleware)
    RemoveMiddleware(name string) bool
    ListMiddlewares() []middleware.Middleware
}
```

**实现**：
```go
// pkg/agent/agent_impl.go

func (a *basicAgent) UseMiddleware(mw middleware.Middleware) {
    a.middlewareStack.Use(mw)
}

func (a *basicAgent) RemoveMiddleware(name string) bool {
    return a.middlewareStack.Remove(name)
}

func (a *basicAgent) ListMiddlewares() []middleware.Middleware {
    return a.middlewareStack.List()
}
```

---

## 5. 示例中间件：Summarization

### 5.1 实现摘要中间件

**文件**：`pkg/middleware/summarization.go`（新建）

```go
package middleware

import (
    "context"
    "fmt"
)

// SummarizationMiddleware 自动摘要中间件
type SummarizationMiddleware struct {
    *BaseMiddleware
    maxTokens     int  // 触发摘要的 token 阈值
    keepRecent    int  // 保留最近 N 条消息
}

// NewSummarizationMiddleware 创建摘要中间件
func NewSummarizationMiddleware(maxTokens, keepRecent int) *SummarizationMiddleware {
    return &SummarizationMiddleware{
        BaseMiddleware: NewBaseMiddleware("summarization", 50),
        maxTokens:      maxTokens,
        keepRecent:     keepRecent,
    }
}

// ExecuteModelCall 在模型调用前检查是否需要摘要
func (m *SummarizationMiddleware) ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error) {
    // 1. 估算当前消息历史的 token 数
    estimatedTokens := m.estimateTokens(req.Messages)

    // 2. 如果超过阈值，触发摘要
    if estimatedTokens > m.maxTokens {
        summarized, err := m.summarizeMessages(ctx, req.Messages, next)
        if err != nil {
            // 摘要失败，继续执行（降级策略）
            fmt.Printf("警告：摘要失败 - %v\n", err)
        } else {
            req.Messages = summarized
        }
    }

    // 3. 继续执行下一个中间件
    return next(ctx, req)
}

// estimateTokens 估算 token 数（简化版：1 token ≈ 4 字符）
func (m *SummarizationMiddleware) estimateTokens(messages []model.Message) int {
    total := 0
    for _, msg := range messages {
        total += len(msg.Content) / 4
    }
    return total
}

// summarizeMessages 对消息历史进行摘要
func (m *SummarizationMiddleware) summarizeMessages(ctx context.Context, messages []model.Message, modelCall ModelCallFunc) ([]model.Message, error) {
    // 1. 保留系统提示词 + 最近 N 条消息
    systemMsg := messages[0] // 假设第一条是 system
    recentMsgs := messages[len(messages)-m.keepRecent:]

    // 2. 对中间消息生成摘要
    oldMsgs := messages[1 : len(messages)-m.keepRecent]
    if len(oldMsgs) == 0 {
        return messages, nil
    }

    // 3. 构造摘要请求
    summaryPrompt := "请将以下对话历史总结为简短摘要（保留关键信息）：\n\n"
    for _, msg := range oldMsgs {
        summaryPrompt += fmt.Sprintf("[%s]: %s\n", msg.Role, msg.Content)
    }

    summaryReq := &ModelRequest{
        Messages: []model.Message{
            {Role: "user", Content: summaryPrompt},
        },
    }

    // 4. 调用模型生成摘要
    summaryResp, err := modelCall(ctx, summaryReq)
    if err != nil {
        return nil, err
    }

    // 5. 组装新消息历史
    newMessages := []model.Message{
        systemMsg,
        {Role: "system", Content: "历史对话摘要：\n" + summaryResp.Message.Content},
    }
    newMessages = append(newMessages, recentMsgs...)

    return newMessages, nil
}
```

### 5.2 使用示例

**文件**：`examples/middleware/main.go`（新建）

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/middleware"
    "github.com/cexll/agentsdk-go/pkg/model/anthropic"
)

func main() {
    // 创建模型
    model := anthropic.NewSDKModel(
        os.Getenv("ANTHROPIC_API_KEY"),
        "claude-3-5-sonnet-20241022",
        1024,
    )

    // 创建 Agent
    ag, err := agent.New(agent.Config{})
    if err != nil {
        log.Fatal(err)
    }
    ag, err = ag.Fork(agent.WithModel(model))
    if err != nil {
        log.Fatal(err)
    }

    // 注册中间件
    ag.UseMiddleware(middleware.NewSummarizationMiddleware(100000, 6))  // 10 万 token 阈值，保留最近 6 条

    // 列出中间件
    for _, mw := range ag.ListMiddlewares() {
        fmt.Printf("中间件：%s（优先级：%d）\n", mw.Name(), mw.Priority())
    }

    // 运行（长对话会自动摘要）
    result, err := ag.Run(context.Background(), "开始长对话测试")
    if err != nil {
        log.Fatal(err)
    }

    fmt.Println(result.Output)
}
```

---

## 6. 测试用例

### 6.1 单元测试

**文件**：`pkg/middleware/stack_test.go`（新建）

```go
package middleware

import (
    "context"
    "testing"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
)

func TestStack_PriorityOrder(t *testing.T) {
    stack := NewStack()

    // 注册三个中间件（乱序）
    mw1 := &testMiddleware{name: "low", priority: 10}
    mw2 := &testMiddleware{name: "high", priority: 90}
    mw3 := &testMiddleware{name: "mid", priority: 50}

    stack.Use(mw1)
    stack.Use(mw2)
    stack.Use(mw3)

    // 验证执行顺序（高优先级先执行）
    list := stack.List()
    require.Len(t, list, 3)
    assert.Equal(t, "high", list[0].Name())
    assert.Equal(t, "mid", list[1].Name())
    assert.Equal(t, "low", list[2].Name())
}

func TestStack_ExecuteModelCall(t *testing.T) {
    stack := NewStack()

    // 记录执行顺序
    var order []string

    mw1 := &testMiddleware{
        name:     "first",
        priority: 90,
        onModelCall: func() { order = append(order, "first-pre") },
    }
    mw2 := &testMiddleware{
        name:     "second",
        priority: 10,
        onModelCall: func() { order = append(order, "second-pre") },
    }

    stack.Use(mw1)
    stack.Use(mw2)

    // 执行
    finalHandler := func(ctx context.Context, req *ModelRequest) (*ModelResponse, error) {
        order = append(order, "core")
        return &ModelResponse{}, nil
    }

    req := &ModelRequest{}
    _, err := stack.ExecuteModelCall(context.Background(), req, finalHandler)
    require.NoError(t, err)

    // 验证顺序：first-pre → second-pre → core
    assert.Equal(t, []string{"first-pre", "second-pre", "core"}, order)
}

// testMiddleware 测试用中间件
type testMiddleware struct {
    name        string
    priority    int
    onModelCall func()
}

func (m *testMiddleware) Name() string                     { return m.name }
func (m *testMiddleware) Priority() int                    { return m.priority }
func (m *testMiddleware) OnStart(ctx context.Context) error { return nil }
func (m *testMiddleware) OnStop(ctx context.Context) error  { return nil }

func (m *testMiddleware) ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error) {
    if m.onModelCall != nil {
        m.onModelCall()
    }
    return next(ctx, req)
}

func (m *testMiddleware) ExecuteToolCall(ctx context.Context, req *ToolCallRequest, next ToolCallFunc) (*ToolCallResponse, error) {
    return next(ctx, req)
}
```

---

## 7. 实施步骤

### 7.1 阶段一：核心架构（2 天）

**任务**：
1. 创建 `pkg/middleware/middleware.go`（接口定义）
2. 创建 `pkg/middleware/base.go`（基础实现）
3. 创建 `pkg/middleware/stack.go`（中间件栈）
4. 编写单元测试 `pkg/middleware/stack_test.go`

**验证**：
- `go test ./pkg/middleware/...` 通过
- Stack 可正确排序和执行

### 7.2 阶段二：集成到 Agent（1 天）

**任务**：
1. 修改 `pkg/agent/agent_impl.go`（添加 middlewareStack 字段）
2. 修改模型调用路径（通过中间件栈）
3. 修改工具调用路径（通过中间件栈）
4. 暴露 `UseMiddleware` 等接口

**验证**：
- `go test ./pkg/agent/...` 通过
- 可注册和移除中间件

### 7.3 阶段三：示例中间件（1 天）

**任务**：
1. 实现 `pkg/middleware/summarization.go`
2. 创建 `examples/middleware/main.go`
3. 编写集成测试

**验证**：
- 长对话会触发自动摘要
- Token 数被有效压缩

---

## 8. Codex 执行指令

### 8.1 任务一：核心架构

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/P0-2-Middleware-Architecture.md 实现中间件核心架构。要求：
  1. 创建 @agentsdk-go/pkg/middleware/middleware.go（接口定义）
  2. 创建 @agentsdk-go/pkg/middleware/base.go（BaseMiddleware）
  3. 创建 @agentsdk-go/pkg/middleware/stack.go（洋葱栈实现）
  4. 创建 @agentsdk-go/pkg/middleware/stack_test.go（单元测试）
  5. 确保 go test ./pkg/middleware/... 通过
  6. 参考 @agentsdk/pkg/middleware/stack.go 的优先级排序逻辑
  7. 用中文输出实现摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### 8.2 任务二：集成到 Agent

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/P0-2-Middleware-Architecture.md 集成中间件到 Agent。要求：
  1. 修改 @agentsdk-go/pkg/agent/agent_impl.go（添加 middlewareStack 字段）
  2. 修改模型调用路径（287-307 行），通过中间件栈调用
  3. 修改工具调用路径（executeTool 方法），通过中间件栈调用
  4. 修改 @agentsdk-go/pkg/agent/agent.go 接口，新增 UseMiddleware/RemoveMiddleware/ListMiddlewares
  5. 实现接口方法
  6. 确保 go test ./pkg/agent/... 通过
  7. 用中文输出实现摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### 8.3 任务三：Summarization 中间件

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/P0-2-Middleware-Architecture.md 实现 Summarization 中间件。要求：
  1. 创建 @agentsdk-go/pkg/middleware/summarization.go（摘要中间件）
  2. 创建 @agentsdk-go/examples/middleware/main.go（使用示例）
  3. 实现 token 估算和自动摘要逻辑
  4. 保留系统提示词和最近 N 条消息
  5. 摘要失败时降级处理（不中断执行）
  6. 用中文输出实现摘要和测试结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

## 9. 验收标准

- [ ] `pkg/middleware/` 包结构完整（middleware.go + base.go + stack.go）
- [ ] Stack 可正确排序中间件（按优先级）
- [ ] 模型调用和工具调用都经过中间件链
- [ ] `go test ./pkg/middleware/...` 通过
- [ ] `go test ./pkg/agent/...` 通过
- [ ] Summarization 中间件可触发自动摘要
- [ ] 无 API 破坏性变更

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**负责人**：Claude Code
**预计工时**：4 天
