package tool

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

const defaultToolOutputThresholdBytes = 64 * 1024

// OutputPersister stores large ToolResult.Output payloads on disk and replaces
// the inline output with a reference string plus OutputRef metadata.
//
// The persisted file layout is:
//
//	/tmp/agentsdk/tool-output/{session_id}/{tool_name}/{timestamp}.output
//
// Callers may override BaseDir and thresholds for tests or custom deployments.
type OutputPersister struct {
	BaseDir               string
	DefaultThresholdBytes int
	PerToolThresholdBytes map[string]int
}

func NewOutputPersister() *OutputPersister {
	return &OutputPersister{
		BaseDir:               toolOutputBaseDir(),
		DefaultThresholdBytes: defaultToolOutputThresholdBytes,
	}
}

func (p *OutputPersister) MaybePersist(call Call, result *ToolResult) error {
	if p == nil || result == nil {
		return nil
	}
	if result.OutputRef != nil {
		return nil
	}
	output := result.Output
	if output == "" {
		return nil
	}

	threshold := p.thresholdFor(call.Name)
	if threshold <= 0 || len(output) <= threshold {
		return nil
	}

	base := strings.TrimSpace(p.BaseDir)
	if base == "" {
		return errors.New("tool output base directory is empty")
	}

	sessionDir := sanitizePathComponent(call.SessionID)
	toolDir := sanitizePathComponent(call.Name)
	dir := filepath.Join(base, sessionDir, toolDir)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}

	f, path, err := createToolOutputFile(dir)
	if err != nil {
		return err
	}

	_, writeErr := f.WriteString(output)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		_ = os.Remove(path)
		return errors.Join(writeErr, closeErr)
	}

	result.Output = formatToolOutputReference(path)
	result.OutputRef = &OutputRef{
		Path:      path,
		SizeBytes: int64(len(output)),
		Truncated: false,
	}
	return nil
}

func (p *OutputPersister) thresholdFor(toolName string) int {
	if p == nil {
		return 0
	}
	canon := strings.ToLower(strings.TrimSpace(toolName))
	if canon != "" && len(p.PerToolThresholdBytes) > 0 {
		if threshold, ok := p.PerToolThresholdBytes[canon]; ok && threshold > 0 {
			return threshold
		}
	}
	if p.DefaultThresholdBytes > 0 {
		return p.DefaultThresholdBytes
	}
	return defaultToolOutputThresholdBytes
}

func toolOutputBaseDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "agentsdk", "tool-output")
	}
	return filepath.Join(string(filepath.Separator), "tmp", "agentsdk", "tool-output")
}

func createToolOutputFile(dir string) (*os.File, string, error) {
	if strings.TrimSpace(dir) == "" {
		return nil, "", errors.New("output directory is empty")
	}

	ts := time.Now().UnixNano()
	for attempts := 0; attempts < 16; attempts++ {
		filename := strconv.FormatInt(ts, 10) + ".output"
		path := filepath.Join(dir, filename)
		f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			if errors.Is(err, os.ErrExist) {
				ts++
				continue
			}
			return nil, "", err
		}
		return f, path, nil
	}
	return nil, "", fmt.Errorf("output file collision under %s", dir)
}

func formatToolOutputReference(path string) string {
	return fmt.Sprintf("[Output saved to: %s]", path)
}

func sanitizePathComponent(value string) string {
	const fallback = "default"
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(trimmed))
	for _, r := range trimmed {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}
