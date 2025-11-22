package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSEOptions configures the HTTP transport for MCP over SSE.
type SSEOptions struct {
	BaseURL              string
	EventsURL            string
	RPCURL               string
	Client               *http.Client
	Headers              map[string]string
	AuthToken            string
	HeartbeatInterval    time.Duration
	HeartbeatTimeout     time.Duration
	ReconnectInterval    time.Duration
	MaxReconnectInterval time.Duration
}

// SSETransport implements the JSON-RPC over SSE bridge.
type SSETransport struct {
	client  *http.Client
	events  string
	rpc     string
	pending *pendingTracker
	consume func() (bool, error)

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	ready     chan struct{}
	readyOnce sync.Once

	hbInterval time.Duration
	hbTimeout  time.Duration
	heartbeat  atomic.Int64

	reconInitial time.Duration
	reconMax     time.Duration

	connMu  sync.Mutex
	conn    io.Closer
	headers map[string]string
	auth    string

	failOnce sync.Once
	failErr  error
}

// NewSSETransport wires an SSE stream and accompanying RPC endpoint.
func NewSSETransport(ctx context.Context, opts SSEOptions) (*SSETransport, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	events, rpc := opts.EventsURL, opts.RPCURL
	base := strings.TrimSuffix(opts.BaseURL, "/")
	if events == "" && base == "" {
		return nil, errors.New("events url or base url required")
	}
	if events == "" {
		events = base + "/events"
	}
	if rpc == "" {
		if base == "" {
			return nil, errors.New("rpc url or base url required")
		}
		rpc = base + "/rpc"
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{Timeout: 0}
	}
	hbInterval := opts.HeartbeatInterval
	if hbInterval <= 0 {
		hbInterval = 5 * time.Second
	}
	hbTimeout := opts.HeartbeatTimeout
	if hbTimeout <= 0 {
		hbTimeout = hbInterval * 2
	}
	reconInitial := opts.ReconnectInterval
	if reconInitial <= 0 {
		reconInitial = 500 * time.Millisecond
	}
	reconMax := opts.MaxReconnectInterval
	if reconMax <= reconInitial {
		reconMax = 8 * reconInitial
	}

	transport := &SSETransport{
		client:       client,
		events:       events,
		rpc:          rpc,
		pending:      newPendingTracker(),
		ready:        make(chan struct{}),
		hbInterval:   hbInterval,
		hbTimeout:    hbTimeout,
		reconInitial: reconInitial,
		reconMax:     reconMax,
		headers:      copyStringMap(opts.Headers),
		auth:         strings.TrimSpace(opts.AuthToken),
	}
	transport.heartbeat.Store(time.Now().UnixNano())
	transport.ctx, transport.cancel = context.WithCancel(ctx)
	transport.wg.Add(1)
	go transport.runStream()
	transport.wg.Add(1)
	go transport.watchHeartbeat()
	return transport, nil
}

