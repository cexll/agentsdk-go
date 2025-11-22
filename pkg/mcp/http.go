package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// HTTPOptions configures the HTTP JSON-RPC transport.
type HTTPOptions struct {
	URL         string
	Client      *http.Client
	Headers     map[string]string
	AuthToken   string
	Timeout     time.Duration
	RetryPolicy RetryPolicy
}

// HTTPTransport implements Transport over plain HTTP.
type HTTPTransport struct {
	url       string
	client    *http.Client
	headers   map[string]string
	authToken string
	retry     RetryPolicy
}

// NewHTTPTransport builds an HTTP transport with optional headers, auth, timeout, and retry.
func NewHTTPTransport(opts HTTPOptions) (*HTTPTransport, error) {
	url := strings.TrimSpace(opts.URL)
	if url == "" {
		return nil, errors.New("http transport requires url")
	}
	client := opts.Client
	if client == nil {
		client = &http.Client{}
	}
	if opts.Timeout > 0 {
		clone := *client
		clone.Timeout = opts.Timeout
		client = &clone
	} else if client.Timeout == 0 {
		clone := *client
		clone.Timeout = 30 * time.Second
		client = &clone
	}

	transport := &HTTPTransport{
		url:       url,
		client:    client,
		headers:   copyStringMap(opts.Headers),
		authToken: strings.TrimSpace(opts.AuthToken),
		retry:     normalizeRetryPolicy(opts.RetryPolicy, defaultHTTPRetryable),
	}
	return transport, nil
}

// Call executes the JSON-RPC request with retries.
func (t *HTTPTransport) Call(ctx context.Context, req *Request) (*Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req.JSONRPC = jsonRPCVersion

	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(req); err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}
	payload := buf.Bytes()

	var lastErr error
	for attempt := 1; attempt <= t.retry.MaxAttempts; attempt++ {
		resp, err := t.do(ctx, payload)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !t.retry.Retryable(err) || attempt == t.retry.MaxAttempts {
			break
		}
		if ctx.Err() != nil {
			lastErr = ctx.Err()
			break
		}
		t.retry.Sleep(t.retry.Backoff(attempt + 1))
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if lastErr == nil {
		return nil, ErrTransportClosed
	}
	return nil, lastErr
}

func (t *HTTPTransport) do(ctx context.Context, payload []byte) (*Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, t.url, bytes.NewReader(payload))
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range t.headers {
		if strings.TrimSpace(k) == "" {
			continue
		}
		req.Header.Set(k, v)
	}
	if t.authToken != "" && req.Header.Get("Authorization") == "" {
		req.Header.Set("Authorization", "Bearer "+t.authToken)
	}

	resp, err := t.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		body, readErr := io.ReadAll(io.LimitReader(resp.Body, 512))
		if readErr != nil {
			return nil, fmt.Errorf("read error body: %w", readErr)
		}
		return nil, &httpStatusError{status: resp.StatusCode, body: strings.TrimSpace(string(body))}
	}

	dec := json.NewDecoder(resp.Body)
	dec.UseNumber()
	var rpcResp Response
	if err := dec.Decode(&rpcResp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return &rpcResp, nil
}

// Close releases any idle connections held by the underlying client.
func (t *HTTPTransport) Close() error {
	if t == nil || t.client == nil {
		return nil
	}
	if closer, ok := t.client.Transport.(interface{ CloseIdleConnections() }); ok {
		closer.CloseIdleConnections()
	}
	return nil
}

type httpStatusError struct {
	status int
	body   string
}

func (e *httpStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.body == "" {
		return fmt.Sprintf("http status %d", e.status)
	}
	return fmt.Sprintf("http status %d: %s", e.status, e.body)
}

func (e *httpStatusError) StatusCode() int { return e.status }

func defaultHTTPRetryable(err error) bool {
	if err == nil {
		return false
	}
	var statusErr interface{ StatusCode() int }
	if errors.As(err, &statusErr) {
		code := statusErr.StatusCode()
		return code == http.StatusRequestTimeout || code == http.StatusTooManyRequests || code >= 500
	}
	return defaultRetryable(err)
}

func normalizeRetryPolicy(policy RetryPolicy, retryable func(error) bool) RetryPolicy {
	if policy.MaxAttempts <= 0 {
		policy.MaxAttempts = 3
	}
	if policy.Backoff == nil {
		policy.Backoff = func(attempt int) time.Duration {
			if attempt <= 1 {
				return 0
			}
			return time.Duration(1<<(attempt-2)) * 50 * time.Millisecond
		}
	}
	if policy.Retryable == nil {
		policy.Retryable = retryable
	}
	if policy.Sleep == nil {
		policy.Sleep = time.Sleep
	}
	return policy
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
