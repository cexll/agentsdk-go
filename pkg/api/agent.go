package api

import (
	"context"
	"errors"
	"fmt"
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
)

type contextKey string

const middlewareStateKey contextKey = "agentsdk.middleware.state"

// Runtime exposes the unified SDK surface that powers CLI/CI/enterprise entrypoints.
type Runtime struct {
	opts      Options
	mode      ModeContext
	settings  *config.Settings
	cfg       *config.Settings
	sandbox   *sandbox.Manager
	sbRoot    string
	registry  *tool.Registry
	executor  *tool.Executor
	recorder  HookRecorder
	hooks     *corehooks.Executor
	histories *historyStore

	cmdExec *commands.Executor
	skReg   *skills.Registry
	subMgr  *subagents.Manager
	plugins []*plugins.ClaudePlugin

	mu sync.RWMutex
}

// New instantiates a unified runtime bound to the provided options.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	opts = opts.withDefaults()
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
	skReg, err := registerSkills(opts.Skills)
	if err != nil {
		return nil, err
	}
	cmdExec, err := registerCommands(opts.Commands)
	if err != nil {
		return nil, err
	}
	subMgr, err := registerSubagents(opts.Subagents)
	if err != nil {
		return nil, err
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
	if err := registerMCPServers(registry, sbox, mcpServers); err != nil {
		return nil, err
	}
	executor := tool.NewExecutor(registry, sbox)

	recorder := defaultHookRecorder()
	hooks := newHookExecutor(opts, recorder, settings)

	rt := &Runtime{
		opts:      opts,
		mode:      mode,
		settings:  settings,
		cfg:       projectConfigFromSettings(settings),
		sandbox:   sbox,
		sbRoot:    sbRoot,
		registry:  registry,
		executor:  executor,
		recorder:  recorder,
		hooks:     hooks,
		histories: newHistoryStore(opts.MaxSessions),
		cmdExec:   cmdExec,
		skReg:     skReg,
		subMgr:    subMgr,
		plugins:   plugins,
	}

	if taskTool != nil {
		taskTool.SetRunner(rt.taskRunner())
	}
	return rt, nil
}

