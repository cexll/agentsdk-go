# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

**agentsdk-go** is a Go Agent SDK that implements core Claude Code-style runtime capabilities with a modular architecture. This SDK targets CLI, CI/CD, and enterprise platforms, prioritizing KISS-friendly modularity, a small core, and an optional middleware interception system for in-process extensibility.

**Notes**:
- Docs intentionally avoid hard-coded LOC/coverage numbers. Use `go test`, `go test -race`, and `go test -cover` when you need measurements.

## Architecture

### Architecture (core + feature packages)

```
Core Layer:
├── agent/       - Agent core loop
├── middleware/  - Optional in-process interception (six stages)
├── model/       - Anthropic model adapter
├── tool/        - Tool registry & execution
├── message/     - In-memory message history
└── api/         - Unified SDK interface

Feature Layer:
├── config/      - Configuration loading & hot-reload
├── core/
│   ├── events/  - Event bus
│   └── hooks/   - Hooks executor
├── sandbox/     - Filesystem & network isolation
├── mcp/         - MCP client
├── runtime/
│   ├── skills/     - Skills management
│   ├── subagents/  - Subagents management
│   ├── commands/   - Slash commands parser
│   └── tasks/      - Task tracking and dependencies
└── security/    - Security utilities
```

### 6 Middleware Interception Points

The SDK provides a complete request/response interception chain at every critical point:

```
User Request
     ↓
[before_agent]  ← Request validation, rate limiting, audit logging
     ↓
Agent Loop
     ↓
[before_model]  ← Prompt enhancement, context trimming
     ↓
Model.Generate
     ↓
[after_model]   ← Result filtering, safety checks
     ↓
[before_tool]   ← Tool call validation, parameter checking
     ↓
Tool.Execute
     ↓
[after_tool]    ← Result post-processing, error handling
     ↓
[after_agent]   ← Response formatting, metrics reporting
     ↓
User Response
```

## Common Commands

### Build & Test

```bash
# Run all tests
go test ./...

# Run tests with coverage
go test -coverprofile=coverage.out ./...
go tool cover -html=coverage.out

# Run core module tests only
go test ./pkg/agent/... ./pkg/middleware/... ./pkg/model/...

# Run integration tests
go test ./test/integration/...

# Build CLI tool
make agentctl
# or
go build -o bin/agentctl ./cmd/cli

# Lint
make lint
# or
golangci-lint run
```

### Running Examples

**IMPORTANT**: All examples require `ANTHROPIC_API_KEY` environment variable to be set.

```bash
# Recommended: Use .env file
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY=sk-ant-your-key-here
source .env

# Run examples
go run ./examples/01-basic
go run ./examples/02-cli --session-id demo
go run ./examples/03-http
go run ./examples/04-advanced --prompt "安全巡检" --enable-mcp=false
```

Alternative (direct export):
```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/01-basic
```

### HTTP API Endpoints

When running the HTTP example:

- `GET /health` - Basic liveness probe
- `POST /v1/run` - Blocking request, waits for full response
- `POST /v1/run/stream` - SSE streaming with real-time progress

Example streaming request:
```bash
curl -N -X POST http://localhost:8080/v1/run/stream \
  -H 'Content-Type: application/json' \
  -d '{"prompt": "列出当前目录", "session_id": "demo"}'
```

### Development Workflow

```bash
# Clean build artifacts
make clean

# Install CLI globally
make install

# Generate coverage report
make coverage
```

## Code Architecture Details

### Agent Core Loop

**Location**: `pkg/agent/agent.go`

The agent loop is intentionally kept concise. Key points:
- Uses context for cancellation and timeout
- Executes middleware at each stage
- Limits iterations via `MaxIterations` option
- Returns `ModelOutput` with content, tool calls, and completion status

### Model Adapters

**Location**: `pkg/model/`

Currently supports:
- **Anthropic** (`anthropic.go`) - Primary provider with Claude models
- **OpenAI** (compatibility layer)

Adapters convert between SDK's generic message format and provider-specific APIs.

**Dual Model Interface**: The SDK has two distinct Model interfaces:
- `agent.Model` (`pkg/agent/agent.go`) — defines `Generate(ctx, *Context) (*ModelOutput, error)`. This is the agent-loop-facing interface that the core loop calls each iteration.
- `model.Model` (`pkg/model/interface.go`) — defines `Complete(ctx, Request) (*Response, error)` and `CompleteStream(ctx, Request, StreamHandler) error`. This is the provider-facing interface that adapters implement.

The `pkg/api` layer bridges these two interfaces: it wraps a `model.Model` into an `agent.Model` adapter so the agent loop can call `Generate` while the underlying provider uses `Complete`/`CompleteStream`.

### Tool System

**Location**: `pkg/tool/`

