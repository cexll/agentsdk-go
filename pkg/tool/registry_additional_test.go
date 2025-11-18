package tool

import (
	"context"
	"testing"
)

func TestBuildMCPTransportVariants(t *testing.T) {
	if _, err := buildMCPTransport(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty spec")
	}
	if tr, err := buildMCPTransport(context.Background(), "http://example.com"); err != nil || tr == nil {
		t.Fatalf("http transport failed: %v %v", tr, err)
	}
	if _, err := buildMCPTransport(context.Background(), "stdio://"); err == nil {
		t.Fatalf("expected error for missing command")
	}
	if tr, err := buildMCPTransport(context.Background(), "stdio://echo"); err != nil || tr == nil {
		t.Fatalf("stdio transport failed: %v %v", tr, err)
	}
}

func TestRemoteToolDescription(t *testing.T) {
	rt := &remoteTool{name: "r", description: "remote", schema: &JSONSchema{Type: "object"}}
	if rt.Description() == "" || rt.Schema() == nil {
		t.Fatalf("remote tool metadata missing")
	}
}
