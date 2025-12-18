# API Reference

This document covers the core APIs of agentsdk-go. It reflects the current implementation, fixes legacy naming drift (e.g., `pkg/message` owns history, `pkg/core/events` is the event bus, `pkg/middleware` handles interception), and follows KISS/YAGNI—focusing on composable types, methods, examples, and practical notes.

## pkg/middleware — Six-Stage Pluggable Chain

- `type Stage int` enumerates six fixed hook points: `StageBeforeAgent`, `StageBeforeModel`, `StageAfterModel`, `StageBeforeTool`, `StageAfterTool`, `StageAfterAgent` (`pkg/middleware/types.go:9`). Sparse enum avoids magic numbers; adding a stage requires extending the switch in `Chain.Execute`.
- `type State struct` (`types.go:21`) is the shared carrier across hooks. Fields like `Iteration`, `Agent`, `ModelOutput`, `ToolCall` are `any`; middleware must type-assert and avoid writing conflicting fields.
- `type Middleware interface` (`types.go:34`) declares six hook methods. Implementers can override only the needed stages; others return `nil`.
- `type Funcs struct` (`types.go:46`) lets you assemble middleware quickly with function pointers; missing callbacks are no-ops, `Identifier` shows in error messages—handy for tests and one-off interceptors.
- `type Chain struct` (`chain.go:14`) is a thread-safe sequential executor. `NewChain` filters `nil`; `Use` supports runtime additions. `ChainOption` currently exposes `WithTimeout` to wrap each stage with `context.WithTimeout`.
- `(*Chain).Execute(ctx, stage, *State) error` copies the middleware slice to isolate concurrent `Use`; hook invocation is centralized in `exec`, with `runWithTimeout` handling deadlines and cancellation.

```go
mw := middleware.NewChain([]middleware.Middleware{
	middleware.Funcs{
		Identifier: "audit",
		OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
			st.Values["start"] = time.Now()
			return nil
		},
		OnAfterAgent: func(ctx context.Context, st *middleware.State) error {
			start := st.Values["start"].(time.Time)
			log.Printf("agent took %s", time.Since(start))
			return nil
		},
	},
}, middleware.WithTimeout(2*time.Second))
state := &middleware.State{Values: map[string]any{}}
if err := mw.Execute(ctx, middleware.StageBeforeAgent, state); err != nil {
	panic(err)
}
```

- **Notes**: `State` is reused for a run; initialize its map before writes. Under `WithTimeout`, hooks must handle `context.DeadlineExceeded` promptly. `Middleware.Name()` appears in errors—empty names degrade to `<unnamed>` and hinder debugging.

### Chain Extensions

- `Chain.Use` (`chain.go:38`) holds a write lock; newly added middleware affects later `Execute` calls but not the in-flight one. To group by stage, build multiple chains and nest execution.
- `runWithTimeout` (`chain.go:66`) runs directly when `timeout <= 0`, otherwise spins a goroutine and listens on `done`; if the hook launches goroutines, propagate `ctx` to avoid leaks.
- `middlewareName` (`chain.go:101`) lets middleware expose `Name()`; empty returns render `<unnamed>`, so explicitly name middleware for better logs.
- Use namespaces inside `State.Values` (e.g., `"audit.start"`) to avoid key collisions. Share custom structs as pointers or immutable types to reduce copies.

## pkg/agent — Agent Loop, Context, Options, ModelOutput

- `type Model interface` (`pkg/agent/agent.go:16`) exposes `Generate(context.Context, *Context) (*ModelOutput, error)`, allowing a model to emit the next step based on accumulated state.
- `type ToolExecutor interface` (`agent.go:21`) abstracts tool dispatch; `Execute(ctx, call, *Context)` must return `ToolResult` or error. If tools are configured but `ToolExecutor` is `nil`, `Run` returns `tool executor is nil`.
- `type ToolCall` / `ToolResult` / `ModelOutput` (`agent.go:26-43`) carry model-driven tool calls and generated text. `ModelOutput.Done` short-circuits the loop; empty `ToolCalls` is also a stop condition.
- `type Agent struct` holds `model`, `tools`, `opts`, `mw`. `New(model, tools, opts)` (`agent.go:55`) calls `opts.withDefaults()` and auto-creates an empty chain when middleware is missing.
- `(*Agent).Run(ctx, *Context)` (`agent.go:70`) is the core loop: triggers `StageBeforeAgent`, then per-iteration `StageBeforeModel`, `StageAfterModel`, tool calls, `StageAfterTool`, and final `StageAfterAgent`. `MaxIterations` overflow returns `ErrMaxIterations`.
- `type Context struct` (`context.go:6`) tracks run state (`Iteration`, `Values`, `ToolResults`, `StartedAt`, `LastModelOutput`). `NewContext` presets `StartedAt` and an empty map to avoid caller initialization bugs.
- `type Options struct` (`options.go:12`) exposes `MaxIterations`, `Timeout`, `Middleware *middleware.Chain`. `withDefaults` injects `middleware.NewChain(nil)`.

