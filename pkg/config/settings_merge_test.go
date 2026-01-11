package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeSettingsDeepCopyAndOverrides(t *testing.T) {
	lower := &Settings{
		APIKeyHelper:         "lower-helper",
		CleanupPeriodDays:    30,
		CompanyAnnouncements: []string{"a"},
		Env:                  map[string]string{"K1": "V1", "shared": "low"},
		IncludeCoAuthoredBy:  boolPtr(true),
		Model:                "claude-3",
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes:  intPtr(100),
			AsyncThresholdBytes: intPtr(200),
		},
		Permissions: &PermissionsConfig{
			Allow:                 []string{"fs"},
			DefaultMode:           "askBeforeRunningTools",
			Ask:                   []string{"net"},
			AdditionalDirectories: []string{"/data"},
		},
		Hooks: &HooksConfig{
			PreToolUse: map[string]string{"bash": "echo low"},
		},
		Sandbox: &SandboxConfig{
			Enabled:          boolPtr(false),
			ExcludedCommands: []string{"rm"},
			Network: &SandboxNetworkConfig{
				AllowUnixSockets: []string{"/tmp/agent.sock"},
			},
		},
		EnabledPlugins: map[string]bool{"p1": true},
	}

	higher := &Settings{
		CleanupPeriodDays:    7,
		CompanyAnnouncements: []string{"b", "a"},
		Env:                  map[string]string{"K2": "V2", "shared": "high"},
		IncludeCoAuthoredBy:  boolPtr(false),
		Model:                "claude-3-5",
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes: intPtr(150),
		},
		Permissions: &PermissionsConfig{
			Allow:       []string{"fs", "net"},
			DefaultMode: "acceptEdits",
			Ask:         []string{"net"},
		},
		Hooks: &HooksConfig{
			PreToolUse:  map[string]string{"bash": "echo high"},
			PostToolUse: map[string]string{"bash": "echo done"},
		},
		Sandbox: &SandboxConfig{
			Enabled:          boolPtr(true),
			ExcludedCommands: []string{"sudo"},
			Network: &SandboxNetworkConfig{
				AllowUnixSockets: []string{"/tmp/agent.sock", "/var/run/docker.sock"},
				HTTPProxyPort:    intPtr(8080),
			},
		},
		EnabledPlugins: map[string]bool{"p2": true, "p1": false},
	}

	merged := MergeSettings(lower, higher)
	require.NotNil(t, merged)
	require.Equal(t, "claude-3-5", merged.Model)
	require.Equal(t, 7, merged.CleanupPeriodDays)
	require.Equal(t, map[string]string{"K1": "V1", "K2": "V2", "shared": "high"}, merged.Env)
	require.Equal(t, []string{"a", "b"}, merged.CompanyAnnouncements)
	require.NotSame(t, lower.Env, merged.Env)
	require.NotSame(t, higher.Env, merged.Env)
	require.False(t, *merged.IncludeCoAuthoredBy)

	require.Equal(t, []string{"fs", "net"}, merged.Permissions.Allow)
	require.Equal(t, []string{"net"}, merged.Permissions.Ask)
	require.Equal(t, "acceptEdits", merged.Permissions.DefaultMode)
	require.Equal(t, []string{"/data"}, merged.Permissions.AdditionalDirectories)

	require.Equal(t, map[string]string{"bash": "echo high"}, merged.Hooks.PreToolUse)
	require.Equal(t, map[string]string{"bash": "echo done"}, merged.Hooks.PostToolUse)

	require.True(t, *merged.Sandbox.Enabled)
	require.Equal(t, []string{"rm", "sudo"}, merged.Sandbox.ExcludedCommands)
	require.Equal(t, []string{"/tmp/agent.sock", "/var/run/docker.sock"}, merged.Sandbox.Network.AllowUnixSockets)
	require.Equal(t, 8080, *merged.Sandbox.Network.HTTPProxyPort)

	require.Equal(t, map[string]bool{"p1": false, "p2": true}, merged.EnabledPlugins)

	require.NotNil(t, merged.BashOutput)
	require.Equal(t, 150, *merged.BashOutput.SyncThresholdBytes)
	require.Equal(t, 200, *merged.BashOutput.AsyncThresholdBytes)
	require.NotSame(t, lower.BashOutput, merged.BashOutput)
	require.NotSame(t, lower.BashOutput.SyncThresholdBytes, merged.BashOutput.SyncThresholdBytes)

	// Ensure inputs untouched.
	require.Equal(t, "claude-3", lower.Model)
	require.Equal(t, map[string]string{"K1": "V1", "shared": "low"}, lower.Env)
	require.Nil(t, higher.Sandbox.Network.SocksProxyPort)
}

func TestMergePermissionsDeduplication(t *testing.T) {
	lower := &PermissionsConfig{
		Allow: []string{"a", "b"},
		Deny:  []string{"c"},
	}
	higher := &PermissionsConfig{
		Allow: []string{"b", "c"},
		Deny:  []string{"d"},
	}
	out := mergePermissions(lower, higher)
	require.Equal(t, []string{"a", "b", "c"}, out.Allow)
	require.Equal(t, []string{"c", "d"}, out.Deny)
}

