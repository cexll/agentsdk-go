package toolbuiltin

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	defaultBashTimeout = 10 * time.Minute
	maxBashTimeout     = 60 * time.Minute
	maxBashOutputLen   = 30000
	bashDescript       = `
	# Bash Tool Documentation

	Executes bash commands in a persistent shell session with optional timeout, ensuring proper handling and security measures.

	**IMPORTANT**: This tool is for terminal operations like git, npm, docker, etc. DO NOT use it for file operations (reading, writing, editing, searching, finding files) - use specialized tools instead.

	## Pre-Execution Steps

	### 1. Directory Verification
	- If creating new directories/files, first use 'ls' to verify the parent directory exists
	- Example: Before 'mkdir foo/bar', run 'ls foo' to check "foo" exists

	### 2. Command Execution
	- Always quote file paths with spaces using double quotes
	- Examples:
	- ✅ 'cd "/Users/name/My Documents"'
	- ❌ 'cd /Users/name/My Documents'
	- ✅ 'python "/path/with spaces/script.py"'
	- ❌ 'python /path/with spaces/script.py'

	## Usage Notes

	- **Required**: command argument
	- **Optional**: timeout in milliseconds (max 600000ms/10 min, default 120000ms/2 min)
	- **Description**: Write clear 5-10 word description of command purpose
	- **Output limit**: Saved to disk if exceeds 30000 characters
	- **Async execution**: Set 'async=true' for long-running tasks (dev servers, log tailing). Use BashStatus with task_id to poll status (no output consumption), BashOutput with task_id to poll output, and KillTask to stop.

	## Command Preferences

	Avoid using Bash for these operations - use dedicated tools instead:
	- File search → Use **Glob** (NOT find/ls)
	- Content search → Use **Grep** (NOT grep/rg)
	- Read files → Use **Read** (NOT cat/head/tail)
	- Edit files → Use **Edit** (NOT sed/awk)
	- Write files → Use **Write** (NOT echo >/cat <<EOF)
	- Communication → Output text directly (NOT echo/printf)

	## Multiple Commands

	- **Parallel (independent)**: Make multiple Bash tool calls in single message
	- **Sequential (dependent)**: Chain with '&&' (e.g., 'git add . && git commit -m "message" && git push')
	- **Sequential (ignore failures)**: Use ';'
	- **DO NOT**: Use newlines to separate commands (except in quoted strings)

	## Working Directory

	Maintain current directory by using absolute paths and avoiding 'cd':
	- ✅ 'pytest /foo/bar/tests'
	- ❌ 'cd /foo/bar && pytest tests'

	---

	## Git Commit Protocol

	**Only create commits when explicitly requested by user.**

	### Git Safety Rules
	- ❌ NEVER update git config
	- ❌ NEVER run destructive commands (push --force, hard reset) unless explicitly requested
	- ❌ NEVER skip hooks (--no-verify, --no-gpg-sign) unless explicitly requested
	- ❌ NEVER force push to main/master (warn user if requested)
	- ⚠️ Avoid 'git commit --amend' (only use when: user explicitly requests OR adding pre-commit hook edits)
	- ✅ Before amending: ALWAYS check authorship ('git log -1 --format='%an %ae'')
	- ⚠️ NEVER commit unless explicitly asked

	### Commit Steps

	**1. Gather information (parallel)**
	'''bash
	git status
	git diff
	git log
	'''

	**2. Analyze and draft**
	- Summarize change nature (feature/enhancement/fix/refactor/test/docs)
	- Don't commit secret files (.env, credentials.json) - warn user
	- Draft concise 1-2 sentence message focusing on "why" not "what"

	**3. Execute commit (sequential where needed)**
	'''bash
	git add [files]
	git commit -m "$(cat <<'EOF'
	Commit message here.
	EOF
	)"
	git status  # Verify success
	'''

	**4. Handle pre-commit hook failures**
	- Retry ONCE if commit fails
	- If files modified by hook, verify safe to amend:
	- Check authorship: 'git log -1 --format='%an %ae''
	- Check not pushed: 'git status' shows "Your branch is ahead"
	- If both true → amend; otherwise → create NEW commit

	### Important Notes
	- ❌ NEVER run additional code exploration commands
	- ❌ NEVER use TodoWrite or Task tools
	- ❌ DO NOT push unless explicitly asked
	- ❌ NEVER use '-i' flag (interactive not supported)
	- ⚠️ Don't create empty commits if no changes
	- ✅ ALWAYS use HEREDOC for commit messages

	---

	## Pull Request Protocol

	Use 'gh' command via Bash tool for ALL GitHub tasks (issues, PRs, checks, releases).

	### PR Creation Steps

	**1. Understand branch state (parallel)**
	'''bash
	git status
	git diff
	git log
	git diff [base-branch]...HEAD
	'''
	Check if branch tracks remote and is up to date.

	**2. Analyze and draft**
	Review ALL commits (not just latest) that will be included in PR.

	**3. Create PR (parallel where possible)**
	'''bash
	# Create branch if needed
	# Push with -u flag if needed
	gh pr create --title "the pr title" --body "$(cat <<'EOF'
	## Summary
	<1-3 bullet points>

	## Test plan
	[Bulleted markdown checklist of TODOs for testing the pull request...]
	EOF
	)"
	'''

	### Important Notes
	- ❌ DO NOT use TodoWrite or Task tools
	- ✅ Return PR URL when done

	---

	## Other Common Operations

	**View PR comments:**
	'''bash
	gh api repos/foo/bar/pulls/123/comments
	'''
	`
)

var bashSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"command": map[string]interface{}{
			"type":        "string",
			"description": "Command string executed via bash without shell metacharacters.",
		},
		"timeout": map[string]interface{}{
			"type":        "number",
			"description": "Optional timeout in seconds (defaults to 30, caps at 120).",
		},
		"workdir": map[string]interface{}{
			"type":        "string",
			"description": "Optional working directory relative to the sandbox root.",
		},
		"async": map[string]interface{}{
			"type":        "boolean",
			"description": "Run command asynchronously and return a task_id immediately.",
		},
		"task_id": map[string]interface{}{
			"type":        "string",
			"description": "Optional async task id to use when async=true.",
		},
	},
	Required: []string{"command"},
}

// BashTool executes validated commands using bash within a sandbox.
type BashTool struct {
	sandbox *security.Sandbox
	root    string
	timeout time.Duration

	outputThresholdBytes int
}

// NewBashTool builds a BashTool rooted at the current directory.
func NewBashTool() *BashTool {
	return NewBashToolWithRoot("")
}

// NewBashToolWithRoot builds a BashTool rooted at the provided directory.
func NewBashToolWithRoot(root string) *BashTool {
	resolved := resolveRoot(root)
	return &BashTool{
		sandbox: security.NewSandbox(resolved),
		root:    resolved,
		timeout: defaultBashTimeout,

		outputThresholdBytes: maxBashOutputLen,
	}
}

// NewBashToolWithSandbox builds a BashTool with a custom sandbox.
// Used when sandbox needs to be pre-configured (e.g., disabled mode).
func NewBashToolWithSandbox(root string, sandbox *security.Sandbox) *BashTool {
	resolved := resolveRoot(root)
	return &BashTool{
		sandbox: sandbox,
		root:    resolved,
		timeout: defaultBashTimeout,

		outputThresholdBytes: maxBashOutputLen,
	}
}

// SetOutputThresholdBytes controls when output is spooled to disk.
func (b *BashTool) SetOutputThresholdBytes(threshold int) {
	if b == nil {
		return
	}
	b.outputThresholdBytes = threshold
}

func (b *BashTool) effectiveOutputThresholdBytes() int {
	if b == nil || b.outputThresholdBytes <= 0 {
		return maxBashOutputLen
	}
	return b.outputThresholdBytes
}

// AllowShellMetachars enables shell pipes and metacharacters (CLI mode).
func (b *BashTool) AllowShellMetachars(allow bool) {
	if b != nil && b.sandbox != nil {
		b.sandbox.AllowShellMetachars(allow)
	}
}

func (b *BashTool) Name() string { return "Bash" }

func (b *BashTool) Description() string {
	return "Execute validated bash commands inside the agent workspace."
}

func (b *BashTool) Schema() *tool.JSONSchema { return bashSchema }

