package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cexll/agentsdk-go/pkg/event"
)

type teamMember struct {
	name   string
	role   TeamRole
	agent  Agent
	caps   []string
	active atomic.Int64
	mu     sync.Mutex
}

func newTeamMember(cfg TeamMemberConfig) (*teamMember, error) {
	if cfg.Agent == nil {
		return nil, fmt.Errorf("team: agent for %s is nil", cfg.Name)
	}
	role := cfg.Role.Default()
	if err := role.Validate(); err != nil {
		return nil, err
	}
	name := strings.TrimSpace(cfg.Name)
	if name == "" {
		return nil, errors.New("team: member name required")
	}
	return &teamMember{name: name, role: role, agent: cfg.Agent, caps: normalizeCaps(cfg.Capabilities)}, nil
}

func (m *teamMember) beginTask() func() {
	m.mu.Lock()
	m.active.Add(1)
	return func() {
		m.active.Add(-1)
		m.mu.Unlock()
	}
}

func (m *teamMember) load() int64 { return m.active.Load() }

func normalizeCaps(src []string) []string {
	if len(src) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	out := make([]string, 0, len(src))
	for _, cap := range src {
		trimmed := strings.ToLower(strings.TrimSpace(cap))
		if trimmed == "" {
			continue
		}
		if _, ok := seen[trimmed]; ok {
			continue
		}
		seen[trimmed] = struct{}{}
		out = append(out, trimmed)
	}
	sort.Strings(out)
	return out
}

func cloneTasks(tasks []TeamTask) []TeamTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]TeamTask, len(tasks))
	for i, task := range tasks {
		out[i] = task.normalized("")
	}
	return out
}

func decorate(base, notes string) string {
	base = strings.TrimSpace(base)
	notes = strings.TrimSpace(notes)
	switch {
	case base == "":
		return notes
	case notes == "":
		return base
	default:
		return base + "\n\n" + notes
	}
}

type scheduledTask struct {
	index int
	task  TeamTask
}

func labelFor(task scheduledTask) string {
	if name := strings.TrimSpace(task.task.Name); name != "" {
		return name
	}
	return fmt.Sprintf("%s-%d", task.task.Role.Default().String(), task.index+1)
}

type teamAccumulator struct {
	mu      sync.Mutex
	outputs []string
	events  []event.Event
	calls   []ToolCall
	usage   TokenUsage
	reason  string
}

func newAccumulator(size int) *teamAccumulator {
	return &teamAccumulator{outputs: make([]string, 0, size)}
}

func (a *teamAccumulator) add(label string, res *RunResult) {
	if res == nil {
		return
	}
	text := strings.TrimSpace(res.Output)
	if label != "" {
		if text == "" {
			a.outputs = append(a.outputs, fmt.Sprintf("%s: (no output)", label))
		} else {
			a.outputs = append(a.outputs, fmt.Sprintf("%s: %s", label, text))
		}
	} else if text != "" {
		a.outputs = append(a.outputs, text)
	}
	if len(res.Events) > 0 {
		a.events = append(a.events, cloneEvents(res.Events)...)
	}
	if len(res.ToolCalls) > 0 {
		a.calls = append(a.calls, cloneToolCalls(res.ToolCalls)...)
	}
	a.usage.InputTokens += res.Usage.InputTokens
	a.usage.OutputTokens += res.Usage.OutputTokens
	a.usage.TotalTokens += res.Usage.TotalTokens
	a.usage.CacheTokens += res.Usage.CacheTokens
	if res.StopReason != "" {
		a.reason = res.StopReason
	}
}

func (a *teamAccumulator) result(mode CollaborationMode) *RunResult {
	reason := a.reason
	if reason == "" {
		reason = fmt.Sprintf("team_%s", mode.String())
	}
	return &RunResult{
		Output:     strings.Join(a.outputs, "\n"),
		ToolCalls:  a.calls,
		Usage:      a.usage,
		StopReason: reason,
		Events:     a.events,
	}
}

