# Task System Example

演示 Task 依赖跟踪系统的完整功能。

## 功能展示

- **TaskCreate**: 创建带 subject/description/activeForm 的任务
- **TaskUpdate**: 更新任务状态、设置依赖关系（blocks/blockedBy）
- **TaskList**: 列出所有任务及依赖关系
- **TaskGet**: 获取单个任务详情
- **自动依赖解析**: 任务完成时自动解除下游任务阻塞

## 运行

```bash
# 设置 API key
export ANTHROPIC_API_KEY=sk-ant-...

# 运行示例
go run examples/09-task-system/main.go
```

## 测试场景

示例创建 3 个任务并设置依赖链：

```
任务 A (读取配置) → 任务 B (验证配置) → 任务 C (启动服务)
```

验证：
1. 任务创建和依赖关系设置
2. 任务状态更新（pending → in_progress → completed）
3. 依赖自动解除（A 完成后 B 自动解除阻塞）
4. 任务查询和列表展示
