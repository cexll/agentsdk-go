package api

import (
	"log"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
)

func newHookExecutor(opts Options, recorder HookRecorder, settings *config.Settings) *corehooks.Executor {
	execOpts := []corehooks.ExecutorOption{
		corehooks.WithMiddleware(opts.HookMiddleware...),
		corehooks.WithTimeout(opts.HookTimeout),
	}
	if opts.ProjectRoot != "" {
		execOpts = append(execOpts, corehooks.WithWorkDir(opts.ProjectRoot))
	}
	exec := corehooks.NewExecutor(execOpts...)
	if len(opts.TypedHooks) > 0 {
		exec.Register(opts.TypedHooks...)
	}
	if !hooksDisabled(settings) {
		hooks := buildSettingsHooks(settings, opts.ProjectRoot)
		if len(hooks) > 0 {
			exec.Register(hooks...)
		}
	}
	_ = recorder
	return exec
}

func hooksDisabled(settings *config.Settings) bool {
	return settings != nil && settings.DisableAllHooks != nil && *settings.DisableAllHooks
}

// buildSettingsHooks converts settings.Hooks config to ShellHook structs.
func buildSettingsHooks(settings *config.Settings, projectRoot string) []corehooks.ShellHook {
	if settings == nil || settings.Hooks == nil {
		return nil
	}

	var hooks []corehooks.ShellHook
	env := map[string]string{}
	for k, v := range settings.Env {
		env[k] = v
	}
	if projectRoot != "" {
		env["CLAUDE_PROJECT_DIR"] = projectRoot
	}

	addEntries := func(event coreevents.EventType, entries []config.HookMatcherEntry, prefix string) {
		for _, entry := range entries {
			normalizedMatcher := normalizeToolSelectorPattern(entry.Matcher)
			sel, err := corehooks.NewSelector(normalizedMatcher, "")
			if err != nil {
				continue
			}
			for _, hookDef := range entry.Hooks {
				switch hookDef.Type {
				case "command", "":
					if hookDef.Command == "" {
						continue
					}
					timeout := time.Duration(0)
					if hookDef.Timeout > 0 {
						timeout = time.Duration(hookDef.Timeout) * time.Second
					}
					hooks = append(hooks, corehooks.ShellHook{
						Event:         event,
						Command:       hookDef.Command,
						Selector:      sel,
						Timeout:       timeout,
						Env:           env,
						Name:          "settings:" + prefix + ":" + normalizedMatcher,
						Async:         hookDef.Async,
						Once:          hookDef.Once,
						StatusMessage: hookDef.StatusMessage,
					})
				case "prompt", "agent":
					log.Printf("hooks: skipping %s hook type %q (not yet supported)", prefix, hookDef.Type)
				}
			}
		}
	}

	addEntries(coreevents.PreToolUse, settings.Hooks.PreToolUse, "pre")
	addEntries(coreevents.PostToolUse, settings.Hooks.PostToolUse, "post")
	addEntries(coreevents.PostToolUseFailure, settings.Hooks.PostToolUseFailure, "post_failure")
	addEntries(coreevents.PermissionRequest, settings.Hooks.PermissionRequest, "permission")
	addEntries(coreevents.SessionStart, settings.Hooks.SessionStart, "session_start")
	addEntries(coreevents.SessionEnd, settings.Hooks.SessionEnd, "session_end")
	addEntries(coreevents.SubagentStart, settings.Hooks.SubagentStart, "subagent_start")
	addEntries(coreevents.SubagentStop, settings.Hooks.SubagentStop, "subagent_stop")
	addEntries(coreevents.Stop, settings.Hooks.Stop, "stop")
	addEntries(coreevents.Notification, settings.Hooks.Notification, "notification")
	addEntries(coreevents.UserPromptSubmit, settings.Hooks.UserPromptSubmit, "user_prompt")
	addEntries(coreevents.PreCompact, settings.Hooks.PreCompact, "pre_compact")

	return hooks
}

// normalizeToolSelectorPattern maps wildcard "*" to the selector wildcard (empty pattern).
func normalizeToolSelectorPattern(pattern string) string {
	if strings.TrimSpace(pattern) == "*" {
		return ""
	}
	return pattern
}
