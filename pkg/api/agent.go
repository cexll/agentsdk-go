package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"maps"
	"net/url"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/agent"
	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/plugins"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
	toolbuiltin "github.com/cexll/agentsdk-go/pkg/tool/builtin"
	"github.com/google/uuid"
)

type contextKey string

const middlewareStateKey contextKey = "agentsdk.middleware.state"
const streamEmitCtxKey contextKey = "agentsdk.stream.emit"

func withStreamEmit(ctx context.Context, emit streamEmitFunc) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if emit == nil {
		return ctx
	}
	return context.WithValue(ctx, streamEmitCtxKey, emit)
}

func streamEmitFromContext(ctx context.Context) streamEmitFunc {
	if ctx == nil {
		return nil
	}
	if emit, ok := ctx.Value(streamEmitCtxKey).(streamEmitFunc); ok {
		return emit
	}
	return nil
}

// Runtime exposes the unified SDK surface that powers CLI/CI/enterprise entrypoints.
type Runtime struct {
	opts        Options
	mode        ModeContext
	settings    *config.Settings
	cfg         *config.Settings
	rulesLoader *config.RulesLoader
	sandbox     *sandbox.Manager
	sbRoot      string
	registry    *tool.Registry
	executor    *tool.Executor
	// recorder is retained for backward compatibility.
	// Deprecated: hook events are now recorded per-request via preparedRun.recorder.
	recorder    HookRecorder
	hooks       *corehooks.Executor
	histories   *historyStore
	sessionGate *sessionGate

	cmdExec   *commands.Executor
	skReg     *skills.Registry
	subMgr    *subagents.Manager
	plugins   []*plugins.ClaudePlugin
	tokens    *tokenTracker
	compactor *compactor
	tracer    Tracer

	mu sync.RWMutex

	runMu     sync.Mutex
	runWG     sync.WaitGroup
	closeOnce sync.Once
	closeErr  error
	closed    bool
}

// New instantiates a unified runtime bound to the provided options.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	opts = opts.withDefaults()
	opts = opts.frozen()
	mode := opts.modeContext()

	settings, err := loadSettings(opts)
	if err != nil {
		return nil, err
	}

	mdl, err := resolveModel(ctx, opts)
	if err != nil {
		return nil, err
	}
	opts.Model = mdl

	sbox, sbRoot := buildSandboxManager(opts, settings)
	cmdExec, cmdErrs := buildCommandsExecutor(opts)
	if len(cmdErrs) > 0 {
		for _, err := range cmdErrs {
			log.Printf("command loader warning: %v", err)
		}
	}
	skReg, skErrs := buildSkillsRegistry(opts)
	if len(skErrs) > 0 {
		for _, err := range skErrs {
			log.Printf("skill loader warning: %v", err)
		}
	}
	subMgr, subErrs := buildSubagentsManager(opts)
	if len(subErrs) > 0 {
		for _, err := range subErrs {
			log.Printf("subagent loader warning: %v", err)
		}
	}
	registry := tool.NewRegistry()
	plugins, err := discoverPlugins(opts.ProjectRoot, settings)
	if err != nil {
		return nil, err
	}
	taskTool, err := registerTools(registry, opts, settings, skReg, cmdExec)
	if err != nil {
		return nil, err
	}
	mcpServers := collectMCPServers(settings, plugins, opts.MCPServers)
	if err := registerMCPServers(ctx, registry, sbox, mcpServers); err != nil {
		return nil, err
	}
	executor := tool.NewExecutor(registry, sbox)

	recorder := defaultHookRecorder()
	hooks := newHookExecutor(opts, recorder, settings)
	compactor := newCompactor(opts.ProjectRoot, opts.AutoCompact, opts.Model, opts.TokenLimit, hooks)

	// Initialize tracer (noop without 'otel' build tag)
	tracer, err := NewTracer(opts.OTEL)
	if err != nil {
		return nil, fmt.Errorf("otel tracer init: %w", err)
	}

	var rulesLoader *config.RulesLoader
	if opts.RulesEnabled == nil || (opts.RulesEnabled != nil && *opts.RulesEnabled) {
		rulesLoader = config.NewRulesLoader(opts.ProjectRoot)
		if _, err := rulesLoader.LoadRules(); err != nil {
			log.Printf("rules loader warning: %v", err)
		}
		if err := rulesLoader.WatchChanges(nil); err != nil {
			log.Printf("rules watcher warning: %v", err)
		}
	}

	rt := &Runtime{
		opts:        opts,
		mode:        mode,
		settings:    settings,
		cfg:         projectConfigFromSettings(settings),
		rulesLoader: rulesLoader,
		sandbox:     sbox,
		sbRoot:      sbRoot,
		registry:    registry,
		executor:    executor,
		recorder:    recorder,
		hooks:       hooks,
		histories:   newHistoryStore(opts.MaxSessions),
		cmdExec:     cmdExec,
		skReg:       skReg,
		subMgr:      subMgr,
		plugins:     plugins,
		tokens:      newTokenTracker(opts.TokenTracking, opts.TokenCallback),
		compactor:   compactor,
		tracer:      tracer,
	}
	rt.sessionGate = newSessionGate()

	if taskTool != nil {
		taskTool.SetRunner(rt.taskRunner())
	}
	return rt, nil
}

