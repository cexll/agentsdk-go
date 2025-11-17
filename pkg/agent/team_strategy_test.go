package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/workflow"
)

func TestTeamStrategyModeMatrix(t *testing.T) {
	baseTasks := []TeamTask{
		{Name: "plan", Role: TeamRoleLeader, Instruction: "plan"},
		{Name: "build-go", Role: TeamRoleWorker, Instruction: "build-go", Capabilities: []string{"go"}},
		{Name: "build-py", Role: TeamRoleWorker, Instruction: "build-py", Capabilities: []string{"python"}},
		{Name: "review", Role: TeamRoleReviewer, Instruction: "review"},
	}
	type verifier func(*testing.T, map[string]*stubAgent, *RunResult)
	verify := map[TaskAllocationStrategy]verifier{
		StrategyRoundRobin: verifyRoundRobinStrategy,
		StrategyCapability: verifyCapabilityStrategy,
		StrategyLeastLoaded: func(t *testing.T, spies map[string]*stubAgent, _ *RunResult) {
			t.Helper()
			if got := len(spies["worker-a"].runs); got != 0 {
				t.Fatalf("least-load should skip busy worker, got %d runs", got)
			}
			if got := len(spies["worker-b"].runs); got != 2 {
				t.Fatalf("least-load should route both tasks to worker-b, got %d", got)
			}
		},
	}
	cases := []struct {
		name     string
		strategy TaskAllocationStrategy
		mode     CollaborationMode
		prepare  func(*TeamAgent) func()
	}{
		{name: "sequential_round_robin", strategy: StrategyRoundRobin, mode: CollaborationSequential},
		{name: "parallel_round_robin", strategy: StrategyRoundRobin, mode: CollaborationParallel},
		{name: "hierarchical_round_robin", strategy: StrategyRoundRobin, mode: CollaborationHierarchical},
		{name: "sequential_capability", strategy: StrategyCapability, mode: CollaborationSequential},
		{name: "parallel_capability", strategy: StrategyCapability, mode: CollaborationParallel},
		{name: "hierarchical_capability", strategy: StrategyCapability, mode: CollaborationHierarchical},
		{
			name:     "sequential_least_load",
			strategy: StrategyLeastLoaded,
			mode:     CollaborationSequential,
			prepare: func(team *TeamAgent) func() {
				return applyExternalLoad(team, "worker-a", 5)
			},
		},
		{
			name:     "parallel_least_load",
			strategy: StrategyLeastLoaded,
			mode:     CollaborationParallel,
			prepare: func(team *TeamAgent) func() {
				return applyExternalLoad(team, "worker-a", 5)
			},
		},
		{
			name:     "hierarchical_least_load",
			strategy: StrategyLeastLoaded,
			mode:     CollaborationHierarchical,
			prepare: func(team *TeamAgent) func() {
				return applyExternalLoad(team, "worker-a", 5)
			},
		},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			team, spies := newStrategyTeam(t)
			if tt.prepare != nil {
				cleanup := tt.prepare(team)
				if cleanup != nil {
					t.Cleanup(cleanup)
				}
			}
			cfg := TeamRunConfig{
				Mode:     tt.mode,
				Strategy: tt.strategy,
				Tasks:    cloneTeamTasks(baseTasks),
			}
			ctx := WithTeamRunConfig(context.Background(), cfg)
			res, err := team.Run(ctx, "matrix")
			if err != nil {
				t.Fatalf("run: %v", err)
			}
			if res == nil {
				t.Fatal("expected run result")
			}
			if res.StopReason == "" {
				t.Fatal("expected stop reason to be populated")
			}
			if check := verify[tt.strategy]; check != nil {
				check(t, spies, res)
			}
		})
	}
}

