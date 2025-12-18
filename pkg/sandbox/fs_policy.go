package sandbox

import (
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/cexll/agentsdk-go/pkg/security"
)

// FileSystemAllowList enforces path boundaries using PathResolver to block traversal and symlinks.
type FileSystemAllowList struct {
	mu       sync.RWMutex
	allow    []string
	resolver *security.PathResolver
}

// NewFileSystemAllowList initialises a policy rooted at root with optional extra allowed prefixes.
func NewFileSystemAllowList(root string, allow ...string) *FileSystemAllowList {
	resolver := security.NewPathResolver()
	p := &FileSystemAllowList{
		resolver: resolver,
	}
	p.Allow(root)
	for _, path := range allow {
		p.Allow(path)
	}
	return p
}

// Allow registers an additional allowed absolute path prefix.
func (p *FileSystemAllowList) Allow(path string) {
	if p == nil {
		return
	}
	clean := normalize(path)
	if clean == "" {
		return
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	for _, existing := range p.allow {
		if existing == clean {
			return
		}
	}
	p.allow = append(p.allow, clean)
}

// Roots returns a copy of the allowlist.
func (p *FileSystemAllowList) Roots() []string {
	if p == nil {
		return nil
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	out := make([]string, len(p.allow))
	copy(out, p.allow)
	return out
}

// Validate ensures the provided path resolves inside the allowlist without crossing symlinks.
func (p *FileSystemAllowList) Validate(path string) error {
	if p == nil {
		return fmt.Errorf("%w: policy not initialised", ErrPathDenied)
	}
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return fmt.Errorf("%w: empty path", ErrPathDenied)
	}
	resolver := p.resolver
	if resolver == nil {
		resolver = security.NewPathResolver()
	}

	resolved, err := resolver.Resolve(trimmed)
	if err != nil {
		if strings.Contains(strings.ToLower(err.Error()), "symlink") {
			return fmt.Errorf("%w: %w", ErrSymlinkDetected, err)
		}
		return fmt.Errorf("%w: %w", ErrPathDenied, err)
	}

	clean := normalize(resolved)
	p.mu.RLock()
	roots := append([]string(nil), p.allow...)
	p.mu.RUnlock()
	for _, root := range roots {
		if within(clean, root) {
			return nil
		}
	}
	return fmt.Errorf("%w: %s", ErrPathDenied, clean)
}

func normalize(path string) string {
	if strings.TrimSpace(path) == "" {
		return ""
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return filepath.Clean(path)
	}
	return filepath.Clean(abs)
}

func within(path, root string) bool {
	if root == "" {
		return false
	}
	path = filepath.Clean(path)
	root = filepath.Clean(root)
	if path == root {
		return true
	}
	sep := string(filepath.Separator)
	if !strings.HasSuffix(root, sep) {
		root += sep
	}
	return strings.HasPrefix(path+sep, root)
}
