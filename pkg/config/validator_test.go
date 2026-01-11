package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestValidateSettingsSuccess(t *testing.T) {
	httpPort, socksPort := 8080, 1080
	s := &Settings{
		Model: "claude-3",
		ToolOutput: &ToolOutputConfig{
			DefaultThresholdBytes: 0,
			PerToolThresholdBytes: map[string]int{"bash": 1},
		},
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes:  intPtr(30_000),
			AsyncThresholdBytes: intPtr(1024 * 1024),
		},
		Permissions: &PermissionsConfig{
			DefaultMode: "acceptEdits",
			Allow:       []string{"Bash(git:*)"},
			Ask:         []string{"Read(*.go)"},
			Deny:        []string{"Glob(**/*)"},
		},
		Hooks: &HooksConfig{
			PreToolUse:  map[string]string{"bash": "echo pre"},
			PostToolUse: map[string]string{"file_read": "echo post"},
		},
		Sandbox: &SandboxConfig{
			ExcludedCommands: []string{"shutdown", "reboot"},
			Network: &SandboxNetworkConfig{
				HTTPProxyPort:  &httpPort,
				SocksProxyPort: &socksPort,
			},
		},
		EnabledPlugins: map[string]bool{"custom-plugin@official": true},
		ExtraKnownMarketplaces: map[string]MarketplaceSource{
			"official": {Source: "github"},
		},
		StatusLine:       &StatusLineConfig{Type: "command", Command: "echo ok"},
		ForceLoginMethod: "claudeai",
	}

	require.NoError(t, ValidateSettings(s))
}

func TestValidateToolOutputConfigRejectsInvalidThresholds(t *testing.T) {
	s := &Settings{
		Model: "claude-3",
		ToolOutput: &ToolOutputConfig{
			DefaultThresholdBytes: -1,
			PerToolThresholdBytes: map[string]int{
				"Bash": 0,
			},
		},
	}

	err := ValidateSettings(s)
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "toolOutput.defaultThresholdBytes")
	require.Contains(t, msg, "toolOutput.perToolThresholdBytes")
}

func TestValidateSettingsAggregatesErrors(t *testing.T) {
	badHTTP, badSocks := 0, 70000
	s := &Settings{
		Model: "",
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes:  intPtr(0),
			AsyncThresholdBytes: intPtr(-1),
		},
		Permissions: &PermissionsConfig{
			DefaultMode: "invalid",
			Allow:       []string{"tool()"},
		},
		Hooks: &HooksConfig{
			PreToolUse: map[string]string{"bad[": "", "bash": ""},
		},
		Sandbox: &SandboxConfig{
			ExcludedCommands: []string{"", "  "},
			Network: &SandboxNetworkConfig{
				HTTPProxyPort:  &badHTTP,
				SocksProxyPort: &badSocks,
			},
		},
		EnabledPlugins: map[string]bool{"broken-key": true},
		ExtraKnownMarketplaces: map[string]MarketplaceSource{
			"broken": {Source: "unknown"},
		},
		StatusLine:       &StatusLineConfig{Type: "unknown"},
		ForceLoginMethod: "invalid",
	}

	err := ValidateSettings(s)
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "model is required")
	require.Contains(t, msg, "permissions.defaultMode")
	require.Contains(t, msg, "permissions.allow[0]")
	require.Contains(t, msg, "hooks.preToolUse")
	require.Contains(t, msg, "sandbox.network.httpProxyPort")
	require.Contains(t, msg, "sandbox.network.socksProxyPort")
	require.Contains(t, msg, "sandbox.excludedCommands[0]")
	require.Contains(t, msg, "bashOutput.syncThresholdBytes")
	require.Contains(t, msg, "bashOutput.asyncThresholdBytes")
	require.Contains(t, msg, "enabledPlugins[broken-key]")
	require.Contains(t, msg, "extraKnownMarketplaces[broken]")
	require.Contains(t, msg, "statusLine.type")
	require.Contains(t, msg, "forceLoginMethod")
}

