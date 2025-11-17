# P1-3: 崩溃自愈机制实现方案

## 1. 设计原理

### 1.1 参考 Kode-agent-sdk 的自愈机制

**核心机制**（`Kode-agent-sdk/src/core/agent.ts`）：

1. **处理超时自保** (line 171, 780)
   ```typescript
   // 处理超时 5 分钟自动重启
   if (Date.now() - processingStart > 300000) {
       await this.restartProcessing()
   }
   ```

2. **工具执行超时** (line 1161)
   ```typescript
   const toolPromise = tool.execute(params)
   const timeoutPromise = sleep(toolTimeout)
   const result = await Promise.race([toolPromise, timeoutPromise])
   ```

3. **autoSeal 自动封口** (line 1410-1436)
   ```typescript
   // 崩溃模式：自动封口未完成的工具调用
   for (const toolCall of pendingToolCalls) {
       messages.push({
           role: 'user',
           content: [{
               type: 'tool_result',
               tool_use_id: toolCall.id,
               content: 'ERROR: Process interrupted',
               is_error: true
           }]
       })
   }
   ```

---

## 2. 实现方案

### 2.1 工具执行超时

**文件**：`pkg/agent/agent_impl.go`（修改 executeTool）

```go
// executeTool 执行工具（添加超时控制）
func (a *basicAgent) executeTool(ctx context.Context, name string, args map[string]any, timeout time.Duration) (*ToolCall, error) {
    // 默认超时 5 分钟
    if timeout == 0 {
        timeout = 5 * time.Minute
    }
    
    // 创建超时 context
    toolCtx, cancel := context.WithTimeout(ctx, timeout)
    defer cancel()
    
    // 通过中间件执行工具
    req := &middleware.ToolCallRequest{
        Name:      name,
        Arguments: args,
        SessionID: "", // TODO: 从上下文获取
        Metadata:  make(map[string]any),
    }
    
    // 使用 channel 接收结果（支持超时）
    resultCh := make(chan *middleware.ToolCallResponse, 1)
    errCh := make(chan error, 1)
    
    go func() {
        resp, err := a.middlewareStack.ExecuteToolCall(toolCtx, req, a.finalToolHandler)
        if err != nil {
            errCh <- err
        } else {
            resultCh <- resp
        }
    }()
    
    // 等待结果或超时
    select {
    case resp := <-resultCh:
        return &ToolCall{
            ID:        generateToolCallID(),
            Name:      name,
            Arguments: args,
            Output:    resp.Output,
            Error:     resp.Error,
        }, nil
        
    case err := <-errCh:
        return nil, err
        
    case <-toolCtx.Done():
        // 超时
        return &ToolCall{
            ID:        generateToolCallID(),
            Name:      name,
            Arguments: args,
            Output:    fmt.Sprintf("工具执行超时（%v）", timeout),
            Error:     fmt.Errorf("tool execution timeout"),
        }, nil
    }
}
```

### 2.2 autoSeal 自动封口

**文件**：`pkg/agent/recovery.go`（新建）

```go
package agent

import (
    "context"
    "fmt"
    
    "github.com/cexll/agentsdk-go/pkg/session"
)

// RecoveryManager 恢复管理器
type RecoveryManager struct {
    session session.Session
}

// NewRecoveryManager 创建恢复管理器
func NewRecoveryManager(sess session.Session) *RecoveryManager {
    return &RecoveryManager{session: sess}
}

// AutoSeal 自动封口未完成的工具调用
func (r *RecoveryManager) AutoSeal(ctx context.Context) error {
    // 1. 扫描 session 查找未完成的工具调用
    messages, err := r.session.List(session.Filter{})
    if err != nil {
        return err
    }
    
    pendingTools := r.findPendingToolCalls(messages)
    
    // 2. 为每个未完成的工具调用添加错误结果
    for _, tc := range pendingTools {
        errorMsg := session.Message{
            Role:    "user",
            Content: fmt.Sprintf("工具调用 %s(%s) 因进程中断而失败", tc.Name, tc.ID),
            ToolCalls: []session.ToolCall{
                {
                    ID:     tc.ID,
                    Name:   tc.Name,
                    Output: "ERROR: Process interrupted",
                    Error:  fmt.Errorf("process crashed before tool completion"),
                },
            },
        }
        
        if err := r.session.Append(errorMsg); err != nil {
            return err
        }
    }
    
    return nil
}

// findPendingToolCalls 查找未完成的工具调用
func (r *RecoveryManager) findPendingToolCalls(messages []session.Message) []session.ToolCall {
    var pending []session.ToolCall
    completed := make(map[string]bool)
    
    // 第一遍：标记已完成的工具
    for _, msg := range messages {
        for _, tc := range msg.ToolCalls {
            if tc.Output != "" || tc.Error != nil {
                completed[tc.ID] = true
            }
        }
    }
    
    // 第二遍：找出未完成的
    for _, msg := range messages {
        if msg.Role == "assistant" {
            for _, tc := range msg.ToolCalls {
                if !completed[tc.ID] {
                    pending = append(pending, tc)
                }
            }
        }
    }
    
    return pending
}
```

