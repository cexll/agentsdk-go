package toolbuiltin

import "testing"

func TestNormalizeTaskStatus(t *testing.T) {
	t.Parallel()

	cases := map[string]string{
		"":            TaskStatusPending,
		" pending ":   TaskStatusPending,
		"in_progress": TaskStatusInProgress,
		"in-progress": TaskStatusInProgress,
		"completed":   TaskStatusCompleted,
		"complete":    TaskStatusCompleted,
		"done":        TaskStatusCompleted,
		"blocked":     TaskStatusBlocked,
		"unknown":     "",
	}

	for input, want := range cases {
		if got := normalizeTaskStatus(input); got != want {
			t.Fatalf("normalizeTaskStatus(%q)=%q want %q", input, got, want)
		}
	}
}
