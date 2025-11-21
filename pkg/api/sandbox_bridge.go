package api

import (
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

// buildSandboxManager wires filesystem/network/resource policies using options
// and settings.json. Respects settings.Sandbox.Enabled to allow disabling
// sandbox validation entirely. Defaults to enabled for backwards compatibility.
func buildSandboxManager(opts Options, settings *config.Settings) (*sandbox.Manager, string) {
	// Check if sandbox is explicitly disabled in settings
	if settings != nil && settings.Sandbox != nil && settings.Sandbox.Enabled != nil && !*settings.Sandbox.Enabled {
		// Return a pass-through sandbox manager that skips all validation
		root := opts.Sandbox.Root
		if root == "" {
			root = opts.ProjectRoot
		}
		return sandbox.NewManager(nil, nil, nil), filepath.Clean(root)
	}

	root := opts.Sandbox.Root
	if root == "" {
		root = opts.ProjectRoot
	}
	root = filepath.Clean(root)
	resolvedRoot, err := filepath.EvalSymlinks(root)

	fs := sandbox.NewFileSystemAllowList(root)
	if err == nil && strings.TrimSpace(resolvedRoot) != "" {
		fs.Allow(resolvedRoot)
		root = resolvedRoot
	}

	for _, extra := range additionalSandboxPaths(settings) {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}
	for _, extra := range opts.Sandbox.AllowedPaths {
		fs.Allow(extra)
		if r, err := filepath.EvalSymlinks(extra); err == nil && strings.TrimSpace(r) != "" {
			fs.Allow(r)
		}
	}

	netAllow := opts.Sandbox.NetworkAllow
	if len(netAllow) == 0 {
		netAllow = defaultNetworkAllowList(opts.EntryPoint)
	}

	nw := sandbox.NewDomainAllowList(netAllow...)
	return sandbox.NewManager(fs, nw, sandbox.NewResourceLimiter(opts.Sandbox.ResourceLimit)), root
}

func additionalSandboxPaths(settings *config.Settings) []string {
	if settings == nil || settings.Permissions == nil {
		return nil
	}
	var out []string
	seen := map[string]struct{}{}
	for _, path := range settings.Permissions.AdditionalDirectories {
		clean := strings.TrimSpace(path)
		if clean == "" {
			continue
		}
		abs, err := filepath.Abs(clean)
		if err == nil {
			clean = abs
		}
		if _, ok := seen[clean]; ok {
			continue
		}
		seen[clean] = struct{}{}
		out = append(out, clean)
	}
	return out
}
