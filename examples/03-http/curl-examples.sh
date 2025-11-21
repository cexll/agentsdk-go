#!/usr/bin/env bash
set -euo pipefail

if ! command -v curl >/dev/null 2>&1; then
  echo "curl is required" >&2
  exit 1
fi

BASE_URL=${BASE_URL:-http://localhost:8080}
PROMPT=${PROMPT:-"Say hi from curl"}
SESSION=${SESSION:-"curl-demo-$(date +%s)"}

pretty() {
  if command -v jq >/dev/null 2>&1; then
    jq .
  else
    cat
  fi
}

echo ">>> POST $BASE_URL/v1/run"
curl -sS -X POST "$BASE_URL/v1/run" \
  -H 'Content-Type: application/json' \
  -d "{\"prompt\":\"$PROMPT\",\"session_id\":\"$SESSION\"}" | pretty

echo "\n>>> POST $BASE_URL/v1/run/stream"
curl --no-buffer -N -X POST "$BASE_URL/v1/run/stream" \
  -H 'Content-Type: application/json' \
  -d "{\"prompt\":\"$PROMPT\",\"session_id\":\"${SESSION}-stream\"}"
