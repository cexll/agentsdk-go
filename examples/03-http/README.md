# 03-http: Minimal HTTP API

The leanest HTTP example: one shared `api.Runtime`, three endpoints, zero extra middleware.

## Run
```bash
export ANTHROPIC_API_KEY=sk-ant-...
# OR use ANTHROPIC_AUTH_TOKEN (takes precedence)
# export ANTHROPIC_AUTH_TOKEN=your-token
go run ./examples/03-http
```
Defaults to `:8080`. Override with `AGENTSDK_HTTP_ADDR`. Choose a model with `AGENTSDK_MODEL` (default `claude-3-5-sonnet-20241022`). Optionally set `ANTHROPIC_BASE_URL` for custom endpoints.

## Endpoints
- `GET /health` → `{"status":"ok"}`
- `POST /v1/run` → blocking JSON response
- `POST /v1/run/stream` → Server-Sent Events (ping every 15s)

## Concurrency

The HTTP server uses a single shared `api.Runtime` that is fully thread-safe:
- **Multiple concurrent requests** are handled safely
- **Same `session_id`**: Requests are automatically queued and executed serially
- **Different `session_id`s**: Execute in parallel without blocking each other
- **No manual locking required**: The Runtime handles all synchronization internally

Example: 10 concurrent requests with unique session IDs will execute in parallel, but 10 requests with the same session ID will execute one at a time.

## Request Body
```json
{
  "prompt": "Summarize agentsdk-go in one sentence",
  "session_id": "demo-123",          // optional; auto-generated when missing
  "timeout_ms": 3600000              // optional; default 3600000ms (60 minutes)
}
```

**Timeout Configuration**:
- Default timeout: **60 minutes** (适配 codex、测试等长时间任务)
- Override per request: Set `timeout_ms` in the request body (milliseconds)
- Recommended timeouts:
  - Simple commands: 30000 - 60000ms (30s - 1min)
  - File operations: 60000 - 120000ms (1 - 2min)
  - Codex analysis: 300000 - 600000ms (5 - 10min)
  - Test suites: 600000 - 1800000ms (10 - 30min)

## Examples
```bash
# Sync call
curl -sS -X POST http://localhost:8080/v1/run \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"hello"}'

# Streaming
curl --no-buffer -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt":"list examples"}'
```
