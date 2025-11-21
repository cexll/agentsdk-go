package main

import (
	"log/slog"
	"os/exec"
	"strings"
)

func buildMCPServers(cfg runConfig, logger *slog.Logger) []string {
	if !cfg.enableMCP {
		return nil
	}
	server := cfg.mcpServer
	if server == "" {
		server = "stdio://uvx mcp-server-time"
	}
	if needsUVX(server) && !binaryAvailable("uvx") {
		logger.Warn("uvx not found; disabling MCP server", "server", server)
		return nil
	}
	logger.Info("MCP server enabled", "server", server)
	return []string{server}
}

func needsUVX(spec string) bool {
	return strings.HasPrefix(spec, "stdio://") && strings.Contains(spec, "uvx")
}

func binaryAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
