package api

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	coremw "github.com/cexll/agentsdk-go/pkg/core/middleware"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

var (
	ErrMissingModel            = errors.New("api: model factory is required")
	ErrConcurrentExecution     = errors.New("concurrent execution on same session is not allowed")
	ErrRuntimeClosed           = errors.New("api: runtime is closed")
	ErrToolUseDenied           = errors.New("api: tool use denied by hook")
	ErrToolUseRequiresApproval = errors.New("api: tool use requires approval")
)

type EntryPoint string

const (
	EntryPointCLI      EntryPoint = "cli"
	EntryPointCI       EntryPoint = "ci"
	EntryPointPlatform EntryPoint = "platform"
	defaultEntrypoint             = EntryPointCLI
	defaultMaxSessions            = 1000
)

// ModelTier represents cost-based model classification for optimization.
type ModelTier string

const (
	ModelTierLow  ModelTier = "low"  // Low cost: Haiku
	ModelTierMid  ModelTier = "mid"  // Mid cost: Sonnet
	ModelTierHigh ModelTier = "high" // High cost: Opus
)

// CLIContext captures optional metadata supplied by the CLI surface.
type CLIContext struct {
	User      string
	Workspace string
	Args      []string
	Flags     map[string]string
}

// CIContext captures CI/CD metadata for parameter matrix validation.
type CIContext struct {
	Provider string
	Pipeline string
	RunID    string
	SHA      string
	Ref      string
	Matrix   map[string]string
	Metadata map[string]string
}

// PlatformContext captures enterprise platform metadata such as org/project.
type PlatformContext struct {
	Organization string
	Project      string
	Environment  string
	Labels       map[string]string
}

// ModeContext binds an entrypoint to optional contextual metadata blocks.
type ModeContext struct {
	EntryPoint EntryPoint
	CLI        *CLIContext
	CI         *CIContext
	Platform   *PlatformContext
}

// SandboxOptions mirrors sandbox.Manager construction knobs exposed at the API
// layer so callers can customise filesystem/network/resource guards without
// touching lower-level packages.
type SandboxOptions struct {
	Root          string
	AllowedPaths  []string
	NetworkAllow  []string
	ResourceLimit sandbox.ResourceLimits
}

// PermissionRequest captures a permission prompt for sandbox "ask" matches.
type PermissionRequest struct {
	ToolName   string
	ToolParams map[string]any
	SessionID  string
	Rule       string
	Target     string
	Reason     string
	Approval   *security.ApprovalRecord
}

// PermissionRequestHandler lets hosts synchronously allow/deny PermissionAsk decisions.
type PermissionRequestHandler func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error)

// SkillRegistration wires runtime skill definitions + handlers.
type SkillRegistration struct {
	Definition skills.Definition
	Handler    skills.Handler
}

// CommandRegistration wires slash command definitions + handlers.
type CommandRegistration struct {
	Definition commands.Definition
	Handler    commands.Handler
}

// SubagentRegistration wires runtime subagents into the dispatcher.
type SubagentRegistration struct {
	Definition subagents.Definition
	Handler    subagents.Handler
}

// ModelFactory allows callers to supply arbitrary model implementations.
type ModelFactory interface {
	Model(ctx context.Context) (model.Model, error)
}

// ModelFactoryFunc turns a function into a ModelFactory.
type ModelFactoryFunc func(context.Context) (model.Model, error)

// Model implements ModelFactory.
func (fn ModelFactoryFunc) Model(ctx context.Context) (model.Model, error) {
	if fn == nil {
		return nil, ErrMissingModel
	}
	return fn(ctx)
}

