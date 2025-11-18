# Simplified HTTP API Example

This tutorial shows how to wrap a single shared `api.Runtime` behind three HTTP endpoints using only the Go standard library. Much like `examples/cli/basic.go`, it keeps the surface area tight so you can study how the SDK wires together without production-oriented scaffolding.

## Highlights
- 418 lines total: `main.go` (162) handles process wiring, `server.go` (256) implements routing and JSON/SSE plumbing — a 52% reduction from the previous 877-line version.
- One long-lived runtime instance serves every request; requests no longer build ad-hoc runtimes or sandbox patches.
- Only the core endpoints remain (`GET /healthz`, `POST /v1/run`, `POST /v1/run/stream`); `/v1/tools/execute` has been removed.
- Configuration dropped to the essentials so you only think about listen address, project root, model, timeouts, concurrency caps, and Anthropic credentials.

## Quick Start
1. Go 1.23+ and a valid `ANTHROPIC_API_KEY` in the environment.
2. From the repo root run:
   ```bash
   export ANTHROPIC_API_KEY=sk-ant-...
   go run ./examples/http
   ```
3. The server listens on `:8080` by default. Stop it with `CTRL+C`.

Just like the CLI example, the HTTP server auto-creates a throwaway `.claude/config.yaml` if `AGENTSDK_PROJECT_ROOT` is unset, so you can experiment without preparing project files.

## Configuration

| Env var | Purpose | Default |
| --- | --- | --- |
| `AGENTSDK_HTTP_ADDR` | Listen address | `:8080` |
| `AGENTSDK_PROJECT_ROOT` | Workspace root exposed to the agent/tools | auto-generated temp dir with minimal config |
| `AGENTSDK_MODEL` | Anthropic model name for the shared runtime | `claude-3-5-sonnet-20241022` |
| `AGENTSDK_DEFAULT_TIMEOUT` | Default per-request timeout (Go duration or ms) | `45s` |
| `AGENTSDK_MAX_SESSIONS` | In-memory session cap for the runtime LRU | `500` |
| `ANTHROPIC_API_KEY` | Anthropic API credential | required |
| `ANTHROPIC_BASE_URL` | Override for Anthropic endpoint (optional) | unset |

Removed environment variables: `AGENTSDK_SANDBOX_ROOT`, `AGENTSDK_RESOURCE_CPU_PERCENT`, `AGENTSDK_RESOURCE_MEMORY_MB`, `AGENTSDK_RESOURCE_DISK_MB`, `AGENTSDK_MAX_BODY_BYTES`, `AGENTSDK_MAX_TIMEOUT`, `AGENTSDK_NETWORK_ALLOW`. The example purposely avoids exposing runtime resource knobs to keep the story focused on HTTP wiring.

## API Surface
- `GET /healthz` — basic liveness probe returning `{"status":"ok"}`.
- `POST /v1/run` — blocking agent execution with JSON in/out.
- `POST /v1/run/stream` — Server-Sent Events backed by `Runtime.RunStream` with Anthropic-compatible event frames.

## Example: Non-Streaming Run
```bash
curl -sS -X POST http://localhost:8080/v1/run \
  -H 'Content-Type: application/json' \
  -d '{
        "prompt": "用一句话解释 agentsdk-go",
        "session_id": "demo-client",
        "metadata": {"customer": "demo"}
      }'
```

Typical response:
```json
{
  "session_id": "demo-client",
  "output": "agentsdk-go 把 Anthropic Agents API 封装成 Go 友好的运行时。",
  "stop_reason": "end_turn",
  "usage": {"input_tokens": 120, "output_tokens": 45},
  "tags": {"customer": "demo"},
  "tool_calls": []
}
```
Unknown JSON fields are rejected up-front, bodies larger than 1 MiB are refused, and every request reuses the shared runtime plus default timeout unless `timeout_ms` is provided.

## Example: Streaming Run (SSE)
```bash
curl --no-buffer -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "列出 examples 目录", "session_id": "stream-demo"}'
```
SSE frames mirror Anthropic's Messages API; only `data:` lines are emitted:
```
data: {"type":"agent_start","session_id":"stream-demo"}

data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"cli"}}

data: {"type":"agent_stop","stop_reason":"end_turn"}
```
A heartbeat `data: {"type":"ping"}` goes out every 15 s so proxies keep the stream alive. Errors return `data: {"type":"error", ...}` before the connection closes.

## Code Structure
- `main.go` bootstraps configuration, builds the shared `api.Runtime`, and wires graceful shutdown.
- `server.go` defines the HTTP handlers, strict JSON decoding, SSE writer, and lightweight request context helpers.

Because the runtime is built once and shared, there is no per-request sandbox/resource mutation logic. If you need that flexibility for production, treat this example as a starting point rather than a drop-in service.
