# Multi-Model Example

This example demonstrates multi-model support, allowing you to configure
different models for different subagent types to optimize costs.

## Features

- **Model Pool**: Configure multiple models indexed by tier (`low`/`mid`/`high`).
- **Subagent-Model Mapping**: Bind specific subagent types to specific model tiers.
- **Request-Level Override**: Override model tier for individual requests.
- **Automatic Fallback**: Subagents not in the mapping use the default model.

## Configuration

```go
sonnetProvider := &model.AnthropicProvider{ModelName: "claude-sonnet-4-20250514"}
opusProvider := &model.AnthropicProvider{ModelName: "claude-opus-4-20250514"}
haikuProvider := &model.AnthropicProvider{ModelName: "claude-3-5-haiku-20241022"}

sonnet, _ := sonnetProvider.Model(ctx)
opus, _ := opusProvider.Model(ctx)
haiku, _ := haikuProvider.Model(ctx)

rt, _ := api.New(ctx, api.Options{
    Model: sonnet, // default model

    ModelPool: map[api.ModelTier]model.Model{
        api.ModelTierHigh: opus,   // for planning
        api.ModelTierMid:  sonnet, // for exploration & general
        api.ModelTierLow:  haiku,  // available for custom use
    },

    // Inspired by Claude Code's "opus plan" model selection
    SubagentModelMapping: map[string]api.ModelTier{
        "plan":            api.ModelTierHigh, // Opus for complex reasoning
        "explore":         api.ModelTierMid,  // Sonnet for exploration
        "general-purpose": api.ModelTierMid,  // Sonnet for general tasks
    },
})
```

## Running

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/05-multimodel
```

## Model Selection Strategy (Opus Plan)

| Subagent Type | Recommended Tier | Rationale |
|---------------|------------------|-----------|
| plan | high (Opus) | Complex reasoning, architecture decisions |
| explore | mid (Sonnet) | Code exploration, pattern matching |
| general-purpose | mid (Sonnet) | Balanced for most tasks |
| (custom) | low (Haiku) | Optional, for simple/fast tasks |

## Request-Level Override

You can override the model tier for individual requests:

```go
resp, err := rt.Run(ctx, api.Request{
    Prompt:    "Simple question",
    SessionID: "demo",
    Model:     api.ModelTierLow, // Force use of low-tier model
})
```

Priority order:
1. `Request.Model` (explicit override)
2. `SubagentModelMapping` (subagent type mapping)
3. `Options.Model` (default model)
