package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	grepResultLimit = 100
	grepMaxDepth    = 8
	grepMaxContext  = 5
	grepToolDesc    = "Search files for regular expression matches within the workspace."
)

var (
	grepSchema = &tool.JSONSchema{
		Type: "object",
		Properties: map[string]interface{}{
			"pattern": map[string]interface{}{
				"type":        "string",
				"description": "Regular expression evaluated per line.",
			},
			"path": map[string]interface{}{
				"type":        "string",
				"description": "File or directory to search (relative to workspace root).",
			},
			"context_lines": map[string]interface{}{
				"type":        "integer",
				"description": fmt.Sprintf("Lines of context to show before/after (0-%d).", grepMaxContext),
			},
		},
		Required: []string{"pattern", "path"},
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
	sandbox    *security.Sandbox
	root       string
	maxResults int
	maxDepth   int
	maxContext int
}

// NewGrepTool builds a GrepTool rooted at the current directory.
func NewGrepTool() *GrepTool { return NewGrepToolWithRoot("") }

// NewGrepToolWithRoot builds a GrepTool rooted at the provided directory.
func NewGrepToolWithRoot(root string) *GrepTool {
	resolved := resolveRoot(root)
	return &GrepTool{
		sandbox:    security.NewSandbox(resolved),
		root:       resolved,
		maxResults: grepResultLimit,
		maxDepth:   grepMaxDepth,
		maxContext: grepMaxContext,
	}
}

// NewGrepToolWithSandbox builds a GrepTool using a custom sandbox.
func NewGrepToolWithSandbox(root string, sandbox *security.Sandbox) *GrepTool {
	resolved := resolveRoot(root)
	return &GrepTool{
		sandbox:    sandbox,
		root:       resolved,
		maxResults: grepResultLimit,
		maxDepth:   grepMaxDepth,
		maxContext: grepMaxContext,
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
	contextLines, err := parseContextLines(params, g.maxContext)
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

	re, err := regexp.Compile(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile pattern: %w", err)
	}

	matches := make([]GrepMatch, 0, minInt(8, g.maxResults))
	var truncated bool
	if info.IsDir() {
		truncated, err = g.searchDirectory(ctx, targetPath, re, contextLines, &matches)
	} else {
		truncated, err = g.searchFile(ctx, targetPath, re, contextLines, &matches)
	}
	if err != nil {
		return nil, err
	}

	return &tool.ToolResult{
		Success: true,
		Output:  formatGrepOutput(matches, truncated),
		Data: map[string]interface{}{
			"pattern":   pattern,
			"path":      displayPath(targetPath, g.root),
			"matches":   matches,
			"count":     len(matches),
			"truncated": truncated,
		},
	}, nil
}

func parseGrepPattern(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["pattern"]
	if !ok {
		return "", errors.New("pattern is required")
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("pattern must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("pattern cannot be empty")
	}
	return value, nil
}

func parseContextLines(params map[string]interface{}, max int) (int, error) {
	if params == nil {
		return 0, nil
	}
	raw, ok := params["context_lines"]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := intFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("context_lines must be integer: %w", err)
	}
	if value < 0 {
		return 0, errors.New("context_lines cannot be negative")
	}
	if value > max {
		return max, nil
	}
	return value, nil
}

func (g *GrepTool) resolveSearchPath(params map[string]interface{}) (string, fs.FileInfo, error) {
	raw, ok := params["path"]
	if !ok {
		return "", nil, errors.New("path is required")
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", nil, fmt.Errorf("path must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil, errors.New("path cannot be empty")
	}
	candidate := value
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(g.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if err := g.sandbox.ValidatePath(candidate); err != nil {
		return "", nil, err
	}
	info, err := os.Stat(candidate)
	if err != nil {
		return "", nil, fmt.Errorf("stat path: %w", err)
	}
	return candidate, info, nil
}

func (g *GrepTool) searchDirectory(ctx context.Context, root string, re *regexp.Regexp, contextLines int, matches *[]GrepMatch) (bool, error) {
	root = filepath.Clean(root)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if d.Type()&fs.ModeSymlink != 0 {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			if relativeDepth(root, path) > g.maxDepth {
				return filepath.SkipDir
			}
			return nil
		}
		truncated, err := g.searchFile(ctx, path, re, contextLines, matches)
		if err != nil {
			return err
		}
		if truncated {
			return errGrepLimitReached
		}
		return nil
	})
	if errors.Is(err, errGrepLimitReached) {
		return true, nil
	}
	if err != nil {
		return false, err
	}
	return false, nil
}

func (g *GrepTool) searchFile(ctx context.Context, path string, re *regexp.Regexp, contextLines int, matches *[]GrepMatch) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	if err := g.sandbox.ValidatePath(path); err != nil {
		return false, err
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return false, fmt.Errorf("read file: %w", err)
	}
	lines := splitGrepLines(string(data))
	display := displayPath(path, g.root)
	for idx, line := range lines {
		if !re.MatchString(line) {
			continue
		}
		match := GrepMatch{
			File:  display,
			Line:  idx + 1,
			Match: line,
		}
		if before, after := surroundingLines(lines, idx, contextLines); len(before) > 0 || len(after) > 0 {
			if len(before) > 0 {
				match.Before = before
			}
			if len(after) > 0 {
				match.After = after
			}
		}
		*matches = append(*matches, match)
		if len(*matches) >= g.maxResults {
			return true, nil
		}
	}
	return false, nil
}