func (b *BashTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	if b == nil || b.sandbox == nil {
		return nil, errors.New("bash tool is not initialised")
	}
	async, err := parseAsyncFlag(params)
	if err != nil {
		return nil, err
	}
	command, err := extractCommand(params)
	if err != nil {
		return nil, err
	}
	if err := b.sandbox.ValidateCommand(command); err != nil {
		return nil, err
	}
	workdir, err := b.resolveWorkdir(params)
	if err != nil {
		return nil, err
	}
	timeout, err := b.resolveTimeout(params)
	if err != nil {
		return nil, err
	}

	if async {
		id, err := optionalAsyncTaskID(params)
		if err != nil {
			return nil, err
		}
		if id == "" {
			id = generateAsyncTaskID()
		}
		if err := DefaultAsyncTaskManager().startWithContext(ctx, id, command, workdir, timeout); err != nil {
			return nil, err
		}
		payload := map[string]interface{}{
			"task_id": id,
			"status":  "running",
		}
		out, _ := json.Marshal(payload)
		return &tool.ToolResult{Success: true, Output: string(out), Data: payload}, nil
	}

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Env = os.Environ()
	cmd.Dir = workdir

	spool := newBashOutputSpool(ctx, b.effectiveOutputThresholdBytes())
	cmd.Stdout = spool.StdoutWriter()
	cmd.Stderr = spool.StderrWriter()

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	output, outputFile, spoolErr := spool.Finalize()

	data := map[string]interface{}{
		"workdir":     workdir,
		"duration_ms": duration.Milliseconds(),
		"timeout_ms":  timeout.Milliseconds(),
	}
	if outputFile != "" {
		data["output_file"] = outputFile
	}
	if spoolErr != nil {
		data["spool_error"] = spoolErr.Error()
	}

	result := &tool.ToolResult{
		Success: runErr == nil,
		Output:  output,
		Data:    data,
	}

	if runErr != nil {
		if errors.Is(execCtx.Err(), context.DeadlineExceeded) {
			return result, fmt.Errorf("command timeout after %s", timeout)
		}
		return result, fmt.Errorf("command failed: %w", runErr)
	}
	return result, nil
}

func (b *BashTool) resolveWorkdir(params map[string]interface{}) (string, error) {
	dir := b.root
	if raw, ok := params["workdir"]; ok && raw != nil {
		value, err := coerceString(raw)
		if err != nil {
			return "", fmt.Errorf("workdir must be string: %w", err)
		}
		value = strings.TrimSpace(value)
		if value != "" {
			dir = value
		}
	}
	if !filepath.IsAbs(dir) {
		dir = filepath.Join(b.root, dir)
	}
	dir = filepath.Clean(dir)
	return b.ensureDirectory(dir)
}

func (b *BashTool) ensureDirectory(path string) (string, error) {
	if err := b.sandbox.ValidatePath(path); err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", fmt.Errorf("workdir stat: %w", err)
	}
	if !info.IsDir() {
		return "", fmt.Errorf("workdir %s is not a directory", path)
	}
	return path, nil
}

func (b *BashTool) resolveTimeout(params map[string]interface{}) (time.Duration, error) {
	timeout := b.timeout
	raw, ok := params["timeout"]
	if !ok || raw == nil {
		return timeout, nil
	}
	dur, err := durationFromParam(raw)
	if err != nil {
		return 0, fmt.Errorf("invalid timeout: %w", err)
	}
	if dur == 0 {
		return timeout, nil
	}
	if dur > maxBashTimeout {
		dur = maxBashTimeout
	}
	return dur, nil
}

func extractCommand(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", errors.New("params is nil")
	}
	raw, ok := params["command"]
	if !ok {
		// 提供更详细的错误信息帮助调试
		keys := make([]string, 0, len(params))
		for k := range params {
			keys = append(keys, k)
		}
		if len(keys) == 0 {
			return "", errors.New("command is required (params is empty)")
		}
		return "", fmt.Errorf("command is required (got params with keys: %v)", keys)
	}
	cmd, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("command must be string: %w", err)
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", errors.New("command cannot be empty")
	}
	return cmd, nil
}