// Options configures the unified SDK runtime.
type Options struct {
	EntryPoint  EntryPoint
	Mode        ModeContext
	ProjectRoot string
	// EmbedFS 可选的嵌入文件系统，用于支持将 .claude 目录打包到二进制
	// 当设置时，文件加载优先级为：OS 文件系统 > 嵌入 FS
	// 这允许运行时通过创建本地文件来覆盖嵌入的默认配置
	EmbedFS           fs.FS
	SettingsPath      string
	SettingsOverrides *config.Settings
	SettingsLoader    *config.SettingsLoader

	Model        model.Model
	ModelFactory ModelFactory

	// ModelPool maps tiers to model instances for cost optimization.
	// Use ModelTier constants (ModelTierLow, ModelTierMid, ModelTierHigh) as keys.
	ModelPool map[ModelTier]model.Model
	// SubagentModelMapping maps subagent type names to model tiers.
	// Keys should be lowercase subagent types: "general-purpose", "explore", "plan".
	// Subagents not in this map use the default Model.
	SubagentModelMapping map[string]ModelTier

	// DefaultEnableCache sets the default prompt caching behavior for all requests.
	// Individual requests can override this via Request.EnablePromptCache.
	// Prompt caching reduces costs for repeated context (system prompts, conversation history).
	DefaultEnableCache bool

	SystemPrompt string
	RulesEnabled *bool // nil = 默认启用，false = 禁用

	Middleware        []middleware.Middleware
	MiddlewareTimeout time.Duration
	MaxIterations     int
	Timeout           time.Duration
	TokenLimit        int
	MaxSessions       int

	Tools []tool.Tool

	// TaskStore overrides the default in-memory task store used by task_* built-ins.
	// When nil, runtime creates and owns a fresh in-memory store.
	// When provided, ownership remains with the caller.
	TaskStore tasks.Store

	// EnabledBuiltinTools controls which built-in tools are registered when Options.Tools is empty.
	// - nil (default): register all built-ins to preserve current behaviour
	// - empty slice: disable all built-in tools
	// - non-empty: enable only the listed built-ins (case-insensitive).
	// If Tools is non-empty, this whitelist is ignored in favour of the legacy Tools override.
	// Available built-in names include: bash, file_read, file_write, grep, glob.
	EnabledBuiltinTools []string

	// DisallowedTools is a blacklist of tool names (case-insensitive) that will not be registered.
	DisallowedTools []string

	// CustomTools appends caller-supplied tool.Tool implementations to the selected built-ins
	// when Tools is empty. Ignored when Tools is non-empty (legacy override takes priority).
	CustomTools []tool.Tool
	MCPServers  []string

	TypedHooks     []corehooks.ShellHook
	HookMiddleware []coremw.Middleware
	HookTimeout    time.Duration

	Skills    []SkillRegistration
	Commands  []CommandRegistration
	Subagents []SubagentRegistration

	Sandbox SandboxOptions

	// TokenTracking enables token usage statistics collection.
	// When true, the runtime tracks input/output tokens per session and model.
	TokenTracking bool
	// TokenCallback is called synchronously after token usage is recorded.
	// Only called when TokenTracking is true. The callback should be lightweight
	// and non-blocking to avoid delaying the agent execution. If you need async
	// processing, spawn a goroutine inside the callback.
	TokenCallback TokenCallback

	// PermissionRequestHandler handles sandbox PermissionAsk decisions. Returning
	// PermissionAllow continues tool execution; PermissionDeny rejects it; PermissionAsk
	// leaves the request pending.
	PermissionRequestHandler PermissionRequestHandler
	// ApprovalQueue optionally persists permission decisions and supports session whitelists.
	ApprovalQueue *security.ApprovalQueue
	// ApprovalApprover labels approvals/denials stored in ApprovalQueue.
	ApprovalApprover string
	// ApprovalWhitelistTTL controls session whitelist duration for approvals.
	ApprovalWhitelistTTL time.Duration
	// ApprovalWait blocks tool execution until a pending approval is resolved.
	ApprovalWait bool

	// AutoCompact enables automatic context compaction for long sessions.
	AutoCompact CompactConfig

	// OTEL configures OpenTelemetry distributed tracing.
	// Requires build tag 'otel' for actual instrumentation; otherwise no-op.
	OTEL OTELConfig

	fsLayer *config.FS
}

// DefaultSubagentDefinitions exposes the built-in subagent type catalog so
// callers can seed api.Options.Subagents or extend the metadata when wiring
// custom handlers.
func DefaultSubagentDefinitions() []subagents.Definition {
	return subagents.BuiltinDefinitions()
}

// Request captures a single logical run invocation. Tags/T traits/Channels are
// forwarded to the declarative runtime layers (skills/subagents) while
// RunContext overrides the agent-level execution knobs.
type Request struct {
	Prompt            string
	ContentBlocks     []model.ContentBlock // Multimodal content; when non-empty, used alongside Prompt
	Mode              ModeContext
	SessionID         string
	RequestID         string    `json:"request_id,omitempty"` // Auto-generated UUID or user-provided
	Model             ModelTier // Optional: override model tier for this request
	EnablePromptCache *bool     // Optional: enable prompt caching (nil uses global default)
	Traits            []string
	Tags              map[string]string
	Channels          []string
	Metadata          map[string]any
	TargetSubagent    string
	ToolWhitelist     []string
	ForceSkills       []string
}

