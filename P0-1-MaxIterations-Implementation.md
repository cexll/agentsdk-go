# P0-1: MaxIterations 防护实现方案

## 1. 问题分析

### 当前状态
**文件**：`pkg/agent/agent_impl.go:243-349`

**问题代码**：
```go
// 第 244 行：无限循环，完全忽略 MaxIterations
for {
    // Check context cancellation
    if err := ctx.Err(); err != nil {
        result.StopReason = "context_cancelled"
        // ...
    }

    // Emit iteration start event
    iteration++
    // ... 模型调用 + 工具执行

    // Check stop condition: no tool calls
    if len(resp.ToolCalls) == 0 {
        result.Output = strings.TrimSpace(resp.Content)
        result.StopReason = "complete"
        break
    }

    // ❌ 问题：循环末尾无 MaxIterations 检查，可能无限执行
}
```

### 风险等级
**🔴 P0 - 生产阻塞**
- 模型异常返回连续工具调用时，Agent 会无限循环
- CPU/内存/API 配额耗尽
- 生产环境不可接受

### 影响范围
- `pkg/agent/agent_impl.go:243-349` - `runWithEmitter` 方法
- `pkg/agent/context.go:9-57` - `RunContext.MaxIterations` 定义但未使用
- `pkg/agent/result.go` - 需新增 StopReason 类型

---

## 2. 实现方案

### 2.1 修改循环控制

**文件**：`pkg/agent/agent_impl.go`

**修改位置**：第 244 行

**修改前**：
```go
// Agentic loop: continue until no tool calls
iteration := 0
for {
    // ... 现有逻辑
}
```

**修改后**：
```go
// Agentic loop: continue until no tool calls or max iterations reached
iteration := 0
maxIterations := runCtx.MaxIterations
if maxIterations <= 0 {
    maxIterations = 10  // fallback to default
}

for iteration < maxIterations {
    iteration++

    // Check context cancellation (现有逻辑保持)
    if err := ctx.Err(); err != nil {
        result.StopReason = "context_cancelled"
        // ...
    }

    // Emit iteration start event (现有逻辑保持)
    if err := appendAndEmit(progressEvent(runCtx.SessionID, "iteration_start", fmt.Sprintf("starting iteration %d/%d", iteration, maxIterations), map[string]any{
        "iteration":     iteration,
        "maxIterations": maxIterations,
    })); err != nil {
        return result, err
    }

    // ... 现有模型调用 + 工具执行逻辑 ...

    // Check stop condition: no tool calls (现有逻辑保持)
    if len(resp.ToolCalls) == 0 {
        result.Output = strings.TrimSpace(resp.Content)
        result.StopReason = "complete"
        break
    }

    // ✅ 新增：检查是否达到最大迭代次数
    if iteration >= maxIterations {
        result.Output = strings.TrimSpace(resp.Content)
        result.StopReason = "max_iterations"

        // 发出警告事件
        warnMsg := fmt.Sprintf("达到最大迭代次数 %d，强制停止", maxIterations)
        if err := appendAndEmit(errorEvent(runCtx.SessionID, "max_iterations", fmt.Errorf(warnMsg), false)); err != nil {
            return result, err
        }

        break
    }

    // 执行工具调用...（现有逻辑）
}

// 循环结束后的收尾逻辑保持不变
```

### 2.2 新增 StopReason 类型

**文件**：`pkg/agent/result.go`

**修改位置**：添加常量定义

```go
// StopReason 定义停止原因
const (
    StopReasonComplete         = "complete"          // 正常完成
    StopReasonMaxIterations    = "max_iterations"    // 达到最大迭代次数
    StopReasonContextCancelled = "context_cancelled" // 上下文取消
    StopReasonModelError       = "model_error"       // 模型错误
    StopReasonSessionError     = "session_error"     // 会话错误
    StopReasonNoModel          = "no_model"          // 无模型配置
)
```

### 2.3 修改事件消息

