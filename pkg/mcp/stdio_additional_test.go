package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type nopWriteCloser struct{}

func (nopWriteCloser) Write(p []byte) (int, error) { return len(p), nil }
func (nopWriteCloser) Close() error                { return nil }

func TestSTDIOTransportSendClosed(t *testing.T) {
	tr := &STDIOTransport{}
	if err := tr.send(&Request{ID: "1"}); err == nil {
		t.Fatal("expected closed error")
	}
}

func TestSTDIOTransportSendEncodeError(t *testing.T) {
	tr := &STDIOTransport{
		stdin: nopWriteCloser{},
		enc:   json.NewEncoder(errorWriter{}),
	}
	if err := tr.send(&Request{ID: "1"}); err == nil || !strings.Contains(err.Error(), "encode request") {
		t.Fatalf("expected encode error, got %v", err)
	}
}

func TestSTDIOTransportCallContextCancel(t *testing.T) {
	tr := &STDIOTransport{
		pending: newPendingTracker(),
		stdin:   nopWriteCloser{},
		enc:     json.NewEncoder(io.Discard),
	}
	ctx, cancel := context.WithCancel(context.Background())
	req := &Request{ID: "1"}
	go func() {
		time.Sleep(5 * time.Millisecond)
		cancel()
	}()
	if _, err := tr.Call(ctx, req); err == nil {
		t.Fatal("expected context cancellation")
	}
}

func TestSTDIOTransportCallSuccess(t *testing.T) {
	tr := &STDIOTransport{
		pending: newPendingTracker(),
		stdin:   nopWriteCloser{},
		enc:     json.NewEncoder(io.Discard),
	}
	req := &Request{ID: "ok"}
	go func() {
		time.Sleep(5 * time.Millisecond)
		tr.pending.deliver(req.ID, callResult{resp: &Response{ID: req.ID}})
	}()
	if _, err := tr.Call(context.Background(), req); err != nil {
		t.Fatalf("call failed: %v", err)
	}
}

func TestSTDIOTransportCallSendError(t *testing.T) {
	tr := &STDIOTransport{
		pending: newPendingTracker(),
		stdin:   nopWriteCloser{},
		enc:     json.NewEncoder(errorWriter{}),
	}
	if _, err := tr.Call(context.Background(), &Request{ID: "bad"}); err == nil {
		t.Fatal("expected send error")
	}
}

func TestSTDIOTransportReadLoopHandlesError(t *testing.T) {
	tr := &STDIOTransport{
		pending: newPendingTracker(),
	}
	tr.readLoop(strings.NewReader("not json"))
	if tr.failErr == nil {
		t.Fatal("expected failErr to be populated")
	}
}

type errorWriter struct{}

func (errorWriter) Write([]byte) (int, error) { return 0, errors.New("write fail") }

func TestSTDIOTransportStartupTimeout(t *testing.T) {
	if os.Getenv("TASK003_STDIO_TIMEOUT_HELPER") == "1" {
		return
	}
	opts := STDIOOptions{
		Args:           []string{"-test.run=TestSTDIOTransportTimeoutHelper"},
		Env:            append(os.Environ(), "TASK003_STDIO_TIMEOUT_HELPER=1"),
		StartupTimeout: 10 * time.Millisecond,
	}
	_, err := NewSTDIOTransport(context.Background(), os.Args[0], opts)
	if err == nil || !strings.Contains(err.Error(), "startup") {
		t.Fatalf("expected startup timeout error, got %v", err)
	}
}

func TestSTDIOTransportHelper(t *testing.T) {
	if os.Getenv("TASK003_STDIO_HELPER") != "1" {
		t.Skip("helper")
	}
}

func TestSTDIOTransportTimeoutHelper(t *testing.T) {
	if os.Getenv("TASK003_STDIO_TIMEOUT_HELPER") != "1" {
		t.Skip("helper")
	}
	os.Exit(0)
}

func TestNewSTDIOTransportNilContext(t *testing.T) {
	if os.Getenv("MCP_STDIO_HELPER") == "1" {
		return
	}
	opts := STDIOOptions{
		Args: []string{"-test.run", "TestSTDIOTransportIntegration"},
		Env:  append(os.Environ(), "MCP_STDIO_HELPER=1"),
		Dir:  filepath.Dir(os.Args[0]),
	}
	transport, err := NewSTDIOTransport(context.TODO(), os.Args[0], opts)
	if err != nil {
		t.Fatalf("new transport: %v", err)
	}
	if err := transport.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestNewSTDIOTransportStartFailure(t *testing.T) {
	_, err := NewSTDIOTransport(context.Background(), filepath.Join(os.TempDir(), "missing-binary"), STDIOOptions{})
	if err == nil {
		t.Fatal("expected start error")
	}
}
