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
haikuProvider := &model.AnthropicProvider{ModelName: "claude-3-5-haiku-20241022"}
sonnetProvider := &model.AnthropicProvider{ModelName: "claude-sonnet-4-20250514"}

haiku, _ := haikuProvider.Model(ctx)
sonnet, _ := sonnetProvider.Model(ctx)

rt, _ := api.New(ctx, api.Options{
    Model: sonnet, // default model

    ModelPool: map[api.ModelTier]model.Model{
        api.ModelTierLow:  haiku,
        api.ModelTierMid:  sonnet,
        api.ModelTierHigh: sonnet, // placeholder for opus
    },

    SubagentModelMapping: map[string]api.ModelTier{
        "explore": api.ModelTierLow,  // use Haiku for exploration
        "plan":    api.ModelTierHigh, // use Opus for planning
    },
})
```

## Running

```bash
export ANTHROPIC_API_KEY=sk-ant-...
go run ./examples/05-multimodel
```

## Cost Optimization Strategy

| Subagent Type | Recommended Tier | Rationale |
|---------------|------------------|-----------|
| explore | low | Fast exploration, simple pattern matching |
| plan | mid/high | Needs good reasoning for planning |
| general-purpose | high | Complex reasoning tasks |

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
