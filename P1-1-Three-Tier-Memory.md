# P1-1: 三层记忆系统实现方案

## 1. 设计原理

### 1.1 什么是三层记忆？

参考 agentsdk 的设计，三层记忆系统将 Agent 的记忆分为三个层次：

```
┌─────────────────────────────────────────────┐
│  1. Agent Memory（长期人格记忆）              │
│     - 文件：/agent.md                        │
│     - 用途：Agent 身份、规则、能力描述        │
│     - 注入：每次模型调用的 System Prompt     │
│     - 更新：通过专用工具手动更新              │
└─────────────────────────────────────────────┘
┌─────────────────────────────────────────────┐
│  2. Working Memory（短期上下文记忆）         │
│     - 作用域：thread_id / resource_id        │
│     - 用途：任务状态、临时变量、进度跟踪      │
│     - 注入：按作用域加载到 System Prompt    │
│     - 更新：LLM 通过 update_working_memory   │
└─────────────────────────────────────────────┘
┌─────────────────────────────────────────────┐
│  3. Semantic Memory（语义记忆/向量检索）     │
│     - 存储：向量数据库                        │
│     - 用途：知识库、文档、历史经验            │
│     - 注入：语义搜索 TopK 结果               │
│     - 更新：自动 embedding + 存储            │
└─────────────────────────────────────────────┘
```

### 1.2 参考实现分析

**agentsdk 的三层记忆**（`agentsdk/pkg/middleware/`）：

1. **AgentMemory** (`agent_memory.go:35-170`)
   - 懒加载 `/agent.md` 文件
   - 提供 `read_agent_memory` 和 `update_agent_memory` 工具
   - 在每次模型调用前注入到 System Prompt

2. **WorkingMemory** (`working_memory.go:37-134`)
   - 支持 thread_id 和 resource_id 作用域
   - TTL 过期机制
   - JSON Schema 校验
   - 提供 `update_working_memory` 工具

3. **SemanticMemory** (`memory/semantic.go:1-120`)
   - VectorStore 接口抽象
   - Embedder 接口抽象
   - Provenance 溯源
   - Confidence 置信度

---

## 2. 架构设计

### 2.1 目录结构

```
pkg/memory/
├── memory.go           # 核心接口定义
├── agent_memory.go     # 长期人格记忆
├── working_memory.go   # 短期上下文记忆
├── semantic_memory.go  # 语义记忆接口
├── backend.go          # 存储后端抽象
└── embedding.go        # Embedding 接口

pkg/middleware/
├── agent_memory_middleware.go      # AgentMemory 中间件
├── working_memory_middleware.go    # WorkingMemory 中间件
└── semantic_memory_middleware.go   # SemanticMemory 中间件
```

### 2.2 核心接口设计

**文件**：`pkg/memory/memory.go`

```go
package memory

import (
    "context"
    "time"
)

// AgentMemoryStore 长期人格记忆存储
type AgentMemoryStore interface {
    // Read 读取 agent 配置文件
    Read(ctx context.Context) (string, error)
    
    // Write 写入 agent 配置文件
    Write(ctx context.Context, content string) error
    
    // Exists 检查文件是否存在
    Exists(ctx context.Context) bool
}

// WorkingMemoryStore 短期上下文记忆存储
type WorkingMemoryStore interface {
    // Get 获取工作记忆
    Get(ctx context.Context, scope Scope) (*WorkingMemory, error)
    
    // Set 设置工作记忆
    Set(ctx context.Context, scope Scope, memory *WorkingMemory) error
    
    // Delete 删除工作记忆
    Delete(ctx context.Context, scope Scope) error
    
    // List 列出所有作用域
    List(ctx context.Context) ([]Scope, error)
}

// SemanticMemoryStore 语义记忆存储
type SemanticMemoryStore interface {
    // Store 存储文本（自动 embedding）
    Store(ctx context.Context, namespace, text string, metadata map[string]any) error
    
    // Recall 语义检索
    Recall(ctx context.Context, namespace, query string, topK int) ([]Memory, error)
    
    // Delete 删除命名空间
    Delete(ctx context.Context, namespace string) error
}

// Scope 作用域定义
type Scope struct {
    ThreadID   string  // 线程 ID
    ResourceID string  // 资源 ID（可选）
}

// WorkingMemory 工作记忆
type WorkingMemory struct {
    Data      map[string]any  // 数据
    Schema    *JSONSchema     // JSON Schema（可选）
    CreatedAt time.Time       // 创建时间
    UpdatedAt time.Time       // 更新时间
    TTL       time.Duration   // 过期时间
}

// Memory 语义记忆条目
type Memory struct {
    ID         string          // 唯一 ID
    Content    string          // 文本内容
    Embedding  []float64       // 向量
    Metadata   map[string]any  // 元数据
    Score      float64         // 相似度分数
    Namespace  string          // 命名空间
    Provenance *Provenance     // 溯源信息
}

// Provenance 溯源信息
type Provenance struct {
    Source    string          // 来源
    Timestamp time.Time       // 时间戳
    Agent     string          // Agent ID
}

// JSONSchema JSON Schema 定义
type JSONSchema struct {
    Type       string                 `json:"type"`
    Properties map[string]interface{} `json:"properties"`
    Required   []string               `json:"required"`
}
```