func relativeDepth(base, target string) int {
	if base == target {
		return 0
	}
	rel, err := filepath.Rel(base, target)
	if err != nil {
		return 0
	}
	rel = filepath.Clean(rel)
	if rel == "." || strings.HasPrefix(rel, "..") {
		return 0
	}
	return len(strings.Split(rel, string(filepath.Separator)))
}

func splitGrepLines(contents string) []string {
	if contents == "" {
		return nil
	}
	lines := strings.Split(contents, "\n")
	for i := range lines {
		lines[i] = strings.TrimRight(lines[i], "\r")
	}
	return lines
}

func surroundingLines(lines []string, idx, contextLines int) ([]string, []string) {
	if contextLines <= 0 {
		return nil, nil
	}
	start := idx - contextLines
	if start < 0 {
		start = 0
	}
	before := append([]string(nil), lines[start:idx]...)

	end := idx + contextLines + 1
	if end > len(lines) {
		end = len(lines)
	}
	after := append([]string(nil), lines[idx+1:end]...)
	return before, after
}

func formatGrepOutput(matches []GrepMatch, truncated bool) string {
	if len(matches) == 0 {
		return "no matches"
	}
	var b strings.Builder
	for i, match := range matches {
		if i > 0 {
			b.WriteByte('\n')
		}
		fmt.Fprintf(&b, "%s:%d: %s", match.File, match.Line, match.Match)
		if len(match.Before) > 0 || len(match.After) > 0 {
			if len(match.Before) > 0 {
				for idx, line := range match.Before {
					fmt.Fprintf(&b, "\n  -%d: %s", len(match.Before)-idx, line)
				}
			}
			if len(match.After) > 0 {
				for idx, line := range match.After {
					fmt.Fprintf(&b, "\n  +%d: %s", idx+1, line)
				}
			}
		}
	}
	if truncated {
		fmt.Fprintf(&b, "\n... truncated to %d results", len(matches))
	}
	return b.String()
}

const (
	maxIntValue = int64(1<<(strconv.IntSize-1) - 1)
	minIntValue = -maxIntValue - 1
)

func intFromParam(value interface{}) (int, error) {
	switch v := value.(type) {
	case int:
		return v, nil
	case int8:
		return int(v), nil
	case int16:
		return int(v), nil
	case int32:
		return int(v), nil
	case int64:
		return intFromInt64(v)
	case uint:
		return intFromUint64(uint64(v))
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		return int(v), nil
	case uint64:
		return intFromUint64(v)
	case float64:
		if v > float64(maxIntValue) || v < float64(minIntValue) {
			return 0, fmt.Errorf("value %v is out of range", v)
		}
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return intFromInt64(int64(v))
	case float32:
		f64 := float64(v)
		if f64 > float64(maxIntValue) || f64 < float64(minIntValue) {
			return 0, fmt.Errorf("value %v is out of range", v)
		}
		if v != float32(int64(v)) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return intFromInt64(int64(v))
	case json.Number:
		i, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return intFromInt64(i)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, errors.New("empty string")
		}
		i, err := strconv.Atoi(trimmed)
		if err != nil {
			return 0, err
		}
		return i, nil
	default:
		return 0, fmt.Errorf("unsupported type %T", value)
	}
}

func intFromInt64(v int64) (int, error) {
	if v > maxIntValue || v < minIntValue {
		return 0, fmt.Errorf("value %d is out of range", v)
	}
	return int(v), nil
}

func intFromUint64(v uint64) (int, error) {
	if v > uint64(maxIntValue) {
		return 0, fmt.Errorf("value %d is out of range", v)
	}
	return int(v), nil
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
