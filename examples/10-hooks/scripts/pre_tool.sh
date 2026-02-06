#!/bin/bash
# PreToolUse hook - 工具执行前验证
#
# 退出码 (Claude Code 规范):
#   0 = 成功，解析 stdout JSON
#   2 = 阻塞错误，stderr 为错误信息
#   其他 = 非阻塞，记录 stderr 并继续
#
# JSON 输出格式 (stdout):
#   {"hookSpecificOutput":{"permissionDecision":"allow|deny|ask","updatedInput":{...}}}
#   {"decision":"deny","reason":"..."}
#   {"continue":false}

# 读取 stdin 的 JSON payload (扁平格式)
payload=$(cat)

# 提取工具名称 (新的扁平格式: tool_name 在顶层)
tool_name=$(echo "$payload" | jq -r '.tool_name // empty')

echo "[PreToolUse] 工具: $tool_name" >&2

# 示例: 拒绝危险命令
command=$(echo "$payload" | jq -r '.tool_input.command // empty')
if echo "$command" | grep -qE 'rm -rf|dd if='; then
    # 通过 JSON 输出拒绝 (exit 0 + decision=deny)
    echo '{"decision":"deny","reason":"危险命令被拒绝"}'
    exit 0
fi

# 允许执行 (空 stdout 也表示允许)
exit 0
