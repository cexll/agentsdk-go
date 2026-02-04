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

func TestPermissionHelpers(t *testing.T) {
	t.Parallel()

	if got := buildPermissionReason(security.PermissionDecision{}); got != "" {
		t.Fatalf("unexpected reason %q", got)
	}
	if got := buildPermissionReason(security.PermissionDecision{Rule: "r"}); got == "" {
		t.Fatalf("expected rule reason")
	}
	if got := formatApprovalCommand("", ""); got != "tool" {
		t.Fatalf("unexpected command %q", got)
	}
	if got := formatApprovalCommand("Bash", "ls"); got != "Bash(ls)" {
		t.Fatalf("unexpected command %q", got)
	}
	if approvalActor(" ") != "host" {
		t.Fatalf("expected host fallback")
	}
}

func TestBuildPermissionResolverAllowDeny(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	queue, err := security.NewApprovalQueue(filepath.Join(dir, "approvals.json"))
	if err != nil {
		t.Fatalf("queue init failed: %v", err)
	}

	allowResolver := buildPermissionResolver(nil, func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error) {
		return coreevents.PermissionAllow, nil
	}, queue, "tester", time.Hour, false)

	call := tool.Call{Name: "Bash", Params: map[string]any{"command": "ls"}, SessionID: "sess"}
	decision := security.PermissionDecision{Action: security.PermissionAsk, Rule: "rule", Target: "ls"}
	allowed, err := allowResolver(context.Background(), call, decision)
	if err != nil {
		t.Fatalf("resolver failed: %v", err)
	}
	if allowed.Action != security.PermissionAllow {
		t.Fatalf("expected allow action, got %v", allowed.Action)
	}
	if !queue.IsWhitelisted("sess") {
		t.Fatalf("expected session whitelisted")
	}

	queue2, err := security.NewApprovalQueue(filepath.Join(dir, "approvals2.json"))
	if err != nil {
		t.Fatalf("queue init failed: %v", err)
	}
	denyResolver := buildPermissionResolver(nil, func(context.Context, PermissionRequest) (coreevents.PermissionDecisionType, error) {
		return coreevents.PermissionDeny, nil
	}, queue2, "tester", 0, false)
	denied, err := denyResolver(context.Background(), call, decision)
	if err != nil {
		t.Fatalf("resolver failed: %v", err)
	}
	if denied.Action != security.PermissionDeny {
		t.Fatalf("expected deny action, got %v", denied.Action)
	}
	if queue2.IsWhitelisted("sess") {
		t.Fatalf("expected session not whitelisted")
	}
}
