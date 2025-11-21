package toolbuiltin

import (
	"context"
	"errors"
	"fmt"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const writeDescription = `Writes a file to the local filesystem.

Usage:
- This tool will overwrite the existing file if there is one at the provided path.
- If this is an existing file, you MUST use the Read tool first to read the file's contents. This tool will fail if you did not read the file first.
- ALWAYS prefer editing existing files in the codebase. NEVER write new files unless explicitly required.
- NEVER proactively create documentation files (*.md) or README files. Only create documentation files if explicitly requested by the User.
- Only use emojis if the user explicitly requests it. Avoid writing emojis to files unless asked.`

var writeSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"file_path": map[string]interface{}{
			"type":        "string",
			"description": "The absolute path to the file to write (must be absolute, not relative)",
		},
		"content": map[string]interface{}{
			"type":        "string",
			"description": "The content to write to the file",
		},
	},
	Required: []string{"file_path", "content"},
}

// WriteTool writes files within the sandbox root.
type WriteTool struct {
	base *fileSandbox
}

// NewWriteTool builds a WriteTool rooted at the current directory.
func NewWriteTool() *WriteTool {
	return NewWriteToolWithRoot("")
}

// NewWriteToolWithRoot builds a WriteTool rooted at the provided directory.
func NewWriteToolWithRoot(root string) *WriteTool {
	return &WriteTool{base: newFileSandbox(root)}
}

// NewWriteToolWithSandbox builds a WriteTool using a custom sandbox.
func NewWriteToolWithSandbox(root string, sandbox *security.Sandbox) *WriteTool {
	return &WriteTool{base: newFileSandboxWithSandbox(root, sandbox)}
}

func (w *WriteTool) Name() string { return "Write" }

func (w *WriteTool) Description() string { return writeDescription }

func (w *WriteTool) Schema() *tool.JSONSchema { return writeSchema }

func (w *WriteTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if w == nil || w.base == nil || w.base.sandbox == nil {
		return nil, errors.New("write tool is not initialised")
	}
	path, err := w.resolveFilePath(params)
	if err != nil {
		return nil, err
	}
	content, err := w.parseContent(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := w.base.writeFile(path, content); err != nil {
		return nil, err
	}

	return &tool.ToolResult{
		Success: true,
		Output:  fmt.Sprintf("wrote %d bytes to %s", len(content), displayPath(path, w.base.root)),
		Data: map[string]interface{}{
			"path":  displayPath(path, w.base.root),
			"bytes": len(content),
		},
	}, nil
}

func (w *WriteTool) resolveFilePath(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["file_path"]
	if !ok {
		return "", errors.New("file_path is required")
	}
	return w.base.resolvePath(raw)
}

func (w *WriteTool) parseContent(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["content"]
	if !ok {
		return "", errors.New("content is required")
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("content must be string: %w", err)
	}
	return value, nil
}
