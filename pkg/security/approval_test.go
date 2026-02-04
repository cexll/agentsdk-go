package security

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type fakeClock struct {
	now time.Time
}

func (f *fakeClock) Now() time.Time {
	return f.now
}

func (f *fakeClock) Advance(d time.Duration) {
	f.now = f.now.Add(d)
}

func newTestQueue(t *testing.T) (*ApprovalQueue, *fakeClock) {
	t.Helper()
	dir := t.TempDir()
	store := filepath.Join(dir, "approvals.json")
	q, err := NewApprovalQueue(store)
	if err != nil {
		t.Fatalf("queue: %v", err)
	}
	clock := &fakeClock{now: time.Unix(1_700_000_000, 0)}
	q.clock = clock.Now
	return q, clock
}

func TestApprovalQueueRequestValidation(t *testing.T) {
	q, _ := newTestQueue(t)
	tests := []struct {
		name    string
		session string
		command string
		wantErr string
	}{
		{name: "missing session", session: "", command: "ls", wantErr: "session id"},
		{name: "missing command", session: "sess", command: "   ", wantErr: "command"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := q.Request(tt.session, tt.command, nil); err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("expected error containing %q got %v", tt.wantErr, err)
			}
		})
	}

	path := filepath.Join(t.TempDir(), "dir", "file.txt")
	rec, err := q.Request("sess", "echo ok", []string{"", path})
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if rec.State != ApprovalPending {
		t.Fatalf("expected pending got %s", rec.State)
	}
	if len(rec.Paths) != 1 || rec.Paths[0] != normalizePath(path) {
		t.Fatalf("paths not normalized: %+v", rec.Paths)
	}
}

