package security

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const defaultMaxDepth = 128

// PathResolver is the third low-level guard: it refuses symlink escapes.
type PathResolver struct {
	maxDepth int
}

// NewPathResolver creates a resolver that forbids excessive nesting and symlinks.
func NewPathResolver() *PathResolver {
	return &PathResolver{maxDepth: defaultMaxDepth}
}

// Resolve canonicalises a path and rejects symlinks along the way.
func (r *PathResolver) Resolve(path string) (string, error) {
	cleanInput := strings.TrimSpace(path)
	if cleanInput == "" {
		return "", fmt.Errorf("security: empty path")
	}

	abs, err := filepath.Abs(cleanInput)
	if err != nil {
		return "", fmt.Errorf("security: abs path failed: %w", err)
	}
	clean := filepath.Clean(abs)
	if clean == string(filepath.Separator) {
		return clean, nil
	}

	parts := strings.Split(clean, string(filepath.Separator))
	var current string
	if filepath.IsAbs(clean) {
		current = string(filepath.Separator)
	}

	depth := 0
	for _, part := range parts {
		if part == "" || part == "." {
			continue
		}
		if part == ".." {
			return "", fmt.Errorf("security: parent traversal detected in %q", path)
		}
		depth++
		if r.maxDepth > 0 && depth > r.maxDepth {
			return "", fmt.Errorf("security: path exceeds max depth %d", r.maxDepth)
		}

		if current == "" || current == string(filepath.Separator) {
			current = filepath.Join(current, part)
		} else {
			current = filepath.Join(current, part)
		}

		if err := ensureNoSymlink(current); err != nil {
			return "", err
		}
	}

	if current == "" {
		current = clean
	}
	return current, nil
}

func ensureNoSymlink(path string) error {
	info, err := os.Lstat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("security: lstat failed for %s: %w", path, err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("security: symlink rejected %s", path)
	}
	return openNoFollow(path)
}
