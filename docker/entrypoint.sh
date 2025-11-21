#!/bin/sh
# 注意:
# 1. 此脚本已废弃，不再被 Dockerfile 使用。
# 2. 新的 HTTP 服务器 (examples/03-http) 直接从环境变量读取配置。
# 3. 保留此文件仅供参考。
set -euo pipefail

CONFIG_PATH="${AGENTCTL_CONFIG:-/var/agentsdk/config.json}"
CONFIG_DIR="$(dirname "${CONFIG_PATH}")"
DEFAULT_MODEL="${DEFAULT_MODEL:-claude-3.5-sonnet}"
API_KEY_VALUE="${API_KEY:-}"
BASE_URL_VALUE="${BASE_URL:-}"
MCP_SERVERS_VALUE="${MCP_SERVERS:-}"

mkdir -p "${CONFIG_DIR}"

if [ ! -f "${CONFIG_PATH}" ]; then
  MCP_JSON=""
  if [ -n "${MCP_SERVERS_VALUE}" ]; then
    OLD_IFS="${IFS}"
    IFS=','
    for server in ${MCP_SERVERS_VALUE}; do
      value="$(echo "${server}" | xargs)"
      if [ -z "${value}" ]; then
        continue
      fi
      if [ -n "${MCP_JSON}" ]; then
        MCP_JSON="${MCP_JSON}, "
      fi
      MCP_JSON="${MCP_JSON}\"${value}\""
    done
    IFS="${OLD_IFS}"
  fi
  cat > "${CONFIG_PATH}" <<EOF
{
  "default_model": "${DEFAULT_MODEL}",
  "api_key": "${API_KEY_VALUE}",
  "base_url": "${BASE_URL_VALUE}",
  "mcp_servers": [${MCP_JSON}]
}
EOF
fi

exec agentctl serve --config "${CONFIG_PATH}" "$@"
