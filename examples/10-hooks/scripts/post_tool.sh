#!/bin/bash
# PostToolUse hook - 工具执行后记录
#
# 退出码: 0=成功, 2=阻塞错误, 其他=非阻塞
# 可通过 stdout JSON 输出 {"continue":false} 来中止后续执行

payload=$(cat)

# 扁平格式: tool_name 和 duration_ms 在顶层
tool_name=$(echo "$payload" | jq -r '.tool_name // empty')
duration_ms=$(echo "$payload" | jq -r '.duration_ms // "unknown"')
has_error=$(echo "$payload" | jq -r '.is_error // false')

if [ "$has_error" = "true" ]; then
    error_msg=$(echo "$payload" | jq -r '.error // empty')
    echo "[PostToolUse] 工具: $tool_name, 耗时: ${duration_ms}ms, 错误: $error_msg" >&2
else
    echo "[PostToolUse] 工具: $tool_name, 耗时: ${duration_ms}ms" >&2
fi

exit 0
