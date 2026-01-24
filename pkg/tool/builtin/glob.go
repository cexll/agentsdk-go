package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/gitignore"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	globResultLimit = 100
	globToolDesc    = `
		- Fast file pattern matching tool that works with any codebase size
		- Supports glob patterns like \"**/*.js\" or \"src/**/*.ts\"
		- Returns matching file paths sorted by modification time
		- Use this tool when you need to find files by name patterns
		- When you are doing an open ended search that may require multiple rounds of globbing and grepping, use the Agent tool instead
		- You can call multiple tools in a single response. It is always better to speculatively perform multiple searches in parallel if they are potentially useful.
	`
)

var globSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"pattern": map[string]interface{}{
			"type":        "string",
			"description": "The glob pattern to match files against",
		},
		"path": map[string]interface{}{
			"type":        "string",
			"description": "The directory to search in. If not specified, the current working directory will be used. IMPORTANT: Omit this field to use the default directory. DO NOT enter \"undefined\" or \"null\" - simply omit it for the default behavior. Must be a valid directory path if provided.",
		},
	},
	Required: []string{"pattern"},
}

// GlobTool looks up files via glob patterns.
type GlobTool struct {
	sandbox          *security.Sandbox
	root             string
	maxResults       int
	respectGitignore bool
	gitignoreMatcher *gitignore.Matcher
}

// NewGlobTool builds a GlobTool rooted at the current directory.
func NewGlobTool() *GlobTool { return NewGlobToolWithRoot("") }

// NewGlobToolWithRoot builds a GlobTool rooted at the provided directory.
func NewGlobToolWithRoot(root string) *GlobTool {
	resolved := resolveRoot(root)
	return &GlobTool{
		sandbox:          security.NewSandbox(resolved),
		root:             resolved,
		maxResults:       globResultLimit,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// NewGlobToolWithSandbox builds a GlobTool using a custom sandbox.
func NewGlobToolWithSandbox(root string, sandbox *security.Sandbox) *GlobTool {
	resolved := resolveRoot(root)
	return &GlobTool{
		sandbox:          sandbox,
		root:             resolved,
		maxResults:       globResultLimit,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// SetRespectGitignore configures whether the tool should respect .gitignore patterns.
func (g *GlobTool) SetRespectGitignore(respect bool) {
	g.respectGitignore = respect
	if respect && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root)
	}
}

func (g *GlobTool) Name() string { return "Glob" }

func (g *GlobTool) Description() string { return globToolDesc }

func (g *GlobTool) Schema() *tool.JSONSchema { return globSchema }

func (g *GlobTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if g == nil || g.sandbox == nil {
		return nil, errors.New("glob tool is not initialised")
	}

	pattern, err := parseGlobPattern(params)
	if err != nil {
		return nil, err
	}
	dir, err := g.resolveDir(params)
	if err != nil {
		return nil, err
	}
	absPattern, err := g.combinePattern(dir, pattern)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	// Initialize gitignore matcher lazily if needed
	if g.respectGitignore && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root)
	}

	matches, err := filepath.Glob(absPattern)
	if err != nil {
		return nil, fmt.Errorf("glob failed: %w", err)
	}

	results := make([]string, 0, len(matches))
	for _, match := range matches {
		clean := filepath.Clean(match)
		if err := g.sandbox.ValidatePath(clean); err != nil {
			return nil, err
		}
		relPath := displayPath(clean, g.root)

		// Filter out gitignored files
		if g.respectGitignore && g.gitignoreMatcher != nil {
			info, statErr := os.Stat(clean)
			isDir := statErr == nil && info.IsDir()
			if g.gitignoreMatcher.Match(relPath, isDir) {
				continue
			}
		}

		results = append(results, relPath)
		if len(results) >= g.maxResults {
			break
		}
	}

	truncated := len(matches) > len(results) || len(results) >= g.maxResults

		return &tool.ToolResult{
			Success: true,
			Output:  formatGlobOutput(results, truncated),
			Data: map[string]interface{}{
				"pattern":   pattern,
				"path":      displayPath(dir, g.root),
				"matches":   results,
				"count":     len(results),
				"truncated": truncated,
			},
		}, nil
}

func parseGlobPattern(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["pattern"]
	if !ok {
		return "", errors.New("pattern is required")
	}
	pattern, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("pattern must be string: %w", err)
	}
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return "", errors.New("pattern cannot be empty")
	}
	return pattern, nil
}

func (g *GlobTool) resolveDir(params map[string]interface{}) (string, error) {
	dir := g.root
	if params != nil {
		if raw, ok := params["path"]; ok && raw != nil {
			value, err := coerceString(raw)
			if err != nil {
				return "", fmt.Errorf("path must be string: %w", err)
			}
			value = strings.TrimSpace(value)
			if value != "" {
				dir = value
			}
		}
	}

	if !filepath.IsAbs(dir) {
		dir = filepath.Join(g.root, dir)
	}
	dir = filepath.Clean(dir)
	if err := g.sandbox.ValidatePath(dir); err != nil {
		return "", err
	}
	info, err := os.Stat(dir)
	if err != nil {
		return "", fmt.Errorf("stat dir: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%s is not a directory", dir)
	}
	return dir, nil
}

func (g *GlobTool) combinePattern(dir, pattern string) (string, error) {
	candidate := pattern
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(dir, candidate)
	}
	candidate = filepath.Clean(candidate)
	parent := filepath.Dir(candidate)
	if err := g.sandbox.ValidatePath(parent); err != nil {
		return "", err
	}
	return candidate, nil
}

func formatGlobOutput(matches []string, truncated bool) string {
	if len(matches) == 0 {
		return "no matches"
	}
	output := strings.Join(matches, "\n")
	if truncated {
		output += fmt.Sprintf("\n... truncated to %d results", len(matches))
	}
	return output
}

func displayPath(path, root string) string {
	cleanPath := filepath.Clean(path)
	cleanRoot := filepath.Clean(root)
	if rel, err := filepath.Rel(cleanRoot, cleanPath); err == nil {
		switch {
		case rel == ".":
			return "."
		case strings.HasPrefix(rel, ".."):
			// Path escaped root; fall back to absolute path.
		default:
			return rel
		}
	}
	return cleanPath
}