---

## 3. 实现方案

### 3.1 AgentMemory 实现

**文件**：`pkg/memory/agent_memory.go`

```go
package memory

import (
    "context"
    "fmt"
    "os"
    "path/filepath"
    "sync"
)

// FileAgentMemoryStore 基于文件的 Agent 记忆存储
type FileAgentMemoryStore struct {
    filePath string
    mu       sync.RWMutex
}

// NewFileAgentMemoryStore 创建文件存储
func NewFileAgentMemoryStore(workDir string) *FileAgentMemoryStore {
    return &FileAgentMemoryStore{
        filePath: filepath.Join(workDir, "agent.md"),
    }
}

// Read 读取 agent.md
func (s *FileAgentMemoryStore) Read(ctx context.Context) (string, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    data, err := os.ReadFile(s.filePath)
    if err != nil {
        if os.IsNotExist(err) {
            return "", fmt.Errorf("agent.md not found: %w", err)
        }
        return "", err
    }
    
    return string(data), nil
}

// Write 写入 agent.md
func (s *FileAgentMemoryStore) Write(ctx context.Context, content string) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    // 确保目录存在
    dir := filepath.Dir(s.filePath)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }
    
    return os.WriteFile(s.filePath, []byte(content), 0644)
}

// Exists 检查文件是否存在
func (s *FileAgentMemoryStore) Exists(ctx context.Context) bool {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    _, err := os.Stat(s.filePath)
    return err == nil
}
```

### 3.2 WorkingMemory 实现

**文件**：`pkg/memory/working_memory.go`

```go
package memory

import (
    "context"
    "encoding/json"
    "fmt"
    "os"
    "path/filepath"
    "sync"
    "time"
)

// FileWorkingMemoryStore 基于文件的工作记忆存储
type FileWorkingMemoryStore struct {
    dir string
    mu  sync.RWMutex
}

// NewFileWorkingMemoryStore 创建文件存储
func NewFileWorkingMemoryStore(workDir string) *FileWorkingMemoryStore {
    return &FileWorkingMemoryStore{
        dir: filepath.Join(workDir, "working_memory"),
    }
}

// Get 获取工作记忆
func (s *FileWorkingMemoryStore) Get(ctx context.Context, scope Scope) (*WorkingMemory, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    path := s.scopePath(scope)
    data, err := os.ReadFile(path)
    if err != nil {
        if os.IsNotExist(err) {
            return nil, nil // 不存在返回 nil
        }
        return nil, err
    }
    
    var wm WorkingMemory
    if err := json.Unmarshal(data, &wm); err != nil {
        return nil, err
    }
    
    // 检查 TTL
    if wm.TTL > 0 && time.Since(wm.UpdatedAt) > wm.TTL {
        return nil, nil // 已过期
    }
    
    return &wm, nil
}

// Set 设置工作记忆
func (s *FileWorkingMemoryStore) Set(ctx context.Context, scope Scope, memory *WorkingMemory) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    memory.UpdatedAt = time.Now()
    if memory.CreatedAt.IsZero() {
        memory.CreatedAt = memory.UpdatedAt
    }
    
    data, err := json.MarshalIndent(memory, "", "  ")
    if err != nil {
        return err
    }
    
    path := s.scopePath(scope)
    dir := filepath.Dir(path)
    if err := os.MkdirAll(dir, 0755); err != nil {
        return err
    }
    
    return os.WriteFile(path, data, 0644)
}

// Delete 删除工作记忆
func (s *FileWorkingMemoryStore) Delete(ctx context.Context, scope Scope) error {
    s.mu.Lock()
    defer s.mu.Unlock()
    
    path := s.scopePath(scope)
    err := os.Remove(path)
    if os.IsNotExist(err) {
        return nil // 不存在视为成功
    }
    return err
}

// List 列出所有作用域
func (s *FileWorkingMemoryStore) List(ctx context.Context) ([]Scope, error) {
    s.mu.RLock()
    defer s.mu.RUnlock()
    
    var scopes []Scope
    
    // 遍历目录
    err := filepath.Walk(s.dir, func(path string, info os.FileInfo, err error) error {
        if err != nil {
            return err
        }
        if info.IsDir() || filepath.Ext(path) != ".json" {
            return nil
        }
        
        // 从路径解析 Scope
        scope, err := s.parseScope(path)
        if err == nil {
            scopes = append(scopes, scope)
        }
        
        return nil
    })
    
    return scopes, err
}

// scopePath 生成作用域路径
func (s *FileWorkingMemoryStore) scopePath(scope Scope) string {
    if scope.ResourceID != "" {
        return filepath.Join(s.dir, scope.ThreadID, scope.ResourceID+".json")
    }
    return filepath.Join(s.dir, scope.ThreadID, "default.json")
}

// parseScope 从路径解析作用域
func (s *FileWorkingMemoryStore) parseScope(path string) (Scope, error) {
    rel, err := filepath.Rel(s.dir, path)
    if err != nil {
        return Scope{}, err
    }
    
    parts := filepath.SplitList(rel)
    if len(parts) < 1 {
        return Scope{}, fmt.Errorf("invalid path")
    }
    
    scope := Scope{ThreadID: parts[0]}
    if len(parts) > 1 {
        filename := parts[len(parts)-1]
        scope.ResourceID = filename[:len(filename)-5] // 去掉 .json
    }
    
    return scope, nil
}
```