// Response aggregates the final agent result together with metadata emitted
// by the unified runtime pipeline (skills/commands/hooks/etc.).
type Response struct {
	Mode           ModeContext
	RequestID      string `json:"request_id,omitempty"` // UUID for distributed tracing
	Result         *Result
	SkillResults   []SkillExecution
	CommandResults []CommandExecution
	Subagent       *subagents.Result
	HookEvents     []coreevents.Event
	// Deprecated: Use Settings instead. Kept for backward compatibility.
	ProjectConfig   *config.Settings
	Settings        *config.Settings
	SandboxSnapshot SandboxReport
	Tags            map[string]string
}

// Result represents the agent execution result.
type Result struct {
	Output     string
	StopReason string
	Usage      model.Usage
	ToolCalls  []model.ToolCall
}

// SkillExecution records individual skill invocations.
type SkillExecution struct {
	Definition skills.Definition
	Result     skills.Result
	Err        error
}

// CommandExecution records slash command invocations.
type CommandExecution struct {
	Definition commands.Definition
	Result     commands.Result
	Err        error
}

// SandboxReport documents the sandbox configuration attached to the runtime.
type SandboxReport struct {
	Roots          []string
	AllowedPaths   []string
	AllowedDomains []string
	ResourceLimits sandbox.ResourceLimits
}

// WithMaxSessions caps how many parallel session histories are retained.
// Values <= 0 fall back to the default.
func WithMaxSessions(n int) func(*Options) {
	return func(o *Options) {
		if n > 0 {
			o.MaxSessions = n
		}
	}
}

// WithTokenTracking enables or disables token usage tracking.
func WithTokenTracking(enabled bool) func(*Options) {
	return func(o *Options) {
		o.TokenTracking = enabled
	}
}

// WithTokenCallback sets a callback function that is called synchronously after
// each model call with the token usage statistics. Automatically enables
// TokenTracking.
func WithTokenCallback(fn TokenCallback) func(*Options) {
	return func(o *Options) {
		o.TokenCallback = fn
		if fn != nil {
			o.TokenTracking = true
		}
	}
}

// WithAutoCompact configures automatic context compaction.
func WithAutoCompact(config CompactConfig) func(*Options) {
	return func(o *Options) {
		o.AutoCompact = config
	}
}

// WithOTEL configures OpenTelemetry distributed tracing.
// Requires build tag 'otel' for actual instrumentation; otherwise no-op.
func WithOTEL(config OTELConfig) func(*Options) {
	return func(o *Options) {
		o.OTEL = config
	}
}

func (o Options) withDefaults() Options {
	// withDefaults normalises entrypoint/mode, resolves project and settings paths,
	// and leaves tool selection untouched: Tools stays as provided (legacy override),
	// EnabledBuiltinTools/CustomTools keep their caller-supplied values for later
	// registration logic (nil means register all built-ins, empty slice means none).
	if o.EntryPoint == "" {
		o.EntryPoint = defaultEntrypoint
	}
	if o.Mode.EntryPoint == "" {
		o.Mode.EntryPoint = o.EntryPoint
	}

	// 智能解析项目根目录
	if o.ProjectRoot == "" || o.ProjectRoot == "." {
		if resolved, err := ResolveProjectRoot(); err == nil {
			o.ProjectRoot = resolved
		} else {
			o.ProjectRoot = "."
		}
	}
	o.ProjectRoot = filepath.Clean(o.ProjectRoot)
	if trimmed := strings.TrimSpace(o.SettingsPath); trimmed != "" {
		if abs, err := filepath.Abs(trimmed); err == nil {
			o.SettingsPath = abs
		} else {
			o.SettingsPath = trimmed
		}
	}

	if o.Sandbox.Root == "" {
		o.Sandbox.Root = o.ProjectRoot
	}

	// 根据 EntryPoint 自动设置网络白名单默认值
	if len(o.Sandbox.NetworkAllow) == 0 {
		o.Sandbox.NetworkAllow = defaultNetworkAllowList(o.EntryPoint)
	}

	if o.MaxSessions <= 0 {
		o.MaxSessions = defaultMaxSessions
	}
	return o
}

