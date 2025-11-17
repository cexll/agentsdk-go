# P1-2: Bookmark 断点续播实现方案

## 1. 设计原理

### 1.1 什么是 Bookmark？

**参考 Kode-agent-sdk** (`src/core/events.ts:149-186`):

```typescript
// 每个事件带单调递增书签
event.bookmark = {
    seq: 42,              // 序列号
    timestamp: Date.now()  // 时间戳
}

// 持久化 Store，支持 since 书签重放
subscribe({ since: lastBookmark })
```

**核心价值**：
- 崩溃后可从断点恢复
- 调试时可回放事件流
- 分布式系统可同步进度

### 1.2 当前状态

**已有**：
- `pkg/event/bus.go` - 三通道事件总线
- `pkg/session/wal.go` - WAL 持久化

**缺失**：
- ❌ 事件无序列号
- ❌ 无法回放历史事件
- ❌ Subscribe 不支持 since 参数

---

## 2. 架构设计

### 2.1 核心接口

**文件**：`pkg/event/bookmark.go`

```go
package event

import "time"

// Bookmark 断点标记
type Bookmark struct {
    Seq       int64      `json:"seq"`        // 单调递增序列号
    Timestamp time.Time  `json:"timestamp"`  // 时间戳
}

// Event 扩展（添加 Bookmark 字段）
type Event struct {
    ID        string      // 事件 ID
    Type      EventType   // 事件类型
    Timestamp time.Time   // 时间戳
    SessionID string      // 会话 ID
    Data      interface{} // 事件数据
    Bookmark  Bookmark    // ✅ 新增：断点标记
}

// EventStore 事件持久化接口
type EventStore interface {
    // Append 追加事件
    Append(event Event) error
    
    // ReadSince 从指定书签开始读取
    ReadSince(bookmark *Bookmark) ([]Event, error)
    
    // ReadRange 读取指定范围
    ReadRange(start, end *Bookmark) ([]Event, error)
    
    // LastBookmark 获取最后一个书签
    LastBookmark() (*Bookmark, error)
}
```

### 2.2 EventBus 扩展

**文件**：`pkg/event/bus.go`（修改）

```go
type EventBus struct {
    progress chan Event
    control  chan Event
    monitor  chan Event
    
    // ✅ 新增
    seq        int64          // 序列号计数器
    seqMu      sync.Mutex     // 序列号互斥锁
    store      EventStore     // 事件存储
    storeAsync bool           // 异步写入
}

// Emit 发送事件（添加 Bookmark）
func (b *EventBus) Emit(event Event) error {
    // 生成序列号
    b.seqMu.Lock()
    b.seq++
    event.Bookmark = Bookmark{
        Seq:       b.seq,
        Timestamp: time.Now(),
    }
    b.seqMu.Unlock()
    
    // 持久化
    if b.store != nil {
        if b.storeAsync {
            go b.store.Append(event)
        } else {
            if err := b.store.Append(event); err != nil {
                // 降级：写入失败仍发送事件
                fmt.Printf("警告：事件持久化失败 - %v\n", err)
            }
        }
    }
    
    // 分发到通道
    switch event.Type {
    case EventProgress, EventThinking, EventToolCall, EventToolResult:
        b.progress <- event
    case EventApprovalReq, EventApprovalResp, EventInterrupt, EventResume:
        b.control <- event
    case EventMetrics, EventAudit, EventError:
        b.monitor <- event
    }
    
    return nil
}

// SubscribeSince 从指定书签开始订阅（✅ 新增）
func (b *EventBus) SubscribeSince(bookmark *Bookmark) (<-chan Event, error) {
    if b.store == nil {
        return nil, fmt.Errorf("event store not configured")
    }
    
    // 读取历史事件
    events, err := b.store.ReadSince(bookmark)
    if err != nil {
        return nil, err
    }
    
    // 创建 channel 并先发送历史事件
    ch := make(chan Event, 100)
    go func() {
        for _, evt := range events {
            ch <- evt
        }
        // 历史事件发送完毕后，继续订阅实时事件
        // TODO: 连接到实时流
    }()
    
    return ch, nil
}
```

### 2.3 FileEventStore 实现

**文件**：`pkg/event/file_store.go`（新建）