func (rt *Runtime) beginRun() error {
	rt.runMu.Lock()
	defer rt.runMu.Unlock()
	if rt.closed {
		return ErrRuntimeClosed
	}
	rt.runWG.Add(1)
	return nil
}

func (rt *Runtime) endRun() {
	rt.runWG.Done()
}

// Run executes the unified pipeline synchronously.
func (rt *Runtime) Run(ctx context.Context, req Request) (*Response, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if err := rt.beginRun(); err != nil {
		return nil, err
	}
	defer rt.endRun()

	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	if err := rt.sessionGate.Acquire(ctx, sessionID); err != nil {
		return nil, ErrConcurrentExecution
	}
	defer rt.sessionGate.Release(sessionID)

	prep, err := rt.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	result, err := rt.runAgent(prep)
	if err != nil {
		return nil, err
	}
	return rt.buildResponse(prep, result), nil
}

// RunStream executes the pipeline asynchronously and returns events over a channel.
func (rt *Runtime) RunStream(ctx context.Context, req Request) (<-chan StreamEvent, error) {
	if rt == nil {
		return nil, ErrRuntimeClosed
	}
	if strings.TrimSpace(req.Prompt) == "" {
		return nil, errors.New("api: prompt is empty")
	}
	sessionID := strings.TrimSpace(req.SessionID)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	req.SessionID = sessionID

	if err := rt.beginRun(); err != nil {
		return nil, err
	}

	// 缓冲区增大以吸收前端延迟（逐字符渲染等）导致的背压，避免 progress emit 阻塞工具执行
	out := make(chan StreamEvent, 512)
	progressChan := make(chan StreamEvent, 256)
	baseCtx := ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	progressMW := newProgressMiddleware(progressChan)
	ctxWithEmit := withStreamEmit(baseCtx, progressMW.streamEmit())
	go func() {
		defer rt.endRun()
		defer close(out)
		if err := rt.sessionGate.Acquire(ctxWithEmit, sessionID); err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: ErrConcurrentExecution.Error(), IsError: &isErr}
			return
		}
		defer rt.sessionGate.Release(sessionID)

		prep, err := rt.prepare(ctxWithEmit, req)
		if err != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: err.Error(), IsError: &isErr}
			return
		}

		done := make(chan struct{})
		go func() {
			defer close(done)
			dropping := false
			for event := range progressChan {
				if dropping {
					continue
				}
				select {
				case out <- event:
				case <-ctxWithEmit.Done():
					dropping = true
				}
			}
		}()

		result, runErr := rt.runAgentWithMiddleware(prep, progressMW)
		close(progressChan)
		<-done

		if runErr != nil {
			isErr := true
			out <- StreamEvent{Type: EventError, Output: runErr.Error(), IsError: &isErr}
			return
		}
		rt.buildResponse(prep, result)
	}()
	return out, nil
}

// Close releases held resources.
func (rt *Runtime) Close() error {
	if rt == nil {
		return nil
	}
	rt.closeOnce.Do(func() {
		rt.runMu.Lock()
		rt.closed = true
		rt.runMu.Unlock()

		rt.runWG.Wait()

		var err error
		if rt.rulesLoader != nil {
			if e := rt.rulesLoader.Close(); e != nil {
				err = e
			}
		}
		if rt.registry != nil {
			rt.registry.Close()
		}
		if rt.tracer != nil {
			if e := rt.tracer.Shutdown(); e != nil && err == nil {
				err = e
			}
		}
		rt.closeErr = err
	})
	return rt.closeErr
}

// Config returns the last loaded project config.
func (rt *Runtime) Config() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.cfg)
}

// Settings exposes the merged settings.json snapshot for callers that need it.
func (rt *Runtime) Settings() *config.Settings {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return config.MergeSettings(nil, rt.settings)
}

// Sandbox exposes the sandbox manager.
func (rt *Runtime) Sandbox() *sandbox.Manager { return rt.sandbox }

// GetSessionStats returns aggregated token stats for a session.
func (rt *Runtime) GetSessionStats(sessionID string) *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetSessionStats(sessionID)
}

// GetTotalStats returns aggregated token stats across all sessions.
func (rt *Runtime) GetTotalStats() *SessionTokenStats {
	if rt == nil || rt.tokens == nil {
		return nil
	}
	return rt.tokens.GetTotalStats()
}

// ----------------- internal helpers -----------------

type preparedRun struct {
	ctx            context.Context
	prompt         string
	history        *message.History
	normalized     Request
	recorder       *hookRecorder
	commandResults []CommandExecution
	skillResults   []SkillExecution
	mode           ModeContext
	toolWhitelist  map[string]struct{}
}

type runResult struct {
	output *agent.ModelOutput
	usage  model.Usage
	reason string
}

