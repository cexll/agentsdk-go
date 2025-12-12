package config

// This file provides pure, allocation-safe merge helpers for Settings.
// All functions return new objects and never mutate inputs.

// MergeSettings deep-merges two Settings structs (lower <- higher) and returns a new instance.
// - Scalars: higher non-zero values override lower.
// - *bool / *int pointers: higher non-nil overrides lower.
// - Maps: merged per key with higher entries overriding.
// - []string: concatenated with de-duplication, preserving order.
// - Nested structs: merged recursively.
func MergeSettings(lower, higher *Settings) *Settings {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneSettings(higher)
	}
	if higher == nil {
		return cloneSettings(lower)
	}

	result := cloneSettings(lower)

	if higher.APIKeyHelper != "" {
		result.APIKeyHelper = higher.APIKeyHelper
	}
	if higher.CleanupPeriodDays != 0 {
		result.CleanupPeriodDays = higher.CleanupPeriodDays
	}
	result.CompanyAnnouncements = mergeStringSlices(lower.CompanyAnnouncements, higher.CompanyAnnouncements)
	result.Env = mergeMaps(lower.Env, higher.Env)
	if higher.IncludeCoAuthoredBy != nil {
		result.IncludeCoAuthoredBy = boolPtr(*higher.IncludeCoAuthoredBy)
	}
	result.Permissions = mergePermissions(lower.Permissions, higher.Permissions)
	result.Hooks = mergeHooks(lower.Hooks, higher.Hooks)
	if higher.DisableAllHooks != nil {
		result.DisableAllHooks = boolPtr(*higher.DisableAllHooks)
	}
	if higher.Model != "" {
		result.Model = higher.Model
	}
	result.MCP = mergeMCPConfig(lower.MCP, higher.MCP)
	result.LegacyMCPServers = mergeStringSlices(lower.LegacyMCPServers, higher.LegacyMCPServers)
	result.StatusLine = mergeStatusLine(lower.StatusLine, higher.StatusLine)
	if higher.OutputStyle != "" {
		result.OutputStyle = higher.OutputStyle
	}
	if higher.ForceLoginMethod != "" {
		result.ForceLoginMethod = higher.ForceLoginMethod
	}
	if higher.ForceLoginOrgUUID != "" {
		result.ForceLoginOrgUUID = higher.ForceLoginOrgUUID
	}
	result.Sandbox = mergeSandbox(lower.Sandbox, higher.Sandbox)
	if higher.EnableAllProjectMCPServers != nil {
		result.EnableAllProjectMCPServers = boolPtr(*higher.EnableAllProjectMCPServers)
	}
	result.EnabledMCPJSONServers = mergeStringSlices(lower.EnabledMCPJSONServers, higher.EnabledMCPJSONServers)
	result.DisabledMCPJSONServers = mergeStringSlices(lower.DisabledMCPJSONServers, higher.DisabledMCPJSONServers)
	result.AllowedMcpServers = mergeMCPServerRules(lower.AllowedMcpServers, higher.AllowedMcpServers)
	result.DeniedMcpServers = mergeMCPServerRules(lower.DeniedMcpServers, higher.DeniedMcpServers)
	result.EnabledPlugins = mergeBoolMap(lower.EnabledPlugins, higher.EnabledPlugins)
	result.ExtraKnownMarketplaces = mergeMarketplaceSources(lower.ExtraKnownMarketplaces, higher.ExtraKnownMarketplaces)
	if higher.AWSAuthRefresh != "" {
		result.AWSAuthRefresh = higher.AWSAuthRefresh
	}
	if higher.AWSCredentialExport != "" {
		result.AWSCredentialExport = higher.AWSCredentialExport
	}
	return result
}

// mergePermissions merges permission lists with de-duplication and overrides scalar fields.
func mergePermissions(lower, higher *PermissionsConfig) *PermissionsConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return clonePermissions(higher)
	}
	if higher == nil {
		return clonePermissions(lower)
	}
	out := clonePermissions(lower)
	out.Allow = mergeStringSlices(lower.Allow, higher.Allow)
	out.Ask = mergeStringSlices(lower.Ask, higher.Ask)
	out.Deny = mergeStringSlices(lower.Deny, higher.Deny)
	out.AdditionalDirectories = mergeStringSlices(lower.AdditionalDirectories, higher.AdditionalDirectories)
	if higher.DefaultMode != "" {
		out.DefaultMode = higher.DefaultMode
	}
	if higher.DisableBypassPermissionsMode != "" {
		out.DisableBypassPermissionsMode = higher.DisableBypassPermissionsMode
	}
	return out
}