func TestTeamStrategyEdgeCases(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		cfg      TeamConfig
		task     TeamTask
		strategy TaskAllocationStrategy
		want     string
	}{
		{
			name: "capability_fallback_when_empty",
			cfg: TeamConfig{
				Name: "cap-fallback",
				Members: []TeamMemberConfig{
					{Name: "alpha", Role: TeamRoleWorker, Agent: newStubAgent("alpha"), Capabilities: []string{"go"}},
					{Name: "beta", Role: TeamRoleWorker, Agent: newStubAgent("beta"), Capabilities: []string{"python"}},
				},
			},
			task:     TeamTask{Instruction: "build", Role: TeamRoleWorker, Capabilities: []string{"rust"}},
			strategy: StrategyCapability,
			want:     "alpha",
		},
		{
			name: "single_member_round_robin",
			cfg: TeamConfig{
				Name: "one",
				Members: []TeamMemberConfig{
					{Name: "solo", Role: TeamRoleWorker, Agent: newStubAgent("solo")},
				},
			},
			task:     TeamTask{Instruction: "task", Role: TeamRoleWorker},
			strategy: StrategyRoundRobin,
			want:     "solo",
		},
		{
			name: "missing_role_fallbacks_to_members",
			cfg: TeamConfig{
				Name: "fallback-role",
				Members: []TeamMemberConfig{
					{Name: "worker-only", Role: TeamRoleWorker, Agent: newStubAgent("worker-only")},
				},
			},
			task:     TeamTask{Instruction: "review", Role: TeamRoleReviewer},
			strategy: StrategyRoundRobin,
			want:     "worker-only",
		},
	}
	for _, tt := range cases {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			team, err := NewTeamAgent(tt.cfg)
			if err != nil {
				t.Fatalf("team: %v", err)
			}
			member := team.selectMember(tt.task.normalized("fallback"), tt.strategy)
			if member == nil {
				t.Fatalf("selectMember returned nil")
			}
			if member.name != tt.want {
				t.Fatalf("selected member = %s want %s", member.name, tt.want)
			}
		})
	}
}

func TestTeamLoadAndCapabilityHelpers(t *testing.T) {
	t.Run("least_loaded_prefers_idle_member", func(t *testing.T) {
		t.Parallel()
		members := []*teamMember{
			{name: "busy"},
			{name: "mid"},
			{name: "idle"},
		}
		members[0].active.Add(5)
		members[1].active.Add(2)
		got := leastLoaded(members)
		if got != members[2] {
			t.Fatalf("expected idle member, got %+v", got)
		}
	})
	t.Run("least_loaded_ties_return_first", func(t *testing.T) {
		t.Parallel()
		members := []*teamMember{
			{name: "first"},
			{name: "second"},
		}
		got := leastLoaded(members)
		if got != members[0] {
			t.Fatalf("expected first member on tie, got %+v", got)
		}
	})
	t.Run("capability_matching_scores", func(t *testing.T) {
		t.Parallel()
		members := []*teamMember{
			{name: "go", caps: []string{"go", "build"}},
			{name: "polyglot", caps: []string{"python", "go"}},
			{name: "qa", caps: []string{"review"}},
		}
		cases := []struct {
			name   string
			needs  []string
			expect string
		}{
			{name: "exact_match", needs: []string{"review"}, expect: "qa"},
			{name: "count_best_score", needs: []string{"python", "go"}, expect: "polyglot"},
			{name: "no_overlap", needs: []string{"rust"}, expect: ""},
		}
		for _, tt := range cases {
			tt := tt
			t.Run(tt.name, func(t *testing.T) {
				t.Parallel()
				got := matchCapability(members, tt.needs)
				if tt.expect == "" {
					if got != nil {
						t.Fatalf("expected nil match, got %+v", got)
					}
					return
				}
				if got == nil || got.name != tt.expect {
					t.Fatalf("expected %s, got %+v", tt.expect, got)
				}
			})
		}
	})
}

