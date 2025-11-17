# 编译错误修复方案

## 问题诊断

**日期**：2025-11-17
**优先级**：P0（阻塞编译）
**影响范围**：examples/、cmd/agentctl

---

## 错误清单

### Error 1: examples 重复 main 函数
**错误信息**：
```
examples/v03_demo.go:13:6: main redeclared in this block
examples/v03_features.go:15:6: main redeclared in this block
examples/v03_validation.go:14:6: main redeclared in this block
```

**根因**：4 个文件在同一个 `package main` 下都定义了 `main()` 函数：
- `examples/demo_tools.go`
- `examples/v03_demo.go`
- `examples/v03_features.go`
- `examples/v03_validation.go`

**解决方案**：将 v03_*.go 移动到子目录
```bash
mkdir -p examples/v03
mv examples/v03_demo.go examples/v03/demo.go
mv examples/v03_features.go examples/v03/features.go
mv examples/v03_validation.go examples/v03/validation.go
```

---

### Error 2: v03_demo.go API 不匹配

#### 2.1 Whitelist.Add 参数不匹配
**错误信息**：
```
examples/v03_demo.go:41:38: not enough arguments in call to wl.Add
    have (string, string, map[string]interface{})
    want (string, string, map[string]any, time.Time)
```

**实际签名**：`pkg/approval/whitelist.go:42`
```go
func (w *Whitelist) Add(sessionID, tool string, params map[string]any, now time.Time) Entry
```

**修复**：
```go
// 修改前
wl.Add("session-1", "bash_execute", params)

// 修改后
wl.Add("session-1", "bash_execute", params, time.Now())
```

#### 2.2 Whitelist.IsApproved 方法不存在
**错误信息**：
```
examples/v03_demo.go:45:8: wl.IsApproved undefined
```

**实际方法**：`pkg/approval/whitelist.go:33`
```go
func (w *Whitelist) Allowed(sessionID, tool string, params map[string]any) bool
```

**修复**：
```go
// 修改前
if wl.IsApproved("session-1", "bash_execute", params) {

// 修改后
if wl.Allowed("session-1", "bash_execute", params) {
```

#### 2.3 ActionNode 结构体使用错误
**错误信息**：
```
examples/v03_demo.go:64:3: unknown field ID in struct literal
examples/v03_demo.go:65:3: unknown field Fn in struct literal, but does have unexported fn
```

**根因**：`pkg/workflow/node.go:48-50`
```go
type ActionNode struct {
    name string  // unexported
    fn   ActionFunc  // unexported
}
```

**正确用法**：`pkg/workflow/node.go:53-55`
```go
func NewAction(name string, fn ActionFunc) *ActionNode
```

**ActionFunc 签名**：
```go
type ActionFunc func(*ExecutionContext) error
```

**修复**：
```go
// 修改前
startNode := &workflow.ActionNode{
    ID: "start",
    Fn: func(ctx context.Context, ec *workflow.ExecutionContext) (string, error) {
        fmt.Println("  -> 执行: start 节点")
        return "end", nil
    },
}

// 修改后
startNode := workflow.NewAction("start", func(ec *workflow.ExecutionContext) error {
    fmt.Println("  -> 执行: start 节点")
    return nil
})
```

#### 2.4 workflow.END 常量不存在
**错误信息**：
```
examples/v03_demo.go:75:20: undefined: workflow.END
```

**根因**：`pkg/workflow/` 包中未定义 `END` 常量

**修复方案**：
- 方案 A（推荐）：使用 `return nil` 表示结束（`NodeResult{Next: nil}`）
- 方案 B：添加 `const END = "__END__"` 到 `pkg/workflow/graph.go`

**采用方案 A**：
```go
// 修改前
endNode := &workflow.ActionNode{
    ID: "end",
    Fn: func(ctx context.Context, ec *workflow.ExecutionContext) (string, error) {
        fmt.Println("  -> 执行: end 节点")
        return workflow.END, nil
    },
}

// 修改后
endNode := workflow.NewAction("end", func(ec *workflow.ExecutionContext) error {
    fmt.Println("  -> 执行: end 节点")
    return nil  // 不返回 Next 节点，自动结束
})
```

