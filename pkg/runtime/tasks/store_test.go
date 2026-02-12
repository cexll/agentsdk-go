package tasks

import (
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/stretchr/testify/require"
	"slices"
	"sync"
	"testing"
	"time"
)

func TestTaskStoreCRUDAndClone(t *testing.T) {
	var store TaskStore

	first, err := store.Create("task-a", "desc-a", "form-a")
	require.NoError(t, err)
	second, err := store.Create("task-b", "desc-b", "form-b")
	require.NoError(t, err)
	if first.ID == "" || second.ID == "" || first.ID == second.ID {
		t.Fatalf("expected unique non-empty IDs: first=%q second=%q", first.ID, second.ID)
	}
	if first.Status != TaskPending || second.Status != TaskPending {
		t.Fatalf("expected pending statuses: %+v %+v", first, second)
	}
	if first.CreatedAt.IsZero() || first.UpdatedAt.IsZero() || first.UpdatedAt.Before(first.CreatedAt) {
		t.Fatalf("unexpected timestamps: %+v", first)
	}

	got, err := store.Get(first.ID)
	require.NoError(t, err)
	if got.ID != first.ID || got.Subject != "task-a" || got.ActiveForm != "form-a" {
		t.Fatalf("unexpected Get: %+v", got)
	}

	// clone isolation
	got.Subject = "mutated"
	got.BlockedBy = []string{"leak"}
	reloaded, err := store.Get(first.ID)
	require.NoError(t, err)
	if reloaded.Subject != "task-a" || len(reloaded.BlockedBy) != 0 {
		t.Fatalf("store leaked internal pointer: %+v", reloaded)
	}

	list := store.List()
	if len(list) != 2 || list[0].ID != first.ID || list[1].ID != second.ID {
		t.Fatalf("expected stable insertion order: %+v", list)
	}
	list[0].Subject = "tamper"
	list2 := store.List()
	if list2[0].Subject != "task-a" {
		t.Fatalf("List returned live references: %+v", list2)
	}

	subject := "task-a-updated"
	owner := "alice"
	status := TaskInProgress
	updated, err := store.Update(first.ID, TaskUpdate{
		Subject: &subject,
		Owner:   &owner,
		Status:  &status,
	})
	require.NoError(t, err)
	if updated.Subject != "task-a-updated" || updated.Owner != "alice" || updated.Status != TaskInProgress {
		t.Fatalf("unexpected Update: %+v", updated)
	}
	if updated.UpdatedAt.Before(first.UpdatedAt) {
		t.Fatalf("expected UpdatedAt to not go backwards: before=%v after=%v", first.UpdatedAt, updated.UpdatedAt)
	}

	require.NoError(t, store.Delete(second.ID))
	if _, err := store.Get(second.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found after delete, got %v", err)
	}
	list = store.List()
	if len(list) != 1 || list[0].ID != first.ID {
		t.Fatalf("unexpected list after delete: %+v", list)
	}
}

func TestNewTaskStore(t *testing.T) {
	store := NewTaskStore()
	if store == nil {
		t.Fatalf("expected non-nil store")
	}
	task, err := store.Create("A", "", "")
	require.NoError(t, err)
	_, err = store.Get(task.ID)
	require.NoError(t, err)
}

func TestNewTaskStoreFromSnapshot(t *testing.T) {
	now := time.Now()
	snapshot := []*Task{
		{
			ID:          "task-1",
			Subject:     "Task One",
			Description: "desc-1",
			ActiveForm:  "form-1",
			Status:      TaskInProgress,
			Owner:       "alice",
			CreatedAt:   now,
			UpdatedAt:   now,
		},
		{
			ID:          "task-2",
			Subject:     "Task Two",
			Description: "desc-2",
			ActiveForm:  "form-2",
			Status:      TaskPending,
			BlockedBy:   []string{"task-1"},
			CreatedAt:   now,
			UpdatedAt:   now,
		},
	}

	store := NewTaskStoreFromSnapshot(snapshot)
	require.NotNil(t, store)

	list := store.Snapshot()
	require.Len(t, list, 2)
	require.Equal(t, "task-1", list[0].ID)
	require.Equal(t, "task-2", list[1].ID)
	require.Equal(t, TaskInProgress, list[0].Status)
	require.Equal(t, "alice", list[0].Owner)
	require.ElementsMatch(t, []string{"task-1"}, list[1].BlockedBy)

	list[0].Subject = "mutated"
	reloaded, err := store.Get("task-1")
	require.NoError(t, err)
	require.Equal(t, "Task One", reloaded.Subject)
}