// mergeHooks merges hook command maps per tool name.
func mergeHooks(lower, higher *HooksConfig) *HooksConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneHooks(higher)
	}
	if higher == nil {
		return cloneHooks(lower)
	}
	out := cloneHooks(lower)
	out.PreToolUse = mergeMaps(lower.PreToolUse, higher.PreToolUse)
	out.PostToolUse = mergeMaps(lower.PostToolUse, higher.PostToolUse)
	out.PermissionRequest = mergeMaps(lower.PermissionRequest, higher.PermissionRequest)
	out.SessionStart = mergeMaps(lower.SessionStart, higher.SessionStart)
	out.SessionEnd = mergeMaps(lower.SessionEnd, higher.SessionEnd)
	out.SubagentStart = mergeMaps(lower.SubagentStart, higher.SubagentStart)
	out.SubagentStop = mergeMaps(lower.SubagentStop, higher.SubagentStop)
	return out
}

// mergeSandbox merges sandbox settings and their nested network config.
func mergeSandbox(lower, higher *SandboxConfig) *SandboxConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneSandbox(higher)
	}
	if higher == nil {
		return cloneSandbox(lower)
	}
	out := cloneSandbox(lower)
	if higher.Enabled != nil {
		out.Enabled = boolPtr(*higher.Enabled)
	}
	if higher.AutoAllowBashIfSandboxed != nil {
		out.AutoAllowBashIfSandboxed = boolPtr(*higher.AutoAllowBashIfSandboxed)
	}
	out.ExcludedCommands = mergeStringSlices(lower.ExcludedCommands, higher.ExcludedCommands)
	if higher.AllowUnsandboxedCommands != nil {
		out.AllowUnsandboxedCommands = boolPtr(*higher.AllowUnsandboxedCommands)
	}
	if higher.EnableWeakerNestedSandbox != nil {
		out.EnableWeakerNestedSandbox = boolPtr(*higher.EnableWeakerNestedSandbox)
	}
	out.Network = mergeSandboxNetwork(lower.Network, higher.Network)
	return out
}

// mergeSandboxNetwork merges network-level sandbox knobs.
func mergeSandboxNetwork(lower, higher *SandboxNetworkConfig) *SandboxNetworkConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneSandboxNetwork(higher)
	}
	if higher == nil {
		return cloneSandboxNetwork(lower)
	}
	out := cloneSandboxNetwork(lower)
	out.AllowUnixSockets = mergeStringSlices(lower.AllowUnixSockets, higher.AllowUnixSockets)
	if higher.AllowLocalBinding != nil {
		out.AllowLocalBinding = boolPtr(*higher.AllowLocalBinding)
	}
	if higher.HTTPProxyPort != nil {
		v := *higher.HTTPProxyPort
		out.HTTPProxyPort = &v
	}
	if higher.SocksProxyPort != nil {
		v := *higher.SocksProxyPort
		out.SocksProxyPort = &v
	}
	return out
}

// mergeMaps merges string maps; higher values override lower keys.
func mergeMaps(lower, higher map[string]string) map[string]string {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]string, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range higher {
		out[k] = v
	}
	return out
}

// mergeStringSlices appends slices and removes duplicates while preserving order.
func mergeStringSlices(lower, higher []string) []string {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(lower)+len(higher))
	out := make([]string, 0, len(lower)+len(higher))
	for _, v := range lower {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	for _, v := range higher {
		if _, ok := seen[v]; ok {
			continue
		}
		seen[v] = struct{}{}
		out = append(out, v)
	}
	return out
}

// mergeBoolMap merges map[string]bool with higher overriding.
func mergeBoolMap(lower, higher map[string]bool) map[string]bool {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]bool, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range higher {
		out[k] = v
	}
	return out
}