Key components:
- `Registry` - Thread-safe tool registration
- `Tool` interface - Name, Description, Schema, Execute
- `Validator` - JSON Schema validation before execution
- Built-in tools (`pkg/tool/builtin/`):
  - `bash` - Execute shell commands with security validation
  - `file_read` / `file_write` - Sandboxed file operations
  - `grep` - Content search with regex support
  - `glob` - File pattern matching
- MCP client support for external tools

**Important**: Tool execution validates parameters against JSON Schema before invocation to catch errors early. All built-in tools respect sandbox policies configured in `.claude/settings.json`.

### Middleware System

**Location**: `pkg/middleware/`

Chain-of-responsibility pattern with 6 stages:
- `BeforeAgent` / `AfterAgent` - Request/response boundary
- `BeforeModel` / `AfterModel` - Model generation boundary
- `BeforeTool` / `AfterTool` - Tool execution boundary

State is passed through `middleware.State` with a `Values` map for cross-middleware data sharing.

### Hooks

**Location**: `pkg/core/hooks/`

Hooks run as shell commands via `ShellHook` and receive a flat JSON payload on stdin. Exit code semantics follow the Claude Code specification:

| Exit Code | Decision | Behavior |
|-----------|----------|----------|
| 0 | Success | Parse stdout as JSON (`HookOutput`) |
| 2 | Blocking Error | stderr is the error message, execution stops |
| Other | Non-blocking | Log stderr and continue |

**JSON Input (stdin)**: Flat format with fields at top level:
```json
{"hook_event_name":"PreToolUse","session_id":"...","cwd":"...","tool_name":"Bash","tool_input":{"command":"ls"}}
```

**JSON Output (stdout, exit 0)**: Structured `HookOutput`:
- `{"decision":"deny","reason":"..."}` — deny tool execution
- `{"hookSpecificOutput":{"permissionDecision":"ask"}}` — request approval
- `{"hookSpecificOutput":{"updatedInput":{...}}}` — modify tool params
- `{"continue":false,"stopReason":"..."}` — stop execution

**Supported Events**: PreToolUse, PostToolUse, UserPromptSubmit, Stop, PermissionRequest, SessionStart, SessionEnd, SubagentStart, SubagentStop, ModelSelected. Each event has event-specific matcher targets (e.g., tool name for tool events, source for SessionStart, reason for SessionEnd).

**Defined but not wired in runtimeHookAdapter**: PostToolUseFailure, Notification, PreCompact are defined as event types in `pkg/core/events/types.go` but do not have corresponding methods in the runtime hook adapter (`pkg/api/options.go`). They can be used with custom hook middleware but are not dispatched by the default runtime.

**ShellHook Options**: `Async` (fire-and-forget), `Once` (per-session dedup), `Timeout` (default 600s for commands), `StatusMessage`.

**Configuration**: Via `.claude/settings.json` using `[]HookMatcherEntry` format or programmatically with `api.Options.TypedHooks`. Legacy `map[string]string` format is auto-converted.

### Message History

**Location**: `pkg/message/`

In-memory message store with:
- LRU eviction policy (configurable via `MaxSessions`)
- Per-session history tracking
- Thread-safe access
- Supports user, assistant, system, and tool result messages

### Configuration

**Location**: `pkg/config/`

The SDK follows Claude Code's `.claude/` directory structure:
```
.claude/
├── settings.json       # Project configuration
├── settings.local.json # Developer overrides (gitignored)
├── skills/           # Skills definitions
├── commands/         # Slash commands
└── agents/           # Subagents definitions
```

Configuration precedence (highest → lowest):
1. Runtime overrides (CLI/API-provided)
2. .claude/settings.local.json
3. .claude/settings.json
4. SDK defaults (no ~/.claude lookup)

Example `settings.json` (official schema):

```json
{
  "permissions": {
    "allow": ["Bash(ls:*)", "Bash(pwd:*)"],
    "deny": ["Read(.env)", "Read(secrets/**)"]
  },
  "env": {
    "MY_VAR": "value"
  },
  "sandbox": {
    "enabled": false
  }
}
```

Hot-reload support via `fsnotify` for configuration changes.

### Security & Sandbox

**Location**: `pkg/sandbox/`, `pkg/security/`

Three-layer defense:

1. **Path whitelist** - Restricts filesystem access
2. **Symlink resolution** - Prevents path traversal via symbolic links
3. **Command validation** - Blocks dangerous commands (rm -rf, etc.)

