//go:build !otel

package api

import "testing"

func TestNoopTracerEndSpan(t *testing.T) {
	tracer, err := NewTracer(OTELConfig{})
	if err != nil {
		t.Fatalf("new tracer: %v", err)
	}
	span := tracer.StartAgentSpan("sess", "run", 0)
	tracer.EndSpan(span, map[string]any{"k": "v"}, nil)
	if err := tracer.Shutdown(); err != nil {
		t.Fatalf("shutdown: %v", err)
	}
}

func TestNoopFileSystemPolicyAllow(t *testing.T) {
	policy := &noopFileSystemPolicy{root: " /tmp "}
	policy.Allow("/tmp")
	if got := policy.Roots(); len(got) != 1 {
		t.Fatalf("expected roots, got %v", got)
	}
	if got := (&noopFileSystemPolicy{}).Roots(); got != nil {
		t.Fatalf("expected nil roots, got %v", got)
	}
}