### 3.3 SemanticMemory 接口

**文件**：`pkg/memory/semantic_memory.go`

```go
package memory

import (
    "context"
)

// SemanticMemoryStore 语义记忆存储（接口）
// 实际实现可以是向量数据库（Chroma, Pinecone, Qdrant）
type SemanticMemoryStore interface {
    Store(ctx context.Context, namespace, text string, metadata map[string]any) error
    Recall(ctx context.Context, namespace, query string, topK int) ([]Memory, error)
    Delete(ctx context.Context, namespace string) error
}

// Embedder 文本向量化接口
type Embedder interface {
    Embed(ctx context.Context, texts []string) ([][]float64, error)
}

// InMemorySemanticMemory 内存实现（简化版，用于演示）
type InMemorySemanticMemory struct {
    embedder  Embedder
    memories  map[string][]Memory
}

func NewInMemorySemanticMemory(embedder Embedder) *InMemorySemanticMemory {
    return &InMemorySemanticMemory{
        embedder: embedder,
        memories: make(map[string][]Memory),
    }
}

// Store 存储记忆（简化实现）
func (s *InMemorySemanticMemory) Store(ctx context.Context, namespace, text string, metadata map[string]any) error {
    // 生成 embedding
    embeddings, err := s.embedder.Embed(ctx, []string{text})
    if err != nil {
        return err
    }
    
    memory := Memory{
        ID:        generateID(),
        Content:   text,
        Embedding: embeddings[0],
        Metadata:  metadata,
        Namespace: namespace,
    }
    
    s.memories[namespace] = append(s.memories[namespace], memory)
    return nil
}

// Recall 语义检索（简化实现：余弦相似度）
func (s *InMemorySemanticMemory) Recall(ctx context.Context, namespace, query string, topK int) ([]Memory, error) {
    // 生成查询 embedding
    embeddings, err := s.embedder.Embed(ctx, []string{query})
    if err != nil {
        return nil, err
    }
    queryEmb := embeddings[0]
    
    // 计算相似度
    memories := s.memories[namespace]
    scored := make([]Memory, len(memories))
    for i, mem := range memories {
        scored[i] = mem
        scored[i].Score = cosineSimilarity(queryEmb, mem.Embedding)
    }
    
    // 排序并返回 TopK
    sortByScore(scored)
    if len(scored) > topK {
        scored = scored[:topK]
    }
    
    return scored, nil
}

// Delete 删除命名空间
func (s *InMemorySemanticMemory) Delete(ctx context.Context, namespace string) error {
    delete(s.memories, namespace)
    return nil
}

// 辅助函数
func cosineSimilarity(a, b []float64) float64 {
    var dotProduct, normA, normB float64
    for i := range a {
        dotProduct += a[i] * b[i]
        normA += a[i] * a[i]
        normB += b[i] * b[i]
    }
    return dotProduct / (sqrt(normA) * sqrt(normB))
}

func sortByScore(memories []Memory) {
    // 简化：使用 sort.Slice 按 Score 降序排序
}

func generateID() string {
    // 简化：生成唯一 ID
    return fmt.Sprintf("mem_%d", time.Now().UnixNano())
}
```