// frozen returns a defensive copy of Options so callers can safely reuse/mutate
// the original Options struct without racing against a live Runtime.
func (o Options) frozen() Options {
	o.Mode = freezeMode(o.Mode)

	if len(o.Middleware) > 0 {
		o.Middleware = append([]middleware.Middleware(nil), o.Middleware...)
	}
	if len(o.Tools) > 0 {
		o.Tools = append([]tool.Tool(nil), o.Tools...)
	}
	if len(o.EnabledBuiltinTools) > 0 {
		o.EnabledBuiltinTools = append([]string(nil), o.EnabledBuiltinTools...)
	}
	if len(o.DisallowedTools) > 0 {
		o.DisallowedTools = append([]string(nil), o.DisallowedTools...)
	}
	if len(o.CustomTools) > 0 {
		o.CustomTools = append([]tool.Tool(nil), o.CustomTools...)
	}
	if len(o.MCPServers) > 0 {
		o.MCPServers = append([]string(nil), o.MCPServers...)
	}
	if len(o.TypedHooks) > 0 {
		hooks := make([]corehooks.ShellHook, len(o.TypedHooks))
		for i, hook := range o.TypedHooks {
			hooks[i] = hook
			if len(hook.Env) > 0 {
				hooks[i].Env = maps.Clone(hook.Env)
			}
		}
		o.TypedHooks = hooks
	}
	if len(o.HookMiddleware) > 0 {
		o.HookMiddleware = append([]coremw.Middleware(nil), o.HookMiddleware...)
	}
	if len(o.Skills) > 0 {
		skillsCopy := make([]SkillRegistration, len(o.Skills))
		for i, reg := range o.Skills {
			skillsCopy[i] = reg
			def := reg.Definition
			if len(def.Metadata) > 0 {
				def.Metadata = maps.Clone(def.Metadata)
			}
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			skillsCopy[i].Definition = def
		}
		o.Skills = skillsCopy
	}
	if len(o.Commands) > 0 {
		o.Commands = append([]CommandRegistration(nil), o.Commands...)
	}
	if len(o.Subagents) > 0 {
		subCopy := make([]SubagentRegistration, len(o.Subagents))
		for i, reg := range o.Subagents {
			subCopy[i] = reg
			def := reg.Definition
			def.BaseContext = def.BaseContext.Clone()
			if len(def.Matchers) > 0 {
				def.Matchers = append([]skills.Matcher(nil), def.Matchers...)
			}
			subCopy[i].Definition = def
		}
		o.Subagents = subCopy
	}

	o.Sandbox = freezeSandboxOptions(o.Sandbox)

	if len(o.ModelPool) > 0 {
		o.ModelPool = maps.Clone(o.ModelPool)
	}
	if len(o.SubagentModelMapping) > 0 {
		o.SubagentModelMapping = maps.Clone(o.SubagentModelMapping)
	}

	return o
}

func freezeSandboxOptions(in SandboxOptions) SandboxOptions {
	out := in
	if len(in.AllowedPaths) > 0 {
		out.AllowedPaths = append([]string(nil), in.AllowedPaths...)
	}
	if len(in.NetworkAllow) > 0 {
		out.NetworkAllow = append([]string(nil), in.NetworkAllow...)
	}
	return out
}

func freezeMode(in ModeContext) ModeContext {
	mode := in
	if mode.CLI != nil {
		cli := *mode.CLI
		if len(cli.Args) > 0 {
			cli.Args = append([]string(nil), cli.Args...)
		}
		if len(cli.Flags) > 0 {
			cli.Flags = maps.Clone(cli.Flags)
		}
		mode.CLI = &cli
	}
	if mode.CI != nil {
		ci := *mode.CI
		if len(ci.Matrix) > 0 {
			ci.Matrix = maps.Clone(ci.Matrix)
		}
		if len(ci.Metadata) > 0 {
			ci.Metadata = maps.Clone(ci.Metadata)
		}
		mode.CI = &ci
	}
	if mode.Platform != nil {
		plat := *mode.Platform
		if len(plat.Labels) > 0 {
			plat.Labels = maps.Clone(plat.Labels)
		}
		mode.Platform = &plat
	}
	return mode
}

