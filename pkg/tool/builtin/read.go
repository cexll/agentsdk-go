package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"strconv"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	readDefaultLineLimit = 2000
	readMaxLineLength    = 2000
	readDescription      = `Reads a file from the local filesystem. You can access any file directly by using this tool.
Assume this tool is able to read all files on the machine. If the User provides a path to a file assume that path is valid. It is okay to read a file that does not exist; an error will be returned.

Usage:
- The file_path parameter must be an absolute path, not a relative path
- By default, it reads up to 2000 lines starting from the beginning of the file
- You can optionally specify a line offset and limit (especially handy for long files), but it's recommended to read the whole file by not providing these parameters
- Any lines longer than 2000 characters will be truncated
- Results are returned using cat -n format, with line numbers starting at 1
- This tool allows Agent Code to read images (eg PNG, JPG, etc). When reading an image file the contents are presented visually as Agent Code is a multimodal LLM.
- This tool can read PDF files (.pdf). PDFs are processed page by page, extracting both text and visual content for analysis.
- This tool can read Jupyter notebooks (.ipynb files) and returns all cells with their outputs, combining code, text, and visualizations.
- This tool can only read files, not directories. To read a directory, use an ls command via the Bash tool.
- You can call multiple tools in a single response. It is always better to speculatively read multiple potentially useful files in parallel.
- You will regularly be asked to read screenshots. If the user provides a path to a screenshot, ALWAYS use this tool to view the file at the path. This tool will work with all temporary file paths.
- If you read a file that exists but has empty contents you will receive a system reminder warning in place of file contents.`
)

var readSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"file_path": map[string]interface{}{
			"type":        "string",
			"description": "The absolute path to the file to read",
		},
		"offset": map[string]interface{}{
			"type":        "number",
			"description": "The line number to start reading from. Only provide if the file is too large to read at once",
		},
		"limit": map[string]interface{}{
			"type":        "number",
			"description": "The number of lines to read. Only provide if the file is too large to read at once.",
		},
	},
	Required: []string{"file_path"},
}

// ReadTool streams files with strict sandbox boundaries.
type ReadTool struct {
	base          *fileSandbox
	defaultLimit  int
	maxLineLength int
}

// NewReadTool builds a ReadTool rooted at the current directory.
func NewReadTool() *ReadTool {
	return NewReadToolWithRoot("")
}

// NewReadToolWithRoot builds a ReadTool rooted at the provided directory.
func NewReadToolWithRoot(root string) *ReadTool {
	return &ReadTool{
		base:          newFileSandbox(root),
		defaultLimit:  readDefaultLineLimit,
		maxLineLength: readMaxLineLength,
	}
}

// NewReadToolWithSandbox builds a ReadTool using a custom sandbox.
func NewReadToolWithSandbox(root string, sandbox *security.Sandbox) *ReadTool {
	return &ReadTool{
		base:          newFileSandboxWithSandbox(root, sandbox),
		defaultLimit:  readDefaultLineLimit,
		maxLineLength: readMaxLineLength,
	}
}

func (r *ReadTool) Name() string { return "Read" }

func (r *ReadTool) Description() string { return readDescription }

func (r *ReadTool) Schema() *tool.JSONSchema { return readSchema }

