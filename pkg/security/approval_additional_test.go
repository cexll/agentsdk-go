package security

import (
	"context"
	"testing"
	"time"
)

func TestApprovalQueueWaitErrorsAndNil(t *testing.T) {
	var nilQueue *ApprovalQueue
	if _, err := nilQueue.Wait(context.Background(), "id"); err == nil {
		t.Fatalf("expected error for nil queue")
	}

	q, _ := newTestQueue(t)
	if _, err := q.Wait(context.Background(), "missing"); err == nil {
		t.Fatalf("expected missing approval error")
	}
}

func TestApprovalQueueEnsureCondLocked(t *testing.T) {
	q := &ApprovalQueue{records: map[string]*ApprovalRecord{}, whitelist: map[string]time.Time{}}
	q.ensureCondLocked()
	if q.cond == nil {
		t.Fatalf("expected cond to be initialized")
	}
	q.ensureCondLocked()
}

func TestApprovalQueueApproveClearsWhitelist(t *testing.T) {
	q, clock := newTestQueue(t)
	rec, err := q.Request("sess", "ls", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	q.whitelist["sess"] = clock.now.Add(time.Hour)
	approved, err := q.Approve(rec.ID, "ops", 0)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.ExpiresAt != nil {
		t.Fatalf("expected expiry cleared")
	}
	if q.IsWhitelisted("sess") {
		t.Fatalf("expected whitelist cleared")
	}
}
