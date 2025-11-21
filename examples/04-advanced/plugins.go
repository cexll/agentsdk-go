package main

import (
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
)

func pluginSummary(resp *api.Response) []string {
	if resp == nil || len(resp.Plugins) == 0 {
		return nil
	}
	lines := make([]string, 0, len(resp.Plugins))
	for _, plug := range resp.Plugins {
		lines = append(lines, plug.Name+"@"+plug.Version+" commands="+joinList(plug.Commands)+" skills="+joinList(plug.Skills))
	}
	return lines
}

func joinList(values []string) string {
	if len(values) == 0 {
		return "-"
	}
	return strings.Join(values, ",")
}
