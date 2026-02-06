package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGetDefaultSettingsValues(t *testing.T) {
	cfg := GetDefaultSettings()
	require.NotNil(t, cfg.CleanupPeriodDays)
	require.Equal(t, 30, *cfg.CleanupPeriodDays)
	require.True(t, *cfg.IncludeCoAuthoredBy)
	require.False(t, *cfg.DisableAllHooks)
	require.NotNil(t, cfg.BashOutput)
	require.NotNil(t, cfg.BashOutput.SyncThresholdBytes)
	require.NotNil(t, cfg.BashOutput.AsyncThresholdBytes)
	require.Equal(t, 30_000, *cfg.BashOutput.SyncThresholdBytes)
	require.Equal(t, 1024*1024, *cfg.BashOutput.AsyncThresholdBytes)
	require.Equal(t, "askBeforeRunningTools", cfg.Permissions.DefaultMode)
	require.NotNil(t, cfg.Sandbox)
	require.False(t, *cfg.Sandbox.Enabled)
	require.True(t, *cfg.Sandbox.AutoAllowBashIfSandboxed)
	require.True(t, *cfg.Sandbox.AllowUnsandboxedCommands)
	require.False(t, *cfg.Sandbox.EnableWeakerNestedSandbox)
	require.False(t, *cfg.Sandbox.Network.AllowLocalBinding)
}

func TestPermissionsValidateErrors(t *testing.T) {
	p := &PermissionsConfig{
		DefaultMode:                  "invalid",
		DisableBypassPermissionsMode: "maybe",
		AdditionalDirectories:        []string{""},
	}
	err := p.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "defaultMode")
	require.Contains(t, err.Error(), "disableBypassPermissionsMode")
	require.Contains(t, err.Error(), "permissions.additionalDirectories[0]")
}

func TestHooksValidateErrors(t *testing.T) {
	h := &HooksConfig{
		PreToolUse: []HookMatcherEntry{
			{Matcher: "bad[", Hooks: []HookDefinition{{Type: "command", Command: "echo"}}},
		},
	}
	err := h.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "hooks.PreToolUse")
}

func TestSandboxValidateErrors(t *testing.T) {
	http, socks := 0, 70000
	s := &SandboxConfig{
		ExcludedCommands: []string{""},
		Network: &SandboxNetworkConfig{
			HTTPProxyPort:  &http,
			SocksProxyPort: &socks,
		},
	}
	err := s.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "sandbox.excludedCommands[0]")
	require.Contains(t, err.Error(), "httpProxyPort")
	require.Contains(t, err.Error(), "socksProxyPort")
}

func TestMCPServerRuleValidate(t *testing.T) {
	var rule MCPServerRule
	err := rule.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "serverName")

	require.NoError(t, MCPServerRule{ServerName: "ok"}.Validate())
}

func TestStatusLineValidate(t *testing.T) {
	err := (&StatusLineConfig{Type: "command"}).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "statusLine.command")

	err = (&StatusLineConfig{Type: "template"}).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "statusLine.template")

	err = (&StatusLineConfig{Type: "unknown"}).Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "not supported")

	cfg := &StatusLineConfig{
		Type:            "template",
		Template:        "ok",
		IntervalSeconds: -1,
		TimeoutSeconds:  -2,
	}
	err = cfg.Validate()
	require.Error(t, err)
	require.Contains(t, err.Error(), "intervalSeconds")
	require.Contains(t, err.Error(), "timeoutSeconds")

	require.NoError(t, (&StatusLineConfig{Type: "template", Template: "ok"}).Validate())
}