**文件**：`pkg/agent/agent_impl.go:260-264`

**修改位置**：iteration_start 事件

**修改前**：
```go
if err := appendAndEmit(progressEvent(runCtx.SessionID, "iteration_start", fmt.Sprintf("starting iteration %d", iteration), map[string]any{
    "iteration": iteration,
})); err != nil {
```

**修改后**：
```go
if err := appendAndEmit(progressEvent(runCtx.SessionID, "iteration_start", fmt.Sprintf("starting iteration %d/%d", iteration, maxIterations), map[string]any{
    "iteration":     iteration,
    "maxIterations": maxIterations,
})); err != nil {
```

---

## 3. 测试用例

### 3.1 单元测试

**文件**：`pkg/agent/agent_test.go`

**新增测试函数**：

```go
func TestAgent_MaxIterations(t *testing.T) {
    tests := []struct {
        name          string
        maxIterations int
        mockToolCalls int // 模拟连续返回工具调用的次数
        wantStopReason string
        wantIterations int
    }{
        {
            name:          "normal completion within limit",
            maxIterations: 10,
            mockToolCalls: 3, // 3 次工具调用后停止
            wantStopReason: "complete",
            wantIterations: 4, // 3 次工具 + 1 次最终回复
        },
        {
            name:          "hit max iterations",
            maxIterations: 5,
            mockToolCalls: 100, // 模拟无限工具调用
            wantStopReason: "max_iterations",
            wantIterations: 5,
        },
        {
            name:          "zero max iterations uses default",
            maxIterations: 0,
            mockToolCalls: 100,
            wantStopReason: "max_iterations",
            wantIterations: 10, // 默认值
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 创建 mock 模型，返回指定次数的工具调用
            mockModel := &mockModelWithEndlessTools{
                remainingToolCalls: tt.mockToolCalls,
            }

            // 创建 Agent
            ag, err := New(Config{})
            require.NoError(t, err)
            ag, err = ag.Fork(WithModel(mockModel))
            require.NoError(t, err)

            // 注册 mock 工具
            err = ag.AddTool(&mockTool{name: "test_tool"})
            require.NoError(t, err)

            // 运行
            ctx := context.Background()
            runCtx := RunContext{
                MaxIterations: tt.maxIterations,
            }
            result, err := ag.RunWithContext(ctx, "test input", runCtx)

            // 验证
            require.NoError(t, err)
            assert.Equal(t, tt.wantStopReason, result.StopReason)
            assert.LessOrEqual(t, len(result.ToolCalls), tt.wantIterations)
        })
    }
}

// mockModelWithEndlessTools 模拟返回连续工具调用的模型
type mockModelWithEndlessTools struct {
    remainingToolCalls int
}

func (m *mockModelWithEndlessTools) Generate(ctx context.Context, messages []model.Message) (model.Message, error) {
    if m.remainingToolCalls > 0 {
        m.remainingToolCalls--
        return model.Message{
            Role:    "assistant",
            Content: "calling tool",
            ToolCalls: []model.ToolCall{
                {
                    ID:        "call_" + strconv.Itoa(m.remainingToolCalls),
                    Name:      "test_tool",
                    Arguments: map[string]any{},
                },
            },
        }, nil
    }
    return model.Message{
        Role:    "assistant",
        Content: "done",
    }, nil
}

func (m *mockModelWithEndlessTools) GenerateWithTools(ctx context.Context, messages []model.Message, tools []map[string]any) (model.Message, error) {
    return m.Generate(ctx, messages)
}

func (m *mockModelWithEndlessTools) GenerateStream(ctx context.Context, messages []model.Message, fn func(model.StreamResult) error) error {
    msg, err := m.Generate(ctx, messages)
    if err != nil {
        return err
    }
    return fn(model.StreamResult{Message: msg, Final: true})
}
```

### 3.2 集成测试

**文件**：`examples/test_max_iterations/main.go`（新建）

