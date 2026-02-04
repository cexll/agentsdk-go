package tool

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNewOutputPersisterAndBaseDir(t *testing.T) {
	p := NewOutputPersister()
	if p == nil || p.BaseDir == "" {
		t.Fatalf("expected persister with base dir")
	}
	if toolOutputBaseDir() == "" {
		t.Fatalf("expected tool output base dir")
	}
}

func TestCreateToolOutputFile(t *testing.T) {
	if _, _, err := createToolOutputFile(" "); err == nil {
		t.Fatalf("expected error for empty dir")
	}
	dir := t.TempDir()
	f, path, err := createToolOutputFile(dir)
	if err != nil {
		t.Fatalf("create file: %v", err)
	}
	f.Close()
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("expected file at %s: %v", path, err)
	}
}

func TestOutputPersisterMaybePersistAdditional(t *testing.T) {
	p := &OutputPersister{
		BaseDir:               t.TempDir(),
		DefaultThresholdBytes: 1,
	}
	call := Call{Name: "echo", SessionID: "sess"}
	res := &ToolResult{Output: "large"}
	if err := p.MaybePersist(call, res); err != nil {
		t.Fatalf("persist: %v", err)
	}
	if res.OutputRef == nil || res.Output == "" || res.OutputRef.Path == "" {
		t.Fatalf("expected output ref")
	}
	if _, err := os.Stat(res.OutputRef.Path); err != nil {
		t.Fatalf("expected persisted file: %v", err)
	}

	p2 := &OutputPersister{BaseDir: "", DefaultThresholdBytes: 1}
	if err := p2.MaybePersist(call, &ToolResult{Output: "large"}); err == nil {
		t.Fatalf("expected empty base dir error")
	}

	p3 := &OutputPersister{BaseDir: filepath.Join(t.TempDir(), "out"), DefaultThresholdBytes: 10}
	res2 := &ToolResult{Output: "small"}
	if err := p3.MaybePersist(call, res2); err != nil {
		t.Fatalf("persist small: %v", err)
	}
	if res2.OutputRef != nil {
		t.Fatalf("expected small output to stay inline")
	}
}

func TestCreateToolOutputFileErrors(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, _, err := createToolOutputFile(filePath); err == nil {
		t.Fatalf("expected error for file path")
	}
}

func TestOutputPersisterThresholdOverrides(t *testing.T) {
	p := &OutputPersister{
		DefaultThresholdBytes: 10,
		PerToolThresholdBytes: map[string]int{"bash": 1},
	}
	if got := p.thresholdFor("Bash"); got != 1 {
		t.Fatalf("expected per-tool threshold, got %d", got)
	}
	if got := p.thresholdFor("Other"); got != 10 {
		t.Fatalf("expected default threshold, got %d", got)
	}
}

func TestOutputPersisterSkipsWhenOutputRefOrEmpty(t *testing.T) {
	p := &OutputPersister{BaseDir: t.TempDir(), DefaultThresholdBytes: 1}
	res := &ToolResult{Output: "large", OutputRef: &OutputRef{Path: "x"}}
	if err := p.MaybePersist(Call{Name: "echo"}, res); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.OutputRef == nil || res.OutputRef.Path != "x" {
		t.Fatalf("expected existing output ref preserved")
	}

	empty := &ToolResult{Output: ""}
	if err := p.MaybePersist(Call{Name: "echo"}, empty); err != nil {
		t.Fatalf("unexpected error for empty output: %v", err)
	}
}