// defaultNetworkAllowList 默认允许本地网络；访问外网需显式配置
func defaultNetworkAllowList(_ EntryPoint) []string {
	return []string{
		"localhost",
		"127.0.0.1",
		"::1",       // IPv6 localhost
		"0.0.0.0",   // 本机所有接口
		"*.local",   // 本地域名
		"192.168.*", // 私有网段
		"10.*",      // 私有网段
		"172.16.*",  // 私有网段
	}
}

func (o Options) modeContext() ModeContext {
	mode := o.Mode
	if mode.EntryPoint == "" {
		mode.EntryPoint = o.EntryPoint
	}
	if mode.EntryPoint == "" {
		mode.EntryPoint = defaultEntrypoint
	}
	return mode
}

func (r Request) normalized(defaultMode ModeContext, fallbackSession string) Request {
	req := r
	if req.Mode.EntryPoint == "" {
		req.Mode.EntryPoint = defaultMode.EntryPoint
		req.Mode.CLI = defaultMode.CLI
		req.Mode.CI = defaultMode.CI
		req.Mode.Platform = defaultMode.Platform
	}
	if req.SessionID == "" {
		req.SessionID = strings.TrimSpace(fallbackSession)
	}
	if req.Tags == nil {
		req.Tags = map[string]string{}
	}
	if req.Metadata == nil {
		req.Metadata = map[string]any{}
	}
	if len(req.ToolWhitelist) > 0 {
		req.ToolWhitelist = cloneStrings(req.ToolWhitelist)
	}
	if len(req.ContentBlocks) > 0 {
		req.ContentBlocks = append([]model.ContentBlock(nil), req.ContentBlocks...)
	}
	if len(req.Channels) > 0 {
		req.Channels = cloneStrings(req.Channels)
	}
	if len(req.Traits) > 0 {
		req.Traits = cloneStrings(req.Traits)
	}
	return req
}

func (r Request) activationContext(prompt string) skills.ActivationContext {
	ctx := skills.ActivationContext{Prompt: prompt}
	if len(r.Channels) > 0 {
		ctx.Channels = append([]string(nil), r.Channels...)
	}
	if len(r.Traits) > 0 {
		ctx.Traits = append([]string(nil), r.Traits...)
	}
	if len(r.Tags) > 0 {
		ctx.Tags = maps.Clone(r.Tags)
	}
	if len(r.Metadata) > 0 {
		ctx.Metadata = maps.Clone(r.Metadata)
	}
	return ctx
}

func cloneStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := append([]string(nil), in...)
	slices.Sort(out)
	return slices.Compact(out)
}

// WithModelPool configures a pool of models indexed by tier.
func WithModelPool(pool map[ModelTier]model.Model) func(*Options) {
	return func(o *Options) {
		if pool != nil {
			o.ModelPool = pool
		}
	}
}

// WithSubagentModelMapping configures subagent-type-to-tier mappings for model selection.
// Keys should be lowercase subagent type names (e.g., "explore", "plan").
func WithSubagentModelMapping(mapping map[string]ModelTier) func(*Options) {
	return func(o *Options) {
		if mapping != nil {
			o.SubagentModelMapping = mapping
		}
	}
}

// HookRecorder mirrors the historical api hook recorder contract.
type HookRecorder interface {
	Record(coreevents.Event)
	Drain() []coreevents.Event
}

// hookRecorder stores hook events for the response payload.
type hookRecorder struct {
	events []coreevents.Event
}

func (r *hookRecorder) Record(evt coreevents.Event) {
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	r.events = append(r.events, evt)
}

func (r *hookRecorder) Drain() []coreevents.Event {
	defer func() { r.events = nil }()
	if len(r.events) == 0 {
		return nil
	}
	return append([]coreevents.Event(nil), r.events...)
}

// defaultHookRecorder implements HookRecorder when callers do not provide one.
func defaultHookRecorder() *hookRecorder {
	return &hookRecorder{}
}

// runtimeHookAdapter wraps the hook executor and recorder.
type runtimeHookAdapter struct {
	executor *corehooks.Executor
	recorder HookRecorder
}