---

### Error 3: cmd/agentctl fakeAgent 缺少方法

**错误信息**：
```
cmd/agentctl/helpers_test.go:52:63: *fakeAgent does not implement agent.Agent (missing method ListMiddlewares)
```

**接口定义**：`pkg/agent/agent.go:45`
```go
type Agent interface {
    // ... 其他方法
    ListMiddlewares() []middleware.Middleware
}
```

**修复**：在 `cmd/agentctl/helpers_test.go:17-48` 的 `fakeAgent` 添加方法：
```go
func (f *fakeAgent) ListMiddlewares() []middleware.Middleware {
    return nil
}
```

---

### Error 4: fmt.Println 冗余换行符（非阻塞）

**错误信息**：
```
examples/test-max-iterations/main.go:75:2: fmt.Println arg list ends with redundant newline
```

**修复**：移除字符串末尾的 `\n`
```go
// 修改前
fmt.Println("=== 中间件优先级与执行顺序测试 ===\n")

// 修改后
fmt.Println("=== 中间件优先级与执行顺序测试 ===")
```

**影响文件**：
- `examples/test-max-iterations/main.go:75,112,115`
- `examples/test-middleware-order/main.go:76,109,123`

---

## 修复优先级

| 错误 | 优先级 | 预计时间 | 是否阻塞编译 |
|------|--------|---------|------------|
| Error 1 | P0 | 5 分钟 | ✅ 是 |
| Error 2 | P0 | 10 分钟 | ✅ 是 |
| Error 3 | P0 | 5 分钟 | ✅ 是 |
| Error 4 | P2 | 2 分钟 | ❌ 否（警告） |

**总计**：~20 分钟

---

## Codex 执行指令

### 任务 1: 修复 examples 结构

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/COMPILATION_FIX.md 修复 examples 重复 main 函数问题。要求：
  1. 创建目录 examples/v03/
  2. 移动 examples/v03_demo.go → examples/v03/demo.go
  3. 移动 examples/v03_features.go → examples/v03/features.go
  4. 移动 examples/v03_validation.go → examples/v03/validation.go
  5. 保持 examples/demo_tools.go 不变
  6. 用中文输出执行结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### 任务 2: 修复 API 不匹配

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/COMPILATION_FIX.md 修复 examples/v03/*.go 的 API 不匹配问题。要求：
  1. 修复 demo.go:
     - Line 41: wl.Add(..., time.Now())
     - Line 45: wl.IsApproved → wl.Allowed
     - Line 63-76: 使用 workflow.NewAction 构造函数
     - 移除 workflow.END 引用，使用 return nil
  2. 同样修复 features.go 和 validation.go 中的类似问题
  3. 添加缺少的 import \"time\"
  4. 修复 fmt.Println 冗余换行符
  5. 确保编译通过：go build ./examples/v03/...
  6. 用中文输出修改摘要" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

### 任务 3: 修复 cmd/agentctl 测试

```bash
uv run ~/.claude/skills/codex/scripts/codex.py \
  "根据 @agentsdk-go/COMPILATION_FIX.md 修复 cmd/agentctl 测试接口不匹配问题。要求：
  1. 修改 cmd/agentctl/helpers_test.go
  2. 在 fakeAgent 结构体添加方法：
     func (f *fakeAgent) ListMiddlewares() []middleware.Middleware { return nil }
  3. 同样修复 examples/test-max-iterations 和 test-middleware-order 的换行符警告
  4. 验证编译：go test -c ./cmd/agentctl/...
  5. 用中文输出验证结果" \
  "gpt-5.1-codex" \
  "/Users/chenwenjie/Downloads/agentsdk-pk/agentsdk-go"
```

---

## 验收标准

- [ ] `go build ./...` 无编译错误
- [ ] `go test -short ./...` 无编译错误
- [ ] `examples/demo_tools.go` 可独立运行
- [ ] `examples/v03/demo.go` 可独立运行
- [ ] 无 API 破坏性变更

---

**文档版本**：v1.0
**创建时间**：2025-11-17
**负责人**：Claude Code
**预计工时**：20 分钟
