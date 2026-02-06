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
	if higher.CleanupPeriodDays != nil {
		result.CleanupPeriodDays = intPtr(*higher.CleanupPeriodDays)
	}
	result.CompanyAnnouncements = mergeStringSlices(lower.CompanyAnnouncements, higher.CompanyAnnouncements)
	result.Env = mergeMaps(lower.Env, higher.Env)
	if higher.IncludeCoAuthoredBy != nil {
		result.IncludeCoAuthoredBy = boolPtr(*higher.IncludeCoAuthoredBy)
	}
	result.Permissions = mergePermissions(lower.Permissions, higher.Permissions)
	result.DisallowedTools = mergeStringSlices(lower.DisallowedTools, higher.DisallowedTools)
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
	result.BashOutput = mergeBashOutput(lower.BashOutput, higher.BashOutput)
	result.ToolOutput = mergeToolOutput(lower.ToolOutput, higher.ToolOutput)
	result.AllowedMcpServers = mergeMCPServerRules(lower.AllowedMcpServers, higher.AllowedMcpServers)
	result.DeniedMcpServers = mergeMCPServerRules(lower.DeniedMcpServers, higher.DeniedMcpServers)
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

// mergeHooks merges hook matcher entries per event type.
// Higher-priority entries are appended after lower-priority ones.
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
	out.PreToolUse = mergeHookEntries(lower.PreToolUse, higher.PreToolUse)
	out.PostToolUse = mergeHookEntries(lower.PostToolUse, higher.PostToolUse)
	out.PostToolUseFailure = mergeHookEntries(lower.PostToolUseFailure, higher.PostToolUseFailure)
	out.PermissionRequest = mergeHookEntries(lower.PermissionRequest, higher.PermissionRequest)
	out.SessionStart = mergeHookEntries(lower.SessionStart, higher.SessionStart)
	out.SessionEnd = mergeHookEntries(lower.SessionEnd, higher.SessionEnd)
	out.SubagentStart = mergeHookEntries(lower.SubagentStart, higher.SubagentStart)
	out.SubagentStop = mergeHookEntries(lower.SubagentStop, higher.SubagentStop)
	out.Stop = mergeHookEntries(lower.Stop, higher.Stop)
	out.Notification = mergeHookEntries(lower.Notification, higher.Notification)
	out.UserPromptSubmit = mergeHookEntries(lower.UserPromptSubmit, higher.UserPromptSubmit)
	out.PreCompact = mergeHookEntries(lower.PreCompact, higher.PreCompact)
	return out
}

// mergeHookEntries concatenates lower and higher hook entries.
func mergeHookEntries(lower, higher []HookMatcherEntry) []HookMatcherEntry {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make([]HookMatcherEntry, 0, len(lower)+len(higher))
	out = append(out, cloneHookEntries(lower)...)
	out = append(out, cloneHookEntries(higher)...)
	return out
}

// cloneHookEntries deep-copies a slice of HookMatcherEntry.
func cloneHookEntries(src []HookMatcherEntry) []HookMatcherEntry {
	if len(src) == 0 {
		return nil
	}
	out := make([]HookMatcherEntry, len(src))
	for i, entry := range src {
		out[i] = HookMatcherEntry{Matcher: entry.Matcher}
		if len(entry.Hooks) > 0 {
			out[i].Hooks = make([]HookDefinition, len(entry.Hooks))
			copy(out[i].Hooks, entry.Hooks)
		}
	}
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

func mergeBashOutput(lower, higher *BashOutputConfig) *BashOutputConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneBashOutput(higher)
	}
	if higher == nil {
		return cloneBashOutput(lower)
	}
	out := cloneBashOutput(lower)
	if higher.SyncThresholdBytes != nil {
		v := *higher.SyncThresholdBytes
		out.SyncThresholdBytes = &v
	}
	if higher.AsyncThresholdBytes != nil {
		v := *higher.AsyncThresholdBytes
		out.AsyncThresholdBytes = &v
	}
	return out
}

func mergeToolOutput(lower, higher *ToolOutputConfig) *ToolOutputConfig {
	if lower == nil && higher == nil {
		return nil
	}
	if lower == nil {
		return cloneToolOutput(higher)
	}
	if higher == nil {
		return cloneToolOutput(lower)
	}
	out := cloneToolOutput(lower)
	if higher.DefaultThresholdBytes != 0 {
		out.DefaultThresholdBytes = higher.DefaultThresholdBytes
	}
	out.PerToolThresholdBytes = mergeIntMap(lower.PerToolThresholdBytes, higher.PerToolThresholdBytes)
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

func mergeIntMap(lower, higher map[string]int) map[string]int {
	if len(lower) == 0 && len(higher) == 0 {
		return nil
	}
	out := make(map[string]int, len(lower)+len(higher))
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
	out.DisallowedTools = mergeStringSlices(nil, src.DisallowedTools)
	out.Hooks = cloneHooks(src.Hooks)
	out.DisableAllHooks = cloneBoolPtr(src.DisableAllHooks)
	out.StatusLine = cloneStatusLine(src.StatusLine)
	out.Sandbox = cloneSandbox(src.Sandbox)
	out.BashOutput = cloneBashOutput(src.BashOutput)
	out.ToolOutput = cloneToolOutput(src.ToolOutput)
	out.AllowedMcpServers = mergeMCPServerRules(nil, src.AllowedMcpServers)
	out.DeniedMcpServers = mergeMCPServerRules(nil, src.DeniedMcpServers)
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
	out.PreToolUse = cloneHookEntries(src.PreToolUse)
	out.PostToolUse = cloneHookEntries(src.PostToolUse)
	out.PostToolUseFailure = cloneHookEntries(src.PostToolUseFailure)
	out.PermissionRequest = cloneHookEntries(src.PermissionRequest)
	out.SessionStart = cloneHookEntries(src.SessionStart)
	out.SessionEnd = cloneHookEntries(src.SessionEnd)
	out.SubagentStart = cloneHookEntries(src.SubagentStart)
	out.SubagentStop = cloneHookEntries(src.SubagentStop)
	out.Stop = cloneHookEntries(src.Stop)
	out.Notification = cloneHookEntries(src.Notification)
	out.UserPromptSubmit = cloneHookEntries(src.UserPromptSubmit)
	out.PreCompact = cloneHookEntries(src.PreCompact)
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

func cloneBashOutput(src *BashOutputConfig) *BashOutputConfig {
	if src == nil {
		return nil
	}
	out := *src
	if src.SyncThresholdBytes != nil {
		v := *src.SyncThresholdBytes
		out.SyncThresholdBytes = &v
	} else {
		out.SyncThresholdBytes = nil
	}
	if src.AsyncThresholdBytes != nil {
		v := *src.AsyncThresholdBytes
		out.AsyncThresholdBytes = &v
	} else {
		out.AsyncThresholdBytes = nil
	}
	return &out
}

func cloneToolOutput(src *ToolOutputConfig) *ToolOutputConfig {
	if src == nil {
		return nil
	}
	out := *src
	out.PerToolThresholdBytes = mergeIntMap(nil, src.PerToolThresholdBytes)
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

func cloneBoolPtr(v *bool) *bool {
	if v == nil {
		return nil
	}
	return boolPtr(*v)
}