```go
mdl := &mockModel{} // implements agent.Model
registry := tool.NewRegistry()
_ = registry.Register(&EchoTool{})
toolExec := tool.NewExecutor(registry, nil)
a, err := agent.New(mdl, toolExec, agent.Options{Timeout: 30 * time.Second})
ctx := agent.NewContext()
ctx.Values["session"] = "demo-1"
out, err := a.Run(context.Background(), ctx)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("final output: %s (tools=%d)\n", out.Content, len(out.ToolCalls))
```

- **Notes**: `Run` falls back to `context.Background()` when `ctx` is nil, but callers should pass a timeout. `Model.Generate` returning `nil` is treated as fatal. `ToolExecutor.Execute` should be idempotent or handle repeats—models may emit duplicate commands across iterations. `StageBeforeTool` is skipped after termination; place cleanup in `StageAfterAgent`.

### Execution Path Details

- `ErrNilModel` (`agent.go:13`) surfaces at construction to avoid deferring failures; `ErrMaxIterations` defends runaway loops with `Options.MaxIterations`.
- `Run` normalizes `ctx`, `Context`, and `options.Middleware`; it does not default `ToolExecutor` to avoid running unknown tools.
- Per-iteration state: `Context.Iteration` equals `State.Iteration`; `State.ToolCall`/`ToolResult` update per tool and are visible to the next `BeforeTool`.
- When `ModelOutput.Done` is true or `ToolCalls` is empty, Agent skips remaining stages and executes `StageAfterAgent`; model implementations should set `Done` explicitly to minimize iterations.
- If `options.Timeout > 0`, the entire loop is wrapped in `context.WithTimeout`; the same deadline applies to model and tools—tune `Timeout` versus internal tool timeouts accordingly.

## pkg/model — Model Interface, Anthropic Provider, Options

- `type Message`, `ToolCall`, `ToolDefinition` (`interface.go:5-24`) define model-level chat and callable tool descriptions using lightweight `string` + `map[string]any`.
- `type Request` (`interface.go:27`) aggregates `Messages`, `Tools`, `System`, `Model`, `MaxTokens`, `Temperature` (pointer to distinguish unset from zero). Callers must order messages correctly.
- `type Response` / `type Usage` (`interface.go:38-48`) provide token accounting; `CacheReadTokens` / `CacheCreationTokens` match Anthropic semantics.
- `type StreamHandler func(StreamResult) error` (`interface.go:56`); `StreamResult` may carry `Delta`, `ToolCall`, `Response`, with `Final` marking completion.
- `type Model interface` (`interface.go:60`) unifies `Complete` and `CompleteStream`; the Agent layer remains model-agnostic.
- `type Provider` and `ProviderFunc` (`provider.go:13-24`) allow deferred model construction; `ProviderFunc.Model` errors on nil functions to avoid silent panics.
- `type AnthropicProvider struct` (`provider.go:27`) implements `Model(ctx)` with `CacheTTL`; `resolveAPIKey` supports explicit config or `ANTHROPIC_API_KEY`.
- `func NewAnthropic(cfg AnthropicConfig) (Model, error)` (`anthropic.go:35`) initializes the Anthropic SDK, default token/retry settings, and `mapModelName`. `AnthropicConfig` accepts `HTTPClient` overrides.
- `(*anthropicModel).Complete` and `CompleteStream` use `buildParams`, `msgs.New`, `msgs.NewStreaming` to call the official SDK. `CompleteStream` handles `ContentBlockDeltaEvent` / `ToolUse` / `MessageDelta`, and emits `StreamResult{Final: true}` when done.

