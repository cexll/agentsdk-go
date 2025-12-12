package config

import (
	"encoding/json"
	"fmt"
)

// claudeCodeHook represents a single hook definition in Claude Code format.
type claudeCodeHook struct {
	Type    string `json:"type"`
	Command string `json:"command"`
	Timeout int    `json:"timeout,omitempty"`
}

// claudeCodeHookEntry represents one matcher entry in Claude Code format.
type claudeCodeHookEntry struct {
	Matcher string           `json:"matcher"`
	Hooks   []claudeCodeHook `json:"hooks"`
}

// UnmarshalJSON implements custom unmarshaling for HooksConfig to support both:
// 1. Claude Code official format (array): {"PostToolUse": [{"matcher": "pattern", "hooks": [...]}]}
// 2. SDK simplified format (map): {"PostToolUse": {"tool-name": "command"}}
func (h *HooksConfig) UnmarshalJSON(data []byte) error {
	// Try to unmarshal as a raw JSON object first to inspect structure
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("hooks: invalid JSON: %w", err)
	}

	initMap := func(dst *map[string]string) {
		if *dst == nil {
			*dst = make(map[string]string)
		}
	}
	// Initialize maps so callers can rely on non-nil fields.
	initMap(&h.PreToolUse)
	initMap(&h.PostToolUse)
	initMap(&h.PermissionRequest)
	initMap(&h.SessionStart)
	initMap(&h.SessionEnd)
	initMap(&h.SubagentStart)
	initMap(&h.SubagentStop)

	fields := []struct {
		name   string
		target *map[string]string
	}{
		{name: "PreToolUse", target: &h.PreToolUse},
		{name: "PostToolUse", target: &h.PostToolUse},
		{name: "PermissionRequest", target: &h.PermissionRequest},
		{name: "SessionStart", target: &h.SessionStart},
		{name: "SessionEnd", target: &h.SessionEnd},
		{name: "SubagentStart", target: &h.SubagentStart},
		{name: "SubagentStop", target: &h.SubagentStop},
	}

	for _, field := range fields {
		if fieldData, ok := raw[field.name]; ok {
			converted, err := parseHookField(fieldData)
			if err != nil {
				return fmt.Errorf("hooks: %s: %w", field.name, err)
			}
			*field.target = converted
		}
	}

	return nil
}

// parseHookField handles both array and map formats for a hook field.
func parseHookField(data json.RawMessage) (map[string]string, error) {
	// Try array format first (Claude Code official format)
	var arrFormat []claudeCodeHookEntry
	if err := json.Unmarshal(data, &arrFormat); err == nil {
		return convertClaudeCodeFormat(arrFormat), nil
	}

	// Try map format (SDK simplified format)
	var mapFormat map[string]string
	if err := json.Unmarshal(data, &mapFormat); err == nil {
		return mapFormat, nil
	}

	return nil, fmt.Errorf("invalid format: expected array or map")
}

// convertClaudeCodeFormat converts Claude Code array format to SDK map format.
// Conversion rules:
// - If matcher is empty, use "*" as the key
// - If matcher is non-empty, use matcher as the key
// - Take the first hook's command as the value
func convertClaudeCodeFormat(entries []claudeCodeHookEntry) map[string]string {
	result := make(map[string]string)
	for _, entry := range entries {
		// Determine the key
		key := entry.Matcher
		if key == "" {
			key = "*"
		}

		// Take the first hook's command if available
		if len(entry.Hooks) > 0 {
			result[key] = entry.Hooks[0].Command
		}
	}
	return result
}
