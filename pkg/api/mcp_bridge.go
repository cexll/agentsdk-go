package api

import (
	"fmt"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/cexll/agentsdk-go/pkg/plugins"
)

type mcpServer struct {
	Name string
	Spec string
	URL  string
}

// collectMCPServers merges explicit API inputs, settings.json entries, and
// plugin-provided .mcp.json servers into a deduplicated list that respects
// managed allow/deny policies.
func collectMCPServers(settings *config.Settings, plugins []*plugins.ClaudePlugin, explicit []string) []string {
	seen := map[string]struct{}{}
	var servers []string
	allowRules := managedAllowRules(settings)
	denyRules := managedDenyRules(settings)

	add := func(name, spec, url string) {
		spec = strings.TrimSpace(spec)
		if spec == "" {
			return
		}
		if !allowedByManagedPolicies(name, url, allowRules, denyRules) {
			return
		}
		if _, ok := seen[spec]; ok {
			return
		}
		seen[spec] = struct{}{}
		servers = append(servers, spec)
	}

	for _, spec := range explicit {
		add("", spec, spec)
	}

	if settings != nil && settings.MCP != nil {
		for name, cfg := range settings.MCP.Servers {
			// Convert MCPServerConfig to spec string
			spec := ""
			switch cfg.Type {
			case "http", "sse":
				spec = cfg.URL
			case "stdio":
				spec = fmt.Sprintf("stdio://%s %s", cfg.Command, strings.Join(cfg.Args, " "))
			default:
				if cfg.URL != "" {
					spec = cfg.URL
				}
			}
			add(name, spec, cfg.URL)
		}
	}

	for _, entry := range pluginMCPServers(settings, plugins) {
		add(entry.Name, entry.Spec, entry.URL)
	}
	return servers
}

func pluginMCPServers(settings *config.Settings, plugins []*plugins.ClaudePlugin) []mcpServer {
	var entries []mcpServer
	if len(plugins) == 0 {
		return entries
	}
	allowAll := settings != nil && settings.EnableAllProjectMCPServers != nil && *settings.EnableAllProjectMCPServers
	enabled := stringSet(settingsEnabledMCP(settings))
	disabled := stringSet(settingsDisabledMCP(settings))
	allowRules := managedAllowRules(settings)
	denyRules := managedDenyRules(settings)

	for _, plug := range plugins {
		if plug == nil || plug.MCPConfig == nil || len(plug.MCPConfig.Data) == 0 {
			continue
		}
		for _, server := range parseMCPConfig(plug) {
			name := server.Name
			if name == "" {
				name = plug.Name
			}
			if !allowedByManagedPolicies(name, server.URL, allowRules, denyRules) {
				continue
			}
			if disabled[name] {
				continue
			}
			if len(enabled) > 0 && !enabled[name] {
				continue
			}
			if !allowAll && len(enabled) == 0 && name == "" {
				// No explicit allow; keep secure default.
				continue
			}
			entries = append(entries, mcpServer{Name: name, Spec: server.Spec, URL: server.URL})
		}
	}
	return entries
}

// parseMCPConfig best-effort extracts server specs from .mcp.json content.
func parseMCPConfig(plug *plugins.ClaudePlugin) []mcpServer {
	var out []mcpServer
	if plug == nil || plug.MCPConfig == nil {
		return out
	}
	raw, ok := plug.MCPConfig.Data["servers"]
	if !ok {
		return out
	}
	list, ok := raw.([]any)
	if !ok {
		return out
	}
	for _, entry := range list {
		spec, name, url := parseMCPEntry(entry)
		if strings.TrimSpace(spec) == "" {
			continue
		}
		out = append(out, mcpServer{Name: name, Spec: spec, URL: url})
	}
	return out
}

func parseMCPEntry(entry any) (spec, name, url string) {
	obj, ok := entry.(map[string]any)
	if !ok {
		return "", "", ""
	}
	if n, ok := obj["name"]; ok {
		name, _ = anyToString(n)
	}

	if rawURL, ok := obj["url"]; ok {
		url, _ = anyToString(rawURL)
		spec = url
	}

	if cmd, ok := obj["command"]; ok && spec == "" {
		command, _ := anyToString(cmd)
		args := stringSlice(obj["args"])
		spec = strings.TrimSpace("stdio://" + strings.TrimSpace(command+" "+strings.Join(args, " ")))
	}

	if spec == "" {
		return "", name, ""
	}
	if url == "" {
		url = spec
	}
	return spec, name, url
}

func settingsEnabledMCP(s *config.Settings) []string {
	if s == nil {
		return nil
	}
	return s.EnabledMCPJSONServers
}

func settingsDisabledMCP(s *config.Settings) []string {
	if s == nil {
		return nil
	}
	return s.DisabledMCPJSONServers
}

func managedAllowRules(s *config.Settings) []config.MCPServerRule {
	if s == nil {
		return nil
	}
	return s.AllowedMcpServers
}

func managedDenyRules(s *config.Settings) []config.MCPServerRule {
	if s == nil {
		return nil
	}
	return s.DeniedMcpServers
}

func allowedByManagedPolicies(name, target string, allow, deny []config.MCPServerRule) bool {
	if matchesRule(name, target, deny) {
		return false
	}
	if len(allow) == 0 {
		return true
	}
	return matchesRule(name, target, allow)
}

func matchesRule(name, target string, rules []config.MCPServerRule) bool {
	for _, rule := range rules {
		if rule.ServerName != "" && !strings.EqualFold(rule.ServerName, name) {
			continue
		}
		if rule.URL != "" && !strings.EqualFold(rule.URL, target) {
			continue
		}
		if strings.TrimSpace(rule.ServerName) == "" && strings.TrimSpace(rule.URL) == "" {
			continue
		}
		return true
	}
	return false
}

func stringSet(items []string) map[string]bool {
	if len(items) == 0 {
		return nil
	}
	set := make(map[string]bool, len(items))
	for _, item := range items {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		set[item] = true
	}
	return set
}

// Debug helper to stringify managed rule decisions in error messages.
func describeRule(rule config.MCPServerRule) string {
	switch {
	case rule.ServerName != "" && rule.URL != "":
		return fmt.Sprintf("%s (%s)", rule.ServerName, rule.URL)
	case rule.ServerName != "":
		return rule.ServerName
	default:
		return rule.URL
	}
}