func coerceString(value interface{}) (string, error) {
	switch v := value.(type) {
	case string:
		return v, nil
	case fmt.Stringer:
		return v.String(), nil
	case []byte:
		return string(v), nil
	case json.Number:
		return v.String(), nil
	default:
		return "", fmt.Errorf("expected string got %T", value)
	}
}

func durationFromParam(value interface{}) (time.Duration, error) {
	switch v := value.(type) {
	case time.Duration:
		if v < 0 {
			return 0, errors.New("duration cannot be negative")
		}
		return v, nil
	case float64:
		return secondsToDuration(v)
	case float32:
		return secondsToDuration(float64(v))
	case int:
		return secondsToDuration(float64(v))
	case int64:
		return secondsToDuration(float64(v))
	case uint:
		return secondsToDuration(float64(v))
	case uint64:
		return secondsToDuration(float64(v))
	case json.Number:
		f, err := v.Float64()
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	case string:
		trimmed := strings.TrimSpace(v)
		if trimmed == "" {
			return 0, nil
		}
		if strings.ContainsAny(trimmed, "hms") {
			return time.ParseDuration(trimmed)
		}
		f, err := strconv.ParseFloat(trimmed, 64)
		if err != nil {
			return 0, err
		}
		return secondsToDuration(f)
	default:
		return 0, fmt.Errorf("unsupported duration type %T", value)
	}
}

