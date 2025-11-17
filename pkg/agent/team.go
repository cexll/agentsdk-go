package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

// CollaborationMode selects the high-level coordination style.
type CollaborationMode int

const (
	CollaborationSequential CollaborationMode = iota + 1
	CollaborationParallel
	CollaborationHierarchical
)

func (m CollaborationMode) String() string {
	switch m {
	case CollaborationParallel:
		return "parallel"
	case CollaborationHierarchical:
		return "hierarchical"
	default:
		return "sequential"
	}
}

func (m CollaborationMode) normalize(defaultMode CollaborationMode) CollaborationMode {
	if m == 0 {
		return defaultMode
	}
	return m
}

// TaskAllocationStrategy determines how members are picked for a task.
type TaskAllocationStrategy int

const (
	StrategyRoundRobin TaskAllocationStrategy = iota + 1
	StrategyLeastLoaded
	StrategyCapability
)

func (s TaskAllocationStrategy) normalize(defaultStrategy TaskAllocationStrategy) TaskAllocationStrategy {
	if s == 0 {
		return defaultStrategy
	}
	return s
}

// TeamMemberConfig defines one agent inside the team.
type TeamMemberConfig struct {
	Name         string
	Role         TeamRole
	Agent        Agent
	Capabilities []string
}

// TeamConfig controls the static composition of TeamAgent.
type TeamConfig struct {
	Name        string
	Members     []TeamMemberConfig
	Strategy    TaskAllocationStrategy
	DefaultMode CollaborationMode
	ShareTools  bool
	EventBus    *event.EventBus
}

// TeamTask captures one instruction routed to a member.
type TeamTask struct {
	Name         string
	Instruction  string
	Role         TeamRole
	Capabilities []string
	Metadata     map[string]any
}

func (t TeamTask) normalized(fallback string) TeamTask {
	cp := TeamTask{
		Name:         strings.TrimSpace(t.Name),
		Instruction:  strings.TrimSpace(t.Instruction),
		Role:         t.Role.Default(),
		Capabilities: normalizeCaps(t.Capabilities),
		Metadata:     cloneMetadata(t.Metadata),
	}
	if cp.Instruction == "" {
		cp.Instruction = strings.TrimSpace(fallback)
	}
	return cp
}

// TeamRunConfig provides per-run overrides layered on top of TeamConfig defaults.
type TeamRunConfig struct {
	Mode          CollaborationMode
	Strategy      TaskAllocationStrategy
	Tasks         []TeamTask
	ShareSession  bool
	ShareEventBus bool
}

func (c TeamRunConfig) merge(override TeamRunConfig) TeamRunConfig {
	if override.Mode != 0 {
		c.Mode = override.Mode
	}
	if override.Strategy != 0 {
		c.Strategy = override.Strategy
	}
	if len(override.Tasks) > 0 {
		c.Tasks = cloneTasks(override.Tasks)
	}
	if override.ShareSession {
		c.ShareSession = true
	}
	if override.ShareEventBus {
		c.ShareEventBus = true
	}
	return c
}

func (c TeamRunConfig) normalize(mode CollaborationMode, strategy TaskAllocationStrategy) TeamRunConfig {
	c.Mode = c.Mode.normalize(mode)
	c.Strategy = c.Strategy.normalize(strategy)
	for i, task := range c.Tasks {
		c.Tasks[i] = task.normalized("")
	}
	return c
}

func (c TeamRunConfig) tasksOrDefault(input string) []TeamTask {
	if len(c.Tasks) == 0 {
		return []TeamTask{{Instruction: strings.TrimSpace(input), Role: TeamRoleWorker}}
	}
	out := make([]TeamTask, len(c.Tasks))
	copy(out, c.Tasks)
	return out
}

// WithTeamRunConfig stores overrides in context.Context.
func WithTeamRunConfig(ctx context.Context, cfg TeamRunConfig) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	return context.WithValue(ctx, teamRunConfigKey{}, cfg)
}

func teamRunConfigFromContext(ctx context.Context) (TeamRunConfig, bool) {
	if ctx == nil {
		return TeamRunConfig{}, false
	}
	cfg, ok := ctx.Value(teamRunConfigKey{}).(TeamRunConfig)
	return cfg, ok
}

type teamRunConfigKey struct{}

// TeamAgent coordinates multiple agents with explicit roles.
type TeamAgent struct {
	name        string
	members     []*teamMember
	byRole      map[TeamRole][]*teamMember
	strategy    TaskAllocationStrategy
	defaultMode CollaborationMode
	shareTools  bool
	sharedBus   *event.EventBus
	hooks       []Hook

	rr     atomic.Uint64
	toolMu sync.RWMutex
	tools  map[string]tool.Tool
}

