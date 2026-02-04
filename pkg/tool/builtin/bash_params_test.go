package toolbuiltin

import (
	"testing"
	"time"
)

func TestParseAsyncFlagAndTaskID(t *testing.T) {
	if _, err := parseAsyncFlag(map[string]any{"async": "nope"}); err == nil {
		t.Fatalf("expected async parse error")
	}
	if v, err := parseAsyncFlag(map[string]any{"async": "true"}); err != nil || !v {
		t.Fatalf("expected true async, got %v err=%v", v, err)
	}
	if _, err := optionalAsyncTaskID(map[string]any{"task_id": 1}); err == nil {
		t.Fatalf("expected task_id type error")
	}
	if _, err := optionalAsyncTaskID(map[string]any{"task_id": ""}); err == nil {
		t.Fatalf("expected empty task_id error")
	}
}

func TestDurationFromParamVariants(t *testing.T) {
	if _, err := durationFromParam(-1.0); err == nil {
		t.Fatalf("expected negative duration error")
	}
	if d, err := durationFromParam("2s"); err != nil || d != 2*time.Second {
		t.Fatalf("expected duration 2s, got %v err=%v", d, err)
	}
	if d, err := durationFromParam("1.5"); err != nil || d != time.Duration(1.5*float64(time.Second)) {
		t.Fatalf("expected duration 1.5s, got %v err=%v", d, err)
	}
	if _, err := durationFromParam(struct{}{}); err == nil {
		t.Fatalf("expected unsupported duration type error")
	}
}

func TestResolveRootFallback(t *testing.T) {
	if got := resolveRoot(" "); got == "" {
		t.Fatalf("expected resolved root")
	}
}
