# 质量优化计划（Short-term）

**目标**：将 agentsdk-go 测试覆盖率从 76% 提升到 90%
**预计时间**：1-2 周
**优先级**：P0（生产就绪前的质量保障）

---

## 现状评估

### 当前测试覆盖率

| 模块 | 当前覆盖率 | 目标覆盖率 | 差距 | 优先级 |
|------|-----------|-----------|------|--------|
| **pkg/agent** | 76.0% | 90% | +14% | P0 |
| **pkg/security** | 79.3% | 70% | ✅ 已达标 | - |
| pkg/middleware | 90.6% | 90% | ✅ 已达标 | - |
| pkg/approval | 90.6% | 90% | ✅ 已达标 | - |
| pkg/event | 85.0% | 85% | ✅ 已达标 | - |

### 关键发现

✅ **TestServerStreamBroadcast** 现在通过（之前是偶发性失败，可能是竞态条件已修复）

⚠️ **pkg/agent 有 10 个函数覆盖率 < 80%**，其中 6 个为 0%：
- `UseMiddleware`: 0.0%
- `RemoveMiddleware`: 0.0%
- `ListMiddlewares`: 0.0%
- `watchdogHandler`: 0.0%
- `generateResponse`: 0.0%
- `buildModelMessages`: 0.0%

---

## 任务分解

### Task 1: 中间件管理测试 (P0)

**目标**：覆盖 `UseMiddleware`, `RemoveMiddleware`, `ListMiddlewares`

**测试用例**：
```go
// pkg/agent/agent_middleware_test.go (新建)

func TestAgent_UseMiddleware(t *testing.T) {
    tests := []struct {
        name       string
        middleware []middleware.Middleware
        wantCount  int
    }{
        {
            name:       "single middleware",
            middleware: []middleware.Middleware{newTestMiddleware("test", 50)},
            wantCount:  1,
        },
        {
            name: "multiple middlewares with priority",
            middleware: []middleware.Middleware{
                newTestMiddleware("low", 10),
                newTestMiddleware("high", 90),
                newTestMiddleware("mid", 50),
            },
            wantCount: 3,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ag, _ := agent.New(agent.Config{})
            for _, mw := range tt.middleware {
                ag.UseMiddleware(mw)
            }

            list := ag.ListMiddlewares()
            assert.Len(t, list, tt.wantCount)

            // 验证排序（高优先级在前）
            if len(list) > 1 {
                for i := 0; i < len(list)-1; i++ {
                    assert.GreaterOrEqual(t, list[i].Priority(), list[i+1].Priority())
                }
            }
        })
    }
}

func TestAgent_RemoveMiddleware(t *testing.T) {
    ag, _ := agent.New(agent.Config{})

    mw := newTestMiddleware("test", 50)
    ag.UseMiddleware(mw)

    // 移除存在的中间件
    removed := ag.RemoveMiddleware("test")
    assert.True(t, removed)
    assert.Len(t, ag.ListMiddlewares(), 0)

    // 移除不存在的中间件
    removed = ag.RemoveMiddleware("nonexistent")
    assert.False(t, removed)
}
```

**预计时间**：2 小时

---

### Task 2: Resume 测试 (P0)

**目标**：将 `Resume` 覆盖率从 66.7% 提升到 90%+

**测试用例**：
```go
// pkg/agent/agent_resume_test.go (新建)

func TestAgent_Resume(t *testing.T) {
    tests := []struct {
        name        string
        events      []event.Event
        bookmark    *event.Bookmark
        wantErr     bool
        wantReason  string
    }{
        {
            name: "resume from bookmark",
            events: []event.Event{
                event.NewEvent(event.EventProgress, "sess", "step1"),
                event.NewEvent(event.EventProgress, "sess", "step2"),
                event.NewEvent(event.EventCompletion, "sess", nil),
            },
            bookmark: &event.Bookmark{Seq: 2},
            wantErr: false,
            wantReason: "complete",
        },
        {
            name: "resume with nil bookmark",
            events: []event.Event{},
            bookmark: nil,
            wantErr: false,
        },
        {
            name: "resume with empty event store",
            events: []event.Event{},
            bookmark: &event.Bookmark{Seq: 1},
            wantErr: false,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            // 创建带事件存储的 Agent
            eventStore := &mockEventStore{events: tt.events}
            ag, _ := agent.New(agent.Config{}, agent.WithEventStore(eventStore))

            result, err := ag.Resume(context.Background(), tt.bookmark)

            if tt.wantErr {
                assert.Error(t, err)
            } else {
                assert.NoError(t, err)
                if tt.wantReason != "" {
                    assert.Equal(t, tt.wantReason, result.StopReason)
                }
            }
        })
    }
}
```

