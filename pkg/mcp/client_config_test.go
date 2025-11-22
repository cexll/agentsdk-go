package mcp

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestNewClientFromServerConfigSelectsHTTP(t *testing.T) {
	origHTTP := httpTransportFactory
	httpTransportFactory = func(opts HTTPOptions) (Transport, error) {
		return &stubTransport{responses: []*Response{{ID: "1"}}}, nil
	}
	defer func() { httpTransportFactory = origHTTP }()

	client, err := NewClientFromServerConfig(context.Background(), "api", ServerConfig{
		Type: "http",
		URL:  "http://api.example",
		Headers: map[string]string{
			"X-Test": "1",
		},
		Timeout: time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("client is nil")
	}
	_ = client.Close()
}

func TestNewClientFromServerConfigSTDIOEnvOrdering(t *testing.T) {
	origSTDIO := stdioTransportFactory
	defer func() { stdioTransportFactory = origSTDIO }()

	var capturedEnv []string
	stdioTransportFactory = func(ctx context.Context, binary string, opts STDIOOptions) (Transport, error) {
		capturedEnv = opts.Env
		return &stubTransport{responses: []*Response{{ID: "1"}}}, nil
	}

	_, err := NewClientFromServerConfig(context.Background(), "local", ServerConfig{
		Command: "/bin/echo",
		Args:    []string{"-n"},
		Env: map[string]string{
			"B": "2",
			"A": "1",
		},
	})
	if err != nil {
		t.Fatalf("build client: %v", err)
	}

	expected := []string{"A=1", "B=2"}
	if len(capturedEnv) != len(expected) {
		t.Fatalf("env size mismatch: %v", capturedEnv)
	}
	for i := range expected {
		if capturedEnv[i] != expected[i] {
			t.Fatalf("env ordering mismatch: %v", capturedEnv)
		}
	}
}

func TestNewClientFromServerConfigMissingFields(t *testing.T) {
	if _, err := NewClientFromServerConfig(context.Background(), "http", ServerConfig{Type: "http"}); err == nil {
		t.Fatal("expected url missing error")
	}
	if _, err := NewClientFromServerConfig(context.Background(), "stdio", ServerConfig{}); err == nil {
		t.Fatal("expected command missing error")
	}
}

func TestNewClientFromServerConfigUnsupportedType(t *testing.T) {
	_, err := NewClientFromServerConfig(context.Background(), "bad", ServerConfig{Type: "ws"})
	if err == nil {
		t.Fatal("expected unsupported type error")
	}
	if !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestNewClientFromServerConfigSSE(t *testing.T) {
	origSSE := sseTransportFactory
	defer func() { sseTransportFactory = origSSE }()

	var capturedOpts SSEOptions
	sseTransportFactory = func(ctx context.Context, opts SSEOptions) (Transport, error) {
		capturedOpts = opts
		return &stubTransport{responses: []*Response{{ID: "1"}}}, nil
	}

	client, err := NewClientFromServerConfig(context.Background(), "sse", ServerConfig{
		Type:      "sse",
		URL:       "http://sse.example",
		Headers:   map[string]string{"X-Test": "1"},
		AuthToken: "token",
		Timeout:   time.Second,
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("client nil")
	}
	if capturedOpts.BaseURL != "http://sse.example" {
		t.Fatalf("base url mismatch: %s", capturedOpts.BaseURL)
	}
	if capturedOpts.Client == nil || capturedOpts.Client.Timeout != time.Second {
		t.Fatalf("client timeout not propagated: %+v", capturedOpts.Client)
	}
	if capturedOpts.Headers["X-Test"] != "1" {
		t.Fatalf("headers not passed: %+v", capturedOpts.Headers)
	}
	if capturedOpts.AuthToken != "token" {
		t.Fatalf("auth token missing: %s", capturedOpts.AuthToken)
	}
}