**Command Validator** (`pkg/security/validator.go`):
- Blocks destructive commands: `dd`, `mkfs`, `fdisk`, `shutdown`, `reboot`, `sudo`
- Pattern-based detection for dangerous rm/rmdir operations
- `AllowShellMetachars` toggle: CLI mode auto-enables; CI/Platform keep blocked
- Default: blocks shell metacharacters `|;&><`$` unless `AllowShellMetachars(true)` is called

Network isolation via allow-list for outbound connections.

## Testing Strategy

### Test Expectations

- New features and bug fixes must include tests where practical.
- Prefer table-driven tests for multiple scenarios.
- Coverage targets are not hard-coded in docs; generate a report with `go test -coverprofile=coverage.out ./...` when needed.

### Test File Patterns

Tests are co-located with implementation:
- Unit tests: `*_test.go` alongside source files
- Additional edge case tests: `*_additional_test.go`
- Integration tests: `test/integration/`

### Running Specific Tests

```bash
# Test a single package
go test ./pkg/agent

# Test with verbose output
go test -v ./pkg/middleware

# Run specific test
go test -run TestAgent_Run ./pkg/agent

# Benchmark tests
go test -bench=. ./pkg/agent
```

## Quick Troubleshooting

### Examples fail with "ANTHROPIC_API_KEY not set"
- Copy `.env.example` to `.env` and set your API key
- Use `source .env` before running examples
- Or export directly: `export ANTHROPIC_API_KEY=sk-ant-...`
- `ANTHROPIC_AUTH_TOKEN` is deprecated, use `ANTHROPIC_API_KEY` instead

### Test hangs or times out
- Some integration tests call live APIs and may take 10+ minutes
- Run unit tests only: `go test -short ./...` (if short mode is implemented)
- Increase timeout: `go test -timeout 15m ./pkg/api`
- Skip specific slow tests: `go test -skip TestToolWhitelistDeniesExecution`

### Sandbox blocks legitimate operations
- Check `.claude/settings.json` permissions
- Use `settings.local.json` for local overrides (gitignored)
- Set `"sandbox": {"enabled": false}` for development

### HTTP example fails to start
- Check if port 8080 is already in use: `lsof -i :8080`
- Set custom port: `export AGENTSDK_HTTP_ADDR=:9090`
- Verify `ANTHROPIC_API_KEY` or `ANTHROPIC_AUTH_TOKEN` is set

## Documentation

Key documentation files:
- `README.md` - Project overview, features, quick start
- `docs/architecture.md` - Detailed architecture analysis (横向对比 16 个项目)
- `docs/getting-started.md` - Step-by-step tutorial
- `docs/api-reference.md` - API documentation
- `docs/security.md` - Security best practices
- `examples/03-http/README.md` - HTTP API guide
- `.claude/specs/claude-code-rewrite/` - Development plans and reports

## Project Principles

This codebase follows Linus Torvalds' philosophy:
- **KISS** - Keep It Simple: Single responsibility, boring code
- **YAGNI** - You Aren't Gonna Need It: Keep dependencies minimal, extend as needed
- **Compatibility** - No API stability guarantees; keep the surface small
- **大道至简** - Simple interfaces, refined implementation

## Important File Locations

- Agent core: `pkg/agent/agent.go`
- Tool registry: `pkg/tool/registry.go`
- Tool executor: `pkg/tool/executor.go`
- Built-in tools: `pkg/tool/builtin/bash.go`, `pkg/tool/builtin/file.go`, `pkg/tool/builtin/grep.go`, `pkg/tool/builtin/glob.go`
- Model providers: `pkg/model/anthropic.go`, `pkg/model/provider.go`
- Middleware chain: `pkg/middleware/chain.go`
- API entry point: `pkg/api/agent.go`
- Security validator: `pkg/security/validator.go`
- Sandbox manager: `pkg/sandbox/`
- CLI tool: `cmd/cli/main.go`
- HTTP server example: `examples/03-http/main.go`

When adding new features, maintain the modular structure and add tests.

## Code Style & Conventions

### File Size Limit

Prefer small files and split by responsibility. Avoid monolithic files that mix unrelated concerns.

If a file starts growing beyond a reasonable size:
1. Split by responsibility (e.g., separate validators, helpers)
2. Extract interfaces to their own files
3. Move test helpers to `*_helpers_test.go`

### Naming Conventions

- Interfaces: `Model`, `Tool`, `ToolExecutor` (noun)
- Implementations: `AnthropicProvider`, `BashTool` (concrete)
- Options structs: Use functional options pattern
- Errors: Declare as package-level `var ErrXxx = errors.New(...)`

### Concurrency

- Use `sync.RWMutex` for shared state (e.g., Registry)
- Context-aware: Always respect `ctx.Done()` in loops
- No naked goroutines: Use errgroup or manage lifecycle explicitly

### Error Handling

```go
// Wrap errors with context
return fmt.Errorf("execute tool %s: %w", name, err)

// Check for specific errors
if errors.Is(err, ErrMaxIterations) { ... }