```go
provider := &model.AnthropicProvider{
	ModelName: "claude-3-5-sonnet",
	CacheTTL:  5 * time.Minute,
}
mdl, err := provider.Model(context.Background())
req := model.Request{
	Messages: []model.Message{
		{Role: "user", Content: "summarize README"},
	},
	MaxTokens: 1024,
}
resp, err := mdl.Complete(ctx, req)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("usage: %d tokens, stop reason=%s\n", resp.Usage.TotalTokens, resp.StopReason)
```

- **Notes**: `AnthropicProvider` caches only one model instance; `CacheTTL <= 0` disables caching to avoid stale clients. `CompleteStream` requires a non-nil `StreamHandler`, otherwise returns `stream callback required`. With tools enabled, `convertTools` strictly validates schemas and fails fast on bad params.

### Streaming and Retry

- `CompleteStream` estimates input tokens via `msgs.CountTokens` (best-effort) and accumulates `usage` during the stream; `MessageDeltaEvent` updates `CacheReadTokens`, etc., then `usageFromFallback` merges on completion.
- `doWithRetry` (same file) applies fixed retry attempts honoring outer `ctx`; control via `AnthropicConfig.MaxRetries` (negative treated as zero).
- `buildParams` picks token limits from `Request.MaxTokens` or defaults; `selectModel` uses request `Model`, then provider `ModelName`, then SDK defaults.
- `convertMessages` / `convertTools` translate internal `model.Request` into Anthropic SDK params; when both `Request.System` and `AnthropicConfig.System` are empty, no `system` block is sent.
- To stop streaming gracefully, have `StreamHandler` check `ctx.Done()` and return that error; the Agent will end immediately.

## pkg/tool — Tool Interface, Registry, ToolCall, ToolResult

- `type Tool interface` (`tool.go:6`) includes `Name`, `Description`, `Schema() *JSONSchema`, `Execute(ctx, params)`. If `Schema` is `nil`, the registry skips validation.
- `type JSONSchema`, `type Validator`, `DefaultValidator` live in `schema.go`/`validator.go`; the Registry calls `validator.Validate` before execution. Use `registry.SetValidator` to inject a custom one.
- `type Registry struct` (`registry.go:20`) offers thread-safe `Register`, `Get`, `List`, `Execute`. `Register` rejects empty or duplicate names; `Execute` validates schema then runs the tool.
- MCP integration: `RegisterMCPServer(ctx, serverPath)` (`registry.go:118`) builds SSE or stdio `ClientSession` via `newMCPClient`, iterates remote tool descriptors into `remoteTool`; remote and local tools share the same namespace.
- Resource cleanup: `Registry.Close()` (`registry.go:198`) closes tracked MCP sessions; repeat calls are safe, close errors are logged and ignored.
- `type Executor struct` (`executor.go:16`) binds a `Registry` with optional `sandbox.Manager`. `Execute` clones params, enforces sandbox, then runs the tool. `ExecuteAll` runs tools concurrently while preserving order.
- `type Call` (`types.go:14`) encapsulates a tool call with `Path`, `Host`, `Usage sandbox.ResourceUsage` so sandbox can leverage request context.
- `type CallResult` (`types.go:36`) records `StartedAt`, `CompletedAt`, `Duration()`. On error, `Err` is set and `Result` may be nil.
- `type ToolResult` (`result.go:3`) exposes `Success`, `Output`, `Data`, `Error` for structured payloads.

```go
reg := tool.NewRegistry()
_ = reg.Register(&ListFilesTool{})
executor := tool.NewExecutor(reg, sandbox.NewManager("/repo", nil))
call := tool.Call{Name: "list_files", Params: map[string]any{"path": "."}}
res, err := executor.Execute(ctx, call)
if err != nil {
	log.Fatal(err)
}
fmt.Printf("%s -> success=%v output=%s\n", res.Call.Name, res.Result.Success, res.Result.Output)
```

- **Notes**: `Executor.Execute` returns `executor is not initialised` if executor or registry is nil. `RegisterMCPServer` requires non-empty, non-duplicate remote tool names. `cloneParams` deep-copies maps/slices but not structs—copy shared byte slices beforehand. When sandbox restricts hosts/paths, `Call.Path` must be absolute.

