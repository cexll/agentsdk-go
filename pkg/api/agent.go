package api

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
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
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/tool"
	toolbuiltin "github.com/cexll/agentsdk-go/pkg/tool/builtin"
)

// Runtime exposes the unified SDK surface that powers CLI/CI/enterprise entrypoints.
type Runtime struct {
	opts      Options
	mode      ModeContext
	loader    *config.Loader
	cfg       *config.ProjectConfig
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

	mu sync.RWMutex
}

// New instantiates a unified runtime bound to the provided options.
func New(ctx context.Context, opts Options) (*Runtime, error) {
	opts = opts.withDefaults()
	mode := opts.modeContext()

	loader := opts.Loader
	if loader == nil {
		loaderOpts := append([]config.LoaderOption(nil), opts.LoaderOptions...)
		if strings.TrimSpace(opts.ClaudeDir) != "" {
			loaderOpts = append(loaderOpts, config.WithClaudeDir(opts.ClaudeDir))
		}
		var err error
		loader, err = config.NewLoader(opts.ProjectRoot, loaderOpts...)
		if err != nil {
			return nil, fmt.Errorf("api: config loader: %w", err)
		}
	}
	cfg, err := loadProjectConfig(loader)
	if err != nil {
		return nil, err
	}

	mdl, err := resolveModel(ctx, opts)
	if err != nil {
		return nil, err
	}
	opts.Model = mdl

	sbox, sbRoot := buildSandboxManager(opts, cfg)
	registry := tool.NewRegistry()
	if err := registerTools(registry, opts, cfg); err != nil {
		return nil, err
	}
	if err := registerMCPServers(registry, sbox, opts.MCPServers); err != nil {
		return nil, err
	}
	executor := tool.NewExecutor(registry, sbox)

	recorder := defaultHookRecorder()
	hooks := newHookExecutor(opts, recorder)

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

	return &Runtime{
		opts:      opts,
		mode:      mode,
		loader:    loader,
		cfg:       cfg,
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
	}, nil
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
	out := make(chan StreamEvent, 16)
	progressChan := make(chan StreamEvent, 8)
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
func (rt *Runtime) Config() *config.ProjectConfig {
	rt.mu.RLock()
	defer rt.mu.RUnlock()
	return rt.cfg
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

	whitelist := make(map[string]struct{}, len(normalized.ToolWhitelist))
	for _, name := range normalized.ToolWhitelist {
		whitelist[name] = struct{}{}
	}

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

	out, err := ag.Run(prep.ctx, agent.NewContext())
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
		SandboxSnapshot: snapshotSandbox(rt.sandbox),
		Tags:            maps.Clone(prep.normalized.Tags),
	}
	return resp
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
	if rt.subMgr == nil {
		return nil, prompt, nil
	}
	request := subagents.Request{
		Target:        req.TargetSubagent,
		Instruction:   prompt,
		Activation:    activation,
		ToolWhitelist: cloneStrings(req.ToolWhitelist),
		Metadata: map[string]any{
			"entrypoint": req.Mode.EntryPoint,
		},
	}
	res, err := rt.subMgr.Dispatch(ctx, request)
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
	resp, err := m.base.Complete(ctx, req)
	if err != nil {
		return nil, err
	}
	m.usage = resp.Usage
	m.stopReason = resp.StopReason

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

func (t *runtimeToolExecutor) Execute(ctx context.Context, call agent.ToolCall, _ *agent.Context) (agent.ToolResult, error) {
	if t.executor == nil {
		return agent.ToolResult{}, errors.New("tool executor not initialised")
	}
	if len(t.allow) > 0 {
		if _, ok := t.allow[call.Name]; !ok {
			return agent.ToolResult{}, fmt.Errorf("tool %s is not whitelisted", call.Name)
		}
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
	if result != nil && result.Result != nil {
		toolResult.Output = result.Result.Output
		meta["data"] = result.Result.Data
	}
	if err != nil {
		meta["error"] = err.Error()
	}
	if len(meta) > 0 {
		toolResult.Metadata = meta
	}

	if hookErr := t.hooks.PostToolUse(ctx, coreToolResultPayload(call, result, err)); hookErr != nil && err == nil {
		// Prefer primary tool error if present; otherwise surface hook failure.
		return toolResult, hookErr
	}

	if t.history != nil && result != nil && result.Result != nil {
		t.history.Append(message.Message{
			Role:    "tool",
			Content: result.Result.Output,
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

func loadProjectConfig(loader *config.Loader) (*config.ProjectConfig, error) {
	cfg, err := loader.Load()
	if err != nil {
		empty := &config.ProjectConfig{Environment: map[string]string{}}
		if strings.Contains(err.Error(), ".claude directory not found") {
			return empty, nil
		}
		if strings.Contains(err.Error(), "plugin manifest not found") || strings.Contains(err.Error(), "invalid plugin name") {
			return empty, nil
		}
		return nil, fmt.Errorf("api: load config: %w", err)
	}
	return cfg, nil
}

func buildSandboxManager(opts Options, cfg *config.ProjectConfig) (*sandbox.Manager, string) {
	root := opts.Sandbox.Root
	if root == "" {
		root = opts.ProjectRoot
	}
	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)
	fs := sandbox.NewFileSystemAllowList(root)
	if err == nil && strings.TrimSpace(resolvedRoot) != "" {
		fs.Allow(resolvedRoot)
		root = resolvedRoot
	}
	for _, extra := range cfgSandboxPaths(cfg) {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}
	for _, extra := range opts.Sandbox.AllowedPaths {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}
	nw := sandbox.NewDomainAllowList(opts.Sandbox.NetworkAllow...)
	return sandbox.NewManager(fs, nw, sandbox.NewResourceLimiter(opts.Sandbox.ResourceLimit)), root
}

func registerTools(registry *tool.Registry, opts Options, cfg *config.ProjectConfig) error {
	tools := opts.Tools
	if len(tools) == 0 {
		bashTool := toolbuiltin.NewBashToolWithRoot(opts.ProjectRoot)
		// CLI 模式下允许管道等 shell 元字符
		if opts.EntryPoint == EntryPointCLI {
			bashTool.AllowShellMetachars(true)
		}
		tools = []tool.Tool{
			bashTool,
			toolbuiltin.NewFileToolWithRoot(opts.ProjectRoot),
			toolbuiltin.NewGrepToolWithRoot(opts.ProjectRoot),
			toolbuiltin.NewGlobToolWithRoot(opts.ProjectRoot),
		}
	}
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		if err := registry.Register(impl); err != nil {
			return fmt.Errorf("api: register tool %s: %w", impl.Name(), err)
		}
	}
	_ = cfg
	return nil
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

func cfgSandboxPaths(cfg *config.ProjectConfig) []string {
	if cfg == nil {
		return nil
	}
	paths := append([]string(nil), cfg.Sandbox.AllowedPaths...)
	seen := map[string]struct{}{}
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		clean := strings.TrimSpace(path)
		if clean == "" {
			continue
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	sort.Strings(out)
	return out
}

func availableTools(registry *tool.Registry, whitelist map[string]struct{}) []model.ToolDefinition {
	if registry == nil {
		return nil
	}
	tools := registry.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		name := strings.TrimSpace(impl.Name())
		if name == "" {
			continue
		}
		if len(whitelist) > 0 {
			if _, ok := whitelist[name]; !ok {
				continue
			}
		}
		defs = append(defs, model.ToolDefinition{
			Name:        name,
			Description: strings.TrimSpace(impl.Description()),
			Parameters:  schemaToMap(impl.Schema()),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func schemaToMap(schema *tool.JSONSchema) map[string]any {
	if schema == nil {
		return nil
	}
	payload := map[string]any{}
	if schema.Type != "" {
		payload["type"] = schema.Type
	}
	if len(schema.Properties) > 0 {
		payload["properties"] = schema.Properties
	}
	if len(schema.Required) > 0 {
		payload["required"] = append([]string(nil), schema.Required...)
	}
	return payload
}

func convertMessages(msgs []message.Message) []model.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, model.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			ToolCalls: convertToolCalls(msg.ToolCalls),
		})
	}
	return out
}

func convertToolCalls(calls []message.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, len(calls))
	for i, call := range calls {
		out[i] = model.ToolCall{ID: call.ID, Name: call.Name, Arguments: cloneArguments(call.Arguments)}
	}
	return out
}

func cloneArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	dup := make(map[string]any, len(args))
	for k, v := range args {
		dup[k] = v
	}
	return dup
}

func registerSkills(registrations []SkillRegistration) (*skills.Registry, error) {
	if len(registrations) == 0 {
		return nil, nil
	}
	reg := skills.NewRegistry()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: skill handler is nil")
		}
		if err := reg.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func registerCommands(registrations []CommandRegistration) (*commands.Executor, error) {
	if len(registrations) == 0 {
		return nil, nil
	}
	exec := commands.NewExecutor()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: command handler is nil")
		}
		if err := exec.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return exec, nil
}

func registerSubagents(registrations []SubagentRegistration) (*subagents.Manager, error) {
	if len(registrations) == 0 {
		return nil, nil
	}
	mgr := subagents.NewManager()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: subagent handler is nil")
		}
		if err := mgr.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

type historyStore struct {
	mu       sync.Mutex
	data     map[string]*message.History
	lastUsed map[string]time.Time
	maxSize  int
}

func newHistoryStore(maxSize int) *historyStore {
	if maxSize <= 0 {
		maxSize = defaultMaxSessions
	}
	return &historyStore{
		data:     map[string]*message.History{},
		lastUsed: map[string]time.Time{},
		maxSize:  maxSize,
	}
}

func (s *historyStore) Get(id string) *message.History {
	if strings.TrimSpace(id) == "" {
		id = defaultSessionID(defaultEntrypoint)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if hist, ok := s.data[id]; ok {
		s.lastUsed[id] = now
		return hist
	}
	hist := message.NewHistory()
	s.data[id] = hist
	s.lastUsed[id] = now
	if len(s.data) > s.maxSize {
		s.evictOldest()
	}
	return hist
}

func (s *historyStore) evictOldest() {
	if len(s.data) <= s.maxSize {
		return
	}
	var oldestKey string
	var oldestTime time.Time
	first := true
	for id, ts := range s.lastUsed {
		if first || ts.Before(oldestTime) {
			oldestKey = id
			oldestTime = ts
			first = false
		}
	}
	if oldestKey == "" {
		return
	}
	delete(s.data, oldestKey)
	delete(s.lastUsed, oldestKey)
}

func newHookExecutor(opts Options, recorder HookRecorder) *corehooks.Executor {
	exec := corehooks.NewExecutor(corehooks.WithMiddleware(opts.HookMiddleware...), corehooks.WithTimeout(opts.HookTimeout))
	if len(opts.TypedHooks) > 0 {
		exec.Register(opts.TypedHooks...)
	}
	_ = recorder
	return exec
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

func removeCommandLines(prompt string, invs []commands.Invocation) string {
	if len(invs) == 0 {
		return prompt
	}
	mask := map[int]struct{}{}
	for _, inv := range invs {
		pos := inv.Position - 1
		if pos >= 0 {
			mask[pos] = struct{}{}
		}
	}
	lines := strings.Split(prompt, "\n")
	kept := make([]string, 0, len(lines))
	for idx, line := range lines {
		if _, drop := mask[idx]; drop {
			continue
		}
		kept = append(kept, line)
	}
	return strings.TrimSpace(strings.Join(kept, "\n"))
}

func applyPromptMetadata(prompt string, meta map[string]any) string {
	if len(meta) == 0 {
		return prompt
	}
	if text, ok := anyToString(meta["api.prompt_override"]); ok {
		prompt = text
	}
	if text, ok := anyToString(meta["api.prepend_prompt"]); ok {
		prompt = strings.TrimSpace(text) + "\n" + prompt
	}
	if text, ok := anyToString(meta["api.append_prompt"]); ok {
		prompt = prompt + "\n" + strings.TrimSpace(text)
	}
	return strings.TrimSpace(prompt)
}

func mergeTags(req *Request, meta map[string]any) {
	if req == nil || len(meta) == 0 {
		return
	}
	if req.Tags == nil {
		req.Tags = map[string]string{}
	}
	if tags, ok := meta["api.tags"].(map[string]string); ok {
		for k, v := range tags {
			req.Tags[k] = v
		}
		return
	}
	if raw, ok := meta["api.tags"].(map[string]any); ok {
		for k, v := range raw {
			req.Tags[k] = fmt.Sprint(v)
		}
	}
}

func applyCommandMetadata(req *Request, meta map[string]any) {
	if req == nil || len(meta) == 0 {
		return
	}
	if target, ok := anyToString(meta["api.target_subagent"]); ok {
		req.TargetSubagent = target
	}
	if wl := stringSlice(meta["api.tool_whitelist"]); len(wl) > 0 {
		req.ToolWhitelist = wl
	}
}

func orderedForcedSkills(reg *skills.Registry, names []string) []skills.Activation {
	if reg == nil || len(names) == 0 {
		return nil
	}
	var activations []skills.Activation
	for _, name := range names {
		skill, ok := reg.Get(name)
		if !ok {
			continue
		}
		activations = append(activations, skills.Activation{Skill: skill})
	}
	return activations
}

func combinePrompt(current string, output any) string {
	text, ok := anyToString(output)
	if !ok || strings.TrimSpace(text) == "" {
		return current
	}
	if current == "" {
		return strings.TrimSpace(text)
	}
	return current + "\n" + strings.TrimSpace(text)
}

func prependPrompt(prompt, prefix string) string {
	if strings.TrimSpace(prefix) == "" {
		return prompt
	}
	if strings.TrimSpace(prompt) == "" {
		return strings.TrimSpace(prefix)
	}
	return strings.TrimSpace(prefix) + "\n\n" + strings.TrimSpace(prompt)
}

func mergeMetadata(dst, src map[string]any) map[string]any {
	if len(src) == 0 {
		return dst
	}
	if dst == nil {
		dst = map[string]any{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}

func anyToString(value any) (string, bool) {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v), true
	case fmt.Stringer:
		return strings.TrimSpace(v.String()), true
	}
	if value == nil {
		return "", false
	}
	return strings.TrimSpace(fmt.Sprint(value)), true
}

func stringSlice(value any) []string {
	switch v := value.(type) {
	case []string:
		out := append([]string(nil), v...)
		sort.Strings(out)
		return out
	case []any:
		var out []string
		for _, entry := range v {
			if text, ok := anyToString(entry); ok && text != "" {
				out = append(out, text)
			}
		}
		sort.Strings(out)
		return out
	case string:
		text := strings.TrimSpace(v)
		if text == "" {
			return nil
		}
		return []string{text}
	default:
		return nil
	}
}

func definitionSnapshot(exec *commands.Executor, name string) commands.Definition {
	if exec == nil {
		return commands.Definition{Name: strings.ToLower(name)}
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, def := range exec.List() {
		if def.Name == lower {
			return def
		}
	}
	return commands.Definition{Name: lower}
}
func snapshotSandbox(mgr *sandbox.Manager) SandboxReport {
	if mgr == nil {
		return SandboxReport{}
	}
	return SandboxReport{ResourceLimits: mgr.Limits()}
}
