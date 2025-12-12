package config

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

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
				PreToolUse: map[string]string{},
				PostToolUse: map[string]string{
					"Write|Edit": "npx prettier --write",
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
				PreToolUse: map[string]string{},
				PostToolUse: map[string]string{
					"*": "npx ccm track",
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
				PreToolUse: map[string]string{
					"bash": "echo pre-bash",
					"*":    "echo pre-all",
				},
				PostToolUse: map[string]string{
					"Write": "echo post-write",
				},
			},
		},
		{
			name: "claude_code_format_multiple_hooks_takes_first",
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
				PreToolUse: map[string]string{},
				PostToolUse: map[string]string{
					"tool": "first-command",
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
				PreToolUse:  map[string]string{},
				PostToolUse: map[string]string{},
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
				PreToolUse:  map[string]string{"bash": "echo pre"},
				PostToolUse: map[string]string{"bash": "echo post"},
			},
		},
		{
			name: "sdk_format_multiple_tools",
			input: `{
				"PreToolUse": {
					"bash": "echo pre-bash",
					"Write": "echo pre-write"
				},
				"PostToolUse": {
					"Read": "echo post-read"
				}
			}`,
			expected: HooksConfig{
				PreToolUse: map[string]string{
					"bash":  "echo pre-bash",
					"Write": "echo pre-write",
				},
				PostToolUse: map[string]string{
					"Read": "echo post-read",
				},
			},
		},
		{
			name: "sdk_format_empty_maps",
			input: `{
				"PreToolUse": {},
				"PostToolUse": {}
			}`,
			expected: HooksConfig{
				PreToolUse:  map[string]string{},
				PostToolUse: map[string]string{},
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
		"SubagentStop": {"worker": "echo sa stop"}
	}`

	var got HooksConfig
	err := json.Unmarshal([]byte(input), &got)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"bash": "echo perm"}, got.PermissionRequest)
	require.Equal(t, map[string]string{"*": "echo start"}, got.SessionStart)
	require.Equal(t, map[string]string{"*": "echo end"}, got.SessionEnd)
	require.Equal(t, map[string]string{"worker": "echo sa start"}, got.SubagentStart)
	require.Equal(t, map[string]string{"worker": "echo sa stop"}, got.SubagentStop)
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

	expected := HooksConfig{
		PreToolUse:  map[string]string{"bash": "echo claude-format"},
		PostToolUse: map[string]string{"Write": "echo sdk-format"},
	}
	require.Equal(t, expected.PreToolUse, got.PreToolUse)
	require.Equal(t, expected.PostToolUse, got.PostToolUse)
}

func TestHooksConfig_UnmarshalJSON_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected HooksConfig
	}{
		{
			name:  "empty_object",
			input: `{}`,
			expected: HooksConfig{
				PreToolUse:  map[string]string{},
				PostToolUse: map[string]string{},
			},
		},
		{
			name: "only_pre_tool_use",
			input: `{
				"PreToolUse": {"bash": "echo pre"}
			}`,
			expected: HooksConfig{
				PreToolUse:  map[string]string{"bash": "echo pre"},
				PostToolUse: map[string]string{},
			},
		},
		{
			name: "only_post_tool_use",
			input: `{
				"PostToolUse": {"bash": "echo post"}
			}`,
			expected: HooksConfig{
				PreToolUse:  map[string]string{},
				PostToolUse: map[string]string{"bash": "echo post"},
			},
		},
		{
			name: "claude_format_empty_array",
			input: `{
				"PreToolUse": [],
				"PostToolUse": []
			}`,
			expected: HooksConfig{
				PreToolUse:  map[string]string{},
				PostToolUse: map[string]string{},
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

	// Both entries have empty matcher, so they should map to "*"
	// The second entry should overwrite the first
	require.Equal(t, map[string]string{
		"*": "npx claude-code-manager track",
	}, got.PostToolUse)
}

func TestConvertClaudeCodeFormat(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    []claudeCodeHookEntry
		expected map[string]string
	}{
		{
			name:     "empty_input",
			input:    []claudeCodeHookEntry{},
			expected: map[string]string{},
		},
		{
			name: "single_entry_with_matcher",
			input: []claudeCodeHookEntry{
				{
					Matcher: "bash",
					Hooks: []claudeCodeHook{
						{Type: "command", Command: "echo test"},
					},
				},
			},
			expected: map[string]string{
				"bash": "echo test",
			},
		},
		{
			name: "single_entry_empty_matcher",
			input: []claudeCodeHookEntry{
				{
					Matcher: "",
					Hooks: []claudeCodeHook{
						{Type: "command", Command: "echo all"},
					},
				},
			},
			expected: map[string]string{
				"*": "echo all",
			},
		},
		{
			name: "entry_with_no_hooks",
			input: []claudeCodeHookEntry{
				{
					Matcher: "tool",
					Hooks:   []claudeCodeHook{},
				},
			},
			expected: map[string]string{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := convertClaudeCodeFormat(tt.input)
			require.Equal(t, tt.expected, got)
		})
	}
}

func TestParseHookField(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		input       string
		expected    map[string]string
		expectError bool
	}{
		{
			name:  "array_format",
			input: `[{"matcher": "tool", "hooks": [{"type": "command", "command": "echo test"}]}]`,
			expected: map[string]string{
				"tool": "echo test",
			},
			expectError: false,
		},
		{
			name:  "map_format",
			input: `{"tool": "echo test"}`,
			expected: map[string]string{
				"tool": "echo test",
			},
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
