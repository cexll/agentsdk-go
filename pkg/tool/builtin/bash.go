package toolbuiltin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/security"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

const (
	defaultBashTimeout = 30 * time.Second
	maxBashTimeout     = 2 * time.Minute
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
	- **Output limit**: Truncated if exceeds 30000 characters
	- **Background execution**: Use 'run_in_background' parameter (no need for '&')

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
	},
	Required: []string{"command"},
}

// BashTool executes validated commands using bash within a sandbox.
type BashTool struct {
	sandbox *security.Sandbox
	root    string
	timeout time.Duration
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
	}
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

	execCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		execCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}

	cmd := exec.CommandContext(execCtx, "bash", "-c", command)
	cmd.Env = os.Environ()
	cmd.Dir = workdir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	runErr := cmd.Run()
	duration := time.Since(start)

	result := &tool.ToolResult{
		Success: runErr == nil,
		Output:  combineOutput(stdout.String(), stderr.String()),
		Data: map[string]interface{}{
			"workdir":     workdir,
			"duration_ms": duration.Milliseconds(),
			"timeout_ms":  timeout.Milliseconds(),
		},
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
