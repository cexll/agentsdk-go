package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const editDescription = `Performs exact string replacements in files. 

Usage:
- You must use your Read tool at least once in the conversation before editing. This tool will error if you attempt an edit without reading the file. 
- When editing text from Read tool output, ensure you preserve the exact indentation (tabs/spaces) as it appears AFTER the line number prefix. The line number prefix format is: spaces + line number + tab. Everything after that tab is the actual file content to match. Never include any part of the line number prefix in the old_string or new_string.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- Only use emojis if the user explicitly requests it. Avoid adding emojis to files unless asked.
- The edit will FAIL if old_string is not unique in the file. Either provide a larger string with more surrounding context to make it unique or use replace_all to change every instance of old_string. 
- Use replace_all for replacing and renaming strings across the file. This parameter is useful if you want to rename a variable for instance.`

var editSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"file_path": map[string]interface{}{
			"type":        "string",
			"description": "The absolute path to the file to modify",
		},
		"old_string": map[string]interface{}{
			"type":        "string",
			"description": "The text to replace",
		},
		"new_string": map[string]interface{}{
			"type":        "string",
			"description": "The text to replace it with (must be different from old_string)",
		},
		"replace_all": map[string]interface{}{
			"type":        "boolean",
			"default":     false,
			"description": "Replace all occurences of old_string (default false)",
		},
	},
	Required: []string{"file_path", "old_string", "new_string"},
}

// EditTool applies safe in-place replacements.
type EditTool struct {
	base *fileSandbox
}

// NewEditTool builds an EditTool rooted at the current directory.
func NewEditTool() *EditTool {
	return NewEditToolWithRoot("")
}

// NewEditToolWithRoot builds an EditTool rooted at the provided directory.
func NewEditToolWithRoot(root string) *EditTool {
	return &EditTool{base: newFileSandbox(root)}
}

// NewEditToolWithSandbox builds an EditTool using a custom sandbox.
func NewEditToolWithSandbox(root string, sandbox *security.Sandbox) *EditTool {
	return &EditTool{base: newFileSandboxWithSandbox(root, sandbox)}
}

func (e *EditTool) Name() string { return "Edit" }

func (e *EditTool) Description() string { return editDescription }

func (e *EditTool) Schema() *tool.JSONSchema { return editSchema }

func (e *EditTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if e == nil || e.base == nil || e.base.sandbox == nil {
		return nil, errors.New("edit tool is not initialised")
	}
	path, err := e.resolveFilePath(params)
	if err != nil {
		return nil, err
	}
	oldString, err := e.parseRequiredString(params, "old_string")
	if err != nil {
		return nil, err
	}
	if oldString == "" {
		return nil, errors.New("old_string cannot be empty")
	}
	newString, err := e.parseRequiredString(params, "new_string")
	if err != nil {
		return nil, err
	}
	if oldString == newString {
		return nil, errors.New("new_string must differ from old_string")
	}
	replaceAll, err := e.parseReplaceAll(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	info, err := os.Stat(path)
	if err != nil {
		return nil, fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return nil, fmt.Errorf("%s is a directory", path)
	}

	content, err := e.base.readFile(path)
	if err != nil {
		return nil, err
	}

	matches := strings.Count(content, oldString)
	if matches == 0 {
		return nil, fmt.Errorf("old_string not found in %s", displayPath(path, e.base.root))
	}
	if !replaceAll && matches != 1 {
		return nil, fmt.Errorf("old_string must be unique when replace_all is false (found %d matches)", matches)
	}

	replacements := matches
	var updated string
	if replaceAll {
		updated = strings.ReplaceAll(content, oldString, newString)
	} else {
		updated = strings.Replace(content, oldString, newString, 1)
		replacements = 1
	}

	if e.base.maxBytes > 0 && int64(len(updated)) > e.base.maxBytes {
		return nil, fmt.Errorf("edited content exceeds %d bytes limit", e.base.maxBytes)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := os.WriteFile(path, []byte(updated), info.Mode()); err != nil {
		return nil, fmt.Errorf("write file: %w", err)
	}

	return &tool.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("applied %d replacement(s)", replacements),
		Data: map[string]interface{}{
			"path":        displayPath(path, e.base.root),
			"matches":     matches,
			"replaced":    replacements,
			"replace_all": replaceAll,
		},
	}, nil
}

func (e *EditTool) resolveFilePath(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["file_path"]
	if !ok {
		return "", errors.New("file_path is required")
	}
	return e.base.resolvePath(raw)
}

func (e *EditTool) parseRequiredString(params map[string]interface{}, key string) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be string: %w", key, err)
	}
	return value, nil
}

func (e *EditTool) parseReplaceAll(params map[string]interface{}) (bool, error) {
	if params == nil {
		return false, nil
	}
	raw, ok := params["replace_all"]
	if !ok || raw == nil {
		return false, nil
	}
	value, err := coerceBool(raw)
	if err != nil {
		return false, fmt.Errorf("replace_all must be boolean: %w", err)
	}
	return value, nil
}

func coerceBool(value interface{}) (bool, error) {
	switch v := value.(type) {
	case bool:
		return v, nil
	case string:
		trimmed := strings.TrimSpace(strings.ToLower(v))
		switch trimmed {
		case "true", "1", "yes", "y":
			return true, nil
		case "false", "0", "no", "n":
			return false, nil
		case "":
			return false, errors.New("empty string")
		default:
			return false, fmt.Errorf("invalid boolean value %q", v)
		}
	case int:
		return v != 0, nil
	case int32:
		return v != 0, nil
	case int64:
		return v != 0, nil
	case uint:
		return v != 0, nil
	case uint32:
		return v != 0, nil
	case uint64:
		return v != 0, nil
	default:
		return false, fmt.Errorf("unsupported bool type %T", value)
	}
}