func TestApprovalQueueAutoWhitelist(t *testing.T) {
	q, clock := newTestQueue(t)
	session := "sess"
	q.mu.Lock()
	q.whitelist[session] = clock.now.Add(time.Minute)
	q.mu.Unlock()

	rec, err := q.Request(session, "rm", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	if rec.State != ApprovalApproved || !rec.AutoApproved {
		t.Fatalf("expected auto approved, got %#v", rec)
	}
	if rec.ApprovedAt == nil || !strings.Contains(rec.Reason, "whitelisted") {
		t.Fatalf("auto approval metadata missing: %#v", rec)
	}
}

func TestApprovalQueueApproveFlow(t *testing.T) {
	q, clock := newTestQueue(t)
	rec, err := q.Request("sess", "ls", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	approved, err := q.Approve(rec.ID, "alice", time.Minute)
	if err != nil {
		t.Fatalf("approve: %v", err)
	}
	if approved.State != ApprovalApproved || approved.Approver != "alice" {
		t.Fatalf("unexpected approval state: %#v", approved)
	}
	if approved.ExpiresAt == nil || approved.ExpiresAt.Before(clock.now) {
		t.Fatalf("whitelist expiry missing: %#v", approved)
	}

	if _, err := q.Approve("missing", "ops", 0); err == nil {
		t.Fatalf("expected error for missing approval")
	}
}

func TestApprovalQueueDenyFlow(t *testing.T) {
	q, _ := newTestQueue(t)
	rec, err := q.Request("sess", "ls", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	denied, err := q.Deny(rec.ID, "bob", "unsafe")
	if err != nil {
		t.Fatalf("deny: %v", err)
	}
	if denied.State != ApprovalDenied || denied.Reason != "unsafe" {
		t.Fatalf("unexpected deny state: %#v", denied)
	}

	if _, err := q.Approve(rec.ID, "ops", 0); err == nil {
		t.Fatalf("expected approve after deny to fail")
	}

	approved, err := q.Request("sess2", "date", nil)
	if err != nil {
		t.Fatalf("second request: %v", err)
	}
	if _, err := q.Approve(approved.ID, "ops", 0); err != nil {
		t.Fatalf("approve second: %v", err)
	}
	if _, err := q.Deny(approved.ID, "ops", "late"); err == nil {
		t.Fatalf("expected deny after approval to error")
	}
}

func TestApprovalQueueWaitResolves(t *testing.T) {
	q, _ := newTestQueue(t)
	rec, err := q.Request("sess", "ls", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		time.Sleep(20 * time.Millisecond)
		_, _ = q.Approve(rec.ID, "ops", 0)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	resolved, err := q.Wait(ctx, rec.ID)
	if err != nil {
		t.Fatalf("wait: %v", err)
	}
	if resolved.State != ApprovalApproved {
		t.Fatalf("expected approved, got %s", resolved.State)
	}
	<-done
}

func TestApprovalQueueWaitContextCancelled(t *testing.T) {
	q, _ := newTestQueue(t)
	rec, err := q.Request("sess", "ls", nil)
	if err != nil {
		t.Fatalf("request: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()

	if _, err := q.Wait(ctx, rec.ID); err == nil {
		t.Fatalf("expected wait timeout error")
	}
	if _, err := q.Wait(context.Background(), ""); err == nil {
		t.Fatalf("expected wait validation error")
	}
}

func TestApprovalQueueDenyMissingID(t *testing.T) {
	q, _ := newTestQueue(t)
	if _, err := q.Deny("missing", "ops", "reason"); err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("expected not found error, got %v", err)
	}
}

func TestApprovalQueueListPendingAndClone(t *testing.T) {
	q, _ := newTestQueue(t)
	first, err := q.Request("s1", "cmd1", nil)
	if err != nil {
		t.Fatalf("request first: %v", err)
	}
	second, err := q.Request("s2", "cmd2", nil)
	if err != nil {
		t.Fatalf("request second: %v", err)
	}
	if _, err := q.Approve(second.ID, "ops", 0); err != nil {
		t.Fatalf("approve second: %v", err)
	}

	pending := q.ListPending()
	if len(pending) != 1 || pending[0].ID != first.ID {
		t.Fatalf("expected only first pending, got %#v", pending)
	}
	pending[0].State = ApprovalApproved
	if q.records[first.ID].State != ApprovalPending {
		t.Fatalf("list returned non-clone")
	}
}

func TestApprovalQueueWhitelistExpiry(t *testing.T) {
	q, clock := newTestQueue(t)
	if q.IsWhitelisted("sess") {
		t.Fatalf("unexpected whitelist")
	}

	q.mu.Lock()
	q.whitelist["sess"] = clock.now.Add(30 * time.Second)
	q.mu.Unlock()
	if !q.IsWhitelisted("sess") {
		t.Fatalf("expected whitelist to be true")
	}

	clock.Advance(time.Minute)
	if q.IsWhitelisted("sess") {
		t.Fatalf("expected whitelist to expire")
	}
}

func TestApprovalQueueLoadExistingState(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "state", "approvals.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatalf("mk store dir: %v", err)
	}

	base := time.Unix(1_700_000_123, 0)
	rec := &ApprovalRecord{
		ID:          "restored",
		SessionID:   "sess",
		Command:     "uptime",
		State:       ApprovalDenied,
		RequestedAt: base,
	}
	snapshot := approvalSnapshot{
		Records:   []*ApprovalRecord{rec},
		Whitelist: map[string]time.Time{"sess": base.Add(time.Minute)},
	}
	data, err := json.Marshal(snapshot)
	if err != nil {
		t.Fatalf("marshal snapshot: %v", err)
	}
	if err := os.WriteFile(store, data, 0o600); err != nil {
		t.Fatalf("write snapshot: %v", err)
	}

	q, err := NewApprovalQueue(store)
	if err != nil {
		t.Fatalf("new queue: %v", err)
	}
	restored, ok := q.records["restored"]
	if !ok || restored.Command != "uptime" || restored.State != ApprovalDenied {
		t.Fatalf("records not restored: %#v", q.records)
	}
	expiry, ok := q.whitelist["sess"]
	if !ok || expiry.Before(base) {
		t.Fatalf("whitelist not restored: %#v", q.whitelist)
	}
}

func TestApprovalQueueLoadCorruptState(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "corrupt", "approvals.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatalf("mk corrupt dir: %v", err)
	}
	if err := os.WriteFile(store, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write corrupt: %v", err)
	}
	if _, err := NewApprovalQueue(store); err == nil || !strings.Contains(err.Error(), "parse approvals") {
		t.Fatalf("expected parse error got %v", err)
	}
}

func TestApprovalQueueLoadReadError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod semantics differ on windows")
	}
	dir := t.TempDir()
	store := filepath.Join(dir, "restricted", "approvals.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatalf("mk restricted dir: %v", err)
	}
	if err := os.WriteFile(store, []byte("{}"), 0o600); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	if err := os.Chmod(store, 0o000); err != nil {
		t.Skipf("chmod unsupported: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(store, 0o600); err != nil {
			t.Fatalf("restore perms: %v", err)
		}
	})

	if _, err := NewApprovalQueue(store); err == nil || !strings.Contains(err.Error(), "load approvals") {
		t.Fatalf("expected read error got %v", err)
	}
}

