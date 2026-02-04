package api

import (
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestToolSelectorAndManagedRulesHelpers(t *testing.T) {
	if got := normalizeToolSelectorPattern("*"); got != "" {
		t.Fatalf("expected wildcard to normalize to empty, got %q", got)
	}
	if got := normalizeToolSelectorPattern("bash"); got != "bash" {
		t.Fatalf("unexpected pattern %q", got)
	}

	settings := &config.Settings{
		AllowedMcpServers: []config.MCPServerRule{{ServerName: "allow"}},
		DeniedMcpServers:  []config.MCPServerRule{{ServerName: "deny"}},
	}
	if len(managedAllowRules(settings)) != 1 || len(managedDenyRules(settings)) != 1 {
		t.Fatalf("unexpected managed rules")
	}
	if managedAllowRules(nil) != nil || managedDenyRules(nil) != nil {
		t.Fatalf("expected nil rules for nil settings")
	}
}