### Dispatch Extensions

- `Executor.ExecuteAll` (`executor.go:55`) launches one goroutine per call; cancellation via context stops early. Order follows the input slice, so callers can sort for predictable logs.
- `registry.hasTool` (`registry.go:169`) checks conflicts before registering MCP tools, preventing remote override of local ones; to override, create a new `Registry` or remove the existing tool (no public Unregister).
- `remoteTool` wraps an MCP tool as local; its `Execute` calls `client.CallTool` (see later in `registry.go`). Remote schemas still go through the validator.
- `Call.cloneParams` (`types.go:25`) recursively handles `map[string]any` and `[]any`; it does not deep-copy structs. Copy nested buffers yourself if needed.
- `CallResult.Duration` is useful for timing metrics; it feeds into `core/events.ToolResultPayload.Duration`.

## pkg/mcp — MCP Client and Compatibility Layer

- Compatibility: `type SpecClient` / `NewSpecClient(spec string)` (`pkg/mcp/mcp.go:63-108`) create a `ClientSession` from a spec string and expose trimmed `ListTools`, `InvokeTool`, `Close`. **Deprecated**—only for legacy API compatibility; prefer the go-sdk `ClientSession`.

## pkg/message — Store, Session, LRU Backbone

- `type Message` and `type ToolCall` (`converter.go:6-17`) are minimal model message forms containing `Role`, `Content`, `ToolCalls`, keeping this package decoupled from specific models.
- `func CloneMessage` / `CloneMessages` (`converter.go:19-40`) deep-copy messages to avoid cross-talk when callers mutate slices.
- `type History struct` (`history.go:7`) holds `messages []Message` + `sync.RWMutex`, with `Append`, `Replace`, `All`, `Last`, `Len`, `Reset`. Returns are cloned to prevent external mutation.
- `func NewHistory() *History` returns an initialized instance; `Append` clones inputs to avoid post-append edits.
- `type TokenCounter` / `type NaiveCounter` (`trimmer.go:4-16`) estimate tokens; defaults overestimate via char length to reduce over-limit risk.
- `type Trimmer struct` (`trimmer.go:22`) combines `MaxTokens` and `Counter`; `Trim(history []Message) []Message` walks backward until budget hits, then reverses to keep timeline.
- Sessions/LRU: managed in `pkg/api/agent.go:849` by `historyStore`, but underlying `message.History` comes from this package. Once evicted, the pointer is discarded; copy `History.All()` for long-term retention.

```go
hist := message.NewHistory()
hist.Append(message.Message{Role: "user", Content: "ping"})
hist.Append(message.Message{Role: "assistant", Content: "pong"})
trim := message.NewTrimmer(100, nil)
active := trim.Trim(hist.All())
fmt.Printf("kept %d messages\n", len(active))
```

- **Notes**: `History` is in-memory only. `Trimmer.Trim` returns an empty slice when `MaxTokens <= 0`—intentionally fail-closed. LRU eviction happens in API; old `History` pointers still read data but no new messages are written. `CloneMessage` shallow-copies maps; callers must handle nested maps/slices.

### Session and LRU Semantics

- `historyStore` (`pkg/api/agent.go:849`) maps `session -> *message.History`; the same session always gets the same instance. After eviction, a new `History` is created—old data is unrecoverable.
- `lastUsed` timestamps update on every `Get`; a coarse `sync.Mutex` favors correctness over max throughput in high concurrency.
- Default `maxSize` is `api.defaultMaxSessions (1000)`; adjust via `api.WithMaxSessions(n)` (`options.go:149`). `n <= 0` is ignored.
- For external persistence, call `History.All()` at session end and store results; on restore, use `Replace`. Clone messages first to avoid mutation.
- `History.Replace` / `Reset` are hot paths; trim inputs beforehand (e.g., `Trimmer.Trim`) to avoid token overruns upstream.

## pkg/core/events — Event Bus and Deduplication