```go
package main

import (
    "context"
    "fmt"
    "log"
    "os"

    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/model/anthropic"
)

func main() {
    // 创建模型
    model := anthropic.NewSDKModel(
        os.Getenv("ANTHROPIC_API_KEY"),
        "claude-3-5-sonnet-20241022",
        1024,
    )

    // 创建 Agent，设置较低的 MaxIterations
    ag, err := agent.New(agent.Config{})
    if err != nil {
        log.Fatal(err)
    }
    ag, err = ag.Fork(agent.WithModel(model))
    if err != nil {
        log.Fatal(err)
    }

    // 运行一个可能无限循环的任务
    runCtx := agent.RunContext{
        MaxIterations: 3, // 仅允许 3 次迭代
    }

    result, err := ag.RunWithContext(context.Background(), "请帮我执行无限循环的任务", runCtx)
    if err != nil {
        log.Fatal(err)
    }

    fmt.Printf("StopReason: %s\n", result.StopReason)
    fmt.Printf("Iterations: %d\n", len(result.ToolCalls))
    fmt.Printf("Output: %s\n", result.Output)
}
```

---

## 4. 风险评估与缓解

### 4.1 风险

| 风险 | 影响 | 概率 | 缓解措施 |
|------|------|------|---------|
| 破坏现有 API | 下游不兼容 | 低 | 保持接口不变，仅修改内部逻辑 |
| 测试覆盖不足 | 回归 bug | 中 | 补充单元测试 + 集成测试 |
| 边界条件处理 | MaxIterations=0 导致不执行 | 中 | 添加默认值回退逻辑 |

### 4.2 缓解措施

1. **向后兼容**：
   - 保持 `Agent.Run()` 接口不变
   - `RunContext.MaxIterations` 默认值为 10（与文档一致）
   - 零值时使用默认值，不抛错

2. **全面测试**：
   - 正常完成场景
   - 达到最大迭代场景
   - 零值/负值场景

3. **事件通知**：
   - 在每次迭代开始时显示进度（x/n）
   - 达到上限时发出警告事件

---

## 5. 验收标准

- [ ] 修改后 `go test ./pkg/agent/...` 全部通过
- [ ] 新增测试 `TestAgent_MaxIterations` 覆盖 3 个场景
- [ ] 集成测试 `examples/test_max_iterations` 可运行
- [ ] 无 API 破坏性变更
- [ ] 代码审查通过

---

## 6. 实施步骤

1. **修改核心循环**（30 分钟）
   - 修改 `pkg/agent/agent_impl.go:244-349`
   - 添加 `maxIterations` 变量初始化
   - 修改 `for` 循环条件
   - 添加迭代上限检查

2. **新增 StopReason 常量**（10 分钟）
   - 修改 `pkg/agent/result.go`
   - 定义 `StopReasonMaxIterations`

3. **编写单元测试**（30 分钟）
   - 新增 `TestAgent_MaxIterations`
   - 实现 `mockModelWithEndlessTools`

4. **编写集成测试**（20 分钟）
   - 创建 `examples/test_max_iterations/main.go`

5. **验证与提交**（10 分钟）
   - 运行 `go test ./...`
   - 运行集成测试
   - 提交代码

**总计：1.5 小时**

---

## 7. Codex 执行指令

```bash
# 执行任务
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/P0-1-MaxIterations-Implementation.md 实现 MaxIterations 防护功能。要求：
  1. 修改 @agentsdk-go/pkg/agent/agent_impl.go 的 runWithEmitter 方法（244-349 行），添加迭代上限检查
  2. 修改 @agentsdk-go/pkg/agent/result.go，新增 StopReasonMaxIterations 常量
  3. 编写单元测试 TestAgent_MaxIterations 到 @agentsdk-go/pkg/agent/agent_test.go
  4. 确保修改后 go test ./pkg/agent/... 通过
  5. 保持向后兼容，MaxIterations=0 时使用默认值 10
  6. 用中文输出修改摘要和验证结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**负责人**：Claude Code