// NewTeamAgent constructs a TeamAgent instance.
func NewTeamAgent(cfg TeamConfig) (*TeamAgent, error) {
	if len(cfg.Members) == 0 {
		return nil, errors.New("team: members missing")
	}
	team := &TeamAgent{
		name:        strings.TrimSpace(cfg.Name),
		strategy:    cfg.Strategy.normalize(StrategyRoundRobin),
		defaultMode: cfg.DefaultMode.normalize(CollaborationSequential),
		shareTools:  cfg.ShareTools,
		sharedBus:   cfg.EventBus,
		byRole:      map[TeamRole][]*teamMember{},
		tools:       map[string]tool.Tool{},
	}
	for _, m := range cfg.Members {
		member, err := newTeamMember(m)
		if err != nil {
			return nil, err
		}
		team.members = append(team.members, member)
		role := member.role.Default()
		team.byRole[role] = append(team.byRole[role], member)
	}
	if len(team.byRole[TeamRoleWorker]) == 0 {
		return nil, errors.New("team: need at least one worker")
	}
	if team.strategy == 0 {
		team.strategy = StrategyRoundRobin
	}
	if team.defaultMode == 0 {
		team.defaultMode = CollaborationSequential
	}
	if !team.shareTools {
		team.shareTools = false
	}
	return team, nil
}

// Run satisfies the Agent interface by delegating tasks to members.
func (t *TeamAgent) Run(ctx context.Context, input string) (*RunResult, error) {
	if t == nil {
		return nil, errors.New("team: agent is nil")
	}
	if ctx == nil {
		return nil, errors.New("team: context is nil")
	}
	if err := t.invokePreHooks(ctx, input); err != nil {
		return nil, err
	}
	cfg := TeamRunConfig{Mode: t.defaultMode, Strategy: t.strategy}
	if override, ok := teamRunConfigFromContext(ctx); ok {
		cfg = cfg.merge(override)
	}
	cfg = cfg.normalize(t.defaultMode, t.strategy)
	tasks := cfg.tasksOrDefault(input)
	for i := range tasks {
		tasks[i] = tasks[i].normalized(input)
	}
	res, err := t.dispatchByMode(ctx, cfg, tasks)
	if res == nil {
		res = &RunResult{}
	}
	if hookErr := t.invokePostHooks(ctx, res); hookErr != nil {
		err = errors.Join(err, hookErr)
	}
	return res, err
}

// RunStream replays the aggregated events through a buffered channel.
func (t *TeamAgent) RunStream(ctx context.Context, input string) (<-chan event.Event, error) {
	if ctx == nil {
		return nil, errors.New("team: context is nil")
	}
	ch := make(chan event.Event, 8)
	go func() {
		defer close(ch)
		runCtx, _ := GetRunContext(ctx)
		sessionID := runCtx.SessionID
		emit := func(evt event.Event) {
			select {
			case ch <- evt:
			case <-ctx.Done():
			}
		}
		emit(progressEvent(sessionID, "team-start", "team run started", nil))
		res, err := t.Run(ctx, input)
		if err != nil {
			emit(errorEvent(sessionID, "team", err, false))
			return
		}
		for _, evt := range res.Events {
			emit(evt)
		}
		emit(progressEvent(sessionID, "team-complete", "team run completed", map[string]any{"stop_reason": res.StopReason}))
	}()
	return ch, nil
}

// Resume delegates replay to the leader agent.
func (t *TeamAgent) Resume(ctx context.Context, bookmark *event.Bookmark) (*RunResult, error) {
	leader := t.leader()
	if leader == nil || leader.agent == nil {
		return nil, errors.New("team: no member available for resume")
	}
	return leader.agent.Resume(ctx, bookmark)
}

// RunWorkflow proxies to the leader (or first worker) for workflow execution.
func (t *TeamAgent) RunWorkflow(ctx context.Context, graph *workflow.Graph, opts ...workflow.ExecutorOption) error {
	leader := t.leader()
	if leader == nil {
		return errors.New("team: no leader or worker available")
	}
	return leader.agent.RunWorkflow(ctx, graph, opts...)
}

// AddTool registers the tool across members when sharing is enabled.
func (t *TeamAgent) AddTool(tl tool.Tool) error {
	if t == nil {
		return errors.New("team: agent is nil")
	}
	if tl == nil {
		return errors.New("team: tool is nil")
	}
	name := strings.TrimSpace(tl.Name())
	if name == "" {
		return errors.New("team: tool name is empty")
	}
	t.toolMu.Lock()
	if t.tools == nil {
		t.tools = map[string]tool.Tool{}
	}
	t.tools[name] = tl
	t.toolMu.Unlock()
	if !t.shareTools {
		return nil
	}
	var joined error
	for _, member := range t.members {
		if err := member.agent.AddTool(tl); err != nil {
			joined = errors.Join(joined, fmt.Errorf("team: add tool to %s: %w", member.name, err))
		}
	}
	return joined
}