func TestApprovalQueueLoadWithoutStorePath(t *testing.T) {
	q := &ApprovalQueue{
		records:   make(map[string]*ApprovalRecord),
		whitelist: make(map[string]time.Time),
	}
	if err := q.load(); err != nil {
		t.Fatalf("load without store should succeed: %v", err)
	}
}

func TestApprovalQueuePersistLockedWritesSnapshot(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "persist", "approvals.json")
	if err := os.MkdirAll(filepath.Dir(store), 0o755); err != nil {
		t.Fatalf("mk persist dir: %v", err)
	}
	now := time.Unix(1_700_000_500, 0)
	q := &ApprovalQueue{
		storePath: store,
		records: map[string]*ApprovalRecord{
			"rid": {
				ID:          "rid",
				SessionID:   "sess",
				Command:     "ls",
				State:       ApprovalPending,
				RequestedAt: now,
			},
		},
		whitelist: map[string]time.Time{"sess": now.Add(time.Minute)},
	}
	if err := q.persistLocked(); err != nil {
		t.Fatalf("persist: %v", err)
	}
	data, err := os.ReadFile(store)
	if err != nil {
		t.Fatalf("read store: %v", err)
	}
	var snapshot approvalSnapshot
	if err := json.Unmarshal(data, &snapshot); err != nil {
		t.Fatalf("unmarshal snapshot: %v", err)
	}
	if len(snapshot.Records) != 1 || snapshot.Records[0].ID != "rid" {
		t.Fatalf("unexpected records: %#v", snapshot.Records)
	}
	if expiry, ok := snapshot.Whitelist["sess"]; !ok || expiry.Before(now) {
		t.Fatalf("whitelist not persisted: %#v", snapshot.Whitelist)
	}
}

func TestApprovalQueuePersistLockedNoStore(t *testing.T) {
	q := &ApprovalQueue{
		records: map[string]*ApprovalRecord{
			"rid": {ID: "rid"},
		},
		whitelist: make(map[string]time.Time),
	}
	if err := q.persistLocked(); err != nil {
		t.Fatalf("persist without store: %v", err)
	}
}

func TestApprovalQueuePersistLockedRenameFailure(t *testing.T) {
	dir := t.TempDir()
	storeDir := filepath.Join(dir, "persist-dir")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatalf("mk dir: %v", err)
	}

	q := &ApprovalQueue{
		storePath: storeDir, // rename onto a directory should fail
		records: map[string]*ApprovalRecord{
			"rid": {ID: "rid", SessionID: "s", Command: "ls", RequestedAt: time.Now()},
		},
		whitelist: make(map[string]time.Time),
	}

	if err := q.persistLocked(); err == nil {
		t.Fatal("expected persist to fail when target is a directory")
	}
}

func TestCloneRecordNilSafe(t *testing.T) {
	if cloneRecord(nil) != nil {
		t.Fatalf("expected nil clone for nil input")
	}
	rec := &ApprovalRecord{
		ID:          "orig",
		SessionID:   "sess",
		Command:     "ls",
		Paths:       []string{"/tmp/file"},
		State:       ApprovalPending,
		RequestedAt: time.Unix(1_700_000_900, 0),
	}
	cloned := cloneRecord(rec)
	if cloned == rec {
		t.Fatalf("clone should allocate new struct")
	}
	rec.Paths[0] = "/tmp/mutated"
	if cloned.Paths[0] == "/tmp/mutated" {
		t.Fatalf("clone did not deep copy paths")
	}
}

func TestNewApprovalIDProducesUniqueValues(t *testing.T) {
	first := newApprovalID()
	second := newApprovalID()
	if first == "" || second == "" {
		t.Fatal("approval id should not be empty")
	}
	if first == second {
		t.Fatalf("expected unique ids, got %s", first)
	}
}
