package toolbuiltin

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestBashToolExecuteScript(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	script := writeScript(t, dir, "script.sh", "#!/bin/sh\necho \"$1-$2\"")

	tool := NewBashToolWithRoot(dir)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "./" + filepath.Base(script) + " foo bar",
		"workdir": dir,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	if got, want := strings.TrimSpace(result.Output), "foo-bar"; got != want {
		t.Fatalf("unexpected output %q want %q", got, want)
	}
}

func TestBashToolBlocksInjectionVectors(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	tool := NewBashToolWithRoot(dir)
	commands := []string{
		"ls; rm -rf /",
		"ls\nrm -rf /",
		filepath.Join(string(filepath.Separator), "usr", "bin", "rm") + " -rf /outside",
		"chmod 777 /tmp/secrets",
		"sudo ls",
	}
	for _, cmd := range commands {
		t.Run(strings.ReplaceAll(cmd, string(os.PathSeparator), "_"), func(t *testing.T) {
			_, err := tool.Execute(context.Background(), map[string]interface{}{
				"command": cmd,
			})
			if err == nil {
				t.Fatalf("expected command %q to be rejected", cmd)
			}
		})
	}
}

func TestBashToolTimeout(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	slow := writeScript(t, dir, "slow.sh", "#!/bin/sh\nsleep 2\necho done")

	tool := NewBashToolWithRoot(dir)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "./" + filepath.Base(slow),
		"timeout": 0.1,
	})
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "timeout") {
		t.Fatalf("expected timeout error, got %v", err)
	}
}

func TestBashToolWorkdirValidation(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	file := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(file, []byte("data"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	tool := NewBashToolWithRoot(dir)
	_, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "true",
		"workdir": filepath.Base(file),
	})
	if err == nil {
		t.Fatalf("expected workdir file to be rejected")
	}

	_, err = tool.Execute(context.Background(), map[string]interface{}{
		"command": "true",
		"workdir": "missing-dir",
	})
	if err == nil {
		t.Fatalf("expected missing workdir to fail")
	}
}

func TestBashToolMetadata(t *testing.T) {
	tool := NewBashTool()
	if tool.Name() == "" || tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("metadata missing")
	}
}

func TestDurationFromParamHelpers(t *testing.T) {
	if dur, err := durationFromParam("2"); err != nil || dur != 2*time.Second {
		t.Fatalf("string seconds parse failed: %v %v", dur, err)
	}
	if _, err := durationFromParam("bad"); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestBashToolTimeoutClamp(t *testing.T) {
	skipIfWindows(t)
	dir := cleanTempDir(t)
	script := writeScript(t, dir, "fast.sh", "#!/bin/sh\necho ok")

	tool := NewBashToolWithRoot(dir)
	result, err := tool.Execute(context.Background(), map[string]interface{}{
		"command": "./" + filepath.Base(script),
		"timeout": 9999,
	})
	if err != nil {
		t.Fatalf("Execute failed: %v", err)
	}
	data, ok := result.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", result.Data)
	}
	timeoutMS, ok := data["timeout_ms"].(int64)
	if !ok {
		t.Fatalf("missing timeout data: %v", data)
	}
	if timeoutMS != maxBashTimeout.Milliseconds() {
		t.Fatalf("expected timeout clamp to %d got %d", maxBashTimeout.Milliseconds(), timeoutMS)
	}
}

func TestExtractCommand(t *testing.T) {
	tests := []struct {
		name    string
		params  map[string]interface{}
		want    string
		wantErr string
	}{
		{"ok", map[string]interface{}{"command": "echo hi"}, "echo hi", ""},
		{"missing", map[string]interface{}{}, "", "required"},
		{"empty", map[string]interface{}{"command": "   "}, "", "cannot be empty"},
		{"wrong type", map[string]interface{}{"command": 123}, "", "must be string"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := extractCommand(tt.params)
			if tt.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
					t.Fatalf("expected error %q got %v", tt.wantErr, err)
				}
				return
			}
			if got != tt.want || err != nil {
				t.Fatalf("extractCommand = %q err=%v want=%q", got, err, tt.want)
			}
		})
	}
}

func TestDurationFromParam(t *testing.T) {
	tests := []struct {
		value   interface{}
		want    time.Duration
		wantErr bool
	}{
		{time.Second, time.Second, false},
		{float64(2), 2 * time.Second, false},
		{json.Number("3.5"), 3500 * time.Millisecond, false},
		{"1.5", 1500 * time.Millisecond, false},
		{"2s", 2 * time.Second, false},
		{int64(4), 4 * time.Second, false},
		{-1, 0, true},
		{"abc", 0, true},
	}
	for _, tt := range tests {
		got, err := durationFromParam(tt.value)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("expected error for %v", tt.value)
			}
			continue
		}
		if err != nil {
			t.Fatalf("unexpected error for %v: %v", tt.value, err)
		}
		if got != tt.want {
			t.Fatalf("durationFromParam(%v)=%v want %v", tt.value, got, tt.want)
		}
	}
}

func TestCombineOutput(t *testing.T) {
	tests := []struct {
		name   string
		stdout string
		stderr string
		want   string
	}{
		{"both", "out\n", "err\n", "out\nerr"},
		{"stdout only", "out\n", "", "out"},
		{"stderr only", "", "err\n", "err"},
		{"empty", "", "", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := combineOutput(tt.stdout, tt.stderr); got != tt.want {
				t.Fatalf("combineOutput(%q,%q)=%q want %q", tt.stdout, tt.stderr, got, tt.want)
			}
		})
	}
}

func TestSecondsToDuration(t *testing.T) {
	if _, err := secondsToDuration(-1); err == nil {
		t.Fatalf("expected error for negative seconds")
	}
	got, err := secondsToDuration(1.5)
	if err != nil {
		t.Fatalf("secondsToDuration unexpected error: %v", err)
	}
	if got != 1500*time.Millisecond {
		t.Fatalf("secondsToDuration returned %v want 1.5s", got)
	}
}

type stubStringer struct{}

func (stubStringer) String() string { return "stringer" }

func TestCoerceString(t *testing.T) {
	tests := []struct {
		value   interface{}
		want    string
		wantErr bool
	}{
		{"text", "text", false},
		{[]byte("bytes"), "bytes", false},
		{json.Number("42"), "42", false},
		{stubStringer{}, "stringer", false},
		{42, "", true},
	}
	for _, tt := range tests {
		got, err := coerceString(tt.value)
		if tt.wantErr {
			if err == nil {
				t.Fatalf("expected error for %v", tt.value)
			}
			continue
		}
		if err != nil || got != tt.want {
			t.Fatalf("coerceString(%v)=%q err=%v want %q", tt.value, got, err, tt.want)
		}
	}
}

func TestResolveRoot(t *testing.T) {
	orig, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	dir := cleanTempDir(t)
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	defer func() {
		_ = os.Chdir(orig)
	}()
	if got := filepath.Clean(resolveRoot("")); got != dir {
		t.Fatalf("expected resolveRoot to return cwd %q got %q", dir, got)
	}
	if rel := resolveRoot(".."); !filepath.IsAbs(rel) {
		t.Fatalf("expected absolute path got %q", rel)
	}
}

func writeScript(t *testing.T, dir, name, contents string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(contents), 0o755); err != nil {
		t.Fatalf("write script: %v", err)
	}
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod script: %v", err)
	}
	return path
}
