package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// mkEntries builds []HookMatcherEntry from alternating (matcher, command) pairs.
func mkEntries(pairs ...string) []HookMatcherEntry {
	var entries []HookMatcherEntry
	for i := 0; i < len(pairs); i += 2 {
		entries = append(entries, HookMatcherEntry{
			Matcher: pairs[i],
			Hooks:   []HookDefinition{{Type: "command", Command: pairs[i+1]}},
		})
	}
	return entries
}

func TestHooksConfig_UnmarshalJSON_ClaudeCodeFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected HooksConfig
	}{
		{
			name: "claude_code_format_with_matcher",
			input: `{
				"PostToolUse": [
					{
						"matcher": "Write|Edit",
						"hooks": [
							{"type": "command", "command": "npx prettier --write", "timeout": 5}
						]
					}
				]
			}`,
			expected: HooksConfig{
				PostToolUse: []HookMatcherEntry{
					{
						Matcher: "Write|Edit",
						Hooks: []HookDefinition{
							{Type: "command", Command: "npx prettier --write", Timeout: 5},
						},
					},
				},
			},
		},
		{
			name: "claude_code_format_empty_matcher",
			input: `{
				"PostToolUse": [
					{
						"matcher": "",
						"hooks": [
							{"type": "command", "command": "npx ccm track", "timeout": 5}
						]
					}
				]
			}`,
			expected: HooksConfig{
				PostToolUse: []HookMatcherEntry{
					{
						Matcher: "",
						Hooks: []HookDefinition{
							{Type: "command", Command: "npx ccm track", Timeout: 5},
						},
					},
				},
			},
		},
		{
			name: "claude_code_format_multiple_entries",
			input: `{
				"PreToolUse": [
					{
						"matcher": "bash",
						"hooks": [
							{"type": "command", "command": "echo pre-bash"}
						]
					},
					{
						"matcher": "",
						"hooks": [
							{"type": "command", "command": "echo pre-all"}
						]
					}
				],
				"PostToolUse": [
					{
						"matcher": "Write",
						"hooks": [
							{"type": "command", "command": "echo post-write"}
						]
					}
				]
			}`,
			expected: HooksConfig{
				PreToolUse: []HookMatcherEntry{
					{Matcher: "bash", Hooks: []HookDefinition{{Type: "command", Command: "echo pre-bash"}}},
					{Matcher: "", Hooks: []HookDefinition{{Type: "command", Command: "echo pre-all"}}},
				},
				PostToolUse: []HookMatcherEntry{
					{Matcher: "Write", Hooks: []HookDefinition{{Type: "command", Command: "echo post-write"}}},
				},
			},
		},
		{
			name: "claude_code_format_multiple_hooks_preserved",
			input: `{
				"PostToolUse": [
					{
						"matcher": "tool",
						"hooks": [
							{"type": "command", "command": "first-command"},
							{"type": "command", "command": "second-command"}
						]
					}
				]
			}`,
			expected: HooksConfig{
				PostToolUse: []HookMatcherEntry{
					{
						Matcher: "tool",
						Hooks: []HookDefinition{
							{Type: "command", Command: "first-command"},
							{Type: "command", Command: "second-command"},
						},
					},
				},
			},
		},
		{
			name: "claude_code_format_empty_hooks_array",
			input: `{
				"PostToolUse": [
					{
						"matcher": "tool",
						"hooks": []
					}
				]
			}`,
			expected: HooksConfig{
				PostToolUse: []HookMatcherEntry{
					{Matcher: "tool", Hooks: []HookDefinition{}},
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got HooksConfig
			err := json.Unmarshal([]byte(tt.input), &got)
			require.NoError(t, err)
			require.Equal(t, tt.expected.PreToolUse, got.PreToolUse)
			require.Equal(t, tt.expected.PostToolUse, got.PostToolUse)
		})
	}
}

