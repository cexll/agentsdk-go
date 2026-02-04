package skills

import (
	"testing"

	"gopkg.in/yaml.v3"
)

func TestToolListUnmarshalYAML(t *testing.T) {
	type wrapper struct {
		Allowed ToolList `yaml:"allowed-tools"`
	}

	var cfg wrapper
	if err := yaml.Unmarshal([]byte("allowed-tools: a, b, a"), &cfg); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(cfg.Allowed) != 2 || cfg.Allowed[0] != "a" || cfg.Allowed[1] != "b" {
		t.Fatalf("unexpected list %v", cfg.Allowed)
	}

	if err := yaml.Unmarshal([]byte("allowed-tools:\n - x\n - y\n"), &cfg); err != nil {
		t.Fatalf("unmarshal sequence: %v", err)
	}
	if len(cfg.Allowed) != 2 {
		t.Fatalf("unexpected sequence list %v", cfg.Allowed)
	}

	if err := yaml.Unmarshal([]byte("allowed-tools:\n - [bad]\n"), &cfg); err == nil {
		t.Fatalf("expected non-string error")
	}

	if err := yaml.Unmarshal([]byte("allowed-tools: \"\""), &cfg); err != nil {
		t.Fatalf("unmarshal empty: %v", err)
	}
	if cfg.Allowed != nil {
		t.Fatalf("expected nil for empty list")
	}
}
