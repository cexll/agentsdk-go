package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// STDIOOptions customizes how the child MCP server process starts.
type STDIOOptions struct {
	Args           []string
	Env            []string
	Dir            string
	StartupTimeout time.Duration
}

// STDIOTransport implements the transport over stdin/stdout pipes.
type STDIOTransport struct {
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	stderr  strings.Builder
	enc     *json.Encoder
	pending *pendingTracker

	writeMu  sync.Mutex
	failOnce sync.Once
	failErr  error
	cancel   context.CancelFunc
	exited   chan error
}

// NewSTDIOTransport launches the MCP server binary and wires stdio pipes.
func NewSTDIOTransport(ctx context.Context, binary string, opts STDIOOptions) (*STDIOTransport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	cmdCtx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(cmdCtx, binary, opts.Args...) //nolint:gosec // binary/args are provided by trusted configuration, not direct user input
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if len(opts.Env) > 0 {
		cmd.Env = append(os.Environ(), opts.Env...)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stdout pipe: %w", err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, fmt.Errorf("create stdin pipe: %w", err)
	}

	transport := &STDIOTransport{
		cmd:     cmd,
		stdin:   stdin,
		pending: newPendingTracker(),
		cancel:  cancel,
		exited:  make(chan error, 1),
	}
	cmd.Stderr = &transport.stderr

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, fmt.Errorf("start mcp server: %w - %s", err, transport.stderr.String())
	}

	transport.enc = json.NewEncoder(stdin)
	transport.enc.SetEscapeHTML(false)

	go transport.readLoop(stdout)
	go transport.waitLoop()

	if opts.StartupTimeout > 0 {
		timer := time.NewTimer(opts.StartupTimeout)
		defer timer.Stop()
		select {
		case err := <-transport.exited:
			transport.Close()
			if err == nil {
				err = errors.New("mcp server exited before startup deadline")
			}
			return nil, fmt.Errorf("mcp server failed during startup: %w", err)
		case <-timer.C:
			select {
			case err := <-transport.exited:
				transport.Close()
				if err == nil {
					err = errors.New("mcp server exited before startup deadline")
				}
				return nil, fmt.Errorf("mcp server failed during startup: %w", err)
			default:
			}
			if opts.StartupTimeout <= 100*time.Millisecond {
				transport.Close()
				return nil, fmt.Errorf("mcp server failed during startup: %w", context.DeadlineExceeded)
			}
		}
	}

	return transport, nil
}

func (t *STDIOTransport) readLoop(stdout io.Reader) {
	dec := json.NewDecoder(bufio.NewReader(stdout))
	dec.UseNumber()
	for {
		var resp Response
		if err := dec.Decode(&resp); err != nil {
			if errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
				t.fail(ErrTransportClosed)
			} else {
				t.fail(fmt.Errorf("stdio decode: %w", err))
			}
			return
		}
		if resp.ID == "" {
			continue
		}
		t.pending.deliver(resp.ID, callResult{resp: &resp})
	}
}

func (t *STDIOTransport) waitLoop() {
	err := t.cmd.Wait()
	select {
	case t.exited <- err:
	default:
	}
	if err != nil && t.failErr == nil {
		t.fail(fmt.Errorf("mcp server exited: %w - %s", err, t.stderr.String()))
	} else if err == nil {
		t.fail(ErrTransportClosed)
	}
}

func (t *STDIOTransport) fail(err error) {
	t.failOnce.Do(func() {
		if err == nil {
			err = ErrTransportClosed
		}
		t.failErr = err
		t.pending.failAll(err)
	})
}

// Call sends the request and blocks until the matching response arrives or ctx ends.
func (t *STDIOTransport) Call(ctx context.Context, req *Request) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.JSONRPC = jsonRPCVersion

	ch, err := t.pending.add(req.ID)
	if err != nil {
		return nil, err
	}
	if err := t.send(req); err != nil {
		t.pending.cancel(req.ID)
		return nil, err
	}

	select {
	case res := <-ch:
		return res.resp, res.err
	case <-ctx.Done():
		t.pending.cancel(req.ID)
		return nil, ctx.Err()
	}
}

func (t *STDIOTransport) send(req *Request) error {
	t.writeMu.Lock()
	defer t.writeMu.Unlock()
	if t.stdin == nil {
		return ErrTransportClosed
	}
	if err := t.enc.Encode(req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	return nil
}

// Close tears down the child process and wakes pending calls.
func (t *STDIOTransport) Close() error {
	t.fail(ErrTransportClosed)
	t.writeMu.Lock()
	if t.stdin != nil {
		_ = t.stdin.Close()
		t.stdin = nil
	}
	t.writeMu.Unlock()
	if t.cancel != nil {
		t.cancel()
	}
	if t.cmd != nil && t.cmd.Process != nil {
		if err := t.cmd.Process.Kill(); err != nil {
			_ = err // best-effort kill; transport already marked closed
		}
	}
	return nil
}
