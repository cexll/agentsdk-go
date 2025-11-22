package mcp

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSSEApplyHeaders(t *testing.T) {
	transport := &SSETransport{
		headers: map[string]string{"X-Test": "1"},
		auth:    "token",
	}
	req, err := http.NewRequest(http.MethodGet, "http://example", nil)
	require.NoError(t, err)
	if err := transport.applyHeaders(req); err != nil {
		t.Fatalf("apply headers: %v", err)
	}
	if req.Header.Get("X-Test") != "1" {
		t.Fatalf("header not applied: %v", req.Header)
	}
	if req.Header.Get("Authorization") != "Bearer token" {
		t.Fatalf("auth header missing: %v", req.Header)
	}

	// Authorization should not override existing value
	req.Header.Set("Authorization", "keep")
	transport.auth = "token"
	transport.headers = map[string]string{}
	if err := transport.applyHeaders(req); err != nil {
		t.Fatalf("apply headers again: %v", err)
	}
	if req.Header.Get("Authorization") != "keep" {
		t.Fatalf("auth header overwritten: %s", req.Header.Get("Authorization"))
	}
}

func TestSSEApplyHeadersNilRequest(t *testing.T) {
	transport := &SSETransport{}
	if err := transport.applyHeaders(nil); err == nil {
		t.Fatal("expected error for nil request")
	}
}
