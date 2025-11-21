package main

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
)

func buildCommands() []api.CommandRegistration {
	exec := []api.CommandRegistration{}
	exec = append(exec, api.CommandRegistration{Definition: commands.Definition{Name: "deploy", Description: "deploy artifact"}, Handler: commands.HandlerFunc(handleDeploy)})
	exec = append(exec, api.CommandRegistration{Definition: commands.Definition{Name: "query", Description: "run read-only queries"}, Handler: commands.HandlerFunc(handleQuery)})
	exec = append(exec, api.CommandRegistration{Definition: commands.Definition{Name: "note", Description: "store small notes"}, Handler: commands.HandlerFunc(handleNote)})
	exec = append(exec, api.CommandRegistration{Definition: commands.Definition{Name: "backup", Description: "ship logs somewhere"}, Handler: commands.HandlerFunc(handleBackup)})
	return exec
}

func demoScript() string {
	return strings.TrimSpace(`
/deploy staging --version 2025.11.20 --region=us-east-1 --force
/query "latency p95" --since "2025-11-20 08:00" --limit=3
/note add "release checklist" "/tmp/release plan.md" --tag "ops crew" --private
/backup run --path=/var/log/app --dest "./tmp/log backup" --compress
    `)
}

func dumpInvocations(invocations []commands.Invocation) []string {
	lines := make([]string, 0, len(invocations)*3)
	for _, inv := range invocations {
		lines = append(lines, fmt.Sprintf("/%s args=%v flags=%v", inv.Name, inv.Args, sortedFlags(inv.Flags)))
	}
	return lines
}

func sortedFlags(flags map[string]string) map[string]string {
	if len(flags) == 0 {
		return nil
	}
	keys := make([]string, 0, len(flags))
	for key := range flags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]string, len(flags))
	for _, key := range keys {
		out[key] = flags[key]
	}
	return out
}

func handleDeploy(_ context.Context, inv commands.Invocation) (commands.Result, error) {
	if len(inv.Args) == 0 {
		return commands.Result{}, errors.New("deploy: target environment is required")
	}
	env := inv.Args[0]
	version := flagValue(inv, "version", "latest")
	region := flagValue(inv, "region", "us-east-1")
	force := flagBool(inv, "force")

	output := fmt.Sprintf("deploying to %s with version %s (region %s, force=%t)", env, version, region, force)
	return commands.Result{Output: output, Metadata: map[string]any{"args": inv.Args, "force": force}}, nil
}

func handleQuery(_ context.Context, inv commands.Invocation) (commands.Result, error) {
	if len(inv.Args) == 0 {
		return commands.Result{}, errors.New("query: search term is required")
	}
	term := inv.Args[0]
	since := flagValue(inv, "since", "(none)")
	limit := flagValue(inv, "limit", "unbounded")
	output := fmt.Sprintf("query term=%q since=%s limit=%s", term, since, limit)
	return commands.Result{Output: output}, nil
}

func handleNote(_ context.Context, inv commands.Invocation) (commands.Result, error) {
	if len(inv.Args) < 2 {
		return commands.Result{}, errors.New("note: need action and body text")
	}
	action, body := inv.Args[0], inv.Args[1]
	tag := flagValue(inv, "tag", "")
	private := flagBool(inv, "private")

	meta := map[string]any{"action": action, "private": private}
	if tag != "" {
		meta["tag"] = tag
	}
	return commands.Result{Output: fmt.Sprintf("note %s: %s", action, body), Metadata: meta}, nil
}

func handleBackup(_ context.Context, inv commands.Invocation) (commands.Result, error) {
	path := flagValue(inv, "path", "")
	dest := flagValue(inv, "dest", "")
	compress := flagBool(inv, "compress")
	if path == "" || dest == "" {
		return commands.Result{}, errors.New("backup: path and dest are required")
	}
	summary := fmt.Sprintf("backup from %s to %s (compress=%t)", path, dest, compress)
	return commands.Result{Output: summary}, nil
}

func flagValue(inv commands.Invocation, name, fallback string) string {
	if v, ok := inv.Flag(name); ok && strings.TrimSpace(v) != "" {
		return v
	}
	return fallback
}

func flagBool(inv commands.Invocation, name string) bool {
	v, ok := inv.Flag(name)
	if !ok {
		return false
	}
	if v == "" {
		return true
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "y", "on":
		return true
	default:
		return false
	}
}