func (h *runtimeHookAdapter) PreToolUse(ctx context.Context, evt coreevents.ToolUsePayload) (map[string]any, error) {
	if h == nil || h.executor == nil {
		return evt.Params, nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PreToolUse, Payload: evt})
	if err != nil {
		return nil, err
	}
	h.record(coreevents.Event{Type: coreevents.PreToolUse, Payload: evt})

	// Print hook stderr output for debugging
	for _, res := range results {
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
	}

	params := evt.Params
	for _, res := range results {
		if res.Output == nil {
			continue
		}
		// Check top-level decision
		if res.Output.Decision == "deny" {
			return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
		}
		// Check continue=false
		if res.Output.Continue != nil && !*res.Output.Continue {
			return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
		}
		// Check hookSpecificOutput for PreToolUse
		if hso := res.Output.HookSpecificOutput; hso != nil {
			switch hso.PermissionDecision {
			case "deny":
				return nil, fmt.Errorf("%w: %s", ErrToolUseDenied, evt.Name)
			case "ask":
				return nil, fmt.Errorf("%w: %s", ErrToolUseRequiresApproval, evt.Name)
			}
			if hso.UpdatedInput != nil {
				params = hso.UpdatedInput
			}
		}
	}
	return params, nil
}

func (h *runtimeHookAdapter) PostToolUse(ctx context.Context, evt coreevents.ToolResultPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PostToolUse, Payload: evt})
	if err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.PostToolUse, Payload: evt})

	// Print hook stderr output for debugging
	for _, res := range results {
		if res.Stderr != "" {
			fmt.Fprint(os.Stderr, res.Stderr)
		}
	}

	// Check if any hook wants to stop
	for _, res := range results {
		if res.Output != nil && res.Output.Continue != nil && !*res.Output.Continue {
			return fmt.Errorf("hooks: PostToolUse hook requested stop: %s", res.Output.StopReason)
		}
	}
	return nil
}

func (h *runtimeHookAdapter) UserPrompt(ctx context.Context, prompt string) error {
	if h == nil || h.executor == nil {
		return nil
	}
	payload := coreevents.UserPromptPayload{Prompt: prompt}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.UserPromptSubmit, Payload: payload}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.UserPromptSubmit, Payload: payload})
	return nil
}

func (h *runtimeHookAdapter) Stop(ctx context.Context, reason string) error {
	if h == nil || h.executor == nil {
		return nil
	}
	payload := coreevents.StopPayload{Reason: reason}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.Stop, Payload: payload}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.Stop, Payload: payload})
	return nil
}

func (h *runtimeHookAdapter) PermissionRequest(ctx context.Context, evt coreevents.PermissionRequestPayload) (coreevents.PermissionDecisionType, error) {
	if h == nil || h.executor == nil {
		return coreevents.PermissionAsk, nil
	}
	results, err := h.executor.Execute(ctx, coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
	if err != nil {
		return coreevents.PermissionAsk, err
	}

	if len(results) == 0 {
		h.record(coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
		return coreevents.PermissionAsk, nil
	}

	decision := coreevents.PermissionAllow
	for _, res := range results {
		if res.Output == nil {
			continue
		}
		switch res.Output.Decision {
		case "deny":
			decision = coreevents.PermissionDeny
		case "ask":
			if decision != coreevents.PermissionDeny {
				decision = coreevents.PermissionAsk
			}
		case "allow":
			// keep current decision
		}
	}
	h.record(coreevents.Event{Type: coreevents.PermissionRequest, Payload: evt})
	return decision, nil
}

func (h *runtimeHookAdapter) SessionStart(ctx context.Context, evt coreevents.SessionPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SessionStart, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SessionStart, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SessionEnd(ctx context.Context, evt coreevents.SessionPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SessionEnd, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SessionEnd, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SubagentStart(ctx context.Context, evt coreevents.SubagentStartPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SubagentStart, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SubagentStart, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) SubagentStop(ctx context.Context, evt coreevents.SubagentStopPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.SubagentStop, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.SubagentStop, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) ModelSelected(ctx context.Context, evt coreevents.ModelSelectedPayload) error {
	if h == nil || h.executor == nil {
		return nil
	}
	if err := h.executor.Publish(coreevents.Event{Type: coreevents.ModelSelected, Payload: evt}); err != nil {
		return err
	}
	h.record(coreevents.Event{Type: coreevents.ModelSelected, Payload: evt})
	return nil
}

func (h *runtimeHookAdapter) record(evt coreevents.Event) {
	if h == nil || h.recorder == nil {
		return
	}
	h.recorder.Record(evt)
}