func secondsToDuration(seconds float64) (time.Duration, error) {
	if seconds < 0 {
		return 0, errors.New("duration cannot be negative")
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func combineOutput(stdout, stderr string) string {
	stdout = strings.TrimRight(stdout, "\r\n")
	stderr = strings.TrimRight(stderr, "\r\n")
	switch {
	case stdout == "" && stderr == "":
		return ""
	case stdout == "":
		return stderr
	case stderr == "":
		return stdout
	default:
		return stdout + "\n" + stderr
	}
}

func resolveRoot(dir string) string {
	trimmed := strings.TrimSpace(dir)
	if trimmed == "" {
		if cwd, err := os.Getwd(); err == nil {
			trimmed = cwd
		} else {
			trimmed = "."
		}
	}
	if abs, err := filepath.Abs(trimmed); err == nil {
		return abs
	}
	return filepath.Clean(trimmed)
}

func parseAsyncFlag(params map[string]interface{}) (bool, error) {
	if params == nil {
		return false, nil
	}
	raw, ok := params["async"]
	if !ok || raw == nil {
		return false, nil
	}
	switch v := raw.(type) {
	case bool:
		return v, nil
	case string:
		val := strings.TrimSpace(v)
		if val == "" {
			return false, nil
		}
		b, err := strconv.ParseBool(val)
		if err != nil {
			return false, fmt.Errorf("async must be boolean: %w", err)
		}
		return b, nil
	default:
		return false, fmt.Errorf("async must be boolean got %T", raw)
	}
}

func optionalAsyncTaskID(params map[string]interface{}) (string, error) {
	if params == nil {
		return "", nil
	}
	raw, ok := params["task_id"]
	if !ok || raw == nil {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("task_id must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("task_id cannot be empty")
	}
	return value, nil
}

func generateAsyncTaskID() string {
	var buf [4]byte
	if _, err := rand.Read(buf[:]); err == nil {
		return "task-" + hex.EncodeToString(buf[:])
	}
	return fmt.Sprintf("task-%d", time.Now().UnixNano())
}

type bashOutputSpool struct {
	threshold  int
	outputPath string
	stdout     *tool.SpoolWriter
	stderr     *tool.SpoolWriter
}

func newBashOutputSpool(ctx context.Context, threshold int) *bashOutputSpool {
	sessionID := bashSessionID(ctx)
	dir := filepath.Join(bashOutputBaseDir(), sanitizePathComponent(sessionID))
	filename := bashOutputFilename()
	outputPath := filepath.Join(dir, filename)

	spool := &bashOutputSpool{
		threshold:  threshold,
		outputPath: outputPath,
	}
	spool.stdout = tool.NewSpoolWriter(threshold, func() (io.WriteCloser, string, error) {
		return openBashOutputFile(outputPath)
	})
	spool.stderr = tool.NewSpoolWriter(threshold, func() (io.WriteCloser, string, error) {
		if err := ensureBashOutputDir(dir); err != nil {
			return nil, "", err
		}
		f, err := os.CreateTemp(dir, "stderr-*.tmp")
		if err != nil {
			return nil, "", err
		}
		return f, f.Name(), nil
	})
	return spool
}

func (s *bashOutputSpool) StdoutWriter() io.Writer { return s.stdout }

func (s *bashOutputSpool) StderrWriter() io.Writer { return s.stderr }

func (s *bashOutputSpool) Append(text string, isStderr bool) error {
	if isStderr {
		_, err := s.stderr.WriteString(text)
		return err
	}
	_, err := s.stdout.WriteString(text)
	return err
}

func (s *bashOutputSpool) Finalize() (string, string, error) {
	if s == nil {
		return "", "", nil
	}
	stdoutCloseErr := s.stdout.Close()
	stderrCloseErr := s.stderr.Close()
	closeErr := errors.Join(stdoutCloseErr, stderrCloseErr)

	if s.stdout.Truncated() || s.stderr.Truncated() {
		combined := combineOutput(s.stdout.String(), s.stderr.String())
		return combined, "", closeErr
	}

	stdoutPath := s.stdout.Path()
	stderrPath := s.stderr.Path()
	defer func() {
		if stderrPath == "" {
			return
		}
		_ = os.Remove(stderrPath)
	}()

	if stdoutPath == "" && stderrPath == "" {
		combined := combineOutput(s.stdout.String(), s.stderr.String())
		if len(combined) <= s.threshold {
			return combined, "", closeErr
		}
		if err := ensureBashOutputDir(filepath.Dir(s.outputPath)); err != nil {
			return combined, "", errors.Join(closeErr, err)
		}
		if err := os.WriteFile(s.outputPath, []byte(combined), 0o600); err != nil {
			return combined, "", errors.Join(closeErr, err)
		}
		return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
	}

	if stdoutPath == "" {
		if err := ensureBashOutputDir(filepath.Dir(s.outputPath)); err != nil {
			combined := combineOutput(s.stdout.String(), s.stderr.String())
			return combined, "", errors.Join(closeErr, err)
		}
		out, err := os.OpenFile(s.outputPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			combined := combineOutput(s.stdout.String(), s.stderr.String())
			return combined, "", errors.Join(closeErr, err)
		}
		if err := writeCombinedOutput(out, s.stdout.String(), stderrPath, s.stderr.String()); err != nil {
			_ = out.Close()
			return "", "", errors.Join(closeErr, err)
		}
		if err := out.Close(); err != nil {
			return "", "", errors.Join(closeErr, err)
		}
		return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
	}

	out, err := os.OpenFile(s.outputPath, os.O_RDWR, 0)
	if err != nil {
		return "", "", errors.Join(closeErr, err)
	}
	stdoutLen, err := trimRightNewlinesInFile(out)
	if err != nil {
		_ = out.Close()
		return "", "", errors.Join(closeErr, err)
	}
	if err := appendStderr(out, stdoutLen, stderrPath, s.stderr.String()); err != nil {
		_ = out.Close()
		return "", "", errors.Join(closeErr, err)
	}
	if err := out.Close(); err != nil {
		return "", "", errors.Join(closeErr, err)
	}
	return formatBashOutputReference(s.outputPath), s.outputPath, closeErr
}

func writeCombinedOutput(out *os.File, stdoutText, stderrPath, stderrText string) error {
	stdoutTrim := strings.TrimRight(stdoutText, "\r\n")
	if stdoutTrim != "" {
		if _, err := out.WriteString(stdoutTrim); err != nil {
			return err
		}
	}
	return appendStderr(out, int64(len(stdoutTrim)), stderrPath, stderrText)
}

func appendStderr(out *os.File, stdoutLen int64, stderrPath, stderrText string) error {
	stderrTrim := strings.TrimRight(stderrText, "\r\n")
	hasStderr := stderrTrim != "" || stderrPath != ""
	if !hasStderr {
		return nil
	}
	stderrLen := int64(len(stderrTrim))
	if stderrPath != "" {
		f, err := os.Open(stderrPath)
		if err != nil {
			return err
		}
		defer f.Close()
		size, err := trimmedFileSize(f)
		if err != nil {
			return err
		}
		stderrLen = size
		if _, err := f.Seek(0, io.SeekStart); err != nil {
			return err
		}
		if stdoutLen > 0 && stderrLen > 0 {
			if _, err := out.WriteString("\n"); err != nil {
				return err
			}
		}
		if stderrLen > 0 {
			if _, err := io.CopyN(out, f, stderrLen); err != nil {
				return err
			}
		}
		return nil
	}
	if stdoutLen > 0 && stderrLen > 0 {
		if _, err := out.WriteString("\n"); err != nil {
			return err
		}
	}
	if stderrLen > 0 {
		if _, err := out.WriteString(stderrTrim); err != nil {
			return err
		}
	}
	return nil
}

func bashOutputBaseDir() string {
	if runtime.GOOS == "windows" {
		return filepath.Join(os.TempDir(), "agentsdk", "bash-output")
	}
	return filepath.Join(string(filepath.Separator), "tmp", "agentsdk", "bash-output")
}

func bashSessionID(ctx context.Context) string {
	const fallback = "default"
	var session string
	if ctx != nil {
		if st, ok := ctx.Value(model.MiddlewareStateKey).(*middleware.State); ok && st != nil {
			if value, ok := st.Values["session_id"]; ok && value != nil {
				if s, err := coerceString(value); err == nil {
					session = s
				}
			}
			if session == "" {
				if value, ok := st.Values["trace.session_id"]; ok && value != nil {
					if s, err := coerceString(value); err == nil {
						session = s
					}
				}
			}
		}
		if session == "" {
			if value, ok := ctx.Value(middleware.TraceSessionIDContextKey).(string); ok {
				session = value
			} else if value, ok := ctx.Value(middleware.SessionIDContextKey).(string); ok {
				session = value
			}
		}
	}
	session = strings.TrimSpace(session)
	if session == "" {
		return fallback
	}
	return session
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

func bashOutputFilename() string {
	var randBuf [4]byte
	ts := time.Now().UnixNano()
	if _, err := rand.Read(randBuf[:]); err == nil {
		return fmt.Sprintf("%d-%s.txt", ts, hex.EncodeToString(randBuf[:]))
	}
	return fmt.Sprintf("%d.txt", ts)
}

func ensureBashOutputDir(dir string) error {
	if strings.TrimSpace(dir) == "" {
		return errors.New("output directory is empty")
	}
	return os.MkdirAll(dir, 0o700)
}

func openBashOutputFile(path string) (*os.File, string, error) {
	dir := filepath.Dir(path)
	if err := ensureBashOutputDir(dir); err != nil {
		return nil, "", err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return nil, "", err
	}
	return f, path, nil
}

func formatBashOutputReference(path string) string {
	return fmt.Sprintf("[Output saved to: %s]", path)
}

func trimmedFileSize(f *os.File) (int64, error) {
	if f == nil {
		return 0, nil
	}
	info, err := f.Stat()
	if err != nil {
		return 0, err
	}
	size := info.Size()
	if size == 0 {
		return 0, nil
	}

	const chunkSize int64 = 1024
	offset := size
	trimmed := size

	for offset > 0 {
		readSize := chunkSize
		if readSize > offset {
			readSize = offset
		}
		buf := make([]byte, readSize)
		if _, err := f.ReadAt(buf, offset-readSize); err != nil {
			return 0, err
		}
		i := len(buf) - 1
		for i >= 0 {
			if buf[i] != '\n' && buf[i] != '\r' {
				break
			}
			i--
		}
		trimmed = (offset - readSize) + int64(i+1)
		if i >= 0 {
			break
		}
		offset -= readSize
	}
	if trimmed < 0 {
		return 0, nil
	}
	return trimmed, nil
}

func trimRightNewlinesInFile(f *os.File) (int64, error) {
	if f == nil {
		return 0, nil
	}
	trimmed, err := trimmedFileSize(f)
	if err != nil {
		return 0, err
	}
	if err := f.Truncate(trimmed); err != nil {
		return 0, err
	}
	if _, err := f.Seek(trimmed, io.SeekStart); err != nil {
		return 0, err
	}
	return trimmed, nil
}