func (rt *Runtime) prepare(ctx context.Context, req Request) (preparedRun, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	fallbackSession := defaultSessionID(rt.mode.EntryPoint)
	normalized := req.normalized(rt.mode, fallbackSession)
	prompt := strings.TrimSpace(normalized.Prompt)
	if prompt == "" {
		return preparedRun{}, errors.New("api: prompt is empty")
	}

	if normalized.SessionID == "" {
		normalized.SessionID = fallbackSession
	}

	// Auto-generate RequestID if not provided (UUID tracking)
	if normalized.RequestID == "" {
		normalized.RequestID = uuid.New().String()
	}

	history := rt.histories.Get(normalized.SessionID)
	recorder := defaultHookRecorder()

	if rt.compactor != nil {
		if _, _, err := rt.compactor.maybeCompact(ctx, history, normalized.SessionID, recorder); err != nil {
			return preparedRun{}, err
		}
	}

	activation := normalized.activationContext(prompt)

	cmdRes, cleanPrompt, err := rt.executeCommands(ctx, prompt, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = cleanPrompt
	activation.Prompt = prompt

	skillRes, promptAfterSkills, err := rt.executeSkills(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSkills
	activation.Prompt = prompt
	whitelist := combineToolWhitelists(normalized.ToolWhitelist, nil)
	return preparedRun{
		ctx:            ctx,
		prompt:         prompt,
		history:        history,
		normalized:     normalized,
		recorder:       recorder,
		commandResults: cmdRes,
		skillResults:   skillRes,
		mode:           normalized.Mode,
		toolWhitelist:  whitelist,
	}, nil
}

func (rt *Runtime) runAgent(prep preparedRun) (runResult, error) {
	return rt.runAgentWithMiddleware(prep)
}

func (rt *Runtime) runAgentWithMiddleware(prep preparedRun, extras ...middleware.Middleware) (runResult, error) {
	// Select model based on request tier or subagent mapping
	selectedModel, selectedTier := rt.selectModelForSubagent(prep.normalized.TargetSubagent, prep.normalized.Model)

	// Emit ModelSelected event if a non-default model was selected
	if selectedTier != "" {
		hookAdapter := &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder}
		// Best-effort event emission; errors are logged but don't block execution
		if err := hookAdapter.ModelSelected(prep.ctx, coreevents.ModelSelectedPayload{
			ToolName:  prep.normalized.TargetSubagent,
			ModelTier: string(selectedTier),
			Reason:    "subagent model mapping",
		}); err != nil {
			log.Printf("api: failed to emit ModelSelected event: %v", err)
		}
	}

	modelAdapter := &conversationModel{
		base:         selectedModel,
		history:      prep.history,
		prompt:       prep.prompt,
		trimmer:      rt.newTrimmer(),
		tools:        availableTools(rt.registry, prep.toolWhitelist),
		systemPrompt: rt.opts.SystemPrompt,
		rulesLoader:  rt.rulesLoader,
		hooks:        &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder},
		recorder:     prep.recorder,
		compactor:    rt.compactor,
		sessionID:    prep.normalized.SessionID,
	}

	toolExec := &runtimeToolExecutor{
		executor: rt.executor,
		hooks:    &runtimeHookAdapter{executor: rt.hooks, recorder: prep.recorder},
		history:  prep.history,
		allow:    prep.toolWhitelist,
		root:     rt.sbRoot,
		host:     "localhost",
	}

	chainItems := make([]middleware.Middleware, 0, len(rt.opts.Middleware)+len(extras))
	if len(rt.opts.Middleware) > 0 {
		chainItems = append(chainItems, rt.opts.Middleware...)
	}
	if len(extras) > 0 {
		chainItems = append(chainItems, extras...)
	}
	chain := middleware.NewChain(chainItems, middleware.WithTimeout(rt.opts.MiddlewareTimeout))
	ag, err := agent.New(modelAdapter, toolExec, agent.Options{
		MaxIterations: rt.opts.MaxIterations,
		Timeout:       rt.opts.Timeout,
		Middleware:    chain,
	})
	if err != nil {
		return runResult{}, err
	}

	agentCtx := agent.NewContext()
	if sessionID := strings.TrimSpace(prep.normalized.SessionID); sessionID != "" {
		agentCtx.Values["session_id"] = sessionID
	}
	// Propagate RequestID through agent context for distributed tracing
	if requestID := strings.TrimSpace(prep.normalized.RequestID); requestID != "" {
		agentCtx.Values["request_id"] = requestID
	}
	if len(prep.normalized.ForceSkills) > 0 {
		agentCtx.Values["request.force_skills"] = append([]string(nil), prep.normalized.ForceSkills...)
	}
	if rt.skReg != nil {
		agentCtx.Values["skills.registry"] = rt.skReg
	}
	out, err := ag.Run(prep.ctx, agentCtx)
	if err != nil {
		return runResult{}, err
	}
	if rt.tokens != nil && rt.tokens.IsEnabled() {
		stats := tokenStatsFromUsage(modelAdapter.usage, "", prep.normalized.SessionID, prep.normalized.RequestID)
		rt.tokens.Record(stats)
		payload := coreevents.TokenUsagePayload{
			InputTokens:   stats.InputTokens,
			OutputTokens:  stats.OutputTokens,
			TotalTokens:   stats.TotalTokens,
			CacheCreation: stats.CacheCreation,
			CacheRead:     stats.CacheRead,
			Model:         stats.Model,
			SessionID:     stats.SessionID,
			RequestID:     stats.RequestID,
		}
		if rt.hooks != nil {
			//nolint:errcheck // token usage events are non-critical notifications
			rt.hooks.Publish(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
		if prep.recorder != nil {
			prep.recorder.Record(coreevents.Event{
				Type:      coreevents.TokenUsage,
				SessionID: stats.SessionID,
				RequestID: stats.RequestID,
				Payload:   payload,
			})
		}
	}
	return runResult{output: out, usage: modelAdapter.usage, reason: modelAdapter.stopReason}, nil
}

func (rt *Runtime) buildResponse(prep preparedRun, result runResult) *Response {
	events := []coreevents.Event(nil)
	if prep.recorder != nil {
		events = prep.recorder.Drain()
	}
	resp := &Response{
		Mode:            prep.mode,
		RequestID:       prep.normalized.RequestID,
		Result:          convertRunResult(result),
		CommandResults:  prep.commandResults,
		SkillResults:    prep.skillResults,
		HookEvents:      events,
		ProjectConfig:   rt.Settings(),
		Settings:        rt.Settings(),
		Plugins:         snapshotPlugins(rt.plugins),
		SandboxSnapshot: rt.sandboxReport(),
		Tags:            maps.Clone(prep.normalized.Tags),
	}
	return resp
}

func (rt *Runtime) sandboxReport() SandboxReport {
	report := snapshotSandbox(rt.sandbox)

	var roots []string
	if root := strings.TrimSpace(rt.sbRoot); root != "" {
		roots = append(roots, root)
	}
	report.Roots = cloneStrings(roots)

	allowed := make([]string, 0, len(rt.opts.Sandbox.AllowedPaths))
	for _, path := range rt.opts.Sandbox.AllowedPaths {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	for _, path := range additionalSandboxPaths(rt.settings) {
		if clean := strings.TrimSpace(path); clean != "" {
			allowed = append(allowed, clean)
		}
	}
	report.AllowedPaths = cloneStrings(allowed)

	domains := rt.opts.Sandbox.NetworkAllow
	if len(domains) == 0 {
		domains = defaultNetworkAllowList(rt.opts.EntryPoint)
	}
	var cleanedDomains []string
	for _, domain := range domains {
		if host := strings.TrimSpace(domain); host != "" {
			cleanedDomains = append(cleanedDomains, host)
		}
	}
	report.AllowedDomains = cloneStrings(cleanedDomains)
	return report
}

func convertRunResult(res runResult) *Result {
	if res.output == nil {
		return nil
	}
	toolCalls := make([]model.ToolCall, len(res.output.ToolCalls))
	for i, call := range res.output.ToolCalls {
		toolCalls[i] = model.ToolCall{Name: call.Name, Arguments: call.Input}
	}
	return &Result{
		Output:     res.output.Content,
		ToolCalls:  toolCalls,
		Usage:      res.usage,
		StopReason: res.reason,
	}
}

func (rt *Runtime) executeCommands(ctx context.Context, prompt string, req *Request) ([]CommandExecution, string, error) {
	if rt.cmdExec == nil {
		return nil, prompt, nil
	}
	invocations, err := commands.Parse(prompt)
	if err != nil {
		if errors.Is(err, commands.ErrNoCommand) {
			return nil, prompt, nil
		}
		return nil, "", err
	}
	cleanPrompt := removeCommandLines(prompt, invocations)
	results, err := rt.cmdExec.Execute(ctx, invocations)
	if err != nil {
		return nil, "", err
	}
	execs := make([]CommandExecution, 0, len(results))
	for _, res := range results {
		def := definitionSnapshot(rt.cmdExec, res.Command)
		execs = append(execs, CommandExecution{Definition: def, Result: res})
		cleanPrompt = applyPromptMetadata(cleanPrompt, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	return execs, cleanPrompt, nil
}

func (rt *Runtime) executeSkills(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) ([]SkillExecution, string, error) {
	if rt.skReg == nil {
		return nil, prompt, nil
	}
	matches := rt.skReg.Match(activation)
	forced := orderedForcedSkills(rt.skReg, req.ForceSkills)
	matches = append(matches, forced...)
	if len(matches) == 0 {
		return nil, prompt, nil
	}
	prefix := ""
	execs := make([]SkillExecution, 0, len(matches))
	seen := map[string]struct{}{}
	for _, match := range matches {
		skill := match.Skill
		if skill == nil {
			continue
		}
		name := skill.Definition().Name
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		res, err := skill.Execute(ctx, activation)
		execs = append(execs, SkillExecution{Definition: skill.Definition(), Result: res, Err: err})
		if err != nil {
			return execs, "", err
		}
		prefix = combinePrompt(prefix, res.Output)
		activation.Metadata = mergeMetadata(activation.Metadata, res.Metadata)
		mergeTags(req, res.Metadata)
		applyCommandMetadata(req, res.Metadata)
	}
	prompt = prependPrompt(prompt, prefix)
	prompt = applyPromptMetadata(prompt, activation.Metadata)
	return execs, prompt, nil
}

func (rt *Runtime) executeSubagent(ctx context.Context, prompt string, activation skills.ActivationContext, req *Request) (*subagents.Result, string, error) {
	if req == nil {
		return nil, prompt, nil
	}

	def, builtin := applySubagentTarget(req)
	if rt.subMgr == nil {
		return nil, prompt, nil
	}
	meta := map[string]any{
		"entrypoint": req.Mode.EntryPoint,
	}
	if len(req.Metadata) > 0 {
		if len(meta) == 0 {
			meta = map[string]any{}
		}
		for k, v := range req.Metadata {
			meta[k] = v
		}
	}
	if session := strings.TrimSpace(req.SessionID); session != "" {
		meta["session_id"] = session
	}
	request := subagents.Request{
		Target:        req.TargetSubagent,
		Instruction:   prompt,
		Activation:    activation,
		ToolWhitelist: cloneStrings(req.ToolWhitelist),
		Metadata:      meta,
	}
	dispatchCtx := ctx
	if subCtx, ok := buildSubagentContext(*req, def, builtin); ok {
		dispatchCtx = subagents.WithContext(ctx, subCtx)
	}
	res, err := rt.subMgr.Dispatch(dispatchCtx, request)
	if err != nil {
		if errors.Is(err, subagents.ErrNoMatchingSubagent) && req.TargetSubagent == "" {
			return nil, prompt, nil
		}
		return nil, "", err
	}
	text := fmt.Sprint(res.Output)
	if strings.TrimSpace(text) != "" {
		prompt = strings.TrimSpace(text)
	}
	prompt = applyPromptMetadata(prompt, res.Metadata)
	mergeTags(req, res.Metadata)
	applyCommandMetadata(req, res.Metadata)
	return &res, prompt, nil
}

func (rt *Runtime) taskRunner() toolbuiltin.TaskRunner {
	return func(ctx context.Context, req toolbuiltin.TaskRequest) (*tool.ToolResult, error) {
		return rt.runTaskInvocation(ctx, req)
	}
}

func (rt *Runtime) runTaskInvocation(ctx context.Context, req toolbuiltin.TaskRequest) (*tool.ToolResult, error) {
	if rt == nil {
		return nil, errors.New("api: runtime is nil")
	}
	if rt.subMgr == nil {
		return nil, errors.New("api: subagent manager is not configured")
	}
	prompt := strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return nil, errors.New("api: task prompt is empty")
	}
	sessionID := strings.TrimSpace(req.Resume)
	if sessionID == "" {
		sessionID = defaultSessionID(rt.mode.EntryPoint)
	}
	reqPayload := &Request{
		Prompt:         prompt,
		Mode:           rt.mode,
		SessionID:      sessionID,
		TargetSubagent: req.SubagentType,
	}
	if desc := strings.TrimSpace(req.Description); desc != "" {
		reqPayload.Metadata = map[string]any{"task.description": desc}
	}
	if req.Model != "" {
		if reqPayload.Metadata == nil {
			reqPayload.Metadata = map[string]any{}
		}
		reqPayload.Metadata["task.model"] = req.Model
	}
	activation := skills.ActivationContext{Prompt: prompt}
	if len(reqPayload.Metadata) > 0 {
		activation.Metadata = maps.Clone(reqPayload.Metadata)
	}
	dispatchCtx := subagents.WithTaskDispatch(ctx)
	res, _, err := rt.executeSubagent(dispatchCtx, prompt, activation, reqPayload)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, errors.New("api: task execution returned no result")
	}
	return convertTaskToolResult(*res), nil
}

func convertTaskToolResult(res subagents.Result) *tool.ToolResult {
	output := strings.TrimSpace(fmt.Sprint(res.Output))
	if output == "" {
		if res.Subagent != "" {
			output = fmt.Sprintf("subagent %s completed", res.Subagent)
		} else {
			output = "subagent completed"
		}
	}
	data := map[string]any{
		"subagent": res.Subagent,
	}
	if len(res.Metadata) > 0 {
		data["metadata"] = res.Metadata
	}
	if res.Error != "" {
		data["error"] = res.Error
	}
	return &tool.ToolResult{
		Success: res.Error == "",
		Output:  output,
		Data:    data,
	}
}

// selectModelForSubagent returns the appropriate model for the given subagent type.
// Priority: 1) Request.Model override, 2) SubagentModelMapping, 3) default Model.
// Returns the selected model and the tier used (empty string if default).
func (rt *Runtime) selectModelForSubagent(subagentType string, requestTier ModelTier) (model.Model, ModelTier) {
	rt.mu.RLock()
	defer rt.mu.RUnlock()

	// Priority 1: Request-level override (方案 C)
	if requestTier != "" {
		if m, ok := rt.opts.ModelPool[requestTier]; ok && m != nil {
			return m, requestTier
		}
	}

	// Priority 2: Subagent type mapping (方案 A)
	if rt.opts.SubagentModelMapping != nil {
		canonical := strings.ToLower(strings.TrimSpace(subagentType))
		if tier, ok := rt.opts.SubagentModelMapping[canonical]; ok {
			if rt.opts.ModelPool != nil {
				if m, ok := rt.opts.ModelPool[tier]; ok && m != nil {
					return m, tier
				}
			}
		}
	}

	// Priority 3: Default model
	return rt.opts.Model, ""
}

func (rt *Runtime) newTrimmer() *message.Trimmer {
	if rt.opts.TokenLimit <= 0 {
		return nil
	}
	return message.NewTrimmer(rt.opts.TokenLimit, nil)
}

// ----------------- adapters -----------------

type conversationModel struct {
	base         model.Model
	history      *message.History
	prompt       string
	trimmer      *message.Trimmer
	tools        []model.ToolDefinition
	systemPrompt string
	rulesLoader  *config.RulesLoader
	usage        model.Usage
	stopReason   string
	hooks        *runtimeHookAdapter
	recorder     *hookRecorder
	compactor    *compactor
	sessionID    string
}

func (m *conversationModel) Generate(ctx context.Context, _ *agent.Context) (*agent.ModelOutput, error) {
	if m.base == nil {
		return nil, errors.New("model is nil")
	}

	if strings.TrimSpace(m.prompt) != "" {
		m.history.Append(message.Message{Role: "user", Content: strings.TrimSpace(m.prompt)})
		if err := m.hooks.UserPrompt(ctx, m.prompt); err != nil {
			return nil, err
		}
		m.prompt = ""
	}

	if m.compactor != nil {
		if _, _, err := m.compactor.maybeCompact(ctx, m.history, m.sessionID, m.recorder); err != nil {
			return nil, err
		}
	}

	snapshot := m.history.All()
	if m.trimmer != nil {
		snapshot = m.trimmer.Trim(snapshot)
	}
	systemPrompt := m.systemPrompt
	if m.rulesLoader != nil {
		if rules := m.rulesLoader.GetContent(); rules != "" {
			systemPrompt = fmt.Sprintf("%s\n\n## Project Rules\n\n%s", systemPrompt, rules)
		}
	}
	req := model.Request{
		Messages:    convertMessages(snapshot),
		Tools:       m.tools,
		System:      systemPrompt,
		MaxTokens:   0,
		Model:       "",
		Temperature: nil,
	}

	// Populate middleware state with model request if available
	if st, ok := ctx.Value(middlewareStateKey).(*middleware.State); ok && st != nil {
		st.ModelInput = req
		if st.Values == nil {
			st.Values = map[string]any{}
		}
		st.Values["model.request"] = req
	}

	resp, err := m.base.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	m.usage = resp.Usage
	m.stopReason = resp.StopReason

	// Populate middleware state with model response and usage
	if st, ok := ctx.Value(middlewareStateKey).(*middleware.State); ok && st != nil {
		st.ModelOutput = resp
		if st.Values == nil {
			st.Values = map[string]any{}
		}
		st.Values["model.response"] = resp
		st.Values["model.usage"] = resp.Usage
		st.Values["model.stop_reason"] = resp.StopReason
	}

	assistant := message.Message{Role: resp.Message.Role, Content: strings.TrimSpace(resp.Message.Content)}
	if len(resp.Message.ToolCalls) > 0 {
		assistant.ToolCalls = make([]message.ToolCall, len(resp.Message.ToolCalls))
		for i, call := range resp.Message.ToolCalls {
			assistant.ToolCalls[i] = message.ToolCall{ID: call.ID, Name: call.Name, Arguments: call.Arguments}
		}
	}
	m.history.Append(assistant)

	out := &agent.ModelOutput{Content: assistant.Content, Done: len(assistant.ToolCalls) == 0}
	if len(assistant.ToolCalls) > 0 {
		out.ToolCalls = make([]agent.ToolCall, len(assistant.ToolCalls))
		for i, call := range assistant.ToolCalls {
			out.ToolCalls[i] = agent.ToolCall{ID: call.ID, Name: call.Name, Input: call.Arguments}
		}
	}
	return out, nil
}

type runtimeToolExecutor struct {
	executor *tool.Executor
	hooks    *runtimeHookAdapter
	history  *message.History
	allow    map[string]struct{}
	root     string
	host     string
}

func (t *runtimeToolExecutor) measureUsage() sandbox.ResourceUsage {
	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	return sandbox.ResourceUsage{MemoryBytes: stats.Alloc}
}

func (t *runtimeToolExecutor) isAllowed(ctx context.Context, name string) bool {
	canon := canonicalToolName(name)
	if canon == "" {
		return false
	}
	reqAllowed := len(t.allow) == 0
	if len(t.allow) > 0 {
		_, reqAllowed = t.allow[canon]
	}
	subCtx, ok := subagents.FromContext(ctx)
	if !ok || len(subCtx.ToolWhitelist) == 0 {
		return reqAllowed
	}
	subSet := toLowerSet(subCtx.ToolWhitelist)
	if len(subSet) == 0 {
		return reqAllowed
	}
	_, subAllowed := subSet[canon]
	if len(t.allow) == 0 {
		return subAllowed
	}
	return reqAllowed && subAllowed
}

func (t *runtimeToolExecutor) Execute(ctx context.Context, call agent.ToolCall, _ *agent.Context) (agent.ToolResult, error) {
	if t.executor == nil {
		return agent.ToolResult{}, errors.New("tool executor not initialised")
	}
	if !t.isAllowed(ctx, call.Name) {
		return agent.ToolResult{}, fmt.Errorf("tool %s is not whitelisted", call.Name)
	}
	if params, err := t.hooks.PreToolUse(ctx, coreToolUsePayload(call)); err != nil {
		return agent.ToolResult{}, err
	} else if params != nil {
		call.Input = params
	}

	callSpec := tool.Call{
		Name:   call.Name,
		Params: call.Input,
		Path:   t.root,
		Host:   t.host,
		Usage:  t.measureUsage(),
	}
	if emit := streamEmitFromContext(ctx); emit != nil {
		callSpec.StreamSink = func(chunk string, isStderr bool) {
			evt := StreamEvent{
				Type:      EventToolExecutionOutput,
				ToolUseID: call.ID,
				Name:      call.Name,
				Output:    chunk,
			}
			evt.IsStderr = &isStderr
			emit(ctx, evt)
		}
	}
	if t.host != "" {
		callSpec.Host = t.host
	}
	result, err := t.executor.Execute(ctx, callSpec)
	toolResult := agent.ToolResult{Name: call.Name}
	meta := map[string]any{}
	content := ""
	if result != nil && result.Result != nil {
		toolResult.Output = result.Result.Output
		meta["data"] = result.Result.Data
		content = result.Result.Output
	}
	if err != nil {
		meta["error"] = err.Error()
		content = fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	if len(meta) > 0 {
		toolResult.Metadata = meta
	}

	if hookErr := t.hooks.PostToolUse(ctx, coreToolResultPayload(call, result, err)); hookErr != nil && err == nil {
		// Prefer primary tool error if present; otherwise surface hook failure.
		return toolResult, hookErr
	}

	if t.history != nil {
		t.history.Append(message.Message{
			Role:    "tool",
			Content: content,
			ToolCalls: []message.ToolCall{{
				ID:        call.ID,
				Name:      call.Name,
				Arguments: call.Input,
			}},
		})
	}
	return toolResult, err
}

func coreToolUsePayload(call agent.ToolCall) coreevents.ToolUsePayload {
	return coreevents.ToolUsePayload{Name: call.Name, Params: call.Input}
}

func coreToolResultPayload(call agent.ToolCall, res *tool.CallResult, err error) coreevents.ToolResultPayload {
	payload := coreevents.ToolResultPayload{Name: call.Name}
	if res != nil && res.Result != nil {
		payload.Result = res.Result.Output
		payload.Duration = res.Duration()
	}
	payload.Err = err
	return payload
}

// ----------------- config + registries -----------------

func registerTools(registry *tool.Registry, opts Options, settings *config.Settings, skReg *skills.Registry, cmdExec *commands.Executor) (*toolbuiltin.TaskTool, error) {
	entry := effectiveEntryPoint(opts)
	tools := opts.Tools
	var taskTool *toolbuiltin.TaskTool

	if len(tools) == 0 {
		sandboxDisabled := settings != nil && settings.Sandbox != nil && settings.Sandbox.Enabled != nil && !*settings.Sandbox.Enabled
		if skReg == nil {
			skReg = skills.NewRegistry()
		}
		if cmdExec == nil {
			cmdExec = commands.NewExecutor()
		}

		factories := builtinToolFactories(opts.ProjectRoot, sandboxDisabled, entry, skReg, cmdExec)
		names := builtinOrder(entry)
		selectedNames := filterBuiltinNames(opts.EnabledBuiltinTools, names)
		for _, name := range selectedNames {
			ctor := factories[name]
			if ctor == nil {
				continue
			}
			impl := ctor()
			if impl == nil {
				continue
			}
			if t, ok := impl.(*toolbuiltin.TaskTool); ok {
				taskTool = t
			}
			tools = append(tools, impl)
		}

		if len(opts.CustomTools) > 0 {
			tools = append(tools, opts.CustomTools...)
		}
	} else {
		taskTool = locateTaskTool(tools)
	}

	disallowed := toLowerSet(opts.DisallowedTools)
	if settings != nil && len(settings.DisallowedTools) > 0 {
		if disallowed == nil {
			disallowed = map[string]struct{}{}
		}
		for _, name := range settings.DisallowedTools {
			if key := canonicalToolName(name); key != "" {
				disallowed[key] = struct{}{}
			}
		}
		if len(disallowed) == 0 {
			disallowed = nil
		}
	}

	seen := make(map[string]struct{})
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		name := strings.TrimSpace(impl.Name())
		if name == "" {
			continue
		}
		canon := canonicalToolName(name)
		if disallowed != nil {
			if _, blocked := disallowed[canon]; blocked {
				log.Printf("tool %s skipped: disallowed", name)
				continue
			}
		}
		if _, ok := seen[canon]; ok {
			log.Printf("tool %s skipped: duplicate name", name)
			continue
		}
		seen[canon] = struct{}{}
		if err := registry.Register(impl); err != nil {
			return nil, fmt.Errorf("api: register tool %s: %w", impl.Name(), err)
		}
	}

	if taskTool == nil {
		taskTool = locateTaskTool(tools)
	}
	return taskTool, nil
}

func builtinToolFactories(root string, sandboxDisabled bool, entry EntryPoint, skReg *skills.Registry, cmdExec *commands.Executor) map[string]func() tool.Tool {
	factories := map[string]func() tool.Tool{}

	bashCtor := func() tool.Tool {
		var bash *toolbuiltin.BashTool
		if sandboxDisabled {
			bash = toolbuiltin.NewBashToolWithSandbox(root, security.NewDisabledSandbox())
		} else {
			bash = toolbuiltin.NewBashToolWithRoot(root)
		}
		if entry == EntryPointCLI {
			bash.AllowShellMetachars(true)
		}
		return bash
	}

	readCtor := func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewReadToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewReadToolWithRoot(root)
	}
	writeCtor := func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewWriteToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewWriteToolWithRoot(root)
	}
	editCtor := func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewEditToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewEditToolWithRoot(root)
	}
	grepCtor := func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewGrepToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewGrepToolWithRoot(root)
	}
	globCtor := func() tool.Tool {
		if sandboxDisabled {
			return toolbuiltin.NewGlobToolWithSandbox(root, security.NewDisabledSandbox())
		}
		return toolbuiltin.NewGlobToolWithRoot(root)
	}

	factories["bash"] = bashCtor
	factories["file_read"] = readCtor
	factories["file_write"] = writeCtor
	factories["file_edit"] = editCtor
	factories["grep"] = grepCtor
	factories["glob"] = globCtor
	factories["web_fetch"] = func() tool.Tool { return toolbuiltin.NewWebFetchTool(nil) }
	factories["web_search"] = func() tool.Tool { return toolbuiltin.NewWebSearchTool(nil) }
	factories["bash_output"] = func() tool.Tool { return toolbuiltin.NewBashOutputTool(nil) }
	factories["bash_status"] = func() tool.Tool { return toolbuiltin.NewBashStatusTool() }
	factories["kill_task"] = func() tool.Tool { return toolbuiltin.NewKillTaskTool() }
	factories["todo_write"] = func() tool.Tool { return toolbuiltin.NewTodoWriteTool() }
	factories["ask_user_question"] = func() tool.Tool { return toolbuiltin.NewAskUserQuestionTool() }
	factories["skill"] = func() tool.Tool { return toolbuiltin.NewSkillTool(skReg, nil) }
	factories["slash_command"] = func() tool.Tool { return toolbuiltin.NewSlashCommandTool(cmdExec) }

	if shouldRegisterTaskTool(entry) {
		factories["task"] = func() tool.Tool { return toolbuiltin.NewTaskTool() }
	}

	return factories
}

