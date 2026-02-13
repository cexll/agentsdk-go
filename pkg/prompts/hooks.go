package prompts

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

// hookConfig represents the JSON structure for hooks configuration.
type hookConfig struct {
	PreToolUse         []hookEntry `json:"PreToolUse,omitempty"`
	PostToolUse        []hookEntry `json:"PostToolUse,omitempty"`
	PostToolUseFailure []hookEntry `json:"PostToolUseFailure,omitempty"`
	PreCompact         []hookEntry `json:"PreCompact,omitempty"`
	ContextCompacted   []hookEntry `json:"ContextCompacted,omitempty"`
	UserPromptSubmit   []hookEntry `json:"UserPromptSubmit,omitempty"`
	SessionStart       []hookEntry `json:"SessionStart,omitempty"`
	SessionEnd         []hookEntry `json:"SessionEnd,omitempty"`
	Stop               []hookEntry `json:"Stop,omitempty"`
	SubagentStart      []hookEntry `json:"SubagentStart,omitempty"`
	SubagentStop       []hookEntry `json:"SubagentStop,omitempty"`
	Notification       []hookEntry `json:"Notification,omitempty"`
}

type hookEntry struct {
	Command string            `json:"command"`
	Matcher string            `json:"matcher,omitempty"`
	Timeout string            `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Name    string            `json:"name,omitempty"`
}

// parseHooks parses hooks from the given directory in the filesystem.
// It looks for hooks.json or individual shell scripts.
func parseHooks(fsys fs.FS, dir string) ([]corehooks.ShellHook, []error) {
	var (
		hooks []corehooks.ShellHook
		errs  []error
	)

	info, err := fs.Stat(fsys, dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		return nil, []error{fmt.Errorf("prompts: stat hooks dir %s: %w", dir, err)}
	}
	if !info.IsDir() {
		return nil, []error{fmt.Errorf("prompts: hooks path %s is not a directory", dir)}
	}

	// Try to load hooks.json first
	jsonPath := path.Join(dir, "hooks.json")
	if content, err := fs.ReadFile(fsys, jsonPath); err == nil {
		parsed, parseErrs := parseHooksJSON(content)
		hooks = append(hooks, parsed...)
		errs = append(errs, parseErrs...)
	}

	// Also scan for shell scripts
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		errs = append(errs, fmt.Errorf("prompts: read hooks dir %s: %w", dir, err))
		return hooks, errs
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		ext := strings.ToLower(filepath.Ext(name))
		if ext != ".sh" && ext != ".bash" {
			continue
		}

		hookPath := path.Join(dir, name)
		content, err := fs.ReadFile(fsys, hookPath)
		if err != nil {
			errs = append(errs, fmt.Errorf("prompts: read hook script %s: %w", hookPath, err))
			continue
		}

		// Parse event type from filename (e.g., pre-tool-use.sh -> PreToolUse)
		baseName := strings.TrimSuffix(name, ext)
		eventType := parseEventTypeFromFilename(baseName)
		if eventType == "" {
			errs = append(errs, fmt.Errorf("prompts: unknown hook event type from filename %s", name))
			continue
		}

		hooks = append(hooks, corehooks.ShellHook{
			Event:   eventType,
			Command: string(content),
			Name:    baseName,
		})
	}

	return hooks, errs
}

func parseHooksJSON(content []byte) ([]corehooks.ShellHook, []error) {
	var cfg hookConfig
	if err := json.Unmarshal(content, &cfg); err != nil {
		return nil, []error{fmt.Errorf("prompts: parse hooks.json: %w", err)}
	}

	var (
		hooks []corehooks.ShellHook
		errs  []error
	)

	eventMappings := []struct {
		eventType events.EventType
		entries   []hookEntry
	}{
		{events.PreToolUse, cfg.PreToolUse},
		{events.PostToolUse, cfg.PostToolUse},
		{events.PostToolUseFailure, cfg.PostToolUseFailure},
		{events.PreCompact, cfg.PreCompact},
		{events.ContextCompacted, cfg.ContextCompacted},
		{events.UserPromptSubmit, cfg.UserPromptSubmit},
		{events.SessionStart, cfg.SessionStart},
		{events.SessionEnd, cfg.SessionEnd},
		{events.Stop, cfg.Stop},
		{events.SubagentStart, cfg.SubagentStart},
		{events.SubagentStop, cfg.SubagentStop},
		{events.Notification, cfg.Notification},
	}

	for _, mapping := range eventMappings {
		for _, entry := range mapping.entries {
			hook, err := buildShellHook(mapping.eventType, entry)
			if err != nil {
				errs = append(errs, err)
				continue
			}
			hooks = append(hooks, hook)
		}
	}

	return hooks, errs
}

func buildShellHook(eventType events.EventType, entry hookEntry) (corehooks.ShellHook, error) {
	if entry.Command == "" {
		return corehooks.ShellHook{}, fmt.Errorf("prompts: hook for %s missing command", eventType)
	}

	hook := corehooks.ShellHook{
		Event:   eventType,
		Command: entry.Command,
		Env:     entry.Env,
		Name:    entry.Name,
	}

	if entry.Matcher != "" {
		selector, err := corehooks.NewSelector("", entry.Matcher)
		if err != nil {
			return corehooks.ShellHook{}, fmt.Errorf("prompts: hook %s invalid matcher: %w", eventType, err)
		}
		hook.Selector = selector
	}

	if entry.Timeout != "" {
		d, err := time.ParseDuration(entry.Timeout)
		if err != nil {
			return corehooks.ShellHook{}, fmt.Errorf("prompts: hook %s invalid timeout: %w", eventType, err)
		}
		hook.Timeout = d
	}

	return hook, nil
}

func parseEventTypeFromFilename(name string) events.EventType {
	normalized := strings.ToLower(strings.ReplaceAll(name, "-", ""))
	normalized = strings.ReplaceAll(normalized, "_", "")

	mapping := map[string]events.EventType{
		"pretooluse":         events.PreToolUse,
		"posttooluse":        events.PostToolUse,
		"posttoolusefailure": events.PostToolUseFailure,
		"precompact":         events.PreCompact,
		"contextcompacted":   events.ContextCompacted,
		"userpromptsubmit":   events.UserPromptSubmit,
		"sessionstart":       events.SessionStart,
		"sessionend":         events.SessionEnd,
		"stop":               events.Stop,
		"subagentstart":      events.SubagentStart,
		"subagentstop":       events.SubagentStop,
		"notification":       events.Notification,
	}

	return mapping[normalized]
}