// UseMiddleware registers middleware on each member agent.
func (t *TeamAgent) UseMiddleware(mw middleware.Middleware) {
	if t == nil || mw == nil {
		return
	}
	for _, member := range t.members {
		if member.agent == nil {
			continue
		}
		member.agent.UseMiddleware(mw)
	}
}

// RemoveMiddleware removes middleware instances from every member.
func (t *TeamAgent) RemoveMiddleware(name string) bool {
	if t == nil {
		return false
	}
	var removed bool
	for _, member := range t.members {
		if member.agent == nil {
			continue
		}
		if member.agent.RemoveMiddleware(name) {
			removed = true
		}
	}
	return removed
}

// ListMiddlewares proxies the first member's middleware list.
func (t *TeamAgent) ListMiddlewares() []middleware.Middleware {
	if t == nil || len(t.members) == 0 || t.members[0].agent == nil {
		return nil
	}
	list := t.members[0].agent.ListMiddlewares()
	if len(list) == 0 {
		return nil
	}
	out := make([]middleware.Middleware, len(list))
	copy(out, list)
	return out
}

// WithHook appends the hook similar to basicAgent behavior.
func (t *TeamAgent) WithHook(h Hook) Agent {
	if h == nil {
		return t
	}
	clone := *t
	clone.hooks = append(append([]Hook(nil), t.hooks...), h)
	return &clone
}

// Fork clones each member to produce an isolated TeamAgent copy.
func (t *TeamAgent) Fork(opts ...ForkOption) (Agent, error) {
	if t == nil {
		return nil, errors.New("team: agent is nil")
	}
	members := make([]TeamMemberConfig, 0, len(t.members))
	for _, member := range t.members {
		forked, err := member.agent.Fork(opts...)
		if err != nil {
			return nil, fmt.Errorf("team: fork %s: %w", member.name, err)
		}
		members = append(members, TeamMemberConfig{
			Name:         member.name,
			Role:         member.role,
			Agent:        forked,
			Capabilities: append([]string(nil), member.caps...),
		})
	}
	clone, err := NewTeamAgent(TeamConfig{
		Name:        t.name,
		Members:     members,
		Strategy:    t.strategy,
		DefaultMode: t.defaultMode,
		ShareTools:  t.shareTools,
		EventBus:    t.sharedBus,
	})
	if err != nil {
		return nil, err
	}
	clone.hooks = append([]Hook(nil), t.hooks...)
	t.toolMu.RLock()
	tools := maps.Clone(t.tools)
	t.toolMu.RUnlock()
	for _, tl := range tools {
		_ = clone.AddTool(tl)
	}
	return clone, nil
}

// Approve forwards the decision to every member.
func (t *TeamAgent) Approve(id string, approved bool) error {
	if t == nil {
		return errors.New("team: agent is nil")
	}
	var joined error
	for _, member := range t.members {
		if err := member.agent.Approve(id, approved); err != nil {
			joined = errors.Join(joined, fmt.Errorf("team: approve %s: %w", member.name, err))
		}
	}
	return joined
}

// Delegate satisfies workflow.SubAgentExecutor so teams can plug into SubAgentMiddleware.
func (t *TeamAgent) Delegate(ctx context.Context, req workflow.SubAgentRequest) (workflow.SubAgentResult, error) {
	if strings.TrimSpace(req.Instruction) == "" {
		return workflow.SubAgentResult{}, errors.New("team: instruction is empty")
	}
	cfg := TeamRunConfig{
		Mode:          CollaborationSequential,
		Strategy:      t.strategy,
		ShareSession:  req.ShareSession,
		ShareEventBus: req.ShareEventBus,
		Tasks: []TeamTask{{
			Name:         req.ID,
			Instruction:  req.Instruction,
			Role:         roleFromMetadata(req.Metadata),
			Capabilities: capsFromMetadata(req.Metadata),
			Metadata:     cloneMetadata(req.Metadata),
		}},
	}
	if ctx == nil {
		ctx = context.Background()
	}
	ctx = WithTeamRunConfig(ctx, cfg)
	if trimmed := strings.TrimSpace(req.SessionID); trimmed != "" {
		ctx = WithRunContext(ctx, RunContext{SessionID: trimmed})
	}
	res, err := t.Run(ctx, req.Instruction)
	if res == nil {
		res = &RunResult{}
	}
	out := workflow.SubAgentResult{
		ID:            req.ID,
		Output:        res.Output,
		StopReason:    res.StopReason,
		SessionID:     strings.TrimSpace(req.SessionID),
		SharedSession: req.ShareSession,
		Session:       req.Session,
		Usage:         exportUsage(res.Usage),
		ToolCalls:     exportToolCalls(res.ToolCalls),
		Events:        cloneEvents(res.Events),
		Metadata:      cloneMetadata(req.Metadata),
	}
	if err != nil {
		out.Error = err.Error()
		return out, err
	}
	return out, nil
}