func roundRobinPick(candidates []*teamMember, seq *atomic.Uint64) *teamMember {
	if len(candidates) == 0 {
		return nil
	}
	idx := int(seq.Add(1)-1) % len(candidates)
	return candidates[idx]
}

func leastLoaded(candidates []*teamMember) *teamMember {
	var best *teamMember
	var bestLoad int64
	for _, c := range candidates {
		load := c.load()
		if best == nil || load < bestLoad {
			best = c
			bestLoad = load
		}
	}
	return best
}

func matchCapability(candidates []*teamMember, needs []string) *teamMember {
	needs = normalizeCaps(needs)
	if len(needs) == 0 {
		return nil
	}
	var best *teamMember
	bestScore := -1
	for _, c := range candidates {
		score := 0
		for _, need := range needs {
			for _, have := range c.caps {
				if have == need {
					score++
					break
				}
			}
		}
		if score > bestScore {
			best = c
			bestScore = score
		}
	}
	if bestScore <= 0 {
		return nil
	}
	return best
}

func cloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	for i, call := range calls {
		out[i] = ToolCall{
			Name:     call.Name,
			Params:   maps.Clone(call.Params),
			Output:   call.Output,
			Error:    call.Error,
			Duration: call.Duration,
			Metadata: maps.Clone(call.Metadata),
		}
	}
	return out
}

func roleFromMetadata(meta map[string]any) TeamRole {
	if raw, ok := meta["role"].(string); ok {
		if role, err := ParseTeamRole(raw); err == nil {
			return role.Default()
		}
	}
	return TeamRoleWorker
}

func capsFromMetadata(meta map[string]any) []string {
	raw := meta["capabilities"]
	switch val := raw.(type) {
	case []string:
		return val
	case []any:
		out := make([]string, 0, len(val))
		for _, item := range val {
			if s, ok := item.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(val, ",")
		out := make([]string, 0, len(parts))
		for _, part := range parts {
			if trimmed := strings.TrimSpace(part); trimmed != "" {
				out = append(out, trimmed)
			}
		}
		return out
	default:
		return nil
	}
}

func (t *TeamAgent) dispatchByMode(ctx context.Context, cfg TeamRunConfig, tasks []TeamTask) (*RunResult, error) {
	scheduled := make([]scheduledTask, len(tasks))
	for i := range tasks {
		scheduled[i] = scheduledTask{index: i, task: tasks[i]}
	}
	switch cfg.Mode {
	case CollaborationParallel:
		return t.runParallel(ctx, cfg, scheduled)
	case CollaborationHierarchical:
		return t.runHierarchical(ctx, cfg, scheduled)
	default:
		return t.runSequential(ctx, cfg, scheduled)
	}
}

func (t *TeamAgent) runSequential(ctx context.Context, cfg TeamRunConfig, tasks []scheduledTask) (*RunResult, error) {
	acc := newAccumulator(len(tasks))
	for _, task := range tasks {
		res, err := t.runSingle(ctx, cfg, task, "")
		acc.add(labelFor(task), res)
		if err != nil {
			return acc.result(cfg.Mode), err
		}
	}
	return acc.result(cfg.Mode), nil
}

func (t *TeamAgent) runParallel(ctx context.Context, cfg TeamRunConfig, tasks []scheduledTask) (*RunResult, error) {
	acc := newAccumulator(len(tasks))
	wg := sync.WaitGroup{}
	errMu := sync.Mutex{}
	var joined error
	for _, task := range tasks {
		task := task
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := t.runSingle(ctx, cfg, task, "")
			acc.mu.Lock()
			acc.add(labelFor(task), res)
			acc.mu.Unlock()
			if err != nil {
				errMu.Lock()
				joined = errors.Join(joined, err)
				errMu.Unlock()
			}
		}()
	}
	wg.Wait()
	return acc.result(cfg.Mode), joined
}

