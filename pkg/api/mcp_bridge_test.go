package api

import (
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestAllowedByManagedPoliciesPrefersDeny(t *testing.T) {
	deny := []config.MCPServerRule{{ServerName: "svc"}, {URL: "http://svc"}}
	allow := []config.MCPServerRule{{ServerName: "svc"}}
	if allowed := allowedByManagedPolicies("svc", "http://svc", allow, deny); allowed {
		t.Fatal("deny rule should win over allow list")
	}
}

func TestCollectMCPServersMergesSourcesAndDedups(t *testing.T) {
	settings := &config.Settings{
		MCP: &config.MCPConfig{
			Servers: map[string]config.MCPServerConfig{
				"settings-server": {Type: "http", URL: "http://settings.example"},
			},
		},
	}
	servers := collectMCPServers(settings, []string{"http://settings.example", "http://other.example"})
	if len(servers) != 2 {
		t.Fatalf("expected deduped two servers, got %d: %+v", len(servers), servers)
	}
}

func TestCollectMCPServersPreservesSettingsOptions(t *testing.T) {
	settings := &config.Settings{
		MCP: &config.MCPConfig{
			Servers: map[string]config.MCPServerConfig{
				"api": {
					Type:           "http",
					URL:            "http://settings.example",
					Headers:        map[string]string{"Authorization": "Bearer x"},
					Env:            map[string]string{"K": "V"},
					TimeoutSeconds: 7,
				},
			},
		},
	}
	servers := collectMCPServers(settings, nil)
	if len(servers) != 1 {
		t.Fatalf("expected one server, got %d: %+v", len(servers), servers)
	}
	if servers[0].Name != "api" {
		t.Fatalf("expected api server name, got %q", servers[0].Name)
	}
	if servers[0].TimeoutSeconds != 7 {
		t.Fatalf("expected timeoutSeconds=7, got %d", servers[0].TimeoutSeconds)
	}
	if servers[0].Headers["Authorization"] != "Bearer x" {
		t.Fatalf("expected headers propagated, got %+v", servers[0].Headers)
	}
	if servers[0].Env["K"] != "V" {
		t.Fatalf("expected env propagated, got %+v", servers[0].Env)
	}
}

func TestMatchesRuleIgnoresEmptyRule(t *testing.T) {
	rules := []config.MCPServerRule{{}}
	if matchesRule("svc", "http://example", rules) {
		t.Fatal("blank rule should not match anything")
	}
	rules = []config.MCPServerRule{{URL: "HTTP://EXAMPLE"}}
	if !matchesRule("svc", "http://example", rules) {
		t.Fatal("expected case-insensitive URL match")
	}
}

func TestCollectMCPServersStdioSpec(t *testing.T) {
	settings := &config.Settings{
		MCP: &config.MCPConfig{
			Servers: map[string]config.MCPServerConfig{
				"stdio": {Type: "stdio", Command: "echo", Args: []string{"hi"}},
			},
		},
	}
	servers := collectMCPServers(settings, nil)
	if len(servers) != 1 {
		t.Fatalf("expected one server, got %d", len(servers))
	}
	if servers[0].Spec != "stdio://echo hi" {
		t.Fatalf("unexpected stdio spec %q", servers[0].Spec)
	}
}

func TestAllowedByManagedPoliciesAllowList(t *testing.T) {
	allow := []config.MCPServerRule{{URL: "http://allowed"}}
	if !allowedByManagedPolicies("svc", "http://allowed", allow, nil) {
		t.Fatalf("expected allowlist to permit matching target")
	}
	if allowedByManagedPolicies("svc", "http://denied", allow, nil) {
		t.Fatalf("expected allowlist to deny non-matching target")
	}
}
