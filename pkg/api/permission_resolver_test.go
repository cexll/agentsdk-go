package api

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestBuildPermissionResolverHandlerAndApprovals(t *testing.T) {
	if buildPermissionResolver(nil, nil, nil, "", 0, false) != nil {
		t.Fatalf("expected nil resolver when no handlers configured")
	}

	queue, err := security.NewApprovalQueue(filepath.Join(t.TempDir(), "approvals.json"))
	if err != nil {
		t.Fatalf("approval queue: %v", err)
	}
	resolver := buildPermissionResolver(nil, func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error) {
		return coreevents.PermissionAllow, nil
	}, queue, "tester", time.Hour, false)
	if resolver == nil {
		t.Fatalf("expected resolver")
	}

	decision, err := resolver(context.Background(), tool.Call{Name: "Bash", SessionID: "sess"}, security.PermissionDecision{
		Action: security.PermissionAsk,
		Rule:   "rule",
		Target: "target",
	})
	if err != nil || decision.Action != security.PermissionAllow {
		t.Fatalf("unexpected decision %+v err=%v", decision, err)
	}
	if !queue.IsWhitelisted("sess") {
		t.Fatalf("expected session to be whitelisted")
	}

	allowed, err := resolver(context.Background(), tool.Call{Name: "Bash"}, security.PermissionDecision{Action: security.PermissionAllow})
	if err != nil || allowed.Action != security.PermissionAllow {
		t.Fatalf("unexpected non-ask decision %+v err=%v", allowed, err)
	}
}

func TestBuildPermissionResolverHandlerUnknown(t *testing.T) {
	resolver := buildPermissionResolver(nil, func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error) {
		return coreevents.PermissionAsk, nil
	}, nil, "", 0, false)
	decision := security.PermissionDecision{Action: security.PermissionAsk, Rule: "rule", Target: "target"}
	res, err := resolver(context.Background(), tool.Call{Name: "Bash"}, decision)
	if err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	if res.Action != security.PermissionAsk {
		t.Fatalf("expected ask decision, got %v", res.Action)
	}
}

func TestBuildPermissionResolverWaitsForApproval(t *testing.T) {
	dir := t.TempDir()
	queue, err := security.NewApprovalQueue(filepath.Join(dir, "approvals.json"))
	if err != nil {
		t.Fatalf("approval queue: %v", err)
	}

	resolver := buildPermissionResolver(nil, func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error) {
		return coreevents.PermissionAsk, nil
	}, queue, "tester", 0, true)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resultCh := make(chan security.PermissionDecision, 1)
	errCh := make(chan error, 1)
	go func() {
		res, err := resolver(ctx, tool.Call{Name: "Bash", SessionID: "sess"}, security.PermissionDecision{
			Action: security.PermissionAsk,
			Rule:   "rule",
			Target: "target",
		})
		resultCh <- res
		errCh <- err
	}()

	var rec *security.ApprovalRecord
	for i := 0; i < 100; i++ {
		pending := queue.ListPending()
		if len(pending) > 0 {
			rec = pending[0]
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if rec == nil {
		t.Fatal("expected pending approval")
	}
	if _, err := queue.Approve(rec.ID, "tester", 0); err != nil {
		t.Fatalf("approve: %v", err)
	}

	res := <-resultCh
	if err := <-errCh; err != nil {
		t.Fatalf("resolver error: %v", err)
	}
	if res.Action != security.PermissionAllow {
		t.Fatalf("expected allow action, got %v", res.Action)
	}
}
