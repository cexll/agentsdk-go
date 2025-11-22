package toolbuiltin

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

// StreamExecute runs the bash command while emitting incremental output. It
// preserves backwards compatibility by sharing validation and metadata with
// Execute, and enforces the same 30k output cap to avoid unbounded buffers.
func (b *BashTool) StreamExecute(ctx context.Context, params map[string]interface{}, emit func(chunk string, isStderr bool)) (*tool.ToolResult, error) {
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

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}

	acc := &streamAccumulator{}
	start := time.Now()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}

	var stdoutErr, stderrErr error
	var wg sync.WaitGroup

	wg.Add(1)
	go func() {
		defer wg.Done()
		stdoutErr = consumeStream(execCtx, stdoutPipe, emit, acc, false)
	}()

	wg.Add(1)
	go func() {
		defer wg.Done()
		stderrErr = consumeStream(execCtx, stderrPipe, emit, acc, true)
	}()

	wg.Wait()
	waitErr := cmd.Wait()
	duration := time.Since(start)

	runErr := waitErr
	if stdoutErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("stdout read: %w", stdoutErr))
	}
	if stderrErr != nil {
		runErr = errors.Join(runErr, fmt.Errorf("stderr read: %w", stderrErr))
	}

	result := &tool.ToolResult{
		Success: runErr == nil,
		Output:  truncateOutput(combineOutput(acc.stdout.String(), acc.stderr.String())),
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
		if errors.Is(execCtx.Err(), context.Canceled) {
			return result, execCtx.Err()
		}
		return result, fmt.Errorf("command failed: %w", runErr)
	}
	return result, nil
}

type streamAccumulator struct {
	mu     sync.Mutex
	total  int
	stdout strings.Builder
	stderr strings.Builder
}

func (a *streamAccumulator) append(text string, isStderr bool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.total >= maxBashOutputLen {
		return
	}
	remaining := maxBashOutputLen - a.total
	if len(text) > remaining {
		text = text[:remaining]
	}
	var dst *strings.Builder
	if isStderr {
		dst = &a.stderr
	} else {
		dst = &a.stdout
	}
	dst.WriteString(text)
	a.total += len(text)
}

func consumeStream(ctx context.Context, r io.ReadCloser, emit func(chunk string, isStderr bool), acc *streamAccumulator, isStderr bool) error {
	defer r.Close()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if emit != nil {
			emit(line, isStderr)
		}
		acc.append(line, isStderr)
		acc.append("\n", isStderr)
		if ctx.Err() != nil {
			break
		}
	}
	if err := scanner.Err(); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func truncateOutput(text string) string {
	if len(text) > maxBashOutputLen {
		return text[:maxBashOutputLen]
	}
	return text
}
