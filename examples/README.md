[中文](README_zh.md) | English

# agentsdk-go Examples

Twelve examples. Run everything from the repo root.

**Environment Setup**

1. Copy `.env.example` to `.env` and set your API key:
```bash
cp .env.example .env
# Edit .env and set ANTHROPIC_API_KEY=sk-ant-your-key-here
```

2. Load environment variables:
```bash
source .env
```

Alternatively, export directly:
```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

**Learning path**
- `01-basic` (~36 lines): single API call, minimal surface, prints one response.
- `02-cli` (~93 lines): CLI REPL with session history and optional config loading.
- `03-http` (~300 lines): REST + SSE server on `:8080`, production-ready wiring.
- `04-advanced` (~1400 lines): full stack with middleware, hooks, MCP, sandbox, skills, subagents.
- `05-custom-tools` (~58 lines): selective built-in tools and custom tool registration.
- `06-embed` (~181 lines): embedded filesystem for `.claude` directory via `go:embed`.
- `07-multimodel` (~130 lines): multi-model pool with tier-based routing and cost optimization.
- `08-askuserquestion` (~474 lines): AskUserQuestion tool integration with build-tag demos.
- `09-task-system` (~56 lines): task tracking with dependencies.
- `10-hooks` (~85 lines): hooks system with PreToolUse/PostToolUse shell hooks.
- `11-reasoning` (~186 lines): reasoning model support (DeepSeek-R1 reasoning_content passthrough).
- `12-multimodal` (~135 lines): multimodal content blocks (text + images).

## 01-basic — minimal entry
- Purpose: fastest way to see the SDK loop in action with one request/response.
- Run:
```bash
source .env
go run ./examples/01-basic
```

## 02-cli — interactive REPL
- Key features: interactive prompt, per-session history, optional `.claude/settings.json` load.
- Run:
```bash
source .env
go run ./examples/02-cli --session-id demo --settings-path .claude/settings.json
```

## 03-http — REST + SSE
- Key features: `/health`, `/v1/run` (blocking), `/v1/run/stream` (SSE, 15s heartbeat); defaults to `:8080`. Fully thread-safe runtime handles concurrent requests automatically.
- Run:
```bash
source .env
go run ./examples/03-http
```

## 04-advanced — full integration
- Key features: end-to-end pipeline with middleware chain, hooks, MCP client, sandbox controls, skills, subagents, streaming output.
- Run:
```bash
source .env
go run ./examples/04-advanced --prompt "安全巡检" --enable-mcp=false
```

## 05-custom-tools — custom tool registration
- Key features: selective built-in tools (`EnabledBuiltinTools`), custom tool implementation (`CustomTools`), demonstrates tool filtering and registration.
- Run:
```bash
source .env
go run ./examples/05-custom-tools
```
- See [05-custom-tools/README.md](05-custom-tools/README.md) for detailed usage and custom tool implementation guide.

## 06-embed — embedded filesystem
- Key features: `EmbedFS` for embedding `.claude` directory into the binary, priority resolution between embedded and on-disk configs.
- Run:
```bash
source .env
go run ./examples/06-embed
```

## 07-multimodel — multi-model support
- Key features: model pool configuration, tier-based model routing (low/mid/high), subagent-model mapping, cost optimization.
- Run:
```bash
source .env
go run ./examples/07-multimodel
```
- See [07-multimodel/README.md](07-multimodel/README.md) for configuration examples and best practices.

## 08-askuserquestion — AskUserQuestion tool
- Key features: three demo modes selected by build tags.
- Run:
```bash
source .env
(cd examples/08-askuserquestion && go run .)                 # full agent scenarios
(cd examples/08-askuserquestion && go run -tags demo_llm .)  # LLM integration test
(cd examples/08-askuserquestion && go run -tags demo_simple .) # tool-only test (no API key needed)
```
- See [08-askuserquestion/README.md](08-askuserquestion/README.md) for detailed usage and implementation patterns.

## 09-task-system — task tracking
- Key features: task creation, dependency management, status tracking via built-in task tools.
- Run:
```bash
source .env
go run ./examples/09-task-system
```

## 10-hooks — hooks system
- Key features: `PreToolUse`/`PostToolUse` shell hooks, async execution, once-per-session dedup.
- Run:
```bash
source .env
go run ./examples/10-hooks
```

## 11-reasoning — reasoning models
- Key features: `reasoning_content` passthrough for thinking models (DeepSeek-R1), streaming support, multi-turn conversations.
- Run:
```bash
export OPENAI_API_KEY=your-key
export OPENAI_BASE_URL=https://api.deepseek.com/v1
go run ./examples/11-reasoning
```

## 12-multimodal — multimodal content
- Key features: text + image content blocks (base64 and URL), `ContentBlocks` in `api.Request`.
- Run:
```bash
source .env
go run ./examples/12-multimodal
```
