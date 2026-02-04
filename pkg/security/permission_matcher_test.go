package security

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/stretchr/testify/require"
)

func TestPermissionMatcherPriority(t *testing.T) {
	cfg := &config.PermissionsConfig{
		Allow: []string{"Read(**/*.md)"},
		Ask:   []string{"Read(**/draft.md)"},
		Deny:  []string{"Read(**/secret.md)"},
	}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}

	alwaysAllowed := matcher.Match("Read", map[string]any{"file_path": "/work/notes/readme.md"})
	if alwaysAllowed.Action != PermissionAllow {
		t.Fatalf("expected allow, got %v", alwaysAllowed.Action)
	}

	ask := matcher.Match("Read", map[string]any{"file_path": "/work/drafts/draft.md"})
	if ask.Action != PermissionAsk || ask.Rule != "Read(**/draft.md)" {
		t.Fatalf("expected ask, got %+v", ask)
	}

	deny := matcher.Match("Read", map[string]any{"file_path": "/work/private/secret.md"})
	if deny.Action != PermissionDeny || deny.Rule != "Read(**/secret.md)" {
		t.Fatalf("expected deny, got %+v", deny)
	}
}

func TestPermissionMatcherRegexAndGlob(t *testing.T) {
	cfg := &config.PermissionsConfig{
		Allow: []string{"Bash(regex:^ls:.*$)"},
		Deny:  []string{"Read(**/*.env)"},
	}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}

	bash := matcher.Match("Bash", map[string]any{"command": "ls -la"})
	if bash.Action != PermissionAllow || bash.Rule == "" || bash.Target == "" {
		t.Fatalf("regex rule not matched: %+v", bash)
	}

	deny := matcher.Match("Read", map[string]any{"file_path": "/repo/config/.env"})
	if deny.Action != PermissionDeny {
		t.Fatalf("expected deny, got %+v", deny)
	}
}

func TestPermissionMatcherMCPToolNames(t *testing.T) {
	cfg := &config.PermissionsConfig{
		Allow: []string{"mcp__demo__*"},
		Deny:  []string{"mcp__demo__danger"},
	}
	matcher, err := NewPermissionMatcher(cfg)
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}

	allow := matcher.Match("mcp__demo__list", nil)
	if allow.Action != PermissionAllow || allow.Rule != "mcp__demo__*" {
		t.Fatalf("expected allow via wildcard rule, got %+v", allow)
	}

	deny := matcher.Match("mcp__demo__danger", nil)
	if deny.Action != PermissionDeny || deny.Rule != "mcp__demo__danger" {
		t.Fatalf("expected deny specific tool, got %+v", deny)
	}

	unknown := matcher.Match("mcp__other__tool", nil)
	if unknown.Action != PermissionUnknown {
		t.Fatalf("expected unknown for unrelated tool, got %+v", unknown)
	}
}

func TestSandboxLoadPermissionsFromClaudeDir(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := map[string]any{
		"permissions": map[string]any{
			"allow": []string{"Bash(ls:*)"},
			"deny":  []string{"Read(**/secret.txt)"},
			"ask":   []string{"Read(**/maybe.txt)"},
		},
	}
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	sb := NewSandbox(root)
	if err := sb.LoadPermissions(root); err != nil {
		t.Fatalf("load permissions: %v", err)
	}

	deny := sb.mustDecision(t, "Read", map[string]any{"file_path": filepath.Join(root, "secret.txt")})
	if deny.Action != PermissionDeny {
		t.Fatalf("expected deny, got %+v", deny)
	}
	allow := sb.mustDecision(t, "Bash", map[string]any{"command": "ls"})
	if allow.Action != PermissionAllow {
		t.Fatalf("expected allow, got %+v", allow)
	}
	ask := sb.mustDecision(t, "Read", map[string]any{"file_path": filepath.Join(root, "maybe.txt")})
	if ask.Action != PermissionAsk {
		t.Fatalf("expected ask, got %+v", ask)
	}

	logs := sb.PermissionAudits()
	if len(logs) != 3 {
		t.Fatalf("expected 3 audit entries, got %d", len(logs))
	}
}

func TestCheckToolPermissionConcurrent(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	settings := map[string]any{
		"permissions": map[string]any{
			"allow": []string{"Read(**/*.md)"},
		},
	}
	data, err := json.Marshal(settings)
	require.NoError(t, err)
	if err := os.WriteFile(filepath.Join(claudeDir, "settings.json"), data, 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	sb := NewSandbox(root)
	if err := sb.LoadPermissions(root); err != nil {
		t.Fatalf("load permissions: %v", err)
	}

	var (
		wg    sync.WaitGroup
		errCh = make(chan error, 25)
	)
	for i := 0; i < 25; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			res, err := sb.CheckToolPermission("Read", map[string]any{"file_path": filepath.Join(root, "doc.md")})
			if err != nil {
				errCh <- err
				return
			}
			if res.Action != PermissionAllow {
				errCh <- fmt.Errorf("expected allow, got %v", res.Action)
			}
		}()
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		if err != nil {
			t.Fatalf("concurrent check failed: %v", err)
		}
	}

	logs := sb.PermissionAudits()
	if len(logs) != 25 {
		t.Fatalf("expected 25 audit entries, got %d", len(logs))
	}
}

func TestCompilePermissionRuleErrors(t *testing.T) {
	if _, err := compilePermissionRule(" "); err == nil {
		t.Fatalf("expected empty rule error")
	}
	if _, err := compilePermissionRule("Read("); err == nil {
		t.Fatalf("expected malformed rule error")
	}
	if _, err := compilePermissionRule("Bash(regex:["); err == nil {
		t.Fatalf("expected regex compile error")
	}
	rule, err := compilePermissionRule("Read(**/*.md)")
	if err != nil || rule == nil || rule.tool == "" {
		t.Fatalf("expected compiled rule, got %v", err)
	}
	pathRule, err := compilePermissionRule("secrets/**")
	if err != nil || pathRule == nil || pathRule.toolMatch == nil {
		t.Fatalf("expected path rule matcher")
	}
}

// mustDecision is a helper to keep tests concise.
func (s *Sandbox) mustDecision(t *testing.T, tool string, params map[string]any) PermissionDecision {
	t.Helper()
	res, err := s.CheckToolPermission(tool, params)
	if err != nil {
		t.Fatalf("check permission: %v", err)
	}
	return res
}