// Call sends the JSON-RPC payload via HTTP POST and awaits the SSE response.
func (t *SSETransport) Call(ctx context.Context, req *Request) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.JSONRPC = jsonRPCVersion

	if err := t.waitReady(ctx); err != nil {
		return nil, err
	}

	ch, err := t.pending.add(req.ID)
	if err != nil {
		return nil, err
	}
	if err := t.dispatch(ctx, req); err != nil {
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

func (t *SSETransport) dispatch(ctx context.Context, req *Request) error {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return fmt.Errorf("encode request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, t.rpc, bytes.NewReader(buf.Bytes()))
	if err != nil {
		return fmt.Errorf("create rpc request: %w", err)
	}
	if err := t.applyHeaders(httpReq); err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := t.client.Do(httpReq)
	if err != nil {
		return fmt.Errorf("rpc request failed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
		if err != nil {
			return fmt.Errorf("rpc status %d (body read failed): %w", resp.StatusCode, err)
		}
		return fmt.Errorf("rpc status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if _, err := io.Copy(io.Discard, resp.Body); err != nil {
		return fmt.Errorf("drain rpc body: %w", err)
	}
	return nil
}

func (t *SSETransport) runStream() {
	defer t.wg.Done()
	backoff := t.reconInitial
	consume := t.consumeOnce
	if t.consume != nil {
		consume = t.consume
	}
	for {
		connected, err := consume()
		if t.ctx.Err() != nil {
			return
		}
		if err != nil {
			t.pending.flush(err)
		}
		if connected {
			backoff = t.reconInitial
		} else if backoff < t.reconMax {
			backoff *= 2
			if backoff > t.reconMax {
				backoff = t.reconMax
			}
		}
		timer := time.NewTimer(backoff)
		select {
		case <-t.ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
	}
}

func (t *SSETransport) consumeOnce() (bool, error) {
	req, err := http.NewRequestWithContext(t.ctx, http.MethodGet, t.events, nil)
	if err != nil {
		return false, fmt.Errorf("build events request: %w", err)
	}
	if err := t.applyHeaders(req); err != nil {
		return false, err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := t.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("connect events: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, err := io.ReadAll(io.LimitReader(resp.Body, 512))
		if err != nil {
			resp.Body.Close()
			return false, fmt.Errorf("events status %d (body read failed): %w", resp.StatusCode, err)
		}
		resp.Body.Close()
		return false, fmt.Errorf("events status %d: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}

	t.setConn(resp.Body)
	t.signalReady()
	t.touchHeartbeat()

	reader := bufio.NewReader(resp.Body)
	var data strings.Builder
	var event string

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.clearConn(resp.Body)
			return true, err
		}
		line = strings.TrimRight(line, "\r\n")
		switch {
		case line == "":
			payload := data.String()
			data.Reset()
			if payload == "" {
				continue
			}
			if event == "heartbeat" {
				t.touchHeartbeat()
				event = ""
				continue
			}
			if err := t.handlePayload(payload); err != nil {
				t.clearConn(resp.Body)
				return true, err
			}
			event = ""
		case strings.HasPrefix(line, ":"):
			t.touchHeartbeat()
		case strings.HasPrefix(line, "event:"):
			event = strings.TrimSpace(line[6:])
		case strings.HasPrefix(line, "data:"):
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(strings.TrimSpace(line[5:]))
		default:
			// treat as data line
			if data.Len() > 0 {
				data.WriteByte('\n')
			}
			data.WriteString(line)
		}
	}
}

func (t *SSETransport) handlePayload(payload string) error {
	if strings.TrimSpace(payload) == "" {
		return nil
	}
	var resp Response
	if err := json.Unmarshal([]byte(payload), &resp); err != nil {
		return fmt.Errorf("decode sse payload: %w", err)
	}
	if resp.ID == "" {
		return nil
	}
	t.pending.deliver(resp.ID, callResult{resp: &resp})
	t.touchHeartbeat()
	return nil
}

func (t *SSETransport) watchHeartbeat() {
	defer t.wg.Done()
	ticker := time.NewTicker(t.hbInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			last := time.Unix(0, t.heartbeat.Load())
			if time.Since(last) > t.hbTimeout {
				t.interrupt()
			}
		case <-t.ctx.Done():
			return
		}
	}
}

func (t *SSETransport) waitReady(ctx context.Context) error {
	select {
	case <-t.ready:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	case <-t.ctx.Done():
		return ErrTransportClosed
	}
}

func (t *SSETransport) touchHeartbeat() {
	t.heartbeat.Store(time.Now().UnixNano())
}

func (t *SSETransport) interrupt() {
	t.connMu.Lock()
	conn := t.conn
	t.conn = nil
	t.connMu.Unlock()
	if conn != nil {
		conn.Close()
	}
}

func (t *SSETransport) setConn(body io.Closer) {
	t.connMu.Lock()
	t.conn = body
	t.connMu.Unlock()
}

func (t *SSETransport) clearConn(body io.Closer) {
	t.connMu.Lock()
	if t.conn == body {
		t.conn = nil
	}
	t.connMu.Unlock()
	body.Close()
}

func (t *SSETransport) fail(err error) {
	t.failOnce.Do(func() {
		if err == nil {
			err = ErrTransportClosed
		}
		t.failErr = err
		t.pending.failAll(err)
		t.signalReady()
	})
}

// Close stops the SSE loops and releases pending callers.
func (t *SSETransport) Close() error {
	t.fail(ErrTransportClosed)
	t.cancel()
	t.interrupt()
	t.wg.Wait()
	return nil
}

func (t *SSETransport) signalReady() {
	t.readyOnce.Do(func() {
		close(t.ready)
	})
}

func (t *SSETransport) applyHeaders(req *http.Request) error {
	if req == nil {
		return errors.New("nil request")
	}
	for k, v := range t.headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	if t.auth != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+t.auth)
	}
	return nil
}