func TestTaskStoreValidationAndErrors(t *testing.T) {
	var store TaskStore

	if _, err := store.Create("   ", "x", "y"); !errors.Is(err, ErrEmptySubject) {
		t.Fatalf("expected empty subject error, got %v", err)
	}
	if _, err := store.Get(" "); !errors.Is(err, ErrInvalidTaskID) {
		t.Fatalf("expected invalid id error, got %v", err)
	}
	if _, err := store.Update(" ", TaskUpdate{}); !errors.Is(err, ErrInvalidTaskID) {
		t.Fatalf("expected invalid id error, got %v", err)
	}
	if err := store.Delete(" "); !errors.Is(err, ErrInvalidTaskID) {
		t.Fatalf("expected invalid id error, got %v", err)
	}

	task, err := store.Create("ok", "", "")
	require.NoError(t, err)
	if _, err := store.Update("missing", TaskUpdate{}); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}

	badStatus := TaskStatus("nope")
	if _, err := store.Update(task.ID, TaskUpdate{Status: &badStatus}); !errors.Is(err, ErrInvalidTaskStatus) {
		t.Fatalf("expected invalid status, got %v", err)
	}
	if err := store.Delete("missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
}

func TestTaskStoreDependenciesAndUnblockOnCompletion(t *testing.T) {
	var store TaskStore

	a, err := store.Create("A", "", "")
	require.NoError(t, err)
	b, err := store.Create("B", "", "")
	require.NoError(t, err)
	c, err := store.Create("C", "", "")
	require.NoError(t, err)

	require.NoError(t, store.AddDependency(b.ID, a.ID))
	require.NoError(t, store.AddDependency(c.ID, b.ID))

	gotA, err := store.Get(a.ID)
	require.NoError(t, err)
	gotB, err := store.Get(b.ID)
	require.NoError(t, err)
	gotC, err := store.Get(c.ID)
	require.NoError(t, err)
	if gotB.Status != TaskBlocked || gotC.Status != TaskBlocked {
		t.Fatalf("expected B and C to be blocked: B=%s C=%s", gotB.Status, gotC.Status)
	}
	if !slices.Contains(gotA.Blocks, b.ID) || !slices.Contains(gotB.BlockedBy, a.ID) {
		t.Fatalf("expected A<->B dependency: A=%+v B=%+v", gotA, gotB)
	}
	if !slices.Contains(gotB.Blocks, c.ID) || !slices.Contains(gotC.BlockedBy, b.ID) {
		t.Fatalf("expected B<->C dependency: B=%+v C=%+v", gotB, gotC)
	}

	// Duplicate add should be idempotent.
	require.NoError(t, store.AddDependency(b.ID, a.ID))
	gotB, err = store.Get(b.ID)
	require.NoError(t, err)
	if len(gotB.BlockedBy) != 1 {
		t.Fatalf("expected no duplicate blockers, got %+v", gotB.BlockedBy)
	}

	statusCompleted := TaskCompleted
	_, err = store.Update(a.ID, TaskUpdate{Status: &statusCompleted})
	require.NoError(t, err)
	gotB, err = store.Get(b.ID)
	require.NoError(t, err)
	gotC, err = store.Get(c.ID)
	require.NoError(t, err)
	if gotB.Status != TaskPending {
		t.Fatalf("expected B unblocked after A completed, got %s", gotB.Status)
	}
	if gotC.Status != TaskBlocked {
		t.Fatalf("expected C to remain blocked by B, got %s", gotC.Status)
	}

	_, err = store.Update(b.ID, TaskUpdate{Status: &statusCompleted})
	require.NoError(t, err)
	gotC, err = store.Get(c.ID)
	require.NoError(t, err)
	if gotC.Status != TaskPending {
		t.Fatalf("expected C unblocked after B completed, got %s", gotC.Status)
	}

	blockedBy := store.GetBlockingTasks(c.ID)
	if len(blockedBy) != 1 || blockedBy[0].ID != b.ID {
		t.Fatalf("expected blocking tasks to include B: %+v", blockedBy)
	}
	blocks := store.GetBlockedTasks(a.ID)
	if len(blocks) != 1 || blocks[0].ID != b.ID {
		t.Fatalf("expected blocked tasks to include B: %+v", blocks)
	}
}

