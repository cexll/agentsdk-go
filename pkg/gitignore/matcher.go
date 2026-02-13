// Package gitignore provides gitignore pattern matching functionality.
// It supports parsing .gitignore files and matching paths against patterns.
package gitignore

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
)

// Matcher matches paths against gitignore patterns.
type Matcher struct {
	patterns []pattern
	root     string
}

// pattern represents a single gitignore pattern.
type pattern struct {
	pattern  string
	negate   bool
	dirOnly  bool
	baseName bool // true if pattern contains no slash (matches any directory level)
}

// NewMatcher creates a Matcher by loading all .gitignore files from root up to
// each subdirectory. The root parameter should be the project root directory.
func NewMatcher(root string) (*Matcher, error) {
	root = filepath.Clean(root)
	m := &Matcher{
		root: root,
	}

	// Load root .gitignore
	if err := m.loadGitignore(root, ""); err != nil {
		return nil, err
	}

	// Add default patterns for common directories that should always be ignored
	m.addDefaultPatterns()

	return m, nil
}

// addDefaultPatterns adds common directories that are typically ignored.
func (m *Matcher) addDefaultPatterns() {
	defaults := []string{
		".git",
	}
	for _, p := range defaults {
		m.patterns = append(m.patterns, pattern{
			pattern:  p,
			baseName: true,
		})
	}
}

// LoadNestedGitignore loads a .gitignore file from a subdirectory.
// The relDir is the relative path from root to the directory containing .gitignore.
func (m *Matcher) LoadNestedGitignore(relDir string) error {
	absDir := filepath.Join(m.root, relDir)
	return m.loadGitignore(absDir, relDir)
}

// loadGitignore parses a .gitignore file and adds its patterns.
// relDir is the directory path relative to root (empty for root .gitignore).
func (m *Matcher) loadGitignore(absDir, relDir string) error {
	gitignorePath := filepath.Join(absDir, ".gitignore")
	file, err := os.Open(gitignorePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // No .gitignore is fine
		}
		return err
	}
	defer file.Close()

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Text()
		p, ok := parseLine(line, relDir)
		if ok {
			m.patterns = append(m.patterns, p)
		}
	}
	return scanner.Err()
}

// parseLine parses a single gitignore line and returns a pattern.
// relDir is prepended to relative patterns from nested .gitignore files.
func parseLine(line, relDir string) (pattern, bool) {
	// Trim trailing spaces (unless escaped)
	line = strings.TrimRight(line, " \t")
	if line == "" {
		return pattern{}, false
	}

	// Comment lines
	if strings.HasPrefix(line, "#") {
		return pattern{}, false
	}

	p := pattern{}

	// Negation
	if strings.HasPrefix(line, "!") {
		p.negate = true
		line = line[1:]
	}

	// Trailing slash means directory only
	if strings.HasSuffix(line, "/") {
		p.dirOnly = true
		line = strings.TrimSuffix(line, "/")
	}

	// If pattern doesn't contain a slash (or only trailing slash removed),
	// it matches at any directory level
	if !strings.Contains(line, "/") {
		p.baseName = true
	} else {
		// Leading slash anchors to root
		line = strings.TrimPrefix(line, "/")
		// Prepend relDir for nested .gitignore patterns
		if relDir != "" && !strings.HasPrefix(line, relDir) {
			line = filepath.Join(relDir, line)
		}
	}

	p.pattern = filepath.ToSlash(line)
	return p, true
}

// Match checks if a path should be ignored.
// The path should be relative to the matcher's root.
// isDir indicates whether the path is a directory.
func (m *Matcher) Match(relPath string, isDir bool) bool {
	if relPath == "" || relPath == "." {
		return false
	}

	relPath = filepath.Clean(relPath)
	relPath = filepath.ToSlash(relPath) // Normalize to forward slashes

	// Check if any parent directory is ignored
	parts := strings.Split(relPath, "/")
	for i := 1; i <= len(parts); i++ {
		subPath := strings.Join(parts[:i], "/")
		subIsDir := i < len(parts) || isDir
		ignored := false
		for _, p := range m.patterns {
			if matchPattern(p, subPath, subIsDir) {
				ignored = !p.negate
			}
		}
		if ignored && i < len(parts) {
			// A parent directory is ignored, so this path is also ignored
			return true
		}
		if i == len(parts) {
			return ignored
		}
	}
	return false
}

// matchPattern checks if a path matches a single pattern.
func matchPattern(p pattern, relPath string, isDir bool) bool {
	// Directory-only patterns don't match files
	if p.dirOnly && !isDir {
		return false
	}

	target := relPath
	patternStr := p.pattern

	// For baseName patterns, match against any path component
	if p.baseName {
		// Try matching against the full path first
		if matchGlob(patternStr, target) {
			return true
		}
		// Then try matching against just the base name
		base := filepath.Base(relPath)
		return matchGlob(patternStr, base)
	}

	// For patterns with path separators, match the full relative path
	return matchGlob(patternStr, target) || matchGlobPrefix(patternStr, target)
}

// matchGlob performs glob-style matching with ** support.
func matchGlob(pattern, name string) bool {
	// Handle ** (matches any number of directories)
	if strings.Contains(pattern, "**") {
		return matchDoublestar(pattern, name)
	}

	// Use filepath.Match for simple glob patterns
	matched, err := filepath.Match(pattern, name)
	if err != nil {
		return false
	}
	return matched
}

// matchGlobPrefix checks if any prefix of name matches the pattern.
// This handles cases like pattern "vendor" matching "vendor/foo/bar".
func matchGlobPrefix(pattern, name string) bool {
	parts := strings.Split(name, "/")
	for i := 1; i <= len(parts); i++ {
		prefix := strings.Join(parts[:i], "/")
		if matchGlob(pattern, prefix) {
			return true
		}
	}
	return false
}

// matchDoublestar handles ** glob patterns.
func matchDoublestar(pattern, name string) bool {
	// Split pattern by **
	parts := strings.Split(pattern, "**")
	if len(parts) == 2 {
		prefix := strings.TrimSuffix(parts[0], "/")
		suffix := strings.TrimPrefix(parts[1], "/")

		// **/ at start matches any prefix
		if prefix == "" {
			if suffix == "" {
				return true
			}
			// Match suffix against name or any suffix of name
			nameParts := strings.Split(name, "/")
			for i := 0; i < len(nameParts); i++ {
				subPath := strings.Join(nameParts[i:], "/")
				if matchGlob(suffix, subPath) {
					return true
				}
				// Also try matching just the base
				if matchGlob(suffix, nameParts[len(nameParts)-1]) {
					return true
				}
			}
			return false
		}

		// Prefix/** matches anything under prefix
		if suffix == "" {
			return strings.HasPrefix(name, prefix+"/") || name == prefix
		}

		// prefix/**/suffix
		if !strings.HasPrefix(name, prefix+"/") && name != prefix {
			return false
		}
		remainder := strings.TrimPrefix(name, prefix+"/")
		return matchGlob(suffix, remainder) || strings.HasSuffix(name, "/"+suffix) || strings.HasSuffix(name, suffix)
	}
	return false
}

// ShouldTraverse checks if a directory should be traversed.
// This is an optimization to skip entire directory trees.
func (m *Matcher) ShouldTraverse(relPath string) bool {
	return !m.Match(relPath, true)
}
