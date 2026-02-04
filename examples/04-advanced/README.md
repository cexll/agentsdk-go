# Advanced Integrated Example

This example wires every advanced capability into one runnable binary. Each feature lives in its own file to keep responsibilities small and readable.

## Features
- Middleware chain: logging, rate limit, security, monitoring, optional trace output (`trace-dir`).
- Hooks: lifecycle callbacks with middleware wrappers.
- MCP: optional stdio MCP server (default `stdio://uvx mcp-server-time`, disabled unless `--enable-mcp`).
- Sandbox: filesystem/network/resource guard configured via flags.
- Skills: auto + manual activation with force flag.
- Subagents: chooser with builtin + custom deploy guard.
- Commands: slash command parser executes inline script inside the prompt.

## Run
```bash
cd examples/04-advanced
# set ANTHROPIC_API_KEY or ANTHROPIC_AUTH_TOKEN only if you swap in a real provider; demo model is offline-safe
go run . --prompt "生成一份安全巡检摘要" --enable-mcp=false
```

Useful flags:
- `--enable-mcp` toggle MCP server registration (requires `uvx` in PATH).
- `--enable-trace` and `--trace-dir trace-out` to inspect trace logs.
- `--enable-hooks/--enable-skills/--enable-subagents/--enable-commands/--enable-sandbox` to isolate components.
- `--force-skill add-note` to run manual skills; `--target-subagent plan` to pin a subagent.

## Expected output
- Final agent message that merges tool results (observe_logs + optional MCP time).
- Sections printing commands executed, skills, subagent chosen, hook events, sandbox snapshot, trace directory, and middleware metrics.

## Layout
- `main.go` orchestrates flags and runtime wiring.
- `middleware.go` middleware chain + trace integration.
- `hooks.go` hook handlers and middleware.
- `mcp.go` MCP toggle and preflight.
- `sandbox.go` sandbox options for the runtime.
- `skills.go` skill registry definitions.
- `subagents.go` subagent definitions and handlers.
- `commands.go` slash command registration and helpers.
- `model.go` demo model + tool implementation.
- `.claude/settings.json` basic env/permissions.