- `type EventType string` (`types.go:8`) predefines `PreToolUse`, `PostToolUse`, `UserPromptSubmit`, `SessionStart`, `Stop`, `SubagentStop`, `Notification`—the allowed subscription set.
- `type Event struct` (`types.go:22`) carries `ID`, `Type`, `Timestamp`, `Payload`. `Validate()` only checks non-empty `Type`; `Bus.Publish` fills the rest.
- `type Handler func(context.Context, Event)` (`bus.go:13`) is a subscriber callback. Each subscription uses its own goroutine + channel to prevent slow handlers from blocking others.
- `type Bus struct` (`bus.go:17`) is the router with `queue`, `subs`, `deduper`, `baseCtx`, `bufSize`, etc. `NewBus(opts...)` starts the loop and returns the pointer.
- `BusOption` (`bus.go:34`) supports `WithBufferSize`, `WithQueueDepth`, `WithDedupWindow`; the latter uses an LRU deduper (default 256) to filter duplicates.
- `(*Bus).Publish(evt Event) error` validates, fills `ID`/`Timestamp`, checks closure, then enqueues asynchronously. Events with IDs in the dedup window are dropped.
- `(*Bus).Subscribe(t EventType, handler Handler, opts...) func()` registers a subscription and returns a cancel function; `WithSubscriptionTimeout` wraps the handler with `context.WithTimeout` per event.
- `(*Bus).Close()` stops the loop, closes channels, and waits on the `WaitGroup`; repeat calls are safe.

```go
bus := events.NewBus(events.WithDedupWindow(128))
unsubscribe := bus.Subscribe(events.UserPromptSubmit, func(ctx context.Context, evt events.Event) {
	payload := evt.Payload.(events.UserPromptPayload)
	log.Printf("prompt=%s", payload.Prompt)
})
_ = bus.Publish(events.Event{
	Type:    events.UserPromptSubmit,
	Payload: events.UserPromptPayload{Prompt: "hello"},
})
unsubscribe()
bus.Close()
```

- **Notes**: `Publish` returns `bus closed` if the bus is shut down. Subscribers must assert `Payload` types consistently. Queue depth is set by `WithBufferSize`; too small will block handlers. Dedup is keyed on event ID—callers must ensure stable IDs.

### Subscription Lifecycle

- `subscription` (`bus.go:118`) wraps the handler in its own consumer goroutine/channel. `stop()` closes the channel and waits for exit; `removeSubscription` deletes from the map before `stop` to avoid deadlocks.
- `SubscriptionOption.WithSubscriptionTimeout` wraps each event with `context.WithTimeout`; handlers should check `ctx.Err()` to avoid slowing fan-out.
- `deduper` (internal) holds an LRU list sized by `WithDedupWindow`; too small admits duplicates, too large increases memory.
- Handlers should avoid blocking I/O; if needed, spawn a goroutine to offload work and prevent backlog.

## pkg/api — Unified Entry, Request, Response

- `type Options` (`pkg/api/options.go:52`) configures Runtime. Key fields: `EntryPoint`, `Mode ModeContext`, `ProjectRoot`, `ClaudeDir`, `Model model.Model`, `ModelFactory`, `SystemPrompt`, `Middleware []middleware.Middleware`, `MiddlewareTimeout`, `MaxIterations`, `Timeout`, `TokenLimit`, `MaxSessions`, `Tools []tool.Tool`, `EnabledBuiltinTools`, `CustomTools`, `MCPServers []string`, `TypedHooks`, `HookMiddleware`, `Skills`, `Commands`, `Subagents`, `Sandbox SandboxOptions`. `withDefaults` sets `EntryPoint`, `Mode.EntryPoint`, `ProjectRoot`, `Sandbox.Root`, `MaxSessions`.
- `type Request` (`options.go:115`) includes `Prompt`, `Mode`, `SessionID`, `Traits`, `Tags`, `Channels`, `Metadata`, `TargetSubagent`, `ToolWhitelist`, `ForceSkills`. `request.normalized` (`pkg/api/agent.go:150`) fills `SessionID`, merges `Mode`, trims prompt.
- `type Response` (`options.go:132`) combines Agent output, skill/command results, hook events, sandbox report, `Settings`, and plugin snapshots. `Result` embeds `model.Usage` and `ToolCalls`.
- `type Runtime struct` (`agent.go:24`) wires config loader, sandbox, tool registry/executor, hooks, `historyStore`, skills/commands/subagents managers, with `sync.RWMutex` for mutable config. Hook events are now recorded per request; `Runtime.recorder` is deprecated and retained only for backward compatibility.
- `func New(ctx, opts) (*Runtime, error)` (`agent.go:40`) loads settings, resolves model, builds sandbox, registers tools/MCP servers, sets up hooks/skills/commands/subagents, and creates `newHistoryStore(opts.MaxSessions)`.
- `func (rt *Runtime) Run(ctx, req) (*Response, error)` (`agent.go:70`) executes the sync flow: `prepare` validates prompt, fetches history, runs commands/skills/subagents, builds `middleware.State`, then calls `runAgent`.
- `func (rt *Runtime) RunStream(ctx, req) (<-chan StreamEvent, error)` (`agent.go:88`) builds a progress middleware and writes `StreamEvent` (`pkg/api/stream.go:5`) to a channel. Types include Anthropic-compatible `message_*` plus `agent_start`, `tool_execution_*`, `error`.
- `type StreamEvent` / `Message` / `ContentBlock` / `Delta` / `Usage` (`stream.go:20-78`) mirror SSE payloads; all fields are optional with JSON tags.
- `historyStore` (`agent.go:849`) manages `map[string]*message.History` and `lastUsed`; `Get(id)` calls `evictOldest()` when exceeding `maxSize` (default 1000 or `Opts.MaxSessions`). Implements the LRU required by the docs.
- Events/Hooks: `HookRecorder`, `corehooks.Executor`, and `core/events.Event` work together; `newProgressMiddleware` turns `middleware.StageBeforeModel` / `StageAfterModel`, etc., into SSE events.