func TestValidatePermissionRule(t *testing.T) {
	valid := []string{"Bash(ls -la)", "Read(*.go)", "tool_1(target)", "mcp.server/list-files", "SimpleTool"}
	for _, rule := range valid {
		require.NoErrorf(t, validatePermissionRule(rule), "expected rule %s to be valid", rule)
	}

	invalid := []string{"", "tool()", "bad tool(target)", "tool(target)(extra)"}
	for _, rule := range invalid {
		require.Error(t, validatePermissionRule(rule), "rule %q should be invalid", rule)
	}
}

func TestValidateToolName_Boundaries(t *testing.T) {
	require.NoError(t, validateToolName("Tool_1-ok"))
	require.ErrorContains(t, validateToolName(""), "empty")
	require.ErrorContains(t, validateToolName("1bad"), "must match")
}

func TestValidatePortRangeBoundaries(t *testing.T) {
	require.NoError(t, validatePortRange(1))
	require.NoError(t, validatePortRange(65535))
	require.Error(t, validatePortRange(0))
	require.Error(t, validatePortRange(70000))
}

func TestValidatePluginKeyCases(t *testing.T) {
	require.NoError(t, validatePluginKey("plug-1@market"))
	for _, key := range []string{"", "missingatsign", "plug@", "@market", "bad$@market"} {
		require.Error(t, validatePluginKey(key))
	}
}

func TestValidateMarketplaceSource(t *testing.T) {
	require.Error(t, validateMarketplaceSource(nil))
	require.ErrorContains(t, validateMarketplaceSource(&MarketplaceSource{}), "empty")
	require.ErrorContains(t, validateMarketplaceSource(&MarketplaceSource{Source: "s3"}), "unsupported")
	require.NoError(t, validateMarketplaceSource(&MarketplaceSource{Source: "git"}))
	require.NoError(t, validateMarketplaceSource(&MarketplaceSource{Source: "github"}))
	require.NoError(t, validateMarketplaceSource(&MarketplaceSource{Source: "directory"}))
}

func TestValidatePermissionsConfig_DisableAndDirs(t *testing.T) {
	p := &PermissionsConfig{
		DefaultMode:                  "askBeforeRunningTools",
		DisableBypassPermissionsMode: "wrong",
		AdditionalDirectories:        []string{"ok", " "},
	}
	err := validatePermissionsConfig(p)
	require.Len(t, err, 2)
	require.Contains(t, err[0].Error()+err[1].Error(), "disableBypassPermissionsMode")
	require.Contains(t, err[0].Error()+err[1].Error(), "permissions.additionalDirectories[1]")
}

func TestValidateHooksConfig_EmptyCommand(t *testing.T) {
	errs := validateHooksConfig(&HooksConfig{
		PreToolUse: map[string]string{"bash": ""},
	})
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "command is empty")
}

func TestValidateHooksConfig_AllowsWildcardAndRegex(t *testing.T) {
	errs := validateHooksConfig(&HooksConfig{
		PreToolUse: map[string]string{
			"*":                   "echo any",
			"^file_(read|write)$": "echo file",
		},
		PostToolUse: map[string]string{
			"grep|awk": "echo post",
		},
	})
	require.Empty(t, errs)
}

func TestValidateHooksConfig_InvalidRegex(t *testing.T) {
	errs := validateHooksConfig(&HooksConfig{
		PreToolUse: map[string]string{"bad[": "echo"},
	})
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "not a valid regexp")
}

func TestValidatePermissionRule_TargetEmpty(t *testing.T) {
	require.ErrorContains(t, validatePermissionRule("Bash(   )"), "target is empty")
	require.ErrorContains(t, validatePermissionRule("Bash(ls"), "must end with )")
}

func TestValidateForceLoginConfig_BlankOrg(t *testing.T) {
	errs := validateForceLoginConfig("claudeai", "   ")
	require.Len(t, errs, 1)
	require.Contains(t, errs[0].Error(), "forceLoginOrgUUID")

	require.NoError(t, ValidateSettings(&Settings{Model: "m", Permissions: &PermissionsConfig{DefaultMode: "askBeforeRunningTools"}}))
}