func TestHooksConfig_UnmarshalJSON_SDKFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected HooksConfig
	}{
		{
			name: "sdk_format_simple",
			input: `{
				"PreToolUse": {"bash": "echo pre"},
				"PostToolUse": {"bash": "echo post"}
			}`,
			expected: HooksConfig{
				PreToolUse:  mkEntries("bash", "echo pre"),
				PostToolUse: mkEntries("bash", "echo post"),
			},
		},
		{
			name: "sdk_format_empty_maps",
			input: `{
				"PreToolUse": {},
				"PostToolUse": {}
			}`,
			expected: HooksConfig{
				PreToolUse:  nil,
				PostToolUse: nil,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got HooksConfig
			err := json.Unmarshal([]byte(tt.input), &got)
			require.NoError(t, err)
			require.Equal(t, tt.expected.PreToolUse, got.PreToolUse)
			require.Equal(t, tt.expected.PostToolUse, got.PostToolUse)
		})
	}
}

func TestHooksConfig_UnmarshalJSON_NewFields(t *testing.T) {
	t.Parallel()
	input := `{
		"PermissionRequest": {"bash": "echo perm"},
		"SessionStart": {"*": "echo start"},
		"SessionEnd": {"*": "echo end"},
		"SubagentStart": {"worker": "echo sa start"},
		"SubagentStop": {"worker": "echo sa stop"},
		"Stop": {"*": "echo stop"},
		"Notification": {"*": "echo notify"},
		"UserPromptSubmit": {"*": "echo prompt"},
		"PreCompact": {"*": "echo compact"},
		"PostToolUseFailure": {"bash": "echo failure"}
	}`

	var got HooksConfig
	err := json.Unmarshal([]byte(input), &got)
	require.NoError(t, err)
	require.Equal(t, mkEntries("bash", "echo perm"), got.PermissionRequest)
	require.Equal(t, mkEntries("*", "echo start"), got.SessionStart)
	require.Equal(t, mkEntries("*", "echo end"), got.SessionEnd)
	require.Equal(t, mkEntries("worker", "echo sa start"), got.SubagentStart)
	require.Equal(t, mkEntries("worker", "echo sa stop"), got.SubagentStop)
	require.Equal(t, mkEntries("*", "echo stop"), got.Stop)
	require.Equal(t, mkEntries("*", "echo notify"), got.Notification)
	require.Equal(t, mkEntries("*", "echo prompt"), got.UserPromptSubmit)
	require.Equal(t, mkEntries("*", "echo compact"), got.PreCompact)
	require.Equal(t, mkEntries("bash", "echo failure"), got.PostToolUseFailure)
}

func TestHooksConfig_UnmarshalJSON_MixedFormat(t *testing.T) {
	t.Parallel()

	input := `{
		"PreToolUse": [
			{
				"matcher": "bash",
				"hooks": [{"type": "command", "command": "echo claude-format"}]
			}
		],
		"PostToolUse": {
			"Write": "echo sdk-format"
		}
	}`

	var got HooksConfig
	err := json.Unmarshal([]byte(input), &got)
	require.NoError(t, err)

	require.Equal(t, []HookMatcherEntry{
		{Matcher: "bash", Hooks: []HookDefinition{{Type: "command", Command: "echo claude-format"}}},
	}, got.PreToolUse)
	require.Equal(t, mkEntries("Write", "echo sdk-format"), got.PostToolUse)
}

func TestHooksConfig_UnmarshalJSON_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected HooksConfig
	}{
		{
			name:     "empty_object",
			input:    `{}`,
			expected: HooksConfig{},
		},
		{
			name: "only_pre_tool_use",
			input: `{
				"PreToolUse": {"bash": "echo pre"}
			}`,
			expected: HooksConfig{
				PreToolUse: mkEntries("bash", "echo pre"),
			},
		},
		{
			name: "only_post_tool_use",
			input: `{
				"PostToolUse": {"bash": "echo post"}
			}`,
			expected: HooksConfig{
				PostToolUse: mkEntries("bash", "echo post"),
			},
		},
		{
			name: "claude_format_empty_array",
			input: `{
				"PreToolUse": [],
				"PostToolUse": []
			}`,
			expected: HooksConfig{
				PreToolUse:  []HookMatcherEntry{},
				PostToolUse: []HookMatcherEntry{},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got HooksConfig
			err := json.Unmarshal([]byte(tt.input), &got)
			require.NoError(t, err)
			require.Equal(t, tt.expected.PreToolUse, got.PreToolUse)
			require.Equal(t, tt.expected.PostToolUse, got.PostToolUse)
		})
	}
}

