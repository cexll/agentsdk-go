package tool

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestOutputPersisterMaybePersist(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	p := &OutputPersister{
		BaseDir:               dir,
		DefaultThresholdBytes: 4,
	}
	call := Call{Name: "Tool", SessionID: "sess"}
	res := &ToolResult{Output: "hello"}

	if err := p.MaybePersist(call, res); err != nil {
		t.Fatalf("persist failed: %v", err)
	}
	if res.OutputRef == nil || !strings.Contains(res.Output, "Output saved") {
		t.Fatalf("expected output reference, got %v", res.Output)
	}
	if !strings.Contains(res.OutputRef.Path, filepath.Clean(dir)) {
		t.Fatalf("unexpected output path %q", res.OutputRef.Path)
	}
}

func TestOutputPersisterThresholds(t *testing.T) {
	t.Parallel()

	p := &OutputPersister{
		DefaultThresholdBytes: 0,
		PerToolThresholdBytes: map[string]int{"foo": 10},
	}
	if got := p.thresholdFor("foo"); got != 10 {
		t.Fatalf("expected per-tool threshold, got %d", got)
	}
	if got := p.thresholdFor("bar"); got <= 0 {
		t.Fatalf("expected default threshold, got %d", got)
	}
}

func TestOutputPersisterErrors(t *testing.T) {
	t.Parallel()

	p := &OutputPersister{BaseDir: " ", DefaultThresholdBytes: 1}
	res := &ToolResult{Output: "hi"}
	if err := p.MaybePersist(Call{Name: "tool", SessionID: "sess"}, res); err == nil {
		t.Fatalf("expected empty base dir error")
	}
}