**预计时间**：3 小时

---

### Task 3: RunWithEmitter 边界测试 (P1)

**目标**：将 `runWithEmitter` 覆盖率从 58.8% 提升到 85%+

**未覆盖的边界情况**：
1. Context 取消
2. MaxIterations 边界值（0, 负数）
3. 模型返回空 ToolCalls
4. Session 保存失败
5. Middleware 链执行错误

**测试用例**：
```go
func TestAgent_RunWithEmitter_ContextCancellation(t *testing.T) {
    ctx, cancel := context.WithCancel(context.Background())

    ag, _ := agent.New(agent.Config{})
    ag, _ = ag.Fork(agent.WithModel(&slowMockModel{delay: 5 * time.Second}))

    go func() {
        time.Sleep(100 * time.Millisecond)
        cancel()
    }()

    result, err := ag.Run(ctx, "test")
    assert.NoError(t, err)
    assert.Equal(t, "context_cancelled", result.StopReason)
}

func TestAgent_RunWithEmitter_MaxIterationsEdgeCases(t *testing.T) {
    tests := []struct {
        name          string
        maxIterations int
        wantDefault   int
    }{
        {"zero uses default", 0, 10},
        {"negative uses default", -1, 10},
        {"explicit value", 5, 5},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            ag, _ := agent.New(agent.Config{
                DefaultContext: agent.RunContext{
                    MaxIterations: tt.maxIterations,
                },
            })

            ag, _ = ag.Fork(agent.WithModel(&endlessMockModel{}))

            result, _ := ag.Run(context.Background(), "test")
            assert.Equal(t, "max_iterations", result.StopReason)
            assert.LessOrEqual(t, len(result.ToolCalls), tt.wantDefault)
        })
    }
}
```

**预计时间**：4 小时

---

### Task 4: 崩溃恢复测试 (P1)

**目标**：覆盖 `watchdogHandler`, `autoSealPending`, `configureRecoveryComponents`

**测试用例**：
```go
// pkg/agent/agent_recovery_test.go (新建)

func TestAgent_WatchdogHandler(t *testing.T) {
    ag, _ := agent.New(agent.Config{
        EnableRecovery: true,
        WatchdogTimeout: 100 * time.Millisecond,
    })

    ag, _ = ag.Fork(agent.WithModel(&slowMockModel{delay: 5 * time.Second}))

    result, err := ag.Run(context.Background(), "test")

    assert.NoError(t, err)
    assert.Equal(t, "context_cancelled", result.StopReason)
}

func TestAgent_AutoSealPending(t *testing.T) {
    // 创建有未完成工具调用的 session
    sess, _ := session.NewMemorySession("test")
    sess.Append(session.Message{
        Role: "assistant",
        ToolCalls: []session.ToolCall{
            {ID: "call_1", Name: "test_tool", Arguments: map[string]any{}},
        },
    })

    ag, _ := agent.New(agent.Config{
        EnableRecovery: true,
    }, agent.WithSession(sess))

    // autoSeal 应该在启动时自动执行
    messages, _ := sess.List(session.Filter{})

    // 验证最后一条消息是封口消息
    lastMsg := messages[len(messages)-1]
    assert.Equal(t, "user", lastMsg.Role)
    assert.Contains(t, lastMsg.Content, "工具调用")
    assert.Contains(t, lastMsg.Content, "因进程中断而失败")
}
```

**预计时间**：3 小时

---

### Task 5: 内部辅助函数测试 (P2)

**目标**：覆盖 `generateResponse`, `buildModelMessages`

**测试用例**：
```go
func TestAgent_GenerateResponse(t *testing.T) {
    ag, _ := agent.New(agent.Config{})
    ag, _ = ag.Fork(agent.WithModel(&mockModel{
        response: "test response",
    }))

    // 通过反射或其他方式测试内部方法
    // 或者通过集成测试覆盖
}

func TestAgent_BuildModelMessages(t *testing.T) {
    sess, _ := session.NewMemorySession("test")
    sess.Append(session.Message{Role: "user", Content: "hello"})
    sess.Append(session.Message{Role: "assistant", Content: "hi"})

    ag, _ := agent.New(agent.Config{}, agent.WithSession(sess))

    // 通过 Run 间接测试 buildModelMessages
    result, _ := ag.Run(context.Background(), "test")
    assert.NotEmpty(t, result.Output)
}
```

**预计时间**：2 小时

---

### Task 6: 审批队列 GC 机制 (P2)

**当前状态**：已实现但无自动 GC

