package sandbox

import (
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestFileSystemAllowListValidate(t *testing.T) {
	root := canonicalTempDir(t)
	policy := NewFileSystemAllowList(root)

	inside := filepath.Join(root, "data", "file.txt")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(inside, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := policy.Validate(inside); err != nil {
		t.Fatalf("inside file rejected: %v", err)
	}

	escape := filepath.Join(root, "..", "etc", "passwd")
	if err := policy.Validate(escape); err == nil || !strings.Contains(err.Error(), "path denied") {
		t.Fatalf("expected escape to be blocked, got %v", err)
	}

	if err := policy.Validate("   "); err == nil {
		t.Fatal("expected empty path error")
	}

	if err := policy.Validate(root); err != nil {
		t.Fatalf("root path should be valid: %v", err)
	}

	var nilPolicy *FileSystemAllowList
	if err := nilPolicy.Validate(root); err == nil {
		t.Fatal("nil policy should reject")
	}
}

func TestFileSystemAllowListSymlink(t *testing.T) {
	root := canonicalTempDir(t)
	outside := filepath.Join(canonicalTempDir(t), "secret.txt")
	if err := os.WriteFile(outside, []byte("secret"), 0o600); err != nil {
		t.Fatalf("write outside: %v", err)
	}

	link := filepath.Join(root, "secret-link")
	if err := os.Symlink(outside, link); err != nil {
		if runtime.GOOS == "windows" && (errors.Is(err, os.ErrPermission) || strings.Contains(strings.ToLower(err.Error()), "privilege")) {
			t.Skipf("symlink requires extra privilege on windows: %v", err)
		}
		t.Fatalf("symlink: %v", err)
	}

	policy := NewFileSystemAllowList(root)
	err := policy.Validate(link)
	if err == nil || !strings.Contains(strings.ToLower(err.Error()), "symlink") {
		t.Fatalf("expected symlink rejection, got %v", err)
	}
}

func TestFileSystemAllowListAdditionalRoots(t *testing.T) {
	root := canonicalTempDir(t)
	shared := canonicalTempDir(t)
	policy := NewFileSystemAllowList(root)
	policy.Allow(shared)

	path := filepath.Join(shared, "cache", "data")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte("ok"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := policy.Validate(path); err != nil {
		t.Fatalf("allowed path rejected: %v", err)
	}
}

func TestFileSystemAllowListRootsSnapshot(t *testing.T) {
	root := canonicalTempDir(t)
	policy := NewFileSystemAllowList(root)
	extra := canonicalTempDir(t)
	policy.Allow(extra)
	policy.Allow("   ") // ignored

	roots := policy.Roots()
	if len(roots) != 2 {
		t.Fatalf("unexpected roots length: %d", len(roots))
	}
	roots[0] = "/tamper"
	if policy.Roots()[0] == "/tamper" {
		t.Fatal("Roots should return copy")
	}
}

func TestResourceLimiter(t *testing.T) {
	limiter := NewResourceLimiter(ResourceLimits{MaxCPUPercent: 50, MaxMemoryBytes: 1024, MaxDiskBytes: 2048})
	if err := limiter.Validate(ResourceUsage{CPUPercent: 40, MemoryBytes: 512, DiskBytes: 1024}); err != nil {
		t.Fatalf("unexpected reject: %v", err)
	}
	if err := limiter.Validate(ResourceUsage{CPUPercent: 60}); err == nil {
		t.Fatal("expected cpu limit error")
	}
	if err := limiter.Validate(ResourceUsage{MemoryBytes: 4096}); err == nil {
		t.Fatal("expected memory limit error")
	}
	if err := limiter.Validate(ResourceUsage{DiskBytes: 4096}); err == nil {
		t.Fatal("expected disk limit error")
	}
}

func TestManagerEnforce(t *testing.T) {
	root := canonicalTempDir(t)
	fsPolicy := NewFileSystemAllowList(root)
	netPolicy := NewDomainAllowList("example.com")
	limiter := NewResourceLimiter(ResourceLimits{MaxCPUPercent: 10})

	manager := NewManager(fsPolicy, netPolicy, limiter)

	path := filepath.Join(root, "allowed.txt")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if err := manager.Enforce(path, "example.com", ResourceUsage{CPUPercent: 5}); err != nil {
		t.Fatalf("enforce should pass: %v", err)
	}

	if err := manager.Enforce(path, "other.com", ResourceUsage{CPUPercent: 5}); err == nil {
		t.Fatal("expected network denial")
	}

	if err := manager.Enforce(path, "example.com", ResourceUsage{CPUPercent: 50}); err == nil {
		t.Fatal("expected resource denial")
	}

	if err := manager.Enforce(filepath.Join(root, "..", "escape"), "example.com", ResourceUsage{}); err == nil {
		t.Fatal("expected early failure on invalid path")
	}

	limits := manager.Limits()
	if limits.MaxCPUPercent != 10 {
		t.Fatalf("unexpected limits %+v", limits)
	}

	var nilManager *Manager
	if err := nilManager.CheckPath("/tmp"); err != nil {
		t.Fatalf("nil manager path: %v", err)
	}
	if err := nilManager.CheckNetwork("example.com"); err != nil {
		t.Fatalf("nil manager network: %v", err)
	}
	if err := nilManager.CheckUsage(ResourceUsage{CPUPercent: 100}); err != nil {
		t.Fatalf("nil manager usage: %v", err)
	}
	if (nilManager.Limits() != ResourceLimits{}) {
		t.Fatalf("nil manager limits should be zero")
	}
}

func TestResourceLimiterNilBehaviour(t *testing.T) {
	var limiter *ResourceLimiter
	if err := limiter.Validate(ResourceUsage{CPUPercent: 999}); err != nil {
		t.Fatalf("nil limiter should not enforce: %v", err)
	}
	if (limiter.Limits() != ResourceLimits{}) {
		t.Fatalf("nil limiter limits not zero")
	}
}

func canonicalTempDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != "" {
		return resolved
	}
	return dir
}