```go
rt, err := api.New(ctx, api.Options{
	EntryPoint: api.EntryPointCLI,
	ModelFactory: model.ModelFactoryFunc(func(ctx context.Context) (model.Model, error) {
		return model.NewAnthropic(model.AnthropicConfig{
			APIKey: os.Getenv("ANTHROPIC_API_KEY"),
		})
	}),
	Tools: []tool.Tool{&EchoTool{}},
	Middleware: []middleware.Middleware{
		middleware.Funcs{Identifier: "logging", OnBeforeAgent: func(ctx context.Context, st *middleware.State) error {
			fmt.Println("starting agent")
			return nil
		}},
	},
	MiddlewareTimeout: 5 * time.Second,
	Sandbox: api.SandboxOptions{Root: ".", AllowedPaths: []string{"./workspace"}},
})
resp, err := rt.Run(ctx, api.Request{Prompt: "list repo stats", SessionID: "cli-1"})
if err != nil {
	log.Fatal(err)
}
fmt.Printf("response=%s stop=%s tokens=%d\n", resp.Result.Output, resp.Result.StopReason, resp.Result.Usage.TotalTokens)
```

### Streaming Usage

```go
eventsCh, err := rt.RunStream(ctx, api.Request{
	Prompt:    "scan project and call /bin/ls if needed",
		SessionID: "cli-2",
})
if err != nil {
	log.Fatal(err)
}
for evt := range eventsCh {
	switch evt.Type {
	case api.EventToolExecutionStart:
		fmt.Printf("tool %s started (iteration %d)\n", evt.Name, deref(evt.Iteration))
	case api.EventContentBlockDelta:
		fmt.Print(evt.Delta.Text)
	case api.EventError:
		fmt.Printf("error: %v\n", evt.Output)
	}
}
```

### ModeContext and Sandbox

- `ModeContext` (`options.go:41`) bundles `EntryPoint` with `CLIContext`, `CIContext`, `PlatformContext`. When `Request.Mode` is empty, Runtime fills it from `Options.Mode`. CLI/CI/Platform structs allow `Metadata`/`Labels` for hooks or skills.
- `SandboxOptions` (`options.go:87`) exposes `Root`, `AllowedPaths`, `NetworkAllow`, `ResourceLimit sandbox.ResourceLimits`; `buildSandboxManager` converts to `sandbox.Manager` shared with the tool executor.
- `SkillRegistration`, `CommandRegistration`, `SubagentRegistration` (`options.go:95-111`) bind declarative runtime definitions with handlers for CLI entry. `registerSkills/Commands/Subagents` validate non-nil handlers.
- `WithMaxSessions` (`options.go:149`) returns a configurator to adjust `Options.MaxSessions` before `api.New`; used with `historyStore` for dynamic session caps.
- `Request.ToolWhitelist` converts to `map[string]struct{}` during `prepare` and gates tool execution; disallowed tools are rejected early.

