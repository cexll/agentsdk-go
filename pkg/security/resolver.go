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
	// Keep separator-only input as root for compatibility across platforms/tests.
	if cleanInput == string(filepath.Separator) {
		return cleanInput, nil
	}

	abs, err := filepath.Abs(cleanInput)
	if err != nil {
		return "", fmt.Errorf("security: abs path failed: %w", err)
	}
	clean := filepath.Clean(abs)
	if clean == string(filepath.Separator) {
		return clean, nil
	}

	current, parts := splitPathForWalk(clean)

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

		current = filepath.Join(current, part)

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

func splitPathForWalk(clean string) (string, []string) {
	sep := string(filepath.Separator)
	if !filepath.IsAbs(clean) {
		return "", strings.Split(clean, sep)
	}

	volume := filepath.VolumeName(clean)
	remainder := clean
	current := sep

	if volume != "" {
		remainder = strings.TrimPrefix(remainder, volume)
		current = volume + sep
	}
	remainder = strings.TrimPrefix(remainder, sep)
	if remainder == "" {
		return current, nil
	}
	return current, strings.Split(remainder, sep)
}