```go
package event

import (
    "bufio"
    "encoding/json"
    "os"
    "sync"
)

// FileEventStore 基于文件的事件存储
type FileEventStore struct {
    path string
    file *os.File
    mu   sync.Mutex
}

func NewFileEventStore(path string) (*FileEventStore, error) {
    file, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_RDWR, 0644)
    if err != nil {
        return nil, err
    }
    
    return &FileEventStore{
        path: path,
        file: file,
    }, nil
}

// Append 追加事件（JSONL 格式）
func (s *FileEventStore) Append(event Event) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    data, err := json.Marshal(event)
    if err != nil {
        return err
    }
    
    _, err = s.file.Write(append(data, '\n'))
    return err
}

// ReadSince 从指定书签开始读取
func (s *FileEventStore) ReadSince(bookmark *Bookmark) ([]Event, error) {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // 重新打开文件读取
    f, err := os.Open(s.path)
    if err != nil {
        return nil, err
    }
    defer f.Close()
    
    var events []Event
    scanner := bufio.NewScanner(f)
    
    for scanner.Scan() {
        var evt Event
        if err := json.Unmarshal(scanner.Bytes(), &evt); err != nil {
            continue
        }
        
        // 过滤：只返回序列号大于 bookmark 的事件
        if bookmark == nil || evt.Bookmark.Seq > bookmark.Seq {
            events = append(events, evt)
        }
    }
    
    return events, scanner.Err()
}

// LastBookmark 获取最后一个书签
func (s *FileEventStore) LastBookmark() (*Bookmark, error) {
    events, err := s.ReadSince(nil)
    if err != nil || len(events) == 0 {
        return nil, err
    }
    
    last := events[len(events)-1]
    return &last.Bookmark, nil
}

func (s *FileEventStore) Close() error {
    return s.file.Close()
}
```

---

## 3. Agent Resume 支持

**文件**：`pkg/agent/agent.go`（扩展接口）

```go
type Agent interface {
    // ... 现有方法
    
    // ✅ 新增：Resume 从书签恢复
    Resume(ctx context.Context, bookmark *Bookmark) (*RunResult, error)
}
```

**实现**：`pkg/agent/agent_impl.go`

```go
func (a *basicAgent) Resume(ctx context.Context, bookmark *Bookmark) (*RunResult, error) {
    // 1. 从事件存储加载历史事件
    events, err := a.eventBus.store.ReadSince(bookmark)
    if err != nil {
        return nil, err
    }
    
    // 2. 重放到 Session
    for _, evt := range events {
        if evt.Type == EventToolResult {
            // 将工具结果写回 session
            // TODO: 解析 evt.Data 并 Append
        }
    }
    
    // 3. 获取最后一个用户输入，继续执行
    // TODO: 从 session 重建上下文
    
    return a.Run(ctx, "继续执行")
}
```

---

## 4. 使用示例

**文件**：`examples/bookmark/main.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/event"
)

func main() {
    ctx := context.Background()
    
    // 创建带事件存储的 Agent
    eventStore, _ := event.NewFileEventStore("/tmp/events.jsonl")
    defer eventStore.Close()
    
    ag, _ := agent.New(agent.Config{
        EventStore: eventStore,
    })
    
    // 第一次运行
    result, err := ag.Run(ctx, "执行长任务")
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("StopReason: %s\n", result.StopReason)
    
    // 获取最后一个书签
    lastBookmark, _ := eventStore.LastBookmark()
    fmt.Printf("LastBookmark: seq=%d\n", lastBookmark.Seq)
    
    // 模拟崩溃后恢复
    fmt.Println("\n--- 模拟崩溃后恢复 ---")
    
    // 从书签恢复
    resumeResult, err := ag.Resume(ctx, lastBookmark)
    if err != nil {
        log.Fatal(err)
    }
    
    fmt.Printf("恢复后输出: %s\n", resumeResult.Output)
}
```

---

## 5. Codex 执行指令

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @P1-2-Bookmark-Resume.md 实现 Bookmark 断点续播功能。要求：
  1. 创建 @pkg/event/bookmark.go（Bookmark 类型定义）
  2. 修改 @pkg/event/event.go（Event 添加 Bookmark 字段）
  3. 修改 @pkg/event/bus.go（Emit 添加序列号生成和持久化）
  4. 创建 @pkg/event/file_store.go（FileEventStore 实现）
  5. 在 @pkg/event/bus.go 添加 SubscribeSince 方法
  6. 在 @pkg/agent/agent.go 添加 Resume 接口
  7. 在 @pkg/agent/agent_impl.go 实现 Resume 方法
  8. 创建示例 @examples/bookmark/main.go
  9. 确保 go test ./pkg/event/... 通过
  10. 用中文输出实现摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**预计工时**：3 天
