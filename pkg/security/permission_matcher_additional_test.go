package security

import (
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestCompilePermissionRuleValidation(t *testing.T) {
	if _, err := compilePermissionRule("   "); err == nil {
		t.Fatal("expected error for empty rule")
	}
	if _, err := compilePermissionRule("Read(file"); err == nil {
		t.Fatal("expected malformed rule error")
	}
	rule, err := compilePermissionRule("Echo")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !rule.match("anything") {
		t.Fatalf("rule without pattern should match all targets")
	}
}

func TestCompilePatternVariants(t *testing.T) {
	matcher, err := compilePattern("regex:^foo$")
	if err != nil {
		t.Fatalf("regex compile failed: %v", err)
	}
	if !matcher("foo") || matcher("bar") {
		t.Fatalf("regex matcher behaved unexpectedly")
	}
	if _, err := compilePattern(""); err == nil {
		t.Fatal("expected empty pattern error")
	}
	if _, err := compilePattern("regex:["); err == nil {
		t.Fatal("expected invalid regex error")
	}
}

func TestDeriveTargetCoverage(t *testing.T) {
	tmp := filepath.Join("tmp", "file.txt")
	tests := []struct {
		name   string
		tool   string
		params map[string]any
		want   string
	}{
		{name: "bash with args", tool: "Bash", params: map[string]any{"command": "ls -la"}, want: "ls:-la"},
		{name: "bash no args", tool: "bash", params: map[string]any{"command": "ls"}, want: "ls:"},
		{name: "bash empty", tool: "bash", params: map[string]any{"command": "   "}, want: ""},
		{name: "read path", tool: "Read", params: map[string]any{"file_path": tmp}, want: filepath.Clean(tmp)},
		{name: "taskget prefers id", tool: "TaskGet", params: map[string]any{"task_id": "task-123", "path": "/tmp/ignored"}, want: "task-123"},
		{name: "generic target key", tool: "Custom", params: map[string]any{"target": "/foo/bar"}, want: filepath.Clean("/foo/bar")},
		{name: "first string fallback", tool: "Other", params: map[string]any{"misc": []byte(" hi ")}, want: "hi"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := deriveTarget(tt.tool, tt.params); got != tt.want {
				t.Fatalf("deriveTarget = %q, want %q", got, tt.want)
			}
		})
	}
}

type stringer struct{ v string }

func (s stringer) String() string { return s.v }

func TestFirstStringAndCoercion(t *testing.T) {
	params := map[string]any{
		"path":      stringer{v: " str "},
		"byte_path": []byte(" /tmp/bytes "),
		"other":     123,
	}
	if got := firstString(params, "missing", "byte_path"); got != "/tmp/bytes" {
		t.Fatalf("expected byte coercion, got %q", got)
	}
	if got := firstString(params, "path"); got != "str" {
		t.Fatalf("expected stringer value, got %q", got)
	}
	if got := firstString(params); got == "" {
		t.Fatal("expected fallback to first string-like value")
	}
	if got := coerceToString(123); got != "" {
		t.Fatalf("expected empty string for non-coercible type, got %q", got)
	}
	if got := firstString(nil); got != "" {
		t.Fatalf("expected empty string for nil params, got %q", got)
	}
}

func TestPermissionMatcherNilAndUnknown(t *testing.T) {
	if matcher, err := NewPermissionMatcher(nil); err != nil || matcher != nil {
		t.Fatalf("nil config should return nil matcher, got %+v err %v", matcher, err)
	}

	cfg := &config.PermissionsConfig{Allow: []string{"Read(**/*.txt)"}}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	unknown := matcher.Match("Write", map[string]any{"path": "/tmp/file.txt"})
	if unknown.Action != PermissionUnknown || unknown.Tool != "Write" {
		t.Fatalf("expected unknown decision, got %+v", unknown)
	}

	badCfg := &config.PermissionsConfig{Ask: []string{"Broken("}}
	if _, err := NewPermissionMatcher(badCfg); err == nil {
		t.Fatal("expected error for malformed ask rule")
	}
}

func TestPermissionMatcherPriorityRespectsCase(t *testing.T) {
	cfg := &config.PermissionsConfig{
		Allow: []string{"Bash(ls:*)"},
		Deny:  []string{"bash(regex:^rm:)"},
	}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	decision := matcher.Match("bash", map[string]any{"command": "rm /tmp"})
	if decision.Action != PermissionDeny {
		t.Fatalf("expected deny despite case differences, got %+v", decision)
	}
	allow := matcher.Match("Bash", map[string]any{"command": "ls"})
	if allow.Action != PermissionAllow {
		t.Fatalf("expected allow, got %+v", allow)
	}
}

func TestPermissionMatcherTaskTools(t *testing.T) {
	cfg := &config.PermissionsConfig{
		Deny: []string{
			"TaskCreate(task-create)",
			"TaskGet(task-get)",
			"TaskUpdate(task-update)",
			"TaskList(task-list)",
		},
	}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}

	tests := []struct {
		tool string
		id   string
		rule string
	}{
		{tool: "TaskCreate", id: "task-create", rule: "TaskCreate(task-create)"},
		{tool: "TaskGet", id: "task-get", rule: "TaskGet(task-get)"},
		{tool: "TaskUpdate", id: "task-update", rule: "TaskUpdate(task-update)"},
		{tool: "TaskList", id: "task-list", rule: "TaskList(task-list)"},
	}

	for _, tt := range tests {
		t.Run(tt.tool, func(t *testing.T) {
			decision := matcher.Match(tt.tool, map[string]any{"task_id": tt.id, "path": "/tmp/ignored"})
			if decision.Action != PermissionDeny || decision.Rule != tt.rule || decision.Target != tt.id {
				t.Fatalf("unexpected decision: %+v", decision)
			}
		})
	}
}
