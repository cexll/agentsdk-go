package config

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMergeStatusLineOverrides(t *testing.T) {
	lower := &StatusLineConfig{
		Type:            "command",
		Command:         "echo low",
		Template:        "",
		IntervalSeconds: 10,
		TimeoutSeconds:  5,
	}
	higher := &StatusLineConfig{
		Type:           "template",
		Template:       "{{.User}}",
		TimeoutSeconds: 15,
	}

	out := mergeStatusLine(lower, higher)
	require.NotSame(t, lower, out)
	require.NotSame(t, higher, out)
	require.Equal(t, "template", out.Type)
	require.Equal(t, "echo low", out.Command) // higher empty should not clobber
	require.Equal(t, "{{.User}}", out.Template)
	require.Equal(t, 10, out.IntervalSeconds)
	require.Equal(t, 15, out.TimeoutSeconds)

	require.Nil(t, mergeStatusLine(nil, nil))
	copied := mergeStatusLine(lower, nil)
	require.Equal(t, lower.Command, copied.Command)
	require.NotSame(t, lower, copied)
}

func TestMergeMCPServerRules(t *testing.T) {
	lower := []MCPServerRule{{ServerName: "low"}}
	higher := []MCPServerRule{{ServerName: "high"}}

	require.Equal(t, higher, mergeMCPServerRules(lower, higher))
	require.Equal(t, lower, mergeMCPServerRules(lower, nil))
	require.Nil(t, mergeMCPServerRules(nil, nil))
}

func TestMergeMCPConfig(t *testing.T) {
	lower := &MCPConfig{Servers: map[string]MCPServerConfig{
		"base": {Type: "stdio", Command: "node"},
	}}
	higher := &MCPConfig{Servers: map[string]MCPServerConfig{
		"remote": {Type: "http", URL: "https://example"},
	}}

	out := mergeMCPConfig(lower, higher)
	require.Len(t, out.Servers, 2)
	require.Equal(t, "node", out.Servers["base"].Command)
	require.Equal(t, "https://example", out.Servers["remote"].URL)

	out.Servers["base"] = MCPServerConfig{}
	require.Equal(t, "node", lower.Servers["base"].Command)
}

func TestMergeHooksAndCloneHooks(t *testing.T) {
	lower := &HooksConfig{
		PreToolUse:        map[string]string{"a": "1"},
		PostToolUse:       map[string]string{"b": "2"},
		PermissionRequest: map[string]string{"p": "x"},
	}
	higher := &HooksConfig{
		PreToolUse:   map[string]string{"a": "9", "c": "3"},
		SessionStart: map[string]string{"s": "1"},
	}

	out := mergeHooks(lower, higher)
	require.NotNil(t, out)
	require.Equal(t, "9", out.PreToolUse["a"])
	require.Equal(t, "3", out.PreToolUse["c"])
	require.Equal(t, "2", out.PostToolUse["b"])
	require.Equal(t, "x", out.PermissionRequest["p"])
	require.Equal(t, "1", out.SessionStart["s"])

	out.PreToolUse["a"] = "changed"
	require.Equal(t, "1", lower.PreToolUse["a"])

	require.Nil(t, mergeHooks(nil, nil))
	require.NotSame(t, lower, mergeHooks(lower, nil))
	require.NotSame(t, higher, mergeHooks(nil, higher))

	cloned := cloneHooks(lower)
	require.NotNil(t, cloned)
	require.NotSame(t, lower, cloned)
	require.Equal(t, lower.PreToolUse["a"], cloned.PreToolUse["a"])
	cloned.PreToolUse["a"] = "z"
	require.Equal(t, "1", lower.PreToolUse["a"])
}

func TestMergeBashOutput(t *testing.T) {
	lower := &BashOutputConfig{
		SyncThresholdBytes:  ptrInt(10),
		AsyncThresholdBytes: ptrInt(20),
	}
	higher := &BashOutputConfig{
		SyncThresholdBytes: ptrInt(99),
	}
	out := mergeBashOutput(lower, higher)
	require.NotNil(t, out)
	require.Equal(t, 99, *out.SyncThresholdBytes)
	require.Equal(t, 20, *out.AsyncThresholdBytes)

	require.Nil(t, mergeBashOutput(nil, nil))
	require.NotNil(t, mergeBashOutput(lower, nil))
	require.NotNil(t, mergeBashOutput(nil, higher))
}

func ptrInt(v int) *int { return &v }
