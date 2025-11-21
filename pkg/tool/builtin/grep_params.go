package toolbuiltin

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

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

func parseOutputMode(params map[string]interface{}) (string, error) {
	const defaultMode = "files_with_matches"
	if params == nil {
		return defaultMode, nil
	}
	raw, ok := params["output_mode"]
	if !ok || raw == nil {
		return defaultMode, nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("output_mode must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("output_mode cannot be empty")
	}
	switch value {
	case "content", "files_with_matches", "count":
		return value, nil
	default:
		return "", errors.New("output_mode must be one of content, files_with_matches, count")
	}
}

func parseBoolParam(params map[string]interface{}, key string) (bool, bool, error) {
	if params == nil {
		return false, false, nil
	}
	raw, ok := params[key]
	if !ok || raw == nil {
		return false, false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, true, nil
	case string:
		s := strings.TrimSpace(strings.ToLower(v))
		switch s {
		case "true":
			return true, true, nil
		case "false":
			return false, true, nil
		default:
			return false, true, fmt.Errorf("%s must be \"true\" or \"false\"", key)
		}
	default:
		return false, true, fmt.Errorf("%s must be boolean", key)
	}
}

func parseGlobFilter(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", nil
	}
	raw, ok := params["glob"]
	if !ok || raw == nil {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("glob must be string: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func parseFileTypeFilter(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", nil
	}
	raw, ok := params["type"]
	if !ok || raw == nil {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("type must be string: %w", err)
	}
	return strings.TrimSpace(value), nil
}

func parseHeadLimit(params map[string]interface{}) (int, error) {
	if params == nil {
		return 0, nil
	}
	raw, ok := params["head_limit"]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := intFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("head_limit must be integer: %w", err)
	}
	if value < 0 {
		return 0, errors.New("head_limit cannot be negative")
	}
	return value, nil
}

func parseOffset(params map[string]interface{}) (int, error) {
	if params == nil {
		return 0, nil
	}
	raw, ok := params["offset"]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := intFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("offset must be integer: %w", err)
	}
	if value < 0 {
		return 0, errors.New("offset cannot be negative")
	}
	return value, nil
}

func applyRegexFlags(pattern string, caseInsensitive, multiline bool) string {
	var flags strings.Builder
	if caseInsensitive {
		flags.WriteByte('i')
	}
	if multiline {
		flags.WriteByte('s')
	}
	if flags.Len() == 0 {
		return pattern
	}
	return "(?" + flags.String() + ")" + pattern
}

func resolveTypeGlobs(fileType string) []string {
	if fileType == "" {
		return nil
	}
	switch strings.ToLower(fileType) {
	case "js":
		return []string{"*.js", "*.mjs", "*.cjs"}
	case "ts":
		return []string{"*.ts"}
	case "tsx":
		return []string{"*.tsx"}
	case "jsx":
		return []string{"*.jsx"}
	case "py", "python":
		return []string{"*.py"}
	case "go", "golang":
		return []string{"*.go"}
	case "rust", "rs":
		return []string{"*.rs"}
	case "java":
		return []string{"*.java"}
	case "c":
		return []string{"*.c"}
	case "cpp", "cc", "cxx", "c++":
		return []string{"*.cpp", "*.cc", "*.cxx", "*.c++"}
	case "h":
		return []string{"*.h"}
	case "hpp", "hh", "hxx":
		return []string{"*.hpp", "*.hh", "*.hxx"}
	case "rb", "ruby":
		return []string{"*.rb"}
	case "php":
		return []string{"*.php"}
	case "cs":
		return []string{"*.cs"}
	case "swift":
		return []string{"*.swift"}
	case "sh", "bash":
		return []string{"*.sh"}
	case "kt", "kotlin":
		return []string{"*.kt", "*.kts"}
	default:
		return []string{"*." + strings.ToLower(fileType)}
	}
}

func parseContextParams(params map[string]interface{}, maxContext int) (int, int, error) {
	if params == nil {
		return 0, 0, nil
	}
	if raw, ok := params["-C"]; ok && raw != nil {
		value, err := intFromParam(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("-C must be integer: %w", err)
		}
		if value < 0 {
			return 0, 0, errors.New("-C cannot be negative")
		}
		if value > maxContext {
			value = maxContext
		}
		return value, value, nil
	}

	before := 0
	after := 0

	if raw, ok := params["-A"]; ok && raw != nil {
		value, err := intFromParam(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("-A must be integer: %w", err)
		}
		if value < 0 {
			return 0, 0, errors.New("-A cannot be negative")
		}
		if value > maxContext {
			value = maxContext
		}
		after = value
	}

	if raw, ok := params["-B"]; ok && raw != nil {
		value, err := intFromParam(raw)
		if err != nil {
			return 0, 0, fmt.Errorf("-B must be integer: %w", err)
		}
		if value < 0 {
			return 0, 0, errors.New("-B cannot be negative")
		}
		if value > maxContext {
			value = maxContext
		}
		before = value
	}

	return before, after, nil
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