**改进计划**：
```go
// pkg/approval/record_gc.go (修改)

// StartAutoGC 启动自动 GC 后台任务
func (l *RecordLog) StartAutoGC(interval time.Duration) {
    l.gcMu.Lock()
    defer l.gcMu.Unlock()

    if l.gcTicker != nil {
        return // 已启动
    }

    l.gcTicker = time.NewTicker(interval)
    l.gcStop = make(chan struct{})

    go func() {
        for {
            select {
            case <-l.gcTicker.C:
                l.RunGC()
            case <-l.gcStop:
                return
            }
        }
    }()
}

// StopAutoGC 停止自动 GC
func (l *RecordLog) StopAutoGC() {
    l.gcMu.Lock()
    defer l.gcMu.Unlock()

    if l.gcTicker != nil {
        l.gcTicker.Stop()
        close(l.gcStop)
        l.gcTicker = nil
    }
}
```

**测试用例**：
```go
func TestRecordLog_AutoGC(t *testing.T) {
    log, _ := approval.NewRecordLog("/tmp/test-gc")
    defer log.Close()

    log.ConfigureGC(
        approval.WithRetentionCount(5),
        approval.WithGCInterval(100 * time.Millisecond),
    )

    // 添加 10 条记录
    for i := 0; i < 10; i++ {
        log.Append(approval.Record{SessionID: "test", Decision: "approved"})
    }

    // 等待自动 GC
    time.Sleep(200 * time.Millisecond)

    status := log.GCStatus()
    assert.Greater(t, status.Runs, int64(0))
    assert.Greater(t, status.TotalDropped, int64(0))
}
```

**预计时间**：3 小时

---

## 实施计划

### Week 1 (P0 任务)

**Day 1-2**: Task 1 - 中间件管理测试
- 创建 `pkg/agent/agent_middleware_test.go`
- 覆盖 UseMiddleware/RemoveMiddleware/ListMiddlewares
- 目标：+5% 覆盖率

**Day 3-4**: Task 2 - Resume 测试
- 创建 `pkg/agent/agent_resume_test.go`
- 覆盖所有 Resume 边界情况
- 目标：+3% 覆盖率

**Day 5**: Task 3 (Part 1) - RunWithEmitter 边界测试
- Context 取消测试
- MaxIterations 边界值测试
- 目标：+4% 覆盖率

### Week 2 (P1/P2 任务)

**Day 1-2**: Task 3 (Part 2) - RunWithEmitter 完整测试
- Session 错误处理
- Middleware 链错误
- 目标：+2% 覆盖率

**Day 3**: Task 4 - 崩溃恢复测试
- Watchdog 超时测试
- AutoSeal 测试
- 目标：+3% 覆盖率

**Day 4**: Task 5 - 内部辅助函数测试
- generateResponse
- buildModelMessages
- 目标：+2% 覆盖率

**Day 5**: Task 6 - 审批队列 GC
- 实现自动 GC
- 补充测试
- 文档更新

---

## 验收标准

- [ ] pkg/agent 覆盖率 >= 90%
- [ ] 所有新测试通过（`go test ./pkg/agent/...`）
- [ ] 无竞态条件（`go test -race ./pkg/agent/...`）
- [ ] 代码审查通过
- [ ] 更新 QUALITY_REPORT.md

---

## Codex 执行指令

### Task 1: 中间件管理测试

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "为 @agentsdk-go/pkg/agent 补充中间件管理测试。要求：
  1. 创建 pkg/agent/agent_middleware_test.go
  2. 测试 UseMiddleware (单个、多个、优先级排序)
  3. 测试 RemoveMiddleware (存在、不存在)
  4. 测试 ListMiddlewares (空列表、排序验证)
  5. 使用 testify/assert
  6. 确保 go test ./pkg/agent/... 通过
  7. 用中文输出测试结果和覆盖率提升" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### Task 2: Resume 测试

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "为 @agentsdk-go/pkg/agent 补充 Resume 测试。要求：
  1. 创建 pkg/agent/agent_resume_test.go
  2. 测试从 bookmark 恢复
  3. 测试 nil bookmark 处理
  4. 测试空事件存储
  5. 测试事件重放逻辑
  6. 覆盖率目标：Resume 函数 > 90%
  7. 用中文输出测试结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### Task 3: RunWithEmitter 边界测试

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "为 @agentsdk-go/pkg/agent 补充 runWithEmitter 边界测试。要求：
  1. 添加到 pkg/agent/agent_test.go
  2. 测试 Context 取消
  3. 测试 MaxIterations 边界值 (0, -1, 正常值)
  4. 测试模型返回空 ToolCalls
  5. 测试 Session 保存失败
  6. 覆盖率目标：runWithEmitter > 85%
  7. 用中文输出测试结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

**计划版本**：v1.0
**创建时间**：2025-11-17
**预计完成**：2025-12-01
**负责人**：Claude Code + Codex