// mergeMarketplaceSources merges marketplace sources by key.
func mergeMarketplaceSources(lower, higher map[string]MarketplaceSource) map[string]MarketplaceSource {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]MarketplaceSource, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = v
	}
	for k, v := range higher {
		out[k] = v
	}
	return out
}

// mergeMarketplaceConfigMap merges marketplace configs (map[string]*MarketplaceConfig) per key.
// nolint:unused // kept for forward-compat when marketplace layering lands.
func mergeMarketplaceConfigMap(lower, higher map[string]*MarketplaceConfig) map[string]*MarketplaceConfig {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]*MarketplaceConfig, len(lower)+len(higher))
	for k, v := range lower {
		out[k] = cloneMarketplaceConfig(v)
	}
	for k, v := range higher {
		out[k] = cloneMarketplaceConfig(v)
		if base, ok := lower[k]; ok && v != nil && base != nil {
			out[k] = mergeMarketplaceConfig(base, v)
		}
	}
	return out
}

// mergeMarketplaceConfig merges two MarketplaceConfig pointers.
// nolint:unused // kept for forward-compat when marketplace layering lands.
func mergeMarketplaceConfig(lower, higher *MarketplaceConfig) *MarketplaceConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneMarketplaceConfig(higher)
	}
	if higher == nil {
		return cloneMarketplaceConfig(lower)
	}
	out := cloneMarketplaceConfig(lower)
	out.EnabledPlugins = mergeBoolMap(lower.EnabledPlugins, higher.EnabledPlugins)
	out.ExtraKnownMarketplaces = mergeMarketplaceSources(lower.ExtraKnownMarketplaces, higher.ExtraKnownMarketplaces)
	return out
}

func mergeStatusLine(lower, higher *StatusLineConfig) *StatusLineConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneStatusLine(higher)
	}
	if higher == nil {
		return cloneStatusLine(lower)
	}
	out := cloneStatusLine(lower)
	if higher.Type != "" {
		out.Type = higher.Type
	}
	if higher.Command != "" {
		out.Command = higher.Command
	}
	if higher.Template != "" {
		out.Template = higher.Template
	}
	if higher.IntervalSeconds != 0 {
		out.IntervalSeconds = higher.IntervalSeconds
	}
	if higher.TimeoutSeconds != 0 {
		out.TimeoutSeconds = higher.TimeoutSeconds
	}
	return out
}

func mergeMCPConfig(lower, higher *MCPConfig) *MCPConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneMCPConfig(higher)
	}
	if higher == nil {
		return cloneMCPConfig(lower)
	}
	out := cloneMCPConfig(lower)
	if len(higher.Servers) > 0 {
		if out.Servers == nil {
			out.Servers = make(map[string]MCPServerConfig, len(higher.Servers))
		}
		for name, cfg := range higher.Servers {
			out.Servers[name] = cloneMCPServerConfig(cfg)
		}
	}
	return out
}

func mergeMCPServerRules(lower, higher []MCPServerRule) []MCPServerRule {
	if len(higher) > 0 {
		return append([]MCPServerRule(nil), higher...)
	}
	if len(lower) == 0 {
		return nil
	}
	return append([]MCPServerRule(nil), lower...)
}

// --- cloning helpers (keep private to avoid aliasing callers) ---

func cloneSettings(src *Settings) *Settings {
	if src == nil {
		return nil
	}
	out := *src
	out.CompanyAnnouncements = mergeStringSlices(nil, src.CompanyAnnouncements)
	out.Env = mergeMaps(nil, src.Env)
	out.IncludeCoAuthoredBy = cloneBoolPtr(src.IncludeCoAuthoredBy)
	out.Permissions = clonePermissions(src.Permissions)
	out.Hooks = cloneHooks(src.Hooks)
	out.DisableAllHooks = cloneBoolPtr(src.DisableAllHooks)
	out.StatusLine = cloneStatusLine(src.StatusLine)
	out.Sandbox = cloneSandbox(src.Sandbox)
	out.EnableAllProjectMCPServers = cloneBoolPtr(src.EnableAllProjectMCPServers)
	out.EnabledMCPJSONServers = mergeStringSlices(nil, src.EnabledMCPJSONServers)
	out.DisabledMCPJSONServers = mergeStringSlices(nil, src.DisabledMCPJSONServers)
	out.AllowedMcpServers = mergeMCPServerRules(nil, src.AllowedMcpServers)
	out.DeniedMcpServers = mergeMCPServerRules(nil, src.DeniedMcpServers)
	out.EnabledPlugins = mergeBoolMap(nil, src.EnabledPlugins)
	out.ExtraKnownMarketplaces = mergeMarketplaceSources(nil, src.ExtraKnownMarketplaces)
	out.MCP = cloneMCPConfig(src.MCP)
	out.LegacyMCPServers = mergeStringSlices(nil, src.LegacyMCPServers)
	return &out
}

