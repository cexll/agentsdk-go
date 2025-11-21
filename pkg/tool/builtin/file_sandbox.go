package toolbuiltin

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/security"
)

const defaultMaxFileBytes = 1 << 20 // 1 MiB

// fileSandbox enforces sandboxed filesystem operations shared by file tools.
type fileSandbox struct {
	sandbox  *security.Sandbox
	root     string
	maxBytes int64
}

func newFileSandbox(root string) *fileSandbox {
	resolved := resolveRoot(root)
	return newFileSandboxWithSandbox(resolved, security.NewSandbox(resolved))
}

func newFileSandboxWithSandbox(root string, sandbox *security.Sandbox) *fileSandbox {
	return &fileSandbox{
		sandbox:  sandbox,
		root:     resolveRoot(root),
		maxBytes: defaultMaxFileBytes,
	}
}

func (f *fileSandbox) resolvePath(raw interface{}) (string, error) {
	if f == nil || f.sandbox == nil {
		return "", errors.New("file sandbox is not initialised")
	}
	if raw == nil {
		return "", errors.New("path is required")
	}
	pathStr, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("path must be string: %w", err)
	}
	trimmed := strings.TrimSpace(pathStr)
	if trimmed == "" {
		return "", errors.New("path cannot be empty")
	}
	candidate := trimmed
	if !filepath.IsAbs(candidate) {
		candidate = filepath.Join(f.root, candidate)
	}
	candidate = filepath.Clean(candidate)
	if err := f.sandbox.ValidatePath(candidate); err != nil {
		return "", err
	}
	return candidate, nil
}

func (f *fileSandbox) readFile(path string) (string, error) {
	if f == nil || f.sandbox == nil {
		return "", errors.New("file sandbox is not initialised")
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("stat file: %w", err)
	}
	if info.IsDir() {
		return "", fmt.Errorf("%s is a directory", path)
	}
	if f.maxBytes > 0 && info.Size() > f.maxBytes {
		return "", fmt.Errorf("file exceeds %d bytes limit", f.maxBytes)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	if f.maxBytes > 0 && int64(len(data)) > f.maxBytes {
		return "", fmt.Errorf("file exceeds %d bytes limit", f.maxBytes)
	}
	return string(data), nil
}

func (f *fileSandbox) writeFile(path string, content string) error {
	if f == nil || f.sandbox == nil {
		return errors.New("file sandbox is not initialised")
	}
	data := []byte(content)
	if f.maxBytes > 0 && int64(len(data)) > f.maxBytes {
		return fmt.Errorf("content exceeds %d bytes limit", f.maxBytes)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("ensure directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write file: %w", err)
	}
	return nil
}