// Run executes the unified pipeline synchronously.
func (rt *Runtime) Run(ctx context.Context, req Request) (*Response, error) {
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
	prep, err := rt.prepare(ctx, req)
	if err != nil {
		return nil, err
	}
	// 缓冲区增大以吸收前端延迟（逐字符渲染等）导致的背压，避免 progress emit 阻塞工具执行
	out := make(chan StreamEvent, 512)
	progressChan := make(chan StreamEvent, 256)
	baseCtx := prep.ctx
	if baseCtx == nil {
		baseCtx = context.Background()
	}
	progressMW := newProgressMiddleware(progressChan)
	go func() {
		defer close(out)
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
				case <-baseCtx.Done():
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
func (rt *Runtime) Close() error { return nil }

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

// ----------------- internal helpers -----------------

type preparedRun struct {
	ctx            context.Context
	prompt         string
	history        *message.History
	normalized     Request
	commandResults []CommandExecution
	skillResults   []SkillExecution
	subResult      *subagents.Result
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

	history := rt.histories.Get(normalized.SessionID)

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

	subRes, promptAfterSub, err := rt.executeSubagent(ctx, prompt, activation, &normalized)
	if err != nil {
		return preparedRun{}, err
	}
	prompt = promptAfterSub

	def, builtin := subagents.BuiltinDefinition(normalized.TargetSubagent)
	subCtx, hasSubCtx := buildSubagentContext(normalized, def, builtin)
	if hasSubCtx {
		ctx = subagents.WithContext(ctx, subCtx)
	}
	whitelist := combineToolWhitelists(normalized.ToolWhitelist, subCtx.ToolWhitelist)

	return preparedRun{
		ctx:            ctx,
		prompt:         prompt,
		history:        history,
		normalized:     normalized,
		commandResults: cmdRes,
		skillResults:   skillRes,
		subResult:      subRes,
		mode:           normalized.Mode,
		toolWhitelist:  whitelist,
	}, nil
}

func (rt *Runtime) runAgent(prep preparedRun) (runResult, error) {
	return rt.runAgentWithMiddleware(prep)
}

func (rt *Runtime) runAgentWithMiddleware(prep preparedRun, extras ...middleware.Middleware) (runResult, error) {
	modelAdapter := &conversationModel{
		base:         rt.mustModel(),
		history:      prep.history,
		prompt:       prep.prompt,
		trimmer:      rt.newTrimmer(),
		tools:        availableTools(rt.registry, prep.toolWhitelist),
		systemPrompt: rt.opts.SystemPrompt,
		hooks:        &runtimeHookAdapter{executor: rt.hooks, recorder: rt.recorder},
	}

	toolExec := &runtimeToolExecutor{
		executor: rt.executor,
		hooks:    &runtimeHookAdapter{executor: rt.hooks, recorder: rt.recorder},
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
	out, err := ag.Run(prep.ctx, agentCtx)
	if err != nil {
		return runResult{}, err
	}
	return runResult{output: out, usage: modelAdapter.usage, reason: modelAdapter.stopReason}, nil
}

func (rt *Runtime) buildResponse(prep preparedRun, result runResult) *Response {
	resp := &Response{
		Mode:            prep.mode,
		Result:          convertRunResult(result),
		CommandResults:  prep.commandResults,
		SkillResults:    prep.skillResults,
		Subagent:        prep.subResult,
		HookEvents:      rt.recorder.Drain(),
		ProjectConfig:   rt.Config(),
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
	res, _, err := rt.executeSubagent(ctx, prompt, activation, reqPayload)
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

func (rt *Runtime) mustModel() model.Model {
	rt.mu.RLock()
	mdl := rt.opts.Model
	rt.mu.RUnlock()
	return mdl
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
	usage        model.Usage
	stopReason   string
	hooks        *runtimeHookAdapter
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

	snapshot := m.history.All()
	if m.trimmer != nil {
		snapshot = m.trimmer.Trim(snapshot)
	}
	req := model.Request{
		Messages:    convertMessages(snapshot),
		Tools:       m.tools,
		System:      m.systemPrompt,
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
	if err := t.hooks.PreToolUse(ctx, coreToolUsePayload(call)); err != nil {
		return agent.ToolResult{}, err
	}

	callSpec := tool.Call{
		Name:   call.Name,
		Params: call.Input,
		Path:   t.root,
		Host:   t.host,
		Usage:  t.measureUsage(),
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
	tools := opts.Tools
	var taskTool *toolbuiltin.TaskTool
	entry := effectiveEntryPoint(opts)
	if len(tools) == 0 {
		sandboxDisabled := settings != nil && settings.Sandbox != nil && settings.Sandbox.Enabled != nil && !*settings.Sandbox.Enabled
		var (
			bashTool  *toolbuiltin.BashTool
			readTool  *toolbuiltin.ReadTool
			writeTool *toolbuiltin.WriteTool
			editTool  *toolbuiltin.EditTool
			grepTool  *toolbuiltin.GrepTool
			globTool  *toolbuiltin.GlobTool
		)

		if sandboxDisabled {
			disabledSandbox := security.NewDisabledSandbox()
			bashTool = toolbuiltin.NewBashToolWithSandbox(opts.ProjectRoot, disabledSandbox)
			readTool = toolbuiltin.NewReadToolWithSandbox(opts.ProjectRoot, disabledSandbox)
			writeTool = toolbuiltin.NewWriteToolWithSandbox(opts.ProjectRoot, disabledSandbox)
			editTool = toolbuiltin.NewEditToolWithSandbox(opts.ProjectRoot, disabledSandbox)
			grepTool = toolbuiltin.NewGrepToolWithSandbox(opts.ProjectRoot, disabledSandbox)
			globTool = toolbuiltin.NewGlobToolWithSandbox(opts.ProjectRoot, disabledSandbox)
		} else {
			bashTool = toolbuiltin.NewBashToolWithRoot(opts.ProjectRoot)
			readTool = toolbuiltin.NewReadToolWithRoot(opts.ProjectRoot)
			writeTool = toolbuiltin.NewWriteToolWithRoot(opts.ProjectRoot)
			editTool = toolbuiltin.NewEditToolWithRoot(opts.ProjectRoot)
			grepTool = toolbuiltin.NewGrepToolWithRoot(opts.ProjectRoot)
			globTool = toolbuiltin.NewGlobToolWithRoot(opts.ProjectRoot)
		}

		// CLI 模式下允许管道等 shell 元字符
		if entry == EntryPointCLI {
			bashTool.AllowShellMetachars(true)
		}
		if skReg == nil {
			skReg = skills.NewRegistry()
		}
		if cmdExec == nil {
			cmdExec = commands.NewExecutor()
		}
		tools = []tool.Tool{
			bashTool,
			readTool,
			writeTool,
			editTool,
			toolbuiltin.NewWebFetchTool(nil),
			toolbuiltin.NewWebSearchTool(nil),
			toolbuiltin.NewBashOutputTool(nil),
			toolbuiltin.NewTodoWriteTool(),
			toolbuiltin.NewSkillTool(skReg, nil),
			toolbuiltin.NewSlashCommandTool(cmdExec),
			grepTool,
			globTool,
		}
		if shouldRegisterTaskTool(entry) {
			taskTool = toolbuiltin.NewTaskTool()
			tools = append(tools, taskTool)
		}
	} else {
		taskTool = locateTaskTool(tools)
	}
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		if err := registry.Register(impl); err != nil {
			return nil, fmt.Errorf("api: register tool %s: %w", impl.Name(), err)
		}
	}
	return taskTool, nil
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

func registerMCPServers(registry *tool.Registry, manager *sandbox.Manager, servers []string) error {
	for _, server := range servers {
		if err := enforceSandboxHost(manager, server); err != nil {
			return err
		}
		if err := registry.RegisterMCPServer(server); err != nil {
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