---

## 4. 中间件集成

### 4.1 AgentMemory 中间件

**文件**：`pkg/middleware/agent_memory_middleware.go`

```go
package middleware

import (
    "context"
    "fmt"
    
    "github.com/cexll/agentsdk-go/pkg/memory"
)

// AgentMemoryMiddleware 注入 agent.md 到 System Prompt
type AgentMemoryMiddleware struct {
    *BaseMiddleware
    store memory.AgentMemoryStore
}

func NewAgentMemoryMiddleware(store memory.AgentMemoryStore) *AgentMemoryMiddleware {
    return &AgentMemoryMiddleware{
        BaseMiddleware: NewBaseMiddleware("agent_memory", 30),
        store:          store,
    }
}

func (m *AgentMemoryMiddleware) ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error) {
    // 检查文件是否存在
    if !m.store.Exists(ctx) {
        return next(ctx, req)
    }
    
    // 读取 agent.md
    content, err := m.store.Read(ctx)
    if err != nil {
        fmt.Printf("警告：读取 agent.md 失败 - %v\n", err)
        return next(ctx, req)
    }
    
    // 注入到消息历史（作为 system 消息）
    systemMsg := model.Message{
        Role:    "system",
        Content: "# Agent 配置\n\n" + content,
    }
    
    // 在第一条消息前插入
    req.Messages = append([]model.Message{systemMsg}, req.Messages...)
    
    return next(ctx, req)
}
```

### 4.2 WorkingMemory 中间件

**文件**：`pkg/middleware/working_memory_middleware.go`

```go
package middleware

import (
    "context"
    "encoding/json"
    "fmt"
    
    "github.com/cexll/agentsdk-go/pkg/memory"
)

// WorkingMemoryMiddleware 加载工作记忆到 System Prompt
type WorkingMemoryMiddleware struct {
    *BaseMiddleware
    store memory.WorkingMemoryStore
}

func NewWorkingMemoryMiddleware(store memory.WorkingMemoryStore) *WorkingMemoryMiddleware {
    return &WorkingMemoryMiddleware{
        BaseMiddleware: NewBaseMiddleware("working_memory", 40),
        store:          store,
    }
}

func (m *WorkingMemoryMiddleware) ExecuteModelCall(ctx context.Context, req *ModelRequest, next ModelCallFunc) (*ModelResponse, error) {
    // 从 metadata 提取作用域
    threadID, _ := req.Metadata["thread_id"].(string)
    if threadID == "" {
        threadID = req.SessionID
    }
    
    scope := memory.Scope{
        ThreadID:   threadID,
        ResourceID: req.Metadata["resource_id"].(string),
    }
    
    // 加载工作记忆
    wm, err := m.store.Get(ctx, scope)
    if err != nil {
        fmt.Printf("警告：加载工作记忆失败 - %v\n", err)
        return next(ctx, req)
    }
    
    if wm != nil && len(wm.Data) > 0 {
        // 序列化为 JSON
        data, _ := json.MarshalIndent(wm.Data, "", "  ")
        
        // 注入到消息历史
        systemMsg := model.Message{
            Role:    "system",
            Content: fmt.Sprintf("# 工作记忆\n\n```json\n%s\n```", string(data)),
        }
        
        req.Messages = append([]model.Message{systemMsg}, req.Messages...)
    }
    
    return next(ctx, req)
}
```

---

## 5. 工具注册

### 5.1 update_working_memory 工具

**文件**：`pkg/tool/builtin/working_memory_tool.go`