// Declare sentinel errors at package level
var ErrNilModel = errors.New("agent: model is nil")
```

## HTTP API Notes

The HTTP example (`examples/03-http/`) demonstrates:
- **SSE Streaming**: Full Anthropic-compatible event stream
- **Character-by-character output**: Real-time text streaming via `content_block_delta` events
- **Heartbeat**: 15s ping events to prevent connection drops
- **Minimal surface**: Only `/health`, `/v1/run`, `/v1/run/stream` with a single shared runtime

Configuration via environment variables (see `examples/03-http/README.md`).

## MCP Integration

**Location**: `pkg/mcp/`

Supports Model Context Protocol for external tools:
- stdio transport (for local processes)
- SSE transport (for HTTP servers)
- Automatic tool registration from MCP servers
- Configurable via `.claude/settings.json` or `--mcp` CLI flag

Example:
```json
{
  "mcp": {
    "servers": [
      {
        "name": "my-server",
        "command": "node",
        "args": ["server.js"]
      }
    ]
  }
}
```

## Entry Points

The SDK supports three entry point modes:
- **CLI**: Interactive command-line usage
- **CI**: Continuous integration environments
- **Platform**: Embedded in larger applications

Set via `api.Options.EntryPoint`.

## API Usage Patterns

### Basic Usage

```go
// Create Anthropic provider (reads ANTHROPIC_API_KEY from environment)
provider := model.NewAnthropicProvider(
    model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
    model.WithModel("claude-sonnet-4-5"),
)

runtime, err := api.New(ctx, api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
})
if err != nil {
    log.Fatal(err)
}
defer runtime.Close()

result, err := runtime.Run(ctx, api.Request{
    Prompt:    "Your task here",
    SessionID: "session-123",
})
if err != nil {
    log.Fatal(err)
}
fmt.Println(result.Output)
```

### Streaming Usage

```go
events := runtime.RunStream(ctx, api.Request{...})
for event := range events {
    switch event.Type {
    case "model_delta":
        // Handle incremental text
    case "tool_call":
        // Handle tool execution
    }
}
```

### Adding Custom Tools

Implement `tool.Tool` interface:
```go
type CustomTool struct{}
func (t *CustomTool) Name() string { return "my_tool" }
func (t *CustomTool) Description() string { return "..." }
func (t *CustomTool) Schema() *tool.JSONSchema { return &tool.JSONSchema{...} }
func (t *CustomTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // Implementation
}
```

Register before runtime creation via config or programmatically.

## Performance Considerations

- **LRU session cache**: Prevents unbounded memory growth
- **Streaming preferred**: Use `RunStream` for long-running tasks to get immediate feedback
- **Context timeout**: Always set reasonable timeouts on context

## Common Pitfalls

1. **Nil Model Check**: Agent creation requires a non-nil model provider
2. **Session ID Uniqueness**: Reusing session IDs appends to history; use unique IDs for isolation
3. **Sandbox Path Resolution**: Always use absolute paths; symlinks are resolved before validation
4. **Tool Parameter Validation**: Schema validation happens before execution—define schemas accurately
5. **Context Cancellation**: Respect context cancellation in custom tools and middleware
6. **API Key Management**: Never hardcode API keys; use environment variables or secure config management
7. **Command Security**: Built-in bash tool validates commands using `pkg/security/validator.go`; dangerous patterns are rejected by default

## Known Issues

No known issues.

## Testing Strategy

### Test Expectations

- New features and bug fixes must include tests where practical.
- Prefer table-driven tests for multiple scenarios.
- Coverage targets are not hard-coded in docs; generate a report with `go test -coverprofile=coverage.out ./...` when needed.

### Test Timeout Handling

Some integration tests may have long execution times:
- API tests have a 10-minute default timeout
- Use `-timeout` flag to adjust: `go test -timeout 15m ./pkg/api`
- For CI/CD, consider running integration tests separately from unit tests
- Run unit tests only with: `go test -short ./...` (if short mode is implemented)

## Environment Variables

### Required

- `ANTHROPIC_API_KEY` - Anthropic API key for Claude models (required for all examples, unless using auth token)
- `ANTHROPIC_AUTH_TOKEN` - (deprecated) Legacy auth token, use ANTHROPIC_API_KEY instead

### Optional

- `ANTHROPIC_BASE_URL` - Custom Anthropic API base URL (for proxy or private endpoints)

### Optional (HTTP example)

- `AGENTSDK_HTTP_ADDR` - Server address (default: `:8080`)
- `AGENTSDK_MODEL` - Model name (default: `claude-3-5-sonnet-20241022`)
- `AGENTSDK_DEFAULT_TIMEOUT` - Request timeout (default: `45s`)
- `AGENTSDK_MAX_SESSIONS` - Max concurrent sessions (default: `500`)