func TestHooksConfig_UnmarshalJSON_Errors(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expectError bool
	}{
		{
			name:        "invalid_json",
			input:       `{invalid}`,
			expectError: true,
		},
		{
			name: "invalid_field_type_number",
			input: `{
				"PreToolUse": 123
			}`,
			expectError: true,
		},
		{
			name: "invalid_field_type_string",
			input: `{
				"PostToolUse": "not-an-object-or-array"
			}`,
			expectError: true,
		},
		{
			name: "invalid_field_type_boolean",
			input: `{
				"PreToolUse": true
			}`,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got HooksConfig
			err := json.Unmarshal([]byte(tt.input), &got)
			if tt.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestHooksConfig_UnmarshalJSON_RealWorldExample(t *testing.T) {
	t.Parallel()

	// Real example from user's ~/.claude/settings.json
	input := `{
		"PostToolUse": [
			{
				"matcher": "",
				"hooks": [
					{
						"type": "command",
						"command": "npx ccm track",
						"timeout": 5
					}
				]
			},
			{
				"matcher": "",
				"hooks": [
					{
						"type": "command",
						"command": "npx claude-code-manager track",
						"timeout": 5
					}
				]
			}
		]
	}`

	var got HooksConfig
	err := json.Unmarshal([]byte(input), &got)
	require.NoError(t, err)

	// Both entries have empty matcher and should both be preserved as separate entries.
	require.Len(t, got.PostToolUse, 2)
	require.Equal(t, []HookMatcherEntry{
		{
			Matcher: "",
			Hooks:   []HookDefinition{{Type: "command", Command: "npx ccm track", Timeout: 5}},
		},
		{
			Matcher: "",
			Hooks:   []HookDefinition{{Type: "command", Command: "npx claude-code-manager track", Timeout: 5}},
		},
	}, got.PostToolUse)
}

func TestConvertLegacyMapFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    map[string]string
		expected []HookMatcherEntry
	}{
		{
			name:     "empty_input",
			input:    map[string]string{},
			expected: nil,
		},
		{
			name:     "single_entry",
			input:    map[string]string{"bash": "echo test"},
			expected: mkEntries("bash", "echo test"),
		},
		{
			name:     "wildcard_entry",
			input:    map[string]string{"*": "echo all"},
			expected: mkEntries("*", "echo all"),
		},
		{
			name:     "empty_command_skipped",
			input:    map[string]string{"tool": ""},
			expected: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := convertLegacyMapFormat(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseHookField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expected    []HookMatcherEntry
		expectError bool
	}{
		{
			name:  "array_format",
			input: `[{"matcher": "tool", "hooks": [{"type": "command", "command": "echo test"}]}]`,
			expected: []HookMatcherEntry{
				{Matcher: "tool", Hooks: []HookDefinition{{Type: "command", Command: "echo test"}}},
			},
			expectError: false,
		},
		{
			name:  "array_format_defaults_empty_type_to_command",
			input: `[{"matcher": "tool", "hooks": [{"command": "echo test"}]}]`,
			expected: []HookMatcherEntry{
				{Matcher: "tool", Hooks: []HookDefinition{{Type: "command", Command: "echo test"}}},
			},
			expectError: false,
		},
		{
			name:        "map_format",
			input:       `{"tool": "echo test"}`,
			expected:    mkEntries("tool", "echo test"),
			expectError: false,
		},
		{
			name:        "invalid_format",
			input:       `"invalid"`,
			expected:    nil,
			expectError: true,
		},
		{
			name:        "number_format",
			input:       `123`,
			expected:    nil,
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseHookField(json.RawMessage(tt.input))
			if tt.expectError {
				require.Error(t, err)
				require.Nil(t, got)
			} else {
				require.NoError(t, err)
				require.Equal(t, tt.expected, got)
			}
		})
	}
}