func TestTeamAgentDelegateVariants(t *testing.T) {
	t.Run("empty instruction rejected", func(t *testing.T) {
		worker := newStubAgent("delegate")
		team, err := NewTeamAgent(TeamConfig{
			Name:    "delegate",
			Members: []TeamMemberConfig{{Name: "delegate", Role: TeamRoleWorker, Agent: worker}},
		})
		if err != nil {
			t.Fatalf("team: %v", err)
		}
		if _, err := team.Delegate(context.Background(), workflow.SubAgentRequest{}); err == nil || !strings.Contains(err.Error(), "instruction is empty") {
			t.Fatalf("expected empty instruction error, got %v", err)
		}
	})
	t.Run("nil context uses background", func(t *testing.T) {
		worker := newStubAgent("delegate")
		worker.runFunc = func(ctx context.Context, input string) (*RunResult, error) {
			if sid := sessionIDFrom(ctx); !strings.HasPrefix(sid, "delegated-") {
				t.Fatalf("expected delegated session prefix, got %q", sid)
			}
			return &RunResult{Output: "ok", StopReason: "delegated"}, nil
		}
		team, err := NewTeamAgent(TeamConfig{
			Name:    "delegate",
			Members: []TeamMemberConfig{{Name: "delegate", Role: TeamRoleWorker, Agent: worker}},
		})
		if err != nil {
			t.Fatalf("team: %v", err)
		}
		req := workflow.SubAgentRequest{
			ID:          "task-1",
			Instruction: "execute",
			SessionID:   " delegated ",
			Metadata:    map[string]any{"role": "reviewer", "capabilities": []any{"python"}},
		}
		res, err := team.Delegate(nil, req)
		if err != nil {
			t.Fatalf("delegate: %v", err)
		}
		if res.SessionID != "delegated" || res.StopReason != "delegated" {
			t.Fatalf("unexpected result: %+v", res)
		}
		if res.Metadata["role"] != "reviewer" {
			t.Fatalf("metadata not cloned: %+v", res.Metadata)
		}
	})
	t.Run("run error surfaced", func(t *testing.T) {
		worker := newStubAgent("delegate")
		worker.runFunc = func(context.Context, string) (*RunResult, error) {
			return nil, errors.New("run fail")
		}
		team, err := NewTeamAgent(TeamConfig{
			Name:    "delegate",
			Members: []TeamMemberConfig{{Name: "delegate", Role: TeamRoleWorker, Agent: worker}},
		})
		if err != nil {
			t.Fatalf("team: %v", err)
		}
		req := workflow.SubAgentRequest{Instruction: "execute"}
		res, err := team.Delegate(context.Background(), req)
		if err == nil || !strings.Contains(err.Error(), "run fail") {
			t.Fatalf("expected run error, got %v", err)
		}
		if !strings.Contains(res.Error, "run fail") {
			t.Fatalf("expected error propagated, got %+v", res)
		}
	})
}