### Response Details

- `Response.Result` (`options.go:137`) exists on success and contains `Output`, `StopReason`, `Usage`, `ToolCalls`; may be `nil` on early failure.
- `Response.SkillResults`, `CommandResults`, `Subagent` surface declarative outputs; failures populate `Err`.
- `Response.HookEvents` come from `core/events`; `SandboxReport` reflects `SandboxOptions` plus runtime-derived paths; useful for CLI/HTTP exposure of safety settings.
- `Response.Tags` merges `Request.Tags` with forced metadata tags (`mergeTags`), aiding audit.

### Request Normalization Path

- `Request.normalized` (`agent.go:150`) auto-generates `session` via `defaultSessionID` and trims prompt.
- `prepare` (`agent.go:184`) runs slash commands (`executeCommands`) before parsing prompt; matched commands are stripped (`removeCommandLines`).
- `activationContext` and `applyPromptMetadata` translate `Metadata` keys prefixed `api.*` into prompt prepend/append/override; happens before model invocation.
- `toolWhitelist` becomes a map passed to tool execution; missing tools are skipped and logged to prevent policy bypass.

- **Notes**: `Options` require `Model` or `ModelFactory`; missing both returns `ErrMissingModel`. `RunStream` uses an internal goroutine; callers must consume the channel to avoid blocking. `historyStore` is in-memory only. `ToolWhitelist`/`ForceSkills` act at declarative runtime; Agent still iterates all model `ToolCalls`. `SandboxOptions` without `AllowedPaths` default to root-only—overly strict settings cause tool failures.

## Concurrency Model

`pkg/api.Runtime` is fully **thread-safe** with automatic session-level serialization:

**Concurrency Guarantees:**
- **All Runtime methods are thread-safe**: `Run`, `RunStream`, `Close`, `Config`, `Settings`, `GetSessionStats`, etc. can be called concurrently from any goroutine.
- **Same `SessionID`**: Requests are automatically queued and executed serially. The second request waits for the first to complete—no `ErrConcurrentExecution` is thrown (unless context is cancelled).
- **Different `SessionID`s**: Execute in parallel without blocking each other.
- **Graceful shutdown**: `Runtime.Close()` waits for all in-flight `Run`/`RunStream` calls to complete before releasing resources.
- **Zero data races**: All internal state is protected by mutexes and verified with `go test -race`.

**HTTP Server Pattern:**

Use request-scoped or client-specific session IDs so independent requests don't block each other:

```go
func (s *server) handleRun(w http.ResponseWriter, r *http.Request) {
	sessionID := strings.TrimSpace(r.Header.Get("X-Session-ID"))
	if sessionID == "" {
		sessionID = fmt.Sprintf("req-%d", time.Now().UnixNano())
	}

	// Multiple goroutines can call Runtime.Run concurrently
	resp, err := s.runtime.Run(r.Context(), api.Request{
		Prompt:    "your prompt here",
		SessionID: sessionID, // Different IDs = parallel execution
	})
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	_ = resp // write response
}
```

**Implementation Details:**
- `sessionGate` (pkg/api/runtime_helpers.go:1) uses `sync.Map` + channel semaphores for per-session locking.
- `beginRun`/`endRun` coordinate with `Runtime.Close()` via `sync.WaitGroup` to prevent resource leaks.
- Each request gets its own `hookRecorder` (pkg/api/runtime_helpers.go:30) to avoid shared state races.
- `Options.frozen()` deep-copies configuration during `api.New` to prevent external mutation races.

## v0.4.0 New APIs

### Token Statistics

- `type TokenStats` (`pkg/api/token.go`) tracks token usage across requests: `InputTokens`, `OutputTokens`, `CacheReadTokens`, `CacheCreationTokens`, `TotalTokens`.
- `type TokenTracker` (`pkg/api/token.go`) accumulates stats across turns with thread-safe access via `Record(stats)` and `GetStats()`.
- `Options.TokenCallback` is called **synchronously** after each model call for real-time monitoring. The callback should be lightweight and non-blocking to avoid delaying agent execution. If async processing is needed, spawn a goroutine inside the callback.

