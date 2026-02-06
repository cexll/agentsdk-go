# Hooks 示例

演示 agentsdk-go 的 Shell-based Hooks 功能。Hooks 在 agent 执行工具时**自动触发**，无需手动调用。

## 运行

```bash
export ANTHROPIC_API_KEY=sk-ant-...
chmod +x examples/10-hooks/scripts/*.sh
go run ./examples/10-hooks
```

## 配置方式

### 方式一：代码配置 (TypedHooks)

```go
typedHooks := []hooks.ShellHook{
    {
        Event:   events.PreToolUse,
        Command: "/path/to/pre_tool.sh",
    },
    {
        Event:   events.PostToolUse,
        Command: "/path/to/post_tool.sh",
        Async:   true,  // 异步执行，不阻塞主流程
    },
}

rt, _ := api.New(ctx, api.Options{
    TypedHooks: typedHooks,
})
```

### 方式二：配置文件 (.claude/settings.json)

```json
{
  "hooks": {
    "PreToolUse": [
      {
        "matcher": "Bash",
        "hooks": [
          {
            "type": "command",
            "command": "scripts/pre_tool.sh",
            "timeout": 30
          }
        ]
      }
    ]
  }
}
```

## 退出码语义 (Claude Code 规范)

| 退出码 | 含义 | 行为 |
|--------|------|------|
| 0 | 成功 | 解析 stdout JSON 输出 |
| 2 | 阻塞错误 | stderr 作为错误信息，中止执行 |
| 其他 | 非阻塞 | 记录 stderr 日志，继续执行 |

## Hook 事件类型

| Hook | 触发时机 | Matcher 匹配目标 |
|------|---------|-----------------|
| PreToolUse | 工具执行前 | 工具名 |
| PostToolUse | 工具执行后 | 工具名 |
| SessionStart | 会话开始 | source |
| SessionEnd | 会话结束 | reason |
| Notification | 通知 | notification_type |
| PreCompact | 上下文压缩前 | trigger |
| SubagentStart | 子 Agent 启动 | agent_type |
| SubagentStop | 子 Agent 停止 | agent_type |
| Stop | Agent 停止 | (无 matcher) |
| UserPromptSubmit | 用户提交 | (无 matcher) |

## Payload 格式 (扁平化)

Hook 脚本通过 stdin 接收 JSON payload，字段扁平化到顶层：

```json
{
  "hook_event_name": "PreToolUse",
  "session_id": "hooks-demo",
  "cwd": "/path/to/project",
  "tool_name": "Bash",
  "tool_input": {"command": "pwd"}
}
```

## JSON 输出格式 (stdout, exit 0)

Hook 可通过 stdout 输出 JSON 来控制行为：

```json
{"decision": "deny", "reason": "危险命令被拒绝"}
```

```json
{"hookSpecificOutput": {"permissionDecision": "ask"}}
```

```json
{"hookSpecificOutput": {"updatedInput": {"command": "ls -la"}}}
```

```json
{"continue": false, "stopReason": "用户取消"}
```

## ShellHook 选项

| 字段 | 类型 | 说明 |
|------|------|------|
| Async | bool | 异步执行，不阻塞主流程 |
| Once | bool | 每个 session 只执行一次 |
| Timeout | Duration | 自定义超时 (默认 600s) |
| StatusMessage | string | 执行时显示的状态信息 |