func (r *ReadTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if r == nil || r.base == nil || r.base.sandbox == nil {
		return nil, errors.New("read tool is not initialised")
	}
	path, err := r.resolveFilePath(params)
	if err != nil {
		return nil, err
	}
	offset, err := r.parseOffset(params)
	if err != nil {
		return nil, err
	}
	limit, err := r.parseLimit(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	content, err := r.base.readFile(path)
	if err != nil {
		return nil, err
	}

	lines := splitFileLines(content)
	totalLines := len(lines)
	formatted, returned, truncatedLineCount, truncated := r.formatLines(lines, offset, limit)
	if returned == 0 {
		message := fmt.Sprintf("no content in requested range (file has %d lines)", totalLines)
		return &tool.ToolResult{
			Success: true,
			Output:  message,
			Data: map[string]interface{}{
				"path":              displayPath(path, r.base.root),
				"offset":            offset,
				"limit":             limit,
				"total_lines":       totalLines,
				"returned_lines":    returned,
				"line_truncations":  truncatedLineCount,
				"truncated":         true,
				"range_out_of_file": true,
			},
		}, nil
	}

	return &tool.ToolResult{
		Success: true,
		Output:  formatted,
		Data: map[string]interface{}{
			"path":             displayPath(path, r.base.root),
			"offset":           offset,
			"limit":            limit,
			"total_lines":      totalLines,
			"returned_lines":   returned,
			"line_truncations": truncatedLineCount,
			"truncated":        truncated,
		},
	}, nil
}

func (r *ReadTool) resolveFilePath(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["file_path"]
	if !ok {
		return "", errors.New("file_path is required")
	}
	return r.base.resolvePath(raw)
}

func (r *ReadTool) parseOffset(params map[string]interface{}) (int, error) {
	value, err := parseLineNumber(params, "offset")
	if err != nil {
		return 0, err
	}
	if value == 0 {
		return 1, nil
	}
	if value < 0 {
		return 0, errors.New("offset must be >= 1")
	}
	return value, nil
}

func (r *ReadTool) parseLimit(params map[string]interface{}) (int, error) {
	value, err := parseLineNumber(params, "limit")
	if err != nil {
		return 0, err
	}
	switch {
	case value <= 0:
		return r.defaultLimit, nil
	default:
		return value, nil
	}
}

func (r *ReadTool) formatLines(lines []string, offset, limit int) (string, int, int, bool) {
	if len(lines) == 0 {
		return "", 0, 0, false
	}
	start := offset - 1
	if start < 0 {
		start = 0
	}
	if start >= len(lines) {
		return "", 0, 0, false
	}
	if limit <= 0 || limit > len(lines)-start {
		limit = len(lines) - start
	}

	var b strings.Builder
	returned := 0
	truncatedLines := 0
	truncated := start > 0 || start+limit < len(lines)

	for i := start; i < start+limit; i++ {
		lineNumber := i + 1
		formatted, lineTruncated := r.applyLineTruncation(strings.TrimRight(lines[i], "\r"))
		if lineTruncated {
			truncatedLines++
			truncated = true
		}
		fmt.Fprintf(&b, "%6d\t%s", lineNumber, formatted)
		returned++
		if i < start+limit-1 {
			b.WriteByte('\n')
		}
	}
	return b.String(), returned, truncatedLines, truncated
}

func (r *ReadTool) applyLineTruncation(line string) (string, bool) {
	if r.maxLineLength <= 0 || len(line) <= r.maxLineLength {
		return line, false
	}
	suffix := " ...(truncated)"
	cutoff := r.maxLineLength - len(suffix)
	if cutoff < 0 {
		cutoff = 0
	}
	return line[:cutoff] + suffix, true
}

func splitFileLines(content string) []string {
	if content == "" {
		return nil
	}
	return strings.Split(content, "\n")
}

func parseLineNumber(params map[string]interface{}, key string) (int, error) {
	if params == nil {
		return 0, nil
	}
	raw, ok := params[key]
	if !ok || raw == nil {
		return 0, nil
	}
	value, err := coerceInt(raw)
	if err != nil {
		return 0, fmt.Errorf("%s must be a number: %w", key, err)
	}
	return value, nil
}

func coerceInt(value interface{}) (int, error) {
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
		if v > math.MaxInt || v < math.MinInt {
			return 0, fmt.Errorf("value %d out of int range", v)
		}
		return int(v), nil
	case uint:
		if uint64(v) > uint64(math.MaxInt) {
			return 0, fmt.Errorf("value %d out of int range", v)
		}
		return int(v), nil
	case uint8:
		return int(v), nil
	case uint16:
		return int(v), nil
	case uint32:
		if uint64(v) > uint64(math.MaxInt) {
			return 0, fmt.Errorf("value %d out of int range", v)
		}
		return int(v), nil
	case uint64:
		if v > uint64(math.MaxInt) {
			return 0, fmt.Errorf("value %d out of int range", v)
		}
		return int(v), nil
	case float32:
		if math.Trunc(float64(v)) != float64(v) {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return int(v), nil
	case float64:
		if math.Trunc(v) != v {
			return 0, fmt.Errorf("value %v is not an integer", v)
		}
		return int(v), nil
	case json.Number:
		if strings.Contains(v.String(), ".") {
			f, err := v.Float64()
			if err != nil {
				return 0, err
			}
			if math.Trunc(f) != f {
				return 0, fmt.Errorf("value %v is not an integer", f)
			}
			return int(f), nil
		}
		i, err := v.Int64()
		if err != nil {
			return 0, err
		}
		return coerceInt(i)
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