```go
rt, _ := api.New(ctx, api.Options{
    ModelFactory: provider,
    TokenTracking: true,
    TokenCallback: func(stats api.TokenStats) {
        // Called synchronously - keep it fast
        log.Printf("Input: %d, Output: %d, Cache: %d",
            stats.InputTokens, stats.OutputTokens, stats.CacheRead)

        // For slow operations, use a goroutine:
        // go sendToMetricsServer(stats)
    },
})
```

### Auto Compact

- `type Compactor` (`pkg/api/compact.go`) monitors token usage and triggers context compression when `CompactThreshold` is exceeded.
- Uses a separate model (`CompactModel`) for summarization to reduce costs.
- `ShouldCompact(currentTokens)` checks threshold; `Compact(ctx, history)` generates summary.

```go
rt, _ := api.New(ctx, api.Options{
    ModelFactory:     provider,
    CompactThreshold: 100000,              // Trigger at 100k tokens
    CompactModel:     "claude-haiku-4-5",  // Cheaper model for compression
})
```

### Multi-model Support (ModelFactory)

- `Options.ModelFactory` is now `func(ctx context.Context) model.Model` instead of direct `model.Model`.
- Enables subagent-level model binding where different agents use different models.
- `model.NewAnthropicProvider(opts...)` returns a provider implementing the factory pattern.

```go
// Different models for main agent and subagents
mainProvider := model.NewAnthropicProvider(model.WithModel("claude-sonnet-4-5"))
haiku := model.NewAnthropicProvider(model.WithModel("claude-haiku-4-5"))

rt, _ := api.New(ctx, api.Options{
    ModelFactory: mainProvider,
    Subagents: []api.SubagentRegistration{
        {Name: "quick-tasks", ModelFactory: haiku},
    },
})
```

### DisallowedTools

- `Options.DisallowedTools []string` blocks specific tools at runtime.
- Tools in this list are filtered during registration and rejected during execution.
- Configure via `api.Options` or `.claude/settings.json` (`disallowedTools` array).

```go
rt, _ := api.New(ctx, api.Options{
    ModelFactory:     provider,
    DisallowedTools:  []string{"web_search", "web_fetch"},
})
```

### Rules Configuration

- `type RulesLoader` (`pkg/config/rules.go`) loads markdown rules from `.claude/rules/` directory.
- Supports hot-reload via fsnotify; rules are sorted alphabetically and merged into system prompt.
- `GetContent()` returns concatenated rules; `WatchChanges()` enables live updates.

```go
loader := config.NewRulesLoader("/project/.claude/rules")
rules := loader.GetContent() // "# Rule 1\n...\n# Rule 2\n..."
loader.WatchChanges()        // Enable hot-reload
defer loader.Close()
```

### UUID Tracking

- `Request.RequestID` and `Response.RequestID` provide request-level UUID for observability.
- Auto-generated if empty; persists through the request lifecycle.
- Useful for distributed tracing and log correlation.

```go
resp, _ := rt.Run(ctx, api.Request{
    Prompt:    "task",
    RequestID: "custom-uuid-123",  // Or leave empty for auto-generation
})
log.Printf("Request %s completed", resp.RequestID)
```

### OpenTelemetry Integration

- `type Tracer` (`pkg/api/otel.go`) wraps OpenTelemetry spans for agent operations.
- `Options.TracerProvider` accepts a custom `trace.TracerProvider`.
- Spans are created for agent runs, model calls, and tool executions.

```go
import "go.opentelemetry.io/otel"

rt, _ := api.New(ctx, api.Options{
    ModelFactory:    provider,
    TracerProvider:  otel.GetTracerProvider(),
})
```

### Async Bash

- Bash tool now supports `background: true` parameter for non-blocking execution.
- Returns a task ID immediately; use `bash_output` tool to retrieve results later.
- `AsyncTaskManager` (`pkg/tool/builtin/async.go`) manages background tasks with configurable limits.

```go
// Model can request background execution:
// {"name": "bash", "params": {"command": "long-running-task", "background": true}}
// Returns: {"task_id": "abc123"}

// Later, retrieve output:
// {"name": "bash_output", "params": {"task_id": "abc123"}}
```

# Completeness

Sections stay within ~300–400 lines, covering core interfaces, signatures, examples, and notes, focusing on `pkg/message`, `pkg/core/events`, and `pkg/middleware` as key API references for the current SDK.
