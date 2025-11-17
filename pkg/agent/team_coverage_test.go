package agent

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestTeamAgentRunSingleAgent(t *testing.T) {
	t.Parallel()

	agent := newAgentWithModelForTest(t, "team-basic", newMockModel())
	team, err := NewTeamAgent(TeamConfig{
		Name:    "solo-team",
		Members: []TeamMemberConfig{{Name: "solo", Role: TeamRoleWorker, Agent: agent}},
	})
	require.NoError(t, err)

	ctx := WithRunContext(context.Background(), RunContext{SessionID: "team-basic"})
	res, runErr := team.Run(ctx, "simple task")

	require.NoError(t, runErr)
	require.NotNil(t, res)
	assert.Contains(t, res.Output, "simple task")
	assert.NotEmpty(t, res.StopReason)
	assert.True(t, res.Usage.TotalTokens > 0)
}

func TestTeamAgentRunParallelAggregatesAndErrors(t *testing.T) {
	t.Parallel()

	ok := newStubAgent("ok")
	fail := newStubAgent("fail")
	fail.runFunc = func(ctx context.Context, input string) (*RunResult, error) {
		return defaultStubResult("fail", input, sessionIDFrom(ctx)), errors.New("boom")
	}

	team, err := NewTeamAgent(TeamConfig{
		Name: "parallel-team",
		Members: []TeamMemberConfig{
			{Name: "ok", Role: TeamRoleWorker, Agent: ok},
			{Name: "fail", Role: TeamRoleWorker, Agent: fail},
		},
	})
	require.NoError(t, err)

	cfg := TeamRunConfig{
		Mode: CollaborationParallel,
		Tasks: []TeamTask{
			{Name: "first", Instruction: "alpha"},
			{Name: "second", Instruction: "beta"},
		},
	}
	ctx := WithTeamRunConfig(context.Background(), cfg)
	res, runErr := team.Run(ctx, "root")

	require.Error(t, runErr)
	require.NotNil(t, res)
	assert.Contains(t, res.Output, "first")
	assert.Contains(t, res.Output, "second")
	assert.Contains(t, res.Output, "ok:")
	assert.Contains(t, res.Output, "fail:")
}

func TestTeamAgentStrategyOverrideAndSessionForking(t *testing.T) {
	t.Parallel()

	goWorker := newStubAgent("go-agent")
	pyWorker := newStubAgent("py-agent")

	team, err := NewTeamAgent(TeamConfig{
		Name: "strategy-team",
		Members: []TeamMemberConfig{
			{Name: "go-agent", Role: TeamRoleWorker, Agent: goWorker, Capabilities: []string{"go"}},
			{Name: "py-agent", Role: TeamRoleWorker, Agent: pyWorker, Capabilities: []string{"python"}},
		},
	})
	require.NoError(t, err)

	cfg := TeamRunConfig{
		Mode:     CollaborationSequential,
		Strategy: StrategyCapability,
		Tasks: []TeamTask{
			{Name: "backend", Instruction: "build api", Capabilities: []string{"go"}},
			{Name: "scripting", Instruction: "write script", Capabilities: []string{"python"}},
		},
	}
	ctx := WithRunContext(context.Background(), RunContext{SessionID: "root"})
	ctx = WithTeamRunConfig(ctx, cfg)

	res, runErr := team.Run(ctx, "ignored")
	require.NoError(t, runErr)
	require.NotNil(t, res)

	require.Len(t, goWorker.runs, 1)
	require.Len(t, pyWorker.runs, 1)

	assert.True(t, strings.HasPrefix(goWorker.runs[0].session, "root-go-agent-01"))
	assert.True(t, strings.HasPrefix(pyWorker.runs[0].session, "root-py-agent-02"))
}

func TestTeamRunStageSkipsEmptyTasks(t *testing.T) {
	t.Parallel()

	acc := newAccumulator(1)
	team := &TeamAgent{}
	out, err := team.runStage(context.Background(), TeamRunConfig{Mode: CollaborationSequential}, nil, acc, "notes")

	require.NoError(t, err)
	assert.Empty(t, out)
}

func TestTeamRunSingleMissingMember(t *testing.T) {
	t.Parallel()

	team := &TeamAgent{byRole: map[TeamRole][]*teamMember{}}
	cfg := TeamRunConfig{Strategy: StrategyRoundRobin}
	task := scheduledTask{index: 0, task: TeamTask{Role: TeamRoleReviewer, Instruction: "verify"}}

	res, err := team.runSingle(context.Background(), cfg, task, "")
	require.Error(t, err)
	assert.Nil(t, res)
}

func TestTeamLeaderReturnsNilWhenEmpty(t *testing.T) {
	t.Parallel()

	team := &TeamAgent{byRole: map[TeamRole][]*teamMember{}}
	assert.Nil(t, team.leader())
}

func TestTeamExecuteRespectsCanceledContext(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	member := &teamMember{name: "worker", agent: newStubAgent("worker")}
	team := &TeamAgent{}

	res, err := team.execute(ctx, member, "task", 0, TeamRunConfig{})
	require.Error(t, err)
	assert.Nil(t, res)
}
