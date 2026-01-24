package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"regexp"

	"github.com/cexll/agentsdk-go/pkg/gitignore"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	grepResultLimit = 100
	grepMaxDepth    = 8
	grepMaxContext  = 5
	grepToolDesc    = `A powerful search tool built on ripgrep.

Usage:
  - ALWAYS use Grep for search tasks. NEVER invoke 'grep' or 'rg' as a Bash command.
  - Supports full regex syntax (e.g., "log.*Error", "function\s+\w+").
  - Filter files with glob (e.g., "*.js", "**/*.tsx") or type (e.g., "js", "py", "rust", "go").
  - Output modes: "files_with_matches" (default), "content", or "count" via output_mode.
  - Context controls: legacy context_lines or -A/-B/-C for after/before/both sides; -n toggles line numbers (default true).
  - Result shaping: head_limit caps results, offset skips initial matches.
  - Multiline matching: set multiline: true for cross-line patterns like 'struct \{[\s\S]*?field'.
  - Use Task tool for open-ended searches requiring multiple rounds.
  - Pattern syntax: Uses ripgrep (not grep) - literal braces need escaping (use 'interface\{\}' to find 'interface{}' in Go code).`
)

var (
	grepSchema = &tool.JSONSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "The regular expression pattern to search for in file contents",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File or directory to search (relative to workspace root).",
			},
			"output_mode": map[string]interface{}{
				"type":        "string",
				"description": "Output format: content | files_with_matches | count.",
				"enum":        []interface{}{"content", "files_with_matches", "count"},
				"default":     "files_with_matches",
			},
			"glob": map[string]interface{}{
				"type":        "string",
				"description": "File glob filter (e.g., *.js, **/*.tsx).",
			},
			"type": map[string]interface{}{
				"type":        "string",
				"description": "File type filter (e.g., js, py, rust, go).",
			},
			"-A": map[string]interface{}{
				"type":        "integer",
				"description": "Show N lines after each match.",
			},
			"-B": map[string]interface{}{
				"type":        "integer",
				"description": "Show N lines before each match.",
			},
			"-C": map[string]interface{}{
				"type":        "integer",
				"description": "Show N lines before and after each match.",
			},
			"context_lines": map[string]interface{}{
				"type":        "integer",
				"description": fmt.Sprintf("Lines of context to show before/after (0-%d).", grepMaxContext),
			},
			"-n": map[string]interface{}{
				"type":        "boolean",
				"description": "Show line numbers.",
				"default":     true,
			},
			"-i": map[string]interface{}{
				"type":        "boolean",
				"description": "Case-insensitive search.",
			},
			"head_limit": map[string]interface{}{
				"type":        "integer",
				"description": fmt.Sprintf("Limit output to first N results (0-%d).", grepResultLimit),
			},
			"offset": map[string]interface{}{
				"type":        "integer",
				"description": "Skip first N results before outputting matches.",
			},
			"multiline": map[string]interface{}{
				"type":        "boolean",
				"description": "Enable multiline regex mode for cross-line patterns.",
			},
		},
		Required: []string{"pattern"},
	}
	errGrepLimitReached = errors.New("grep: result limit reached")
)

// GrepMatch captures a single match along with optional context.
type GrepMatch struct {
	File   string   `json:"file"`
	Line   int      `json:"line"`
	Match  string   `json:"match"`
	Before []string `json:"before,omitempty"`
	After  []string `json:"after,omitempty"`
}

// GrepTool enables scoped code searches.
type GrepTool struct {
	sandbox          *security.Sandbox
	root             string
	maxResults       int
	maxDepth         int
	maxContext       int
	respectGitignore bool
	gitignoreMatcher *gitignore.Matcher
}

// NewGrepTool builds a GrepTool rooted at the current directory.
func NewGrepTool() *GrepTool { return NewGrepToolWithRoot("") }

// NewGrepToolWithRoot builds a GrepTool rooted at the provided directory.
func NewGrepToolWithRoot(root string) *GrepTool {
	resolved := resolveRoot(root)
	return &GrepTool{
		sandbox:          security.NewSandbox(resolved),
		root:             resolved,
		maxResults:       grepResultLimit,
		maxDepth:         grepMaxDepth,
		maxContext:       grepMaxContext,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// NewGrepToolWithSandbox builds a GrepTool using a custom sandbox.
func NewGrepToolWithSandbox(root string, sandbox *security.Sandbox) *GrepTool {
	resolved := resolveRoot(root)
	return &GrepTool{
		sandbox:          sandbox,
		root:             resolved,
		maxResults:       grepResultLimit,
		maxDepth:         grepMaxDepth,
		maxContext:       grepMaxContext,
		respectGitignore: true, // Default to respecting .gitignore
	}
}

// SetRespectGitignore configures whether the tool should respect .gitignore patterns.
func (g *GrepTool) SetRespectGitignore(respect bool) {
	g.respectGitignore = respect
	if respect && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root)
	}
}

