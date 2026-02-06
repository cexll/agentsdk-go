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
		PreToolUse:        []HookMatcherEntry{{Matcher: "a", Hooks: []HookDefinition{{Type: "command", Command: "1"}}}},
		PostToolUse:       []HookMatcherEntry{{Matcher: "b", Hooks: []HookDefinition{{Type: "command", Command: "2"}}}},
		PermissionRequest: []HookMatcherEntry{{Matcher: "p", Hooks: []HookDefinition{{Type: "command", Command: "x"}}}},
	}
	higher := &HooksConfig{
		PreToolUse:   []HookMatcherEntry{{Matcher: "c", Hooks: []HookDefinition{{Type: "command", Command: "3"}}}},
		SessionStart: []HookMatcherEntry{{Matcher: "s", Hooks: []HookDefinition{{Type: "command", Command: "1"}}}},
	}

	out := mergeHooks(lower, higher)
	require.NotNil(t, out)
	// mergeHookEntries concatenates lower + higher
	require.Len(t, out.PreToolUse, 2)
	require.Equal(t, "a", out.PreToolUse[0].Matcher)
	require.Equal(t, "c", out.PreToolUse[1].Matcher)
	require.Len(t, out.PostToolUse, 1)
	require.Equal(t, "b", out.PostToolUse[0].Matcher)
	require.Len(t, out.PermissionRequest, 1)
	require.Equal(t, "p", out.PermissionRequest[0].Matcher)
	require.Len(t, out.SessionStart, 1)
	require.Equal(t, "s", out.SessionStart[0].Matcher)

	// Mutation isolation
	out.PreToolUse[0].Matcher = "changed"
	require.Equal(t, "a", lower.PreToolUse[0].Matcher)

	require.Nil(t, mergeHooks(nil, nil))
	require.NotSame(t, lower, mergeHooks(lower, nil))
	require.NotSame(t, higher, mergeHooks(nil, higher))

	cloned := cloneHooks(lower)
	require.NotNil(t, cloned)
	require.NotSame(t, lower, cloned)
	require.Equal(t, lower.PreToolUse[0].Matcher, cloned.PreToolUse[0].Matcher)
	cloned.PreToolUse[0].Matcher = "z"
	require.Equal(t, "a", lower.PreToolUse[0].Matcher)
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