```go
package builtin

import (
    "context"
    "fmt"
    
    "github.com/cexll/agentsdk-go/pkg/memory"
    "github.com/cexll/agentsdk-go/pkg/tool"
)

type UpdateWorkingMemoryTool struct {
    store memory.WorkingMemoryStore
}

func NewUpdateWorkingMemoryTool(store memory.WorkingMemoryStore) *UpdateWorkingMemoryTool {
    return &UpdateWorkingMemoryTool{store: store}
}

func (t *UpdateWorkingMemoryTool) Name() string {
    return "update_working_memory"
}

func (t *UpdateWorkingMemoryTool) Description() string {
    return "更新短期工作记忆，用于跟踪任务状态、临时变量等"
}

func (t *UpdateWorkingMemoryTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]any{
            "thread_id": map[string]any{
                "type":        "string",
                "description": "线程 ID",
            },
            "data": map[string]any{
                "type":        "object",
                "description": "要更新的数据（JSON 对象）",
            },
            "ttl_seconds": map[string]any{
                "type":        "integer",
                "description": "过期时间（秒），0 表示永不过期",
            },
        },
        Required: []string{"thread_id", "data"},
    }
}

func (t *UpdateWorkingMemoryTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    threadID := params["thread_id"].(string)
    data := params["data"].(map[string]any)
    ttl := time.Duration(0)
    
    if ttlSec, ok := params["ttl_seconds"].(float64); ok && ttlSec > 0 {
        ttl = time.Duration(ttlSec) * time.Second
    }
    
    scope := memory.Scope{ThreadID: threadID}
    
    // 加载现有记忆
    wm, err := t.store.Get(ctx, scope)
    if err != nil {
        return nil, err
    }
    if wm == nil {
        wm = &memory.WorkingMemory{
            Data: make(map[string]any),
        }
    }
    
    // 合并数据
    for k, v := range data {
        wm.Data[k] = v
    }
    wm.TTL = ttl
    
    // 保存
    if err := t.store.Set(ctx, scope, wm); err != nil {
        return nil, err
    }
    
    return &tool.ToolResult{
        Success: true,
        Output:  fmt.Sprintf("工作记忆已更新（thread_id=%s）", threadID),
    }, nil
}
```

---

## 6. 使用示例

**文件**：`examples/memory/main.go`

```go
package main

import (
    "context"
    "fmt"
    "log"
    
    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/memory"
    "github.com/cexll/agentsdk-go/pkg/middleware"
    "github.com/cexll/agentsdk-go/pkg/tool/builtin"
)

func main() {
    ctx := context.Background()
    workDir := "/tmp/agentsdk-test"
    
    // 创建记忆存储
    agentMemStore := memory.NewFileAgentMemoryStore(workDir)
    workingMemStore := memory.NewFileWorkingMemoryStore(workDir)
    
    // 创建 agent.md
    if err := agentMemStore.Write(ctx, `# Agent 配置

我是一个任务管理 Agent，专门帮助用户跟踪和完成任务。

## 核心能力
- 任务创建和更新
- 进度跟踪
- 优先级管理
`); err != nil {
        log.Fatal(err)
    }
    
    // 创建 Agent
    ag, _ := agent.New(agent.Config{})
    
    // 注册中间件
    ag.UseMiddleware(middleware.NewAgentMemoryMiddleware(agentMemStore))
    ag.UseMiddleware(middleware.NewWorkingMemoryMiddleware(workingMemStore))
    
    // 注册工具
    ag.AddTool(builtin.NewUpdateWorkingMemoryTool(workingMemStore))
    
    // 运行（LLM 会看到 agent.md 内容并可以更新工作记忆）
    result, _ := ag.Run(ctx, "创建一个新任务：实现三层记忆系统")
    
    fmt.Println(result.Output)
}
```

---

## 7. Codex 执行指令

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @P1-1-Three-Tier-Memory.md 实现三层记忆系统。要求：
  1. 创建 @pkg/memory/ 目录，实现核心接口（memory.go）
  2. 实现 AgentMemory（agent_memory.go）
  3. 实现 WorkingMemory（working_memory.go）
  4. 实现 SemanticMemory 接口（semantic_memory.go）
  5. 创建 AgentMemoryMiddleware（pkg/middleware/agent_memory_middleware.go）
  6. 创建 WorkingMemoryMiddleware（pkg/middleware/working_memory_middleware.go）
  7. 创建 update_working_memory 工具（pkg/tool/builtin/working_memory_tool.go）
  8. 创建示例 @examples/memory/main.go
  9. 确保 go test ./pkg/memory/... 通过
  10. 用中文输出实现摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**预计工时**：5 天
