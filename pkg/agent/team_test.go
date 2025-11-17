package agent

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

func TestTeamAgentSequentialRoundRobin(t *testing.T) {
	t.Parallel()
	w1 := newStubAgent("w1")
	w2 := newStubAgent("w2")
	team, err := NewTeamAgent(TeamConfig{
		Name: "seq",
		Members: []TeamMemberConfig{
			{Name: "w1", Role: TeamRoleWorker, Agent: w1},
			{Name: "w2", Role: TeamRoleWorker, Agent: w2},
		},
		Strategy: StrategyRoundRobin,
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	cfg := TeamRunConfig{
		Mode:     CollaborationSequential,
		Strategy: StrategyRoundRobin,
		Tasks: []TeamTask{
			{Name: "t1", Instruction: "alpha"},
			{Name: "t2", Instruction: "beta"},
			{Name: "t3", Instruction: "gamma"},
		},
	}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	res, err := team.Run(ctx, "ignored")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(w1.runs) != 2 || len(w2.runs) != 1 {
		t.Fatalf("unexpected run share w1=%d w2=%d", len(w1.runs), len(w2.runs))
	}
	lines := strings.Split(res.Output, "\n")
	if len(lines) != 3 || !strings.Contains(lines[0], "t1") || !strings.Contains(lines[1], "t2") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestTeamRunConfigHelpers(t *testing.T) {
	t.Parallel()
	if CollaborationParallel.String() != "parallel" || CollaborationHierarchical.String() != "hierarchical" {
		t.Fatalf("unexpected mode strings")
	}
	if CollaborationSequential.String() != "sequential" {
		t.Fatalf("sequential string mismatch")
	}
	cfg := TeamRunConfig{}
	cfg = cfg.normalize(CollaborationSequential, StrategyRoundRobin)
	if cfg.Mode != CollaborationSequential || cfg.Strategy != StrategyRoundRobin {
		t.Fatalf("normalize failed %+v", cfg)
	}
	base := TeamTask{}.normalized(" base ")
	if base.Instruction != "base" {
		t.Fatalf("normalize task failed")
	}
	defaults := TeamRunConfig{}.tasksOrDefault(" input ")
	if defaults[0].Instruction != "input" {
		t.Fatalf("tasksOrDefault failed: %+v", defaults)
	}
	ctx := WithTeamRunConfig(nil, TeamRunConfig{ShareSession: true})
	if rc, ok := teamRunConfigFromContext(ctx); !ok || !rc.ShareSession {
		t.Fatalf("context helpers failed")
	}
}

func TestTeamAgentCapabilityStrategy(t *testing.T) {
	t.Parallel()
	goWorker := newStubAgent("go")
	pyWorker := newStubAgent("py")
	team, err := NewTeamAgent(TeamConfig{
		Name: "cap",
		Members: []TeamMemberConfig{
			{Name: "go", Role: TeamRoleWorker, Agent: goWorker, Capabilities: []string{"go"}},
			{Name: "py", Role: TeamRoleWorker, Agent: pyWorker, Capabilities: []string{"python"}},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	cfg := TeamRunConfig{
		Mode:     CollaborationSequential,
		Strategy: StrategyCapability,
		Tasks: []TeamTask{
			{Instruction: "compile", Capabilities: []string{"go"}},
			{Instruction: "script", Capabilities: []string{"python"}},
		},
	}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	if _, err := team.Run(ctx, "plan"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(goWorker.runs) != 1 || len(pyWorker.runs) != 1 {
		t.Fatalf("capability routing failed go=%d py=%d", len(goWorker.runs), len(pyWorker.runs))
	}
}

func TestTeamAgentAddToolValidation(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("worker")
	team, _ := NewTeamAgent(TeamConfig{Members: []TeamMemberConfig{{Name: "worker", Role: TeamRoleWorker, Agent: worker}}})
	if err := team.AddTool(nil); err == nil {
		t.Fatal("expected nil tool error")
	}
	if err := team.AddTool(&dummyTool{name: ""}); err == nil {
		t.Fatal("expected empty name error")
	}
	worker.toolErr = errors.New("boom")
	team.shareTools = true
	if err := team.AddTool(&dummyTool{name: "x"}); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected propagation error, got %v", err)
	}
}

func TestTeamAgentNewErrorsAndLeaderFallback(t *testing.T) {
	t.Parallel()
	if _, err := NewTeamAgent(TeamConfig{}); err == nil {
		t.Fatal("expected no member error")
	}
	_, err := NewTeamAgent(TeamConfig{Members: []TeamMemberConfig{{Name: "x", Role: TeamRoleLeader}}})
	if err == nil {
		t.Fatal("expected nil agent error")
	}
	team, err := NewTeamAgent(TeamConfig{
		Name: "fallback",
		Members: []TeamMemberConfig{
			{Name: "worker", Role: TeamRoleWorker, Agent: newStubAgent("w")},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	lead := team.leader()
	if lead == nil || lead.name != "worker" {
		t.Fatalf("leader fallback failed %+v", lead)
	}
}

func TestTeamAgentLeastLoadedStrategy(t *testing.T) {
	t.Parallel()
	w1 := newStubAgent("busy")
	w2 := newStubAgent("free")
	team, err := NewTeamAgent(TeamConfig{
		Name: "least",
		Members: []TeamMemberConfig{
			{Name: "w1", Role: TeamRoleWorker, Agent: w1},
			{Name: "w2", Role: TeamRoleWorker, Agent: w2},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	team.members[0].active.Add(1)
	defer team.members[0].active.Add(-1)
	cfg := TeamRunConfig{Strategy: StrategyLeastLoaded, Tasks: []TeamTask{{Instruction: "only"}}}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	if _, err := team.Run(ctx, "payload"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(w2.runs) != 1 {
		t.Fatalf("least loaded should pick w2: %+v", w2.runs)
	}
	if load := team.members[0].load(); load <= 0 {
		t.Fatalf("expected busy member load, got %d", load)
	}
}

func TestTeamAgentParallelMode(t *testing.T) {
	t.Parallel()
	w1 := newStubAgent("p1")
	w2 := newStubAgent("p2")
	team, err := NewTeamAgent(TeamConfig{
		Name: "parallel",
		Members: []TeamMemberConfig{
			{Name: "p1", Role: TeamRoleWorker, Agent: w1},
			{Name: "p2", Role: TeamRoleWorker, Agent: w2},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	cfg := TeamRunConfig{
		Mode: CollaborationParallel,
		Tasks: []TeamTask{
			{Name: "a", Instruction: "one"},
			{Name: "b", Instruction: "two"},
		},
	}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	res, err := team.Run(ctx, "ignored")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(strings.Split(res.Output, "\n")) != 2 {
		t.Fatalf("parallel outputs missing: %q", res.Output)
	}
}

func TestTeamAgentHierarchicalFlow(t *testing.T) {
	t.Parallel()
	leader := newStubAgent("leader")
	worker := newStubAgent("worker")
	reviewer := newStubAgent("reviewer")
	leader.runFunc = func(ctx context.Context, input string) (*RunResult, error) {
		return &RunResult{Output: "plan:" + input, StopReason: "done"}, nil
	}
	worker.runFunc = func(ctx context.Context, input string) (*RunResult, error) {
		if !strings.Contains(input, "plan:") {
			t.Fatalf("worker missing leader notes %q", input)
		}
		return &RunResult{Output: "work:" + input, StopReason: "done"}, nil
	}
	reviewer.runFunc = func(ctx context.Context, input string) (*RunResult, error) {
		if !strings.Contains(input, "work:") {
			t.Fatalf("reviewer missing worker notes %q", input)
		}
		return &RunResult{Output: "review:" + input, StopReason: "done"}, nil
	}
	team, err := NewTeamAgent(TeamConfig{
		Name: "hier",
		Members: []TeamMemberConfig{
			{Name: "leader", Role: TeamRoleLeader, Agent: leader},
			{Name: "worker", Role: TeamRoleWorker, Agent: worker},
			{Name: "reviewer", Role: TeamRoleReviewer, Agent: reviewer},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	cfg := TeamRunConfig{
		Mode: CollaborationHierarchical,
		Tasks: []TeamTask{
			{Role: TeamRoleLeader, Instruction: "scope"},
			{Role: TeamRoleWorker, Instruction: "execute"},
			{Role: TeamRoleReviewer, Instruction: "check"},
		},
	}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	res, err := team.Run(ctx, "brief")
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(res.Output, "plan:") || !strings.Contains(res.Output, "review:") {
		t.Fatalf("unexpected summary %q", res.Output)
	}
}

func TestTeamAgentErrorBranches(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("err")
	team, _ := NewTeamAgent(TeamConfig{Members: []TeamMemberConfig{{Name: "worker", Role: TeamRoleWorker, Agent: worker}}})
	if _, err := team.Run(nil, "x"); err == nil {
		t.Fatal("expected context nil error")
	}
	if _, err := team.Run(context.Background(), ""); err != nil {
		t.Fatalf("empty input still ok: %v", err)
	}
	if _, err := team.Delegate(context.Background(), workflow.SubAgentRequest{}); err == nil {
		t.Fatal("delegate should fail on empty instruction")
	}
}

func TestTeamAgentSharedSessionAndEventBus(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("worker")
	progress := make(chan event.Event, 4)
	control := make(chan event.Event, 4)
	monitor := make(chan event.Event, 4)
	bus := event.NewEventBus(progress, control, monitor)
	team, err := NewTeamAgent(TeamConfig{
		Name:       "shared",
		Members:    []TeamMemberConfig{{Name: "worker", Role: TeamRoleWorker, Agent: worker}},
		EventBus:   bus,
		ShareTools: true,
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	cfg := TeamRunConfig{ShareSession: true, ShareEventBus: true}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	ctx = WithRunContext(ctx, RunContext{SessionID: "root"})
	if _, err := team.Run(ctx, "hello"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(worker.runs) != 1 || worker.runs[0].session != "root" {
		t.Fatalf("session not shared %+v", worker.runs)
	}
	select {
	case <-progress:
	case <-time.After(time.Second):
		t.Fatal("event bus not forwarded")
	}
}

func TestTeamAgentDelegateReturnsSubAgentResult(t *testing.T) {
	t.Parallel()
	reviewer := newStubAgent("reviewer")
	team, err := NewTeamAgent(TeamConfig{
		Name: "delegate",
		Members: []TeamMemberConfig{
			{Name: "leader", Role: TeamRoleLeader, Agent: newStubAgent("leader")},
			{Name: "worker", Role: TeamRoleWorker, Agent: newStubAgent("worker")},
			{Name: "reviewer", Role: TeamRoleReviewer, Agent: reviewer},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	req := workflow.SubAgentRequest{
		ID:          "r1",
		Instruction: "inspect",
		Metadata:    map[string]any{"role": "reviewer", "capabilities": []string{"qa"}},
	}
	res, err := team.Delegate(context.Background(), req)
	if err != nil {
		t.Fatalf("delegate: %v", err)
	}
	if res.ID != "r1" || len(reviewer.runs) != 1 {
		t.Fatalf("unexpected delegate result %+v", res)
	}
	req2 := workflow.SubAgentRequest{
		ID:          "r2",
		Instruction: "ops",
		Metadata:    map[string]any{"capabilities": "qa,ops"},
	}
	if _, err := team.Delegate(context.Background(), req2); err != nil {
		t.Fatalf("delegate string caps: %v", err)
	}
}

func TestTeamAgentAddToolSharing(t *testing.T) {
	t.Parallel()
	sharedWorker := newStubAgent("shared")
	isolatedWorker := newStubAgent("isolated")
	sharedTeam, err := NewTeamAgent(TeamConfig{
		Name:       "shared",
		Members:    []TeamMemberConfig{{Name: "shared", Role: TeamRoleWorker, Agent: sharedWorker}},
		ShareTools: true,
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	isolatedTeam, err := NewTeamAgent(TeamConfig{
		Name:       "isolated",
		Members:    []TeamMemberConfig{{Name: "isolated", Role: TeamRoleWorker, Agent: isolatedWorker}},
		ShareTools: false,
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	toolImpl := &dummyTool{name: "echo"}
	if err := sharedTeam.AddTool(toolImpl); err != nil {
		t.Fatalf("add tool shared: %v", err)
	}
	if len(sharedWorker.tools) != 1 {
		t.Fatalf("shared worker missing tool")
	}
	if err := isolatedTeam.AddTool(toolImpl); err != nil {
		t.Fatalf("add tool isolated: %v", err)
	}
	if len(isolatedWorker.tools) != 0 {
		t.Fatalf("isolated worker should not receive tool")
	}
}

func TestTeamAgentForkClonesMembers(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("forker")
	team, err := NewTeamAgent(TeamConfig{
		Name:    "fork",
		Members: []TeamMemberConfig{{Name: "forker", Role: TeamRoleWorker, Agent: worker}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	clone, err := team.Fork()
	if err != nil {
		t.Fatalf("fork: %v", err)
	}
	cloneTeam := clone.(*TeamAgent)
	if len(cloneTeam.members) != 1 || worker.forked == 0 {
		t.Fatalf("clone mismatch")
	}
}

func TestTeamAgentRunStreamEmitsEvents(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("stream")
	team, err := NewTeamAgent(TeamConfig{
		Name:    "stream",
		Members: []TeamMemberConfig{{Name: "stream", Role: TeamRoleWorker, Agent: worker}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	ch, err := team.RunStream(context.Background(), "hello")
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	count := 0
	for range ch {
		count++
	}
	if count == 0 {
		t.Fatal("expected streamed events")
	}
}

func TestTeamAgentHooksAndWorkflow(t *testing.T) {
	t.Parallel()
	worker := newStubAgent("hook")
	worker.workflowErr = errors.New("graph is nil")
	hook := &recordHook{}
	team, err := NewTeamAgent(TeamConfig{
		Name:    "hook",
		Members: []TeamMemberConfig{{Name: "hook", Role: TeamRoleWorker, Agent: worker}},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	hooked := team.WithHook(hook).(*TeamAgent)
	if _, err := hooked.Run(context.Background(), "hi"); err != nil {
		t.Fatalf("run: %v", err)
	}
	if hook.pre != 1 || hook.post != 1 {
		t.Fatalf("hook counts pre=%d post=%d", hook.pre, hook.post)
	}
	if err := hooked.RunWorkflow(context.Background(), workflow.NewGraph()); err == nil {
		t.Fatalf("expected graph error when nil")
	}
	// ensure Approve propagates
	if err := hooked.Approve("id", true); err != nil {
		t.Fatalf("approve: %v", err)
	}
}

type recordHook struct {
	pre  int
	post int
}

func (h *recordHook) PreRun(context.Context, string) error {
	h.pre++
	return nil
}

func (h *recordHook) PostRun(context.Context, *RunResult) error {
	h.post++
	return nil
}

func (h *recordHook) PreToolCall(context.Context, string, map[string]any) error { return nil }
func (h *recordHook) PostToolCall(context.Context, string, ToolCall) error      { return nil }

type stubAgent struct {
	name        string
	runFunc     func(context.Context, string) (*RunResult, error)
	mu          sync.Mutex
	runs        []stubRun
	tools       []string
	toolErr     error
	approvals   []string
	forked      int
	forkErr     error
	approveErr  error
	workflow    int
	workflowErr error
	middlewares []string
}

type stubRun struct {
	input   string
	session string
}

func newStubAgent(name string) *stubAgent {
	return &stubAgent{name: name, runFunc: func(ctx context.Context, input string) (*RunResult, error) {
		return defaultStubResult(name, input, sessionIDFrom(ctx)), nil
	}}
}

func (s *stubAgent) Run(ctx context.Context, input string) (*RunResult, error) {
	s.mu.Lock()
	s.runs = append(s.runs, stubRun{input: input, session: sessionIDFrom(ctx)})
	fn := s.runFunc
	s.mu.Unlock()
	if fn != nil {
		return fn(ctx, input)
	}
	return defaultStubResult(s.name, input, sessionIDFrom(ctx)), nil
}

func (s *stubAgent) RunStream(ctx context.Context, input string) (<-chan event.Event, error) {
	ch := make(chan event.Event, 4)
	go func() {
		defer close(ch)
		res, err := s.Run(ctx, input)
		if err != nil {
			return
		}
		for _, evt := range res.Events {
			ch <- evt
		}
	}()
	return ch, nil
}

func (s *stubAgent) Resume(context.Context, *event.Bookmark) (*RunResult, error) {
	return &RunResult{StopReason: StopReasonComplete}, nil
}

func (s *stubAgent) RunWorkflow(context.Context, *workflow.Graph, ...workflow.ExecutorOption) error {
	s.mu.Lock()
	s.workflow++
	s.mu.Unlock()
	return s.workflowErr
}

func (s *stubAgent) AddTool(tl tool.Tool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.toolErr != nil {
		return s.toolErr
	}
	s.tools = append(s.tools, tl.Name())
	return nil
}

func (s *stubAgent) WithHook(Hook) Agent { return s }

func (s *stubAgent) Fork(...ForkOption) (Agent, error) {
	s.mu.Lock()
	s.forked++
	err := s.forkErr
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}
	clone := newStubAgent(s.name + "-child")
	clone.runFunc = s.runFunc
	return clone, nil
}

func (s *stubAgent) Approve(id string, _ bool) error {
	s.mu.Lock()
	s.approvals = append(s.approvals, id)
	err := s.approveErr
	s.mu.Unlock()
	return err
}

func (s *stubAgent) UseMiddleware(mw middleware.Middleware) {
	if mw == nil {
		return
	}
	s.mu.Lock()
	s.middlewares = append(s.middlewares, mw.Name())
	s.mu.Unlock()
}

func (s *stubAgent) RemoveMiddleware(string) bool { return false }

func (s *stubAgent) ListMiddlewares() []middleware.Middleware { return nil }

func defaultStubResult(name, input, session string) *RunResult {
	evt := event.NewEvent(event.EventProgress, session, event.ProgressData{Stage: name})
	return &RunResult{
		Output:     name + ":" + strings.TrimSpace(input),
		StopReason: "done",
		ToolCalls:  []ToolCall{{Name: name}},
		Events:     []event.Event{evt},
		Usage:      TokenUsage{InputTokens: 1, OutputTokens: 1, TotalTokens: 2},
	}
}

func sessionIDFrom(ctx context.Context) string {
	if rc, ok := GetRunContext(ctx); ok {
		return rc.SessionID
	}
	return ""
}

type dummyTool struct{ name string }

func (d *dummyTool) Name() string             { return d.name }
func (d *dummyTool) Description() string      { return "dummy" }
func (d *dummyTool) Schema() *tool.JSONSchema { return nil }

func (d *dummyTool) Execute(context.Context, map[string]interface{}) (*tool.ToolResult, error) {
	return &tool.ToolResult{Success: true}, nil
}
