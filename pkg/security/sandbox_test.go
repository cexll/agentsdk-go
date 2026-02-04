package security

import (
	"os"
	"path/filepath"
	"testing"
)

func TestSandboxLoadPermissions(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	settings := `{"permissions":{"allow":["Bash(ls:*)"],"deny":["Read(secret)"]}}`
	path := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(settings), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	s := NewSandbox(root)
	if err := s.LoadPermissions(root); err != nil {
		t.Fatalf("load permissions failed: %v", err)
	}
	decision, err := s.CheckToolPermission("Bash", map[string]any{"command": "ls"})
	if err != nil {
		t.Fatalf("check permission failed: %v", err)
	}
	if decision.Action != PermissionAllow {
		t.Fatalf("expected allow, got %v", decision.Action)
	}
}

func TestSandboxLoadPermissionsInvalid(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	path := filepath.Join(root, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("{bad"), 0o600); err != nil {
		t.Fatalf("write settings: %v", err)
	}

	s := NewSandbox(root)
	if err := s.LoadPermissions(root); err == nil {
		t.Fatalf("expected load error")
	}
}
