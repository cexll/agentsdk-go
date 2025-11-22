package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestNewSSETransportDeprecated(t *testing.T) {
	_, err := NewSSETransport(context.Background(), SSEOptions{BaseURL: "http://example.com"})
	if err == nil {
		t.Fatal("expected error for deprecated function")
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("error should mention deprecated: %v", err)
	}
}

func TestNewSTDIOTransportDeprecated(t *testing.T) {
	_, err := NewSTDIOTransport(context.Background(), "echo", STDIOOptions{Args: []string{"test"}})
	if err == nil {
		t.Fatal("expected error for deprecated function")
	}
	if !strings.Contains(err.Error(), "deprecated") {
		t.Errorf("error should mention deprecated: %v", err)
	}
}

func TestNewClientFromServerConfigStdio(t *testing.T) {
	cfg := ServerConfig{
		Type:    "stdio",
		Command: "echo",
		Args:    []string{"test"},
	}
	client, err := NewClientFromServerConfig(context.Background(), "test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientFromServerConfigSSE(t *testing.T) {
	cfg := ServerConfig{
		Type: "sse",
		URL:  "http://example.com",
	}
	client, err := NewClientFromServerConfig(context.Background(), "test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientFromServerConfigHTTP(t *testing.T) {
	cfg := ServerConfig{
		Type: "http",
		URL:  "http://example.com",
	}
	client, err := NewClientFromServerConfig(context.Background(), "test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewClientFromServerConfigUnsupportedType(t *testing.T) {
	cfg := ServerConfig{
		Type: "unsupported",
	}
	_, err := NewClientFromServerConfig(context.Background(), "test", cfg)
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("error should mention unsupported: %v", err)
	}
}

func TestNewClientFromServerConfigDefaultStdio(t *testing.T) {
	cfg := ServerConfig{
		Command: "echo",
	}
	client, err := NewClientFromServerConfig(context.Background(), "test", cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
}