func clonePermissions(src *PermissionsConfig) *PermissionsConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.Allow = mergeStringSlices(nil, src.Allow)
	out.Ask = mergeStringSlices(nil, src.Ask)
	out.Deny = mergeStringSlices(nil, src.Deny)
	out.AdditionalDirectories = mergeStringSlices(nil, src.AdditionalDirectories)
	return &out
}

func cloneHooks(src *HooksConfig) *HooksConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.PreToolUse = mergeMaps(nil, src.PreToolUse)
	out.PostToolUse = mergeMaps(nil, src.PostToolUse)
	out.PermissionRequest = mergeMaps(nil, src.PermissionRequest)
	out.SessionStart = mergeMaps(nil, src.SessionStart)
	out.SessionEnd = mergeMaps(nil, src.SessionEnd)
	out.SubagentStart = mergeMaps(nil, src.SubagentStart)
	out.SubagentStop = mergeMaps(nil, src.SubagentStop)
	return &out
}

func cloneSandbox(src *SandboxConfig) *SandboxConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.Enabled = cloneBoolPtr(src.Enabled)
	out.AutoAllowBashIfSandboxed = cloneBoolPtr(src.AutoAllowBashIfSandboxed)
	out.ExcludedCommands = mergeStringSlices(nil, src.ExcludedCommands)
	out.AllowUnsandboxedCommands = cloneBoolPtr(src.AllowUnsandboxedCommands)
	out.EnableWeakerNestedSandbox = cloneBoolPtr(src.EnableWeakerNestedSandbox)
	out.Network = cloneSandboxNetwork(src.Network)
	return &out
}

func cloneSandboxNetwork(src *SandboxNetworkConfig) *SandboxNetworkConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.AllowUnixSockets = mergeStringSlices(nil, src.AllowUnixSockets)
	if src.HTTPProxyPort != nil {
		v := *src.HTTPProxyPort
		out.HTTPProxyPort = &v
	}
	if src.SocksProxyPort != nil {
		v := *src.SocksProxyPort
		out.SocksProxyPort = &v
	}
	out.AllowLocalBinding = cloneBoolPtr(src.AllowLocalBinding)
	return &out
}

func cloneMCPConfig(src *MCPConfig) *MCPConfig {
	if src == nil {
		return nil
	}
	out := &MCPConfig{}
	if len(src.Servers) > 0 {
		out.Servers = make(map[string]MCPServerConfig, len(src.Servers))
		for name, cfg := range src.Servers {
			out.Servers[name] = cloneMCPServerConfig(cfg)
		}
	}
	return out
}

func cloneMCPServerConfig(src MCPServerConfig) MCPServerConfig {
	out := src
	out.Args = mergeStringSlices(nil, src.Args)
	out.Env = mergeMaps(nil, src.Env)
	out.Headers = mergeMaps(nil, src.Headers)
	return out
}

func cloneStatusLine(src *StatusLineConfig) *StatusLineConfig {
	if src == nil {
		return nil
	}
	out := *src
	return &out
}

// nolint:unused // kept for forward-compat when marketplace layering lands.
func cloneMarketplaceConfig(src *MarketplaceConfig) *MarketplaceConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.EnabledPlugins = mergeBoolMap(nil, src.EnabledPlugins)
	out.ExtraKnownMarketplaces = mergeMarketplaceSources(nil, src.ExtraKnownMarketplaces)
	return &out
}

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	return boolPtr(*v)
}