func TestTaskStoreCycleDetection(t *testing.T) {
	var store TaskStore

	a, err := store.Create("A", "", "")
	require.NoError(t, err)
	b, err := store.Create("B", "", "")
	require.NoError(t, err)
	c, err := store.Create("C", "", "")
	require.NoError(t, err)

	require.NoError(t, store.AddDependency(b.ID, a.ID))
	require.NoError(t, store.AddDependency(c.ID, b.ID))

	if err := store.AddDependency(a.ID, c.ID); !errors.Is(err, ErrDependencyCycle) {
		t.Fatalf("expected cycle error, got %v", err)
	}
	if err := store.AddDependency(a.ID, a.ID); !errors.Is(err, ErrSelfDependency) {
		t.Fatalf("expected self dependency error, got %v", err)
	}
}

func TestTaskStoreUniqueIDLockedErrors(t *testing.T) {
	var store TaskStore

	orig := rand.Reader
	defer func() { rand.Reader = orig }()

	rand.Reader = repeatReader{b: 1}
	id, err := newTaskID()
	if err != nil {
		t.Fatalf("newTaskID: %v", err)
	}
	store.initLocked()
	store.tasks[id] = &Task{ID: id}
	if _, err := store.uniqueIDLocked(); err == nil {
		t.Fatalf("expected unique id exhaustion")
	}
}

type repeatReader struct {
	b byte
}

func (r repeatReader) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = r.b
	}
	return len(p), nil
}

func TestTaskStoreRemoveDependencyUnblocks(t *testing.T) {
	var store TaskStore

	a, err := store.Create("A", "", "")
	require.NoError(t, err)
	b, err := store.Create("B", "", "")
	require.NoError(t, err)
	c, err := store.Create("C", "", "")
	require.NoError(t, err)

	require.NoError(t, store.AddDependency(c.ID, a.ID))
	require.NoError(t, store.AddDependency(c.ID, b.ID))
	gotC, err := store.Get(c.ID)
	require.NoError(t, err)
	if gotC.Status != TaskBlocked {
		t.Fatalf("expected C blocked, got %s", gotC.Status)
	}

	require.NoError(t, store.RemoveDependency(c.ID, a.ID))
	gotC, err = store.Get(c.ID)
	require.NoError(t, err)
	if gotC.Status != TaskBlocked || len(gotC.BlockedBy) != 1 || gotC.BlockedBy[0] != b.ID {
		t.Fatalf("expected C still blocked by B: %+v", gotC)
	}

	require.NoError(t, store.RemoveDependency(c.ID, b.ID))
	gotC, err = store.Get(c.ID)
	require.NoError(t, err)
	if gotC.Status != TaskPending || len(gotC.BlockedBy) != 0 {
		t.Fatalf("expected C unblocked after removing blockers: %+v", gotC)
	}

	// idempotent removal
	require.NoError(t, store.RemoveDependency(c.ID, b.ID))
}

