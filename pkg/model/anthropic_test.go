package model

import (
	"testing"
)

func TestNewAnthropicRequiresAPIKey(t *testing.T) {
	if _, err := NewAnthropic(AnthropicConfig{}); err == nil {
		t.Fatalf("expected api key error")
	}
}

func TestAnthropicHeaders(t *testing.T) {
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	headers := newAnthropicHeaders(map[string]string{"X-Test": "1"}, map[string]string{"x-api-key": "skip"})
	if headers["x-test"] != "1" {
		t.Fatalf("expected x-test header, got %v", headers)
	}
}

func TestAnthropicRequestOptions(t *testing.T) {
	m := &anthropicModel{configuredAPIKey: "key"}
	opts := m.requestOptions()
	if len(opts) == 0 {
		t.Fatalf("expected request options")
	}
}
