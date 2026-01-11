package tool

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestOutputPersisterDoesNotPersistAtThreshold(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 5,
	}
	result := &ToolResult{Success: true, Output: "12345"}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.Output != "12345" {
		t.Fatalf("expected output to remain inline, got %q", result.Output)
	}
	if result.OutputRef != nil {
		t.Fatalf("expected OutputRef nil, got %+v", result.OutputRef)
	}
}

func TestOutputPersisterPersistsWhenOutputExceedsThreshold(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 5,
	}
	original := "123456"
	result := &ToolResult{Success: true, Output: original}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.OutputRef == nil || strings.TrimSpace(result.OutputRef.Path) == "" {
		t.Fatalf("expected OutputRef populated, got %+v", result.OutputRef)
	}
	const prefix = "[Output saved to: "
	if !strings.HasPrefix(result.Output, prefix) || !strings.HasSuffix(result.Output, "]") {
		t.Fatalf("unexpected reference output %q", result.Output)
	}
	if result.OutputRef.Path != strings.TrimSuffix(strings.TrimPrefix(result.Output, prefix), "]") {
		t.Fatalf("output path mismatch: output=%q ref=%q", result.Output, result.OutputRef.Path)
	}
	wantPrefix := filepath.Join(base, "sess", "echo") + string(filepath.Separator)
	if !strings.HasPrefix(result.OutputRef.Path, wantPrefix) {
		t.Fatalf("expected path under %q, got %q", wantPrefix, result.OutputRef.Path)
	}
	if result.OutputRef.SizeBytes != int64(len(original)) {
		t.Fatalf("unexpected SizeBytes=%d want %d", result.OutputRef.SizeBytes, len(original))
	}
	if result.OutputRef.Truncated {
		t.Fatalf("expected Truncated=false")
	}
	data, err := os.ReadFile(result.OutputRef.Path)
	if err != nil {
		t.Fatalf("read persisted output: %v", err)
	}
	if string(data) != original {
		t.Fatalf("unexpected persisted output %q", string(data))
	}
	baseName := strings.TrimSuffix(filepath.Base(result.OutputRef.Path), ".output")
	if _, err := strconv.ParseInt(baseName, 10, 64); err != nil {
		t.Fatalf("expected numeric timestamp filename, got %q: %v", baseName, err)
	}
}

func TestOutputPersisterPerToolThresholdOverridesDefault(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 5,
		PerToolThresholdBytes: map[string]int{"echo": 10},
	}
	result := &ToolResult{Success: true, Output: strings.Repeat("x", 6)}
	err := p.MaybePersist(Call{Name: "ECHO", SessionID: "sess"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.OutputRef != nil {
		t.Fatalf("expected per-tool override to keep output inline, got %+v", result.OutputRef)
	}
}

func TestOutputPersisterDefaultsSessionIDWhenMissing(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 1,
	}
	result := &ToolResult{Success: true, Output: "ab"}
	err := p.MaybePersist(Call{Name: "echo"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.OutputRef == nil {
		t.Fatalf("expected OutputRef populated")
	}
	wantPrefix := filepath.Join(base, "default", "echo") + string(filepath.Separator)
	if !strings.HasPrefix(result.OutputRef.Path, wantPrefix) {
		t.Fatalf("expected path under %q, got %q", wantPrefix, result.OutputRef.Path)
	}
}

func TestOutputPersisterDegradesWhenBaseDirInvalid(t *testing.T) {
	root := t.TempDir()
	base := filepath.Join(root, "not-a-dir")
	if err := os.WriteFile(base, []byte("file"), 0o600); err != nil {
		t.Fatalf("write file base: %v", err)
	}
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 1,
	}
	result := &ToolResult{Success: true, Output: "ab"}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err == nil {
		t.Fatalf("expected persistence error")
	}
	if result.Output != "ab" {
		t.Fatalf("expected output to remain untouched, got %q", result.Output)
	}
	if result.OutputRef != nil {
		t.Fatalf("expected OutputRef nil on failure, got %+v", result.OutputRef)
	}
}

func TestOutputPersisterDoesNotPersistBelowThreshold(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 5,
	}
	result := &ToolResult{Success: true, Output: "1234"}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.Output != "1234" {
		t.Fatalf("expected output to remain inline, got %q", result.Output)
	}
	if result.OutputRef != nil {
		t.Fatalf("expected OutputRef nil, got %+v", result.OutputRef)
	}
	if _, err := os.Stat(filepath.Join(base, "sess")); err == nil {
		t.Fatalf("expected no session directory to be created")
	}
}

func TestOutputPersisterPerToolThresholdOverridesDefaultLower(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 10,
		PerToolThresholdBytes: map[string]int{"echo": 5},
	}
	result := &ToolResult{Success: true, Output: "123456"}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err != nil {
		t.Fatalf("MaybePersist: %v", err)
	}
	if result.OutputRef == nil {
		t.Fatalf("expected output persisted via per-tool override")
	}
	wantPrefix := filepath.Join(base, "sess", "echo") + string(filepath.Separator)
	if !strings.HasPrefix(result.OutputRef.Path, wantPrefix) {
		t.Fatalf("expected path under %q, got %q", wantPrefix, result.OutputRef.Path)
	}
}

func TestOutputPersisterIsolatesDirectoriesPerToolName(t *testing.T) {
	base := t.TempDir()
	p := &OutputPersister{
		BaseDir:               base,
		DefaultThresholdBytes: 1,
	}

	echo := &ToolResult{Success: true, Output: "echo-out"}
	if err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, echo); err != nil {
		t.Fatalf("persist echo: %v", err)
	}
	grep := &ToolResult{Success: true, Output: "grep-out"}
	if err := p.MaybePersist(Call{Name: "grep", SessionID: "sess"}, grep); err != nil {
		t.Fatalf("persist grep: %v", err)
	}

	if echo.OutputRef == nil || grep.OutputRef == nil {
		t.Fatalf("expected output refs populated, echo=%+v grep=%+v", echo.OutputRef, grep.OutputRef)
	}
	if echo.OutputRef.Path == grep.OutputRef.Path {
		t.Fatalf("expected distinct output files, got %q", echo.OutputRef.Path)
	}
	echoDir := filepath.Dir(echo.OutputRef.Path)
	grepDir := filepath.Dir(grep.OutputRef.Path)
	if echoDir == grepDir {
		t.Fatalf("expected different tool directories, got %q", echoDir)
	}
}

func TestOutputPersisterDegradesWhenBaseDirEmpty(t *testing.T) {
	p := &OutputPersister{
		BaseDir:               "  ",
		DefaultThresholdBytes: 1,
	}
	result := &ToolResult{Success: true, Output: "ab"}
	err := p.MaybePersist(Call{Name: "echo", SessionID: "sess"}, result)
	if err == nil {
		t.Fatalf("expected persistence error")
	}
	if result.Output != "ab" {
		t.Fatalf("expected output to remain untouched, got %q", result.Output)
	}
	if result.OutputRef != nil {
		t.Fatalf("expected OutputRef nil on failure, got %+v", result.OutputRef)
	}
}
