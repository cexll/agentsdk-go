package security

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestSandboxAllowAndValidatePath(t *testing.T) {
	root := tempDirClean(t)
	sb := NewSandbox(root)

	if err := sb.ValidatePath(root); err != nil {
		t.Fatalf("validate root: %v", err)
	}

	outside := tempDirClean(t)
	if err := sb.ValidatePath(outside); err == nil {
		t.Fatalf("expected outside path to be rejected")
	}

	sb.Allow(outside)
	if err := sb.ValidatePath(outside); err != nil {
		t.Fatalf("expected allowed path, got %v", err)
	}

	sb.Allow("") // no-op
}

func TestSandboxDisabledSkipsValidation(t *testing.T) {
	sb := NewDisabledSandbox()
	if err := sb.ValidatePath(""); err != nil {
		t.Fatalf("disabled sandbox should ignore validation: %v", err)
	}
	decision, err := sb.CheckToolPermission("bash", nil)
	if err != nil || decision.Action != PermissionAllow {
		t.Fatalf("expected allow decision, got %+v err=%v", decision, err)
	}
}

func TestWithinSandbox(t *testing.T) {
	root := filepath.Clean("/tmp/root")
	if !withinSandbox(root, root) {
		t.Fatalf("expected exact match to be within sandbox")
	}
	if !withinSandbox(filepath.Join(root, "child"), root) {
		t.Fatalf("expected child to be within sandbox")
	}
	if withinSandbox("/other", root) {
		t.Fatalf("unexpected outside path within sandbox")
	}
	if !withinSandbox("/anything", string(filepath.Separator)) {
		t.Fatalf("root prefix should allow everything")
	}
}

func TestEnsurePermissionsLoadedUsesExistingMatcher(t *testing.T) {
	sb := NewSandbox(tempDirClean(t))
	matcher, err := NewPermissionMatcher(&config.PermissionsConfig{})
	if err != nil {
		t.Fatalf("matcher: %v", err)
	}
	sb.permissions = matcher
	sb.permLoaded = false
	sb.permErr = nil

	if err := sb.ensurePermissionsLoaded(); err != nil {
		t.Fatalf("ensure failed: %v", err)
	}
	if !sb.permLoaded {
		t.Fatalf("expected permLoaded true")
	}

	sb.permLoaded = true
	sb.permErr = errors.New("boom")
	if err := sb.ensurePermissionsLoaded(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected stored error, got %v", err)
	}
}

func TestSandboxLoadPermissionsNilReceiver(t *testing.T) {
	var sb *Sandbox
	if err := sb.LoadPermissions(""); err == nil {
		t.Fatalf("expected nil sandbox error")
	}
}

func TestSandboxLoadPermissionsEmptyConfig(t *testing.T) {
	root := tempDirClean(t)
	sb := NewSandbox("")
	if sb == nil {
		t.Fatalf("expected sandbox")
	}
	if err := sb.LoadPermissions(root); err != nil {
		t.Fatalf("expected empty permissions to load, got %v", err)
	}
}