func TestTaskStoreConcurrentUsage(t *testing.T) {
	var store TaskStore
	const workers = 16
	const perWorker = 25
	created := make(chan string, workers*perWorker)

	var wg sync.WaitGroup
	wg.Add(workers)
	for i := 0; i < workers; i++ {
		i := i
		go func() {
			defer wg.Done()
			for j := 0; j < perWorker; j++ {
				task, err := store.Create(fmt.Sprintf("w%d-%d", i, j), "", "")
				if err != nil {
					t.Errorf("Create: %v", err)
					return
				}
				created <- task.ID
				if _, err := store.Get(task.ID); err != nil {
					t.Errorf("Get: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
	close(created)

	ids := map[string]struct{}{}
	for id := range created {
		if _, exists := ids[id]; exists {
			t.Fatalf("duplicate id %q", id)
		}
		ids[id] = struct{}{}
	}

	list := store.List()
	if len(list) != workers*perWorker {
		t.Fatalf("expected %d tasks, got %d", workers*perWorker, len(list))
	}

	// Readers in parallel with writers.
	var rw sync.WaitGroup
	rw.Add(2)
	go func() {
		defer rw.Done()
		for i := 0; i < 100; i++ {
			_ = store.List()
		}
	}()
	go func() {
		defer rw.Done()
		for i := 0; i < 50; i++ {
			if _, err := store.Create(fmt.Sprintf("late-%d", i), "", ""); err != nil {
				t.Errorf("late Create: %v", err)
				return
			}
		}
	}()
	rw.Wait()
}

func TestTaskStoreBlockedStatusGuards(t *testing.T) {
	var store TaskStore

	blocker, err := store.Create("blocker", "", "")
	require.NoError(t, err)
	task, err := store.Create("task", "", "")
	require.NoError(t, err)
	require.NoError(t, store.AddDependency(task.ID, blocker.ID))
	inProgress := TaskInProgress
	if _, err := store.Update(task.ID, TaskUpdate{Status: &inProgress}); !errors.Is(err, ErrTaskBlocked) {
		t.Fatalf("expected blocked error when starting blocked task, got %v", err)
	}
	completed := TaskCompleted
	if _, err := store.Update(task.ID, TaskUpdate{Status: &completed}); !errors.Is(err, ErrTaskBlocked) {
		t.Fatalf("expected blocked error when completing blocked task, got %v", err)
	}

	_, err = store.Update(blocker.ID, TaskUpdate{Status: &completed})
	require.NoError(t, err)
	_, err = store.Update(task.ID, TaskUpdate{Status: &inProgress})
	require.NoError(t, err)

	// Reopen blocker should re-block downstream.
	pending := TaskPending
	_, err = store.Update(blocker.ID, TaskUpdate{Status: &pending})
	require.NoError(t, err)
	got, err := store.Get(task.ID)
	require.NoError(t, err)
	if got.Status != TaskBlocked {
		t.Fatalf("expected task re-blocked after blocker reopened, got %s", got.Status)
	}
}

func TestDeleteCleansUpDependencies(t *testing.T) {
	var store TaskStore

	a, err := store.Create("A", "", "")
	require.NoError(t, err)
	b, err := store.Create("B", "", "")
	require.NoError(t, err)
	c, err := store.Create("C", "", "")
	require.NoError(t, err)
	require.NoError(t, store.AddDependency(c.ID, a.ID))
	require.NoError(t, store.AddDependency(c.ID, b.ID))

	require.NoError(t, store.Delete(a.ID))

	gotB, err := store.Get(b.ID)
	require.NoError(t, err)
	gotC, err := store.Get(c.ID)
	require.NoError(t, err)
	if slices.Contains(gotB.Blocks, c.ID) != true {
		t.Fatalf("expected B to still block C")
	}
	if slices.Contains(gotC.BlockedBy, a.ID) {
		t.Fatalf("expected deleted dependency removed from C: %+v", gotC)
	}
	if gotC.Status != TaskBlocked {
		t.Fatalf("expected C still blocked by B: %+v", gotC)
	}

	require.NoError(t, store.Delete(b.ID))
	gotC, err = store.Get(c.ID)
	require.NoError(t, err)
	if gotC.Status != TaskPending {
		t.Fatalf("expected C unblocked after deleting last blocker: %+v", gotC)
	}
}

func TestDeleteRemovesFromBlockers(t *testing.T) {
	var store TaskStore

	blockerA, err := store.Create("A", "", "")
	require.NoError(t, err)
	blockerB, err := store.Create("B", "", "")
	require.NoError(t, err)
	task, err := store.Create("C", "", "")
	require.NoError(t, err)

	require.NoError(t, store.AddDependency(task.ID, blockerA.ID))
	require.NoError(t, store.AddDependency(task.ID, blockerB.ID))

	require.NoError(t, store.Delete(task.ID))

	gotA, err := store.Get(blockerA.ID)
	require.NoError(t, err)
	gotB, err := store.Get(blockerB.ID)
	require.NoError(t, err)
	if slices.Contains(gotA.Blocks, task.ID) || slices.Contains(gotB.Blocks, task.ID) {
		t.Fatalf("expected blockers to no longer reference deleted task: A=%+v B=%+v", gotA, gotB)
	}
}

func TestGetBlockedBlockingTasksNilCases(t *testing.T) {
	var store TaskStore
	if got := store.GetBlockedTasks("missing"); got != nil {
		t.Fatalf("expected nil for missing task, got %+v", got)
	}
	if got := store.GetBlockingTasks("missing"); got != nil {
		t.Fatalf("expected nil for missing task, got %+v", got)
	}
	if got := store.GetBlockedTasks(" "); got != nil {
		t.Fatalf("expected nil for empty task id, got %+v", got)
	}
	if got := store.GetBlockingTasks(" "); got != nil {
		t.Fatalf("expected nil for empty task id, got %+v", got)
	}
}

func TestDependencyErrors(t *testing.T) {
	var store TaskStore

	a, err := store.Create("A", "", "")
	require.NoError(t, err)
	if err := store.AddDependency("missing", a.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.AddDependency(a.ID, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.RemoveDependency("missing", a.ID); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.RemoveDependency(a.ID, "missing"); !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("expected not found, got %v", err)
	}
	if err := store.AddDependency(" ", a.ID); !errors.Is(err, ErrInvalidTaskID) {
		t.Fatalf("expected invalid id, got %v", err)
	}
}

func TestTaskStoreHandlesDanglingReferences(t *testing.T) {
	if got := cloneTask(nil); got != nil {
		t.Fatalf("expected nil clone for nil task, got %+v", got)
	}
	if got := cloneStrings(nil); got != nil {
		t.Fatalf("expected nil clone for nil slice, got %+v", got)
	}
	if got := cloneStrings([]string{}); got != nil {
		t.Fatalf("expected nil clone for empty slice, got %+v", got)
	}

	now := time.Now()
	store := TaskStore{
		tasks: map[string]*Task{
			"t1": {
				ID:        "t1",
				Subject:   "x",
				Status:    TaskPending,
				BlockedBy: []string{"missing-blocker"},
				Blocks:    []string{"missing-blocked"},
				CreatedAt: now,
				UpdatedAt: now,
			},
		},
		order: []string{"missing-id", "t1"},
	}

	list := store.List()
	if len(list) != 1 || list[0].ID != "t1" {
		t.Fatalf("expected List to skip missing tasks: %+v", list)
	}

	// Missing blocker IDs should not crash and should not block status transitions.
	inProgress := TaskInProgress
	_, err := store.Update("t1", TaskUpdate{Status: &inProgress})
	require.NoError(t, err)

	// Deleting a task with dangling references should not crash.
	require.NoError(t, store.Delete("t1"))

	// Coverage for early-return paths.
	store.onTaskCompleted("missing")

	// Cycle detector should tolerate dangling nodes.
	if introducesCycleLocked(map[string]*Task{"a": {Blocks: []string{"missing"}}}, "a", "b") {
		t.Fatalf("expected no cycle via missing nodes")
	}
}