func TestTeamAgentApproveAggregatesErrors(t *testing.T) {
	left := newStubAgent("left")
	right := newStubAgent("right")
	left.approveErr = errors.New("left fail")
	right.approveErr = errors.New("right fail")
	team, err := NewTeamAgent(TeamConfig{
		Name: "approvals",
		Members: []TeamMemberConfig{
			{Name: "left", Role: TeamRoleWorker, Agent: left},
			{Name: "right", Role: TeamRoleWorker, Agent: right},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	if err := team.Approve("ticket", true); err == nil || !strings.Contains(err.Error(), "left") || !strings.Contains(err.Error(), "right") {
		t.Fatalf("expected aggregated approval error, got %v", err)
	}
}

func TestTeamAgentForkPropagatesError(t *testing.T) {
	worker := newStubAgent("fork")
	worker.forkErr = errors.New("fork fail")
	team, err := NewTeamAgent(TeamConfig{
		Name:    "fork",
		Members: []TeamMemberConfig{{Name: "fork", Role: TeamRoleWorker, Agent: worker}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	if _, err := team.Fork(); err == nil || !strings.Contains(err.Error(), "fork fail") {
		t.Fatalf("expected fork error, got %v", err)
	}
}

func TestTeamAgentRunNilContext(t *testing.T) {
	team, err := NewTeamAgent(TeamConfig{
		Name:    "nilctx",
		Members: []TeamMemberConfig{{Name: "worker", Role: TeamRoleWorker, Agent: newStubAgent("worker")}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	if _, err := team.Run(nil, "hi"); err == nil || !strings.Contains(err.Error(), "context is nil") {
		t.Fatalf("expected context error, got %v", err)
	}
}

func TestTeamAgentLeaderNilAndWorkflow(t *testing.T) {
	var empty TeamAgent
	if empty.leader() != nil {
		t.Fatal("expected nil leader for empty team")
	}
	if err := empty.RunWorkflow(context.Background(), workflow.NewGraph()); err == nil || !strings.Contains(err.Error(), "no leader") {
		t.Fatalf("expected missing leader error, got %v", err)
	}
}

func TestTeamAgentInvokeHookErrors(t *testing.T) {
	fail := errors.New("hook fail")
	team := &TeamAgent{hooks: []Hook{failingHook{preErr: fail}}}
	if err := team.invokePreHooks(context.Background(), ""); err == nil || !errors.Is(err, fail) {
		t.Fatalf("expected pre hook error, got %v", err)
	}
	team = &TeamAgent{hooks: []Hook{failingHook{postErr: fail}}}
	if err := team.invokePostHooks(context.Background(), &RunResult{}); err == nil || !strings.Contains(err.Error(), "hook fail") {
		t.Fatalf("expected post hook error, got %v", err)
	}
}

func TestTeamRunPreHookError(t *testing.T) {
	worker := newStubAgent("worker")
	team, err := NewTeamAgent(TeamConfig{
		Name:    "hooks",
		Members: []TeamMemberConfig{{Name: "worker", Role: TeamRoleWorker, Agent: worker}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	team.hooks = []Hook{failingHook{preErr: errors.New("pre fail")}}
	if _, err := team.Run(context.Background(), "hi"); err == nil || !strings.Contains(err.Error(), "pre fail") {
		t.Fatalf("expected pre hook failure, got %v", err)
	}
}

func TestNewTeamMemberValidations(t *testing.T) {
	if _, err := newTeamMember(TeamMemberConfig{Name: "", Role: TeamRoleWorker, Agent: newStubAgent("x")}); err == nil {
		t.Fatal("expected missing name error")
	}
	if _, err := newTeamMember(TeamMemberConfig{Name: "x", Role: TeamRoleWorker}); err == nil {
		t.Fatal("expected nil agent error")
	}
	if _, err := newTeamMember(TeamMemberConfig{Name: "x", Role: TeamRole("alien"), Agent: newStubAgent("x")}); err == nil {
		t.Fatal("expected invalid role error")
	}
}

func newStrategyTeam(t *testing.T) (*TeamAgent, map[string]*stubAgent) {
	t.Helper()
	leader := newStubAgent("lead")
	workerA := newStubAgent("worker-a")
	workerB := newStubAgent("worker-b")
	reviewer := newStubAgent("review")
	cfg := TeamConfig{
		Name: "matrix",
		Members: []TeamMemberConfig{
			{Name: "lead", Role: TeamRoleLeader, Agent: leader, Capabilities: []string{"plan"}},
			{Name: "worker-a", Role: TeamRoleWorker, Agent: workerA, Capabilities: []string{"go", "plan"}},
			{Name: "worker-b", Role: TeamRoleWorker, Agent: workerB, Capabilities: []string{"python"}},
			{Name: "review", Role: TeamRoleReviewer, Agent: reviewer, Capabilities: []string{"qa"}},
		},
		Strategy:    StrategyRoundRobin,
		DefaultMode: CollaborationSequential,
	}
	team, err := NewTeamAgent(cfg)
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	spies := map[string]*stubAgent{
		"lead":     leader,
		"worker-a": workerA,
		"worker-b": workerB,
		"review":   reviewer,
	}
	return team, spies
}

func applyExternalLoad(team *TeamAgent, member string, delta int64) func() {
	for _, m := range team.members {
		if m.name == member {
			m.active.Add(delta)
			return func() { m.active.Add(-delta) }
		}
	}
	return nil
}

func cloneTeamTasks(tasks []TeamTask) []TeamTask {
	if len(tasks) == 0 {
		return nil
	}
	out := make([]TeamTask, len(tasks))
	for i, task := range tasks {
		out[i] = task.normalized(task.Instruction)
	}
	return out
}

func verifyRoundRobinStrategy(t *testing.T, spies map[string]*stubAgent, _ *RunResult) {
	t.Helper()
	if got := len(spies["lead"].runs); got != 1 {
		t.Fatalf("leader runs = %d want 1", got)
	}
	if got := len(spies["review"].runs); got != 1 {
		t.Fatalf("reviewer runs = %d want 1", got)
	}
	if got := len(spies["worker-a"].runs); got != 1 {
		t.Fatalf("worker-a runs = %d want 1", got)
	}
	if got := len(spies["worker-b"].runs); got != 1 {
		t.Fatalf("worker-b runs = %d want 1", got)
	}
}

func verifyCapabilityStrategy(t *testing.T, spies map[string]*stubAgent, _ *RunResult) {
	t.Helper()
	if len(spies["worker-a"].runs) != 1 || !strings.Contains(spies["worker-a"].runs[0].input, "build-go") {
		t.Fatalf("worker-a did not handle go task: %+v", spies["worker-a"].runs)
	}
	if len(spies["worker-b"].runs) != 1 || !strings.Contains(spies["worker-b"].runs[0].input, "build-py") {
		t.Fatalf("worker-b did not handle python task: %+v", spies["worker-b"].runs)
	}
}

type failingHook struct {
	Hook
	preErr  error
	postErr error
}

func (f failingHook) PreRun(context.Context, string) error {
	return f.preErr
}

func (f failingHook) PostRun(context.Context, *RunResult) error {
	return f.postErr
}