func (g *GrepTool) Name() string { return "Grep" }

func (g *GrepTool) Description() string { return grepToolDesc }

func (g *GrepTool) Schema() *tool.JSONSchema { return grepSchema }

func (g *GrepTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if g == nil || g.sandbox == nil {
		return nil, errors.New("grep tool is not initialised")
	}

	pattern, err := parseGrepPattern(params)
	if err != nil {
		return nil, err
	}
	outputMode, err := parseOutputMode(params)
	if err != nil {
		return nil, err
	}
	glob, err := parseGlobFilter(params)
	if err != nil {
		return nil, err
	}
	fileType, err := parseFileTypeFilter(params)
	if err != nil {
		return nil, err
	}
	headLimit, err := parseHeadLimit(params)
	if err != nil {
		return nil, err
	}
	if headLimit > g.maxResults {
		headLimit = g.maxResults
	}
	offset, err := parseOffset(params)
	if err != nil {
		return nil, err
	}
	beforeCtx, afterCtx, err := parseContextParams(params, g.maxContext)
	if err != nil {
		return nil, err
	}
	contextLines, err := parseContextLines(params, g.maxContext)
	if err != nil {
		return nil, err
	}
	if beforeCtx == 0 && afterCtx == 0 {
		beforeCtx = contextLines
		afterCtx = contextLines
	}
	caseInsensitive, _, err := parseBoolParam(params, "-i")
	if err != nil {
		return nil, err
	}
	showLineNumbers, providedLineNumbers, err := parseBoolParam(params, "-n")
	if err != nil {
		return nil, err
	}
	if !providedLineNumbers {
		showLineNumbers = true
	}
	multiline, _, err := parseBoolParam(params, "multiline")
	if err != nil {
		return nil, err
	}

	targetPath, info, err := g.resolveSearchPath(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	patternWithFlags := applyRegexFlags(pattern, caseInsensitive, multiline)

	re, err := regexp.Compile(patternWithFlags)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}

	matches := make([]GrepMatch, 0, minInt(8, g.maxResults))
	searchRoot := targetPath
	if !info.IsDir() {
		searchRoot = filepath.Dir(targetPath)
	}

	// Initialize gitignore matcher lazily if needed
	if g.respectGitignore && g.gitignoreMatcher == nil {
		g.gitignoreMatcher, _ = gitignore.NewMatcher(g.root)
	}

	options := grepSearchOptions{
		before:           beforeCtx,
		after:            afterCtx,
		glob:             glob,
		typeGlobs:        resolveTypeGlobs(fileType),
		root:             searchRoot,
		multiline:        multiline,
		gitignoreMatcher: g.gitignoreMatcher,
	}

	var truncated bool
	if info.IsDir() {
		truncated, err = g.searchDirectory(ctx, targetPath, re, options, &matches)
	} else {
		truncated, err = g.searchFile(ctx, targetPath, re, options, &matches)
	}
	if err != nil {
		return nil, err
	}

	formatted := formatGrepOutput(outputMode, matches, showLineNumbers, headLimit, offset, truncated)
	data := map[string]interface{}{
		"pattern":          pattern,
		"compiled_pattern": patternWithFlags,
		"path":             displayPath(targetPath, g.root),
		"matches":          formatted.matches,
		"count":            len(matches),
		"display_count":    formatted.displayCount,
		"total_matches":    len(matches),
		"output_mode":      outputMode,
		"head_limit":       headLimit,
		"offset":           offset,
		"before_context":   beforeCtx,
		"after_context":    afterCtx,
		"line_numbers":     showLineNumbers,
		"case_insensitive": caseInsensitive,
		"multiline":        multiline,
		"glob":             glob,
		"type":             fileType,
		"truncated":        formatted.truncated,
	}
	if len(formatted.files) > 0 {
		data["files"] = formatted.files
	}
	if len(formatted.counts) > 0 {
		data["counts"] = formatted.counts
	}

	return &tool.ToolResult{
		Success: true,
		Output:  formatted.output,
		Data:    data,
	}, nil
}