func builtinOrder(entry EntryPoint) []string {
	order := []string{
		"bash",
		"file_read",
		"file_write",
		"file_edit",
		"web_fetch",
		"web_search",
		"bash_output",
		"bash_status",
		"kill_task",
		"todo_write",
		"ask_user_question",
		"skill",
		"slash_command",
		"grep",
		"glob",
	}
	if shouldRegisterTaskTool(entry) {
		order = append(order, "task")
	}
	return order
}

func filterBuiltinNames(enabled []string, order []string) []string {
	if enabled == nil {
		return append([]string(nil), order...)
	}
	if len(enabled) == 0 {
		return nil
	}
	set := make(map[string]struct{}, len(enabled))
	repl := strings.NewReplacer("-", "_", " ", "_")
	for _, name := range enabled {
		key := strings.ToLower(strings.TrimSpace(name))
		key = repl.Replace(key)
		if key != "" {
			set[key] = struct{}{}
		}
	}
	var filtered []string
	for _, name := range order {
		if _, ok := set[name]; ok {
			filtered = append(filtered, name)
		}
	}
	return filtered
}

func shouldRegisterTaskTool(entry EntryPoint) bool {
	switch entry {
	case EntryPointCLI, EntryPointPlatform:
		return true
	default:
		return false
	}
}

