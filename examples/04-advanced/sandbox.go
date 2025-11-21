package main

import (
	"path/filepath"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
)

func buildSandboxOptions(cfg runConfig, projectRoot string) api.SandboxOptions {
	if !cfg.enableSandbox {
		return api.SandboxOptions{}
	}
	root := projectRoot
	if cfg.sandboxRoot != "" {
		root = cfg.sandboxRoot
	}
	allowed := []string{filepath.Join(root, "workspace"), filepath.Join(root, "shared")}
	return api.SandboxOptions{
		Root:         root,
		AllowedPaths: allowed,
		NetworkAllow: []string{cfg.allowHost},
		ResourceLimit: sandbox.ResourceLimits{
			MaxCPUPercent:  cfg.cpuLimit,
			MaxMemoryBytes: cfg.memLimit,
			MaxDiskBytes:   cfg.diskLimit,
		},
	}
}