### 2.3 处理超时监控

**文件**：`pkg/agent/watchdog.go`（新建）

```go
package agent

import (
    "context"
    "fmt"
    "sync"
    "time"
)

// Watchdog 监控 Agent 处理超时
type Watchdog struct {
    timeout       time.Duration
    onTimeout     func()
    lastHeartbeat time.Time
    mu            sync.RWMutex
    cancel        context.CancelFunc
}

// NewWatchdog 创建监控器
func NewWatchdog(timeout time.Duration, onTimeout func()) *Watchdog {
    return &Watchdog{
        timeout:       timeout,
        onTimeout:     onTimeout,
        lastHeartbeat: time.Now(),
    }
}

// Start 启动监控
func (w *Watchdog) Start(ctx context.Context) {
    ctx, w.cancel = context.WithCancel(ctx)
    
    go func() {
        ticker := time.NewTicker(30 * time.Second) // 每 30 秒检查一次
        defer ticker.Stop()
        
        for {
            select {
            case <-ticker.C:
                w.check()
            case <-ctx.Done():
                return
            }
        }
    }()
}

// Stop 停止监控
func (w *Watchdog) Stop() {
    if w.cancel != nil {
        w.cancel()
    }
}

// Heartbeat 更新心跳
func (w *Watchdog) Heartbeat() {
    w.mu.Lock()
    defer w.mu.Unlock()
    w.lastHeartbeat = time.Now()
}

// check 检查是否超时
func (w *Watchdog) check() {
    w.mu.RLock()
    elapsed := time.Since(w.lastHeartbeat)
    w.mu.RUnlock()
    
    if elapsed > w.timeout {
        fmt.Printf("警告：处理超时（%v），触发恢复\n", elapsed)
        if w.onTimeout != nil {
            w.onTimeout()
        }
    }
}
```

### 2.4 集成到 Agent

**文件**：`pkg/agent/agent_impl.go`（修改）

```go
type basicAgent struct {
    // ... 现有字段
    
    // ✅ 新增
    recoveryMgr *RecoveryManager
    watchdog    *Watchdog
}

// runWithEmitter 添加心跳和恢复逻辑
func (a *basicAgent) runWithEmitter(...) (*RunResult, error) {
    // 启动监控器
    if a.watchdog != nil {
        a.watchdog.Start(ctx)
        defer a.watchdog.Stop()
    }
    
    // 检查是否需要恢复
    if a.recoveryMgr != nil {
        if err := a.recoveryMgr.AutoSeal(ctx); err != nil {
            fmt.Printf("警告：自动封口失败 - %v\n", err)
        }
    }
    
    // 主循环
    for iteration < maxIterations {
        // 更新心跳
        if a.watchdog != nil {
            a.watchdog.Heartbeat()
        }
        
        // ... 现有逻辑
    }
    
    return result, nil
}
```

---

## 3. 使用示例

**文件**：`examples/recovery/main.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    "time"
    
    "github.com/cexll/agentsdk-go/pkg/agent"
)

func main() {
    ctx := context.Background()
    
    // 创建带恢复机制的 Agent
    ag, err := agent.New(
        agent.Config{
            EnableRecovery:  true,
            WatchdogTimeout: 10 * time.Minute,
            ToolTimeout:     30 * time.Second,
        },
    )
    if err != nil {
        log.Fatal(err)
    }
    
    // 运行任务
    result, err := ag.Run(ctx, "执行可能超时的任务")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("结果: %s\n", result.Output)
}
```

---

## 4. Codex 执行指令

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @P1-3-Crash-Recovery.md 实现崩溃自愈机制。要求：
  1. 修改 @pkg/agent/agent_impl.go 的 executeTool 方法，添加超时控制
  2. 创建 @pkg/agent/recovery.go（RecoveryManager + autoSeal）
  3. 创建 @pkg/agent/watchdog.go（Watchdog 监控器）
  4. 在 @pkg/agent/agent_impl.go 集成 RecoveryManager 和 Watchdog
  5. 在 @pkg/agent/config.go 添加恢复相关配置项
  6. 创建示例 @examples/recovery/main.go
  7. 确保 go test ./pkg/agent/... 通过
  8. 用中文输出实现摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**预计工时**：2 天