func (t *TeamAgent) runHierarchical(ctx context.Context, cfg TeamRunConfig, tasks []scheduledTask) (*RunResult, error) {
	acc := newAccumulator(len(tasks))
	var leaders, workers, reviewers []scheduledTask
	for _, task := range tasks {
		switch task.task.Role.Default() {
		case TeamRoleLeader:
			leaders = append(leaders, task)
		case TeamRoleReviewer:
			reviewers = append(reviewers, task)
		default:
			workers = append(workers, task)
		}
	}
	leaderNotes, err := t.runStage(ctx, cfg, leaders, acc, "")
	if err != nil {
		return acc.result(cfg.Mode), err
	}
	workerNotes, err := t.runStage(ctx, cfg, workers, acc, leaderNotes)
	if err != nil {
		return acc.result(cfg.Mode), err
	}
	_, err = t.runStage(ctx, cfg, reviewers, acc, decorate(workerNotes, leaderNotes))
	return acc.result(cfg.Mode), err
}

func (t *TeamAgent) runStage(ctx context.Context, cfg TeamRunConfig, tasks []scheduledTask, acc *teamAccumulator, notes string) (string, error) {
	if len(tasks) == 0 {
		return "", nil
	}
	var outputs []string
	for _, task := range tasks {
		res, err := t.runSingle(ctx, cfg, task, notes)
		acc.add(labelFor(task), res)
		if res != nil && strings.TrimSpace(res.Output) != "" {
			outputs = append(outputs, strings.TrimSpace(res.Output))
		}
		if err != nil {
			return strings.Join(outputs, "\n"), err
		}
	}
	return strings.Join(outputs, "\n"), nil
}

func (t *TeamAgent) runSingle(ctx context.Context, cfg TeamRunConfig, task scheduledTask, notes string) (*RunResult, error) {
	member := t.selectMember(task.task, cfg.Strategy)
	if member == nil {
		return nil, fmt.Errorf("team: no member for role %s", task.task.Role)
	}
	instruction := decorate(task.task.Instruction, notes)
	return t.execute(ctx, member, instruction, task.index, cfg)
}

func (t *TeamAgent) execute(ctx context.Context, member *teamMember, instruction string, idx int, cfg TeamRunConfig) (*RunResult, error) {
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	runCtx, _ := GetRunContext(ctx)
	if !cfg.ShareSession && strings.TrimSpace(runCtx.SessionID) != "" {
		runCtx.SessionID = fmt.Sprintf("%s-%s-%02d", runCtx.SessionID, member.name, idx+1)
	}
	childCtx := WithRunContext(ctx, runCtx)
	finish := member.beginTask()
	res, err := member.agent.Run(childCtx, instruction)
	finish()
	if res == nil {
		res = &RunResult{}
	}
	if cfg.ShareEventBus && t.sharedBus != nil {
		for _, evt := range res.Events {
			_ = t.sharedBus.Emit(evt)
		}
	}
	return res, err
}

func (t *TeamAgent) selectMember(task TeamTask, strategy TaskAllocationStrategy) *teamMember {
	candidates := t.byRole[task.Role.Default()]
	if len(candidates) == 0 {
		candidates = t.members
	}
	if len(candidates) == 0 {
		return nil
	}
	switch strategy.normalize(t.strategy) {
	case StrategyLeastLoaded:
		return leastLoaded(candidates)
	case StrategyCapability:
		if picked := matchCapability(candidates, task.Capabilities); picked != nil {
			return picked
		}
		fallthrough
	default:
		return roundRobinPick(candidates, &t.rr)
	}
}

func (t *TeamAgent) leader() *teamMember {
	if leaders := t.byRole[TeamRoleLeader]; len(leaders) > 0 {
		return leaders[0]
	}
	if workers := t.byRole[TeamRoleWorker]; len(workers) > 0 {
		return workers[0]
	}
	return nil
}

func (t *TeamAgent) invokePreHooks(ctx context.Context, input string) error {
	for _, h := range t.hooks {
		if h == nil {
			continue
		}
		if err := h.PreRun(ctx, input); err != nil {
			return err
		}
	}
	return nil
}

func (t *TeamAgent) invokePostHooks(ctx context.Context, res *RunResult) error {
	var joined error
	for _, h := range t.hooks {
		if h == nil {
			continue
		}
		if err := h.PostRun(ctx, res); err != nil {
			joined = errors.Join(joined, err)
		}
	}
	return joined
}