func TestMergeStringSlicesHandlesNil(t *testing.T) {
	out := mergeStringSlices(nil, []string{"a", "a", "b"})
	require.Equal(t, []string{"a", "b"}, out)
	require.Nil(t, mergeStringSlices(nil, nil))
}

func TestMergeSettingsNestedFields(t *testing.T) {
	lower := &Settings{
		Model:       "base",
		Permissions: &PermissionsConfig{Allow: []string{"A"}, DefaultMode: "askBeforeRunningTools"},
		Hooks:       &HooksConfig{PreToolUse: map[string]string{"bash": "echo low"}},
		Sandbox:     &SandboxConfig{ExcludedCommands: []string{"rm"}, Network: &SandboxNetworkConfig{HTTPProxyPort: intPtr(8080)}},
		BashOutput:  &BashOutputConfig{SyncThresholdBytes: intPtr(30_000)},
		StatusLine:  &StatusLineConfig{Type: "command", Command: "echo"},
		MCP:         &MCPConfig{Servers: map[string]MCPServerConfig{"one": {Type: "stdio", Command: "bin"}}},
	}
	higher := &Settings{
		Model:       "override",
		Permissions: &PermissionsConfig{Ask: []string{"B"}, DefaultMode: "acceptEdits"},
		Hooks:       &HooksConfig{PostToolUse: map[string]string{"bash": "echo hi"}},
		Sandbox:     &SandboxConfig{Network: &SandboxNetworkConfig{SocksProxyPort: intPtr(9000)}},
		BashOutput:  &BashOutputConfig{AsyncThresholdBytes: intPtr(1024 * 1024)},
		StatusLine:  &StatusLineConfig{Type: "template", Template: "ok"},
		MCP:         &MCPConfig{Servers: map[string]MCPServerConfig{"two": {Type: "http", URL: "https://api"}}},
	}

	merged := MergeSettings(lower, higher)
	require.Equal(t, "override", merged.Model)
	require.Contains(t, merged.Permissions.Allow, "A")
	require.Contains(t, merged.Permissions.Ask, "B")
	require.Equal(t, "acceptEdits", merged.Permissions.DefaultMode)
	require.Equal(t, "echo low", merged.Hooks.PreToolUse["bash"])
	require.Equal(t, "echo hi", merged.Hooks.PostToolUse["bash"])
	require.Equal(t, 8080, *merged.Sandbox.Network.HTTPProxyPort)
	require.Equal(t, 9000, *merged.Sandbox.Network.SocksProxyPort)
	require.Equal(t, "template", merged.StatusLine.Type)
	require.Equal(t, "ok", merged.StatusLine.Template)
	require.Equal(t, "bin", merged.MCP.Servers["one"].Command)
	require.Equal(t, "https://api", merged.MCP.Servers["two"].URL)
	require.Equal(t, 30_000, *merged.BashOutput.SyncThresholdBytes)
	require.Equal(t, 1024*1024, *merged.BashOutput.AsyncThresholdBytes)
}

func TestMergeBashOutputNilCases(t *testing.T) {
	require.Nil(t, mergeBashOutput(nil, nil))

	higher := &BashOutputConfig{SyncThresholdBytes: intPtr(1)}
	merged := mergeBashOutput(nil, higher)
	require.NotNil(t, merged)
	require.Equal(t, 1, *merged.SyncThresholdBytes)
	require.NotSame(t, higher, merged)
	require.NotSame(t, higher.SyncThresholdBytes, merged.SyncThresholdBytes)

	lower := &BashOutputConfig{AsyncThresholdBytes: intPtr(2)}
	merged = mergeBashOutput(lower, nil)
	require.NotNil(t, merged)
	require.Equal(t, 2, *merged.AsyncThresholdBytes)
	require.NotSame(t, lower, merged)
	require.NotSame(t, lower.AsyncThresholdBytes, merged.AsyncThresholdBytes)
}

func TestMergeToolOutputDeepCopyAndOverrides(t *testing.T) {
	lower := &Settings{
		ToolOutput: &ToolOutputConfig{
			DefaultThresholdBytes: 100,
			PerToolThresholdBytes: map[string]int{
				"bash":      10,
				"file_read": 20,
			},
		},
	}
	higher := &Settings{
		ToolOutput: &ToolOutputConfig{
			DefaultThresholdBytes: 200,
			PerToolThresholdBytes: map[string]int{
				"bash": 15,
				"grep": 30,
			},
		},
	}

	merged := MergeSettings(lower, higher)
	require.NotNil(t, merged)
	require.NotNil(t, merged.ToolOutput)
	require.Equal(t, 200, merged.ToolOutput.DefaultThresholdBytes)
	require.Equal(t, map[string]int{
		"bash":      15,
		"file_read": 20,
		"grep":      30,
	}, merged.ToolOutput.PerToolThresholdBytes)
	require.NotSame(t, lower.ToolOutput, merged.ToolOutput)
	require.NotSame(t, lower.ToolOutput.PerToolThresholdBytes, merged.ToolOutput.PerToolThresholdBytes)
}

func intPtr(v int) *int { return &v }