func locateTaskTool(tools []tool.Tool) *toolbuiltin.TaskTool {
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		if task, ok := impl.(*toolbuiltin.TaskTool); ok {
			return task
		}
	}
	return nil
}

func effectiveEntryPoint(opts Options) EntryPoint {
	entry := opts.EntryPoint
	if entry == "" {
		entry = opts.Mode.EntryPoint
	}
	if entry == "" {
		entry = defaultEntrypoint
	}
	return entry
}

func registerMCPServers(ctx context.Context, registry *tool.Registry, manager *sandbox.Manager, servers []string) error {
	for _, server := range servers {
		if err := enforceSandboxHost(manager, server); err != nil {
			return err
		}
		if err := registry.RegisterMCPServer(ctx, server); err != nil {
			return fmt.Errorf("api: register MCP %s: %w", server, err)
		}
	}
	return nil
}

func enforceSandboxHost(manager *sandbox.Manager, server string) error {
	if manager == nil || strings.TrimSpace(server) == "" {
		return nil
	}
	if strings.HasPrefix(server, "http://") || strings.HasPrefix(server, "https://") {
		u, err := url.Parse(server)
		if err != nil {
			return fmt.Errorf("api: parse MCP server %s: %w", server, err)
		}
		if err := manager.CheckNetwork(u.Host); err != nil {
			return fmt.Errorf("api: MCP host denied: %w", err)
		}
	}
	return nil
}

func resolveModel(ctx context.Context, opts Options) (model.Model, error) {
	if opts.Model != nil {
		return opts.Model, nil
	}
	if opts.ModelFactory != nil {
		mdl, err := opts.ModelFactory.Model(ctx)
		if err != nil {
			return nil, fmt.Errorf("api: model factory: %w", err)
		}
		return mdl, nil
	}
	return nil, ErrMissingModel
}

func defaultSessionID(entry EntryPoint) string {
	prefix := strings.TrimSpace(string(entry))
	if prefix == "" {
		prefix = string(defaultEntrypoint)
	}
	return fmt.Sprintf("%s-%d", prefix, time.Now().UnixNano())
}
