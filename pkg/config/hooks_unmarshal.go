package config

import (
	"encoding/json"
	"fmt"
)

// UnmarshalJSON implements custom unmarshaling for HooksConfig to support both:
// 1. Claude Code official format (array): {"PostToolUse": [{"matcher": "pattern", "hooks": [...]}]}
// 2. SDK simplified format (map): {"PostToolUse": {"tool-name": "command"}}
func (h *HooksConfig) UnmarshalJSON(data []byte) error {
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("hooks: invalid JSON: %w", err)
	}

	fields := []struct {
		name   string
		target *[]HookMatcherEntry
	}{
		{name: "PreToolUse", target: &h.PreToolUse},
		{name: "PostToolUse", target: &h.PostToolUse},
		{name: "PostToolUseFailure", target: &h.PostToolUseFailure},
		{name: "PermissionRequest", target: &h.PermissionRequest},
		{name: "SessionStart", target: &h.SessionStart},
		{name: "SessionEnd", target: &h.SessionEnd},
		{name: "SubagentStart", target: &h.SubagentStart},
		{name: "SubagentStop", target: &h.SubagentStop},
		{name: "Stop", target: &h.Stop},
		{name: "Notification", target: &h.Notification},
		{name: "UserPromptSubmit", target: &h.UserPromptSubmit},
		{name: "PreCompact", target: &h.PreCompact},
	}

	for _, field := range fields {
		if fieldData, ok := raw[field.name]; ok {
			entries, err := parseHookField(fieldData)
			if err != nil {
				return fmt.Errorf("hooks: %s: %w", field.name, err)
			}
			*field.target = entries
		}
	}

	return nil
}

// parseHookField handles both array and map formats for a hook field.
func parseHookField(data json.RawMessage) ([]HookMatcherEntry, error) {
	// Try array format first (Claude Code official format)
	var arrFormat []HookMatcherEntry
	if err := json.Unmarshal(data, &arrFormat); err == nil {
		// Normalize: default type to "command" where empty
		for i := range arrFormat {
			for j := range arrFormat[i].Hooks {
				if arrFormat[i].Hooks[j].Type == "" {
					arrFormat[i].Hooks[j].Type = "command"
				}
			}
		}
		return arrFormat, nil
	}

	// Try legacy map format (SDK simplified format: {"matcher": "command"})
	var mapFormat map[string]string
	if err := json.Unmarshal(data, &mapFormat); err == nil {
		return convertLegacyMapFormat(mapFormat), nil
	}

	return nil, fmt.Errorf("invalid format: expected array or map")
}

// convertLegacyMapFormat converts the old map[string]string format to []HookMatcherEntry.
func convertLegacyMapFormat(m map[string]string) []HookMatcherEntry {
	if len(m) == 0 {
		return nil
	}
	entries := make([]HookMatcherEntry, 0, len(m))
	for matcher, command := range m {
		if command == "" {
			continue
		}
		entries = append(entries, HookMatcherEntry{
			Matcher: matcher,
			Hooks: []HookDefinition{{
				Type:    "command",
				Command: command,
			}},
		})
	}
	if len(entries) == 0 {
		return nil
	}
	return entries
}
