package agent

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/approval"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

func TestAgentApprovalWalFailure(t *testing.T) {
	agent := newTestAgent(t).(*basicAgent)
	agent.approval = approval.NewQueue(failingStore{err: errors.New("wal append fail")}, approval.NewWhitelist())
	echo := &mockTool{name: "echo"}
	if err := agent.AddTool(echo); err != nil {
		t.Fatalf("add tool: %v", err)
	}
	ctx := WithRunContext(context.Background(), RunContext{
		SessionID:    "fs",
		ApprovalMode: ApprovalRequired,
	})
	_, err := agent.Run(ctx, "tool:echo {}")
	if err == nil || !strings.Contains(err.Error(), "wal append fail") {
		t.Fatalf("expected wal failure, got %v", err)
	}
}

func TestSubAgentManagerSessionForkFailure(t *testing.T) {
	mgr, err := NewSubAgentManager(newTestAgent(t), WithSubAgentSession(failingSession{}))
	if err != nil {
		t.Fatalf("manager: %v", err)
	}
	_, err = mgr.Delegate(context.Background(), workflow.SubAgentRequest{Instruction: "hi"})
	if err == nil || !strings.Contains(err.Error(), "fork session") {
		t.Fatalf("expected fork failure, got %v", err)
	}
}

func TestTeamAddToolAggregatesFailures(t *testing.T) {
	left := newStubAgent("left")
	right := newStubAgent("right")
	left.toolErr = errors.New("left fail")
	right.toolErr = errors.New("right fail")
	team, err := NewTeamAgent(TeamConfig{
		Name:       "share",
		ShareTools: true,
		Members: []TeamMemberConfig{
			{Name: "left", Role: TeamRoleWorker, Agent: left},
			{Name: "right", Role: TeamRoleWorker, Agent: right},
		},
	})
	if err != nil {
		t.Fatalf("team: %v", err)
	}
	err = team.AddTool(&dummyTool{name: "agg"})
	if err == nil || !strings.Contains(err.Error(), "left") || !strings.Contains(err.Error(), "right") {
		t.Fatalf("expected aggregated failure, got %v", err)
	}
}

func TestNewApprovalQueueFallsBackWhenTempDirInvalid(t *testing.T) {
	tempFile := filepath.Join(t.TempDir(), "no-dir")
	if err := os.WriteFile(tempFile, []byte("x"), 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	t.Setenv("TMPDIR", tempFile)
	queue := newApprovalQueue()
	t.Cleanup(func() { _ = queue.Close() })
	rec, ok, err := queue.Request("sess", "echo", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if ok {
		t.Fatalf("unexpected auto-approve on empty whitelist")
	}
	if rec.ID == "" {
		t.Fatal("missing record id from fallback queue")
	}
}

type failingStore struct {
	err error
}

func (s failingStore) Append(approval.Record) error { return s.err }
func (s failingStore) All() []approval.Record       { return nil }
func (s failingStore) Query(approval.Filter) []approval.Record {
	return nil
}
func (s failingStore) Close() error { return nil }

type failingSession struct{}

func (f failingSession) ID() string { return "broken" }
func (f failingSession) Append(session.Message) error {
	return nil
}
func (f failingSession) List(session.Filter) ([]session.Message, error) {
	return nil, nil
}
func (f failingSession) Checkpoint(string) error { return nil }
func (f failingSession) Resume(string) error     { return nil }
func (f failingSession) Fork(string) (session.Session, error) {
	return nil, errors.New("fork failed")
}
func (f failingSession) Close() error { return nil }
