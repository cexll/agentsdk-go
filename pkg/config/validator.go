package config

import (
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var (
	toolNamePattern      = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9_-]*$`)
	pluginSegmentPattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

// ValidateSettings checks the merged Settings structure for logical consistency.
// Aggregates all failures using errors.Join so callers can surface every issue at once.
func ValidateSettings(s *Settings) error {
	if s == nil {
		return errors.New("settings is nil")
	}

	var errs []error

	// model
	if strings.TrimSpace(s.Model) == "" {
		errs = append(errs, errors.New("model is required"))
	}

	// permissions
	errs = append(errs, validatePermissionsConfig(s.Permissions)...)

	// hooks
	errs = append(errs, validateHooksConfig(s.Hooks)...)

	// sandbox
	errs = append(errs, validateSandboxConfig(s.Sandbox)...)

	// mcp
	errs = append(errs, validateMCPConfig(s.MCP, s.LegacyMCPServers)...)

	// plugins & marketplaces
	errs = append(errs, validatePluginsConfig(s.EnabledPlugins, s.ExtraKnownMarketplaces)...)

	// status line
	errs = append(errs, validateStatusLineConfig(s.StatusLine)...)

	// force login options
	errs = append(errs, validateForceLoginConfig(s.ForceLoginMethod, s.ForceLoginOrgUUID)...)

	if len(errs) == 0 {
		return nil
	}
	return errors.Join(errs...)
}

func validatePermissionsConfig(p *PermissionsConfig) []error {
	if p == nil {
		return nil
	}
	var errs []error

	mode := strings.TrimSpace(p.DefaultMode)
	switch mode {
	case "askBeforeRunningTools", "acceptReadOnly", "acceptEdits", "bypassPermissions":
	case "":
		errs = append(errs, errors.New("permissions.defaultMode is required"))
	default:
		errs = append(errs, fmt.Errorf("permissions.defaultMode %q is not supported", mode))
	}

	if p.DisableBypassPermissionsMode != "" && p.DisableBypassPermissionsMode != "disable" {
		errs = append(errs, fmt.Errorf("permissions.disableBypassPermissionsMode must be \"disable\", got %q", p.DisableBypassPermissionsMode))
	}

	errs = append(errs, validateRuleSlice("permissions.allow", p.Allow)...)
	errs = append(errs, validateRuleSlice("permissions.ask", p.Ask)...)
	errs = append(errs, validateRuleSlice("permissions.deny", p.Deny)...)

	for i, dir := range p.AdditionalDirectories {
		if strings.TrimSpace(dir) == "" {
			errs = append(errs, fmt.Errorf("permissions.additionalDirectories[%d] is empty", i))
		}
	}

	return errs
}

func validateRuleSlice(label string, rules []string) []error {
	var errs []error
	for i, rule := range rules {
		if err := validatePermissionRule(rule); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: %w", label, i, err))
		}
	}
	return errs
}

// validatePermissionRule enforces the Tool(target) pattern used by allow/ask/deny.
func validatePermissionRule(rule string) error {
	rule = strings.TrimSpace(rule)
	if rule == "" {
		return errors.New("permission rule is empty")
	}

	if !strings.Contains(rule, "(") {
		return nil
	}

	if !strings.HasSuffix(rule, ")") {
		return fmt.Errorf("permission rule %q must end with )", rule)
	}
	if strings.Count(rule, "(") != 1 || strings.Count(rule, ")") != 1 {
		return fmt.Errorf("permission rule %q must look like Tool(pattern)", rule)
	}
	open := strings.IndexRune(rule, '(')
	tool := rule[:open]
	target := rule[open+1 : len(rule)-1]
	if err := validateToolName(tool); err != nil {
		return fmt.Errorf("invalid tool name: %w", err)
	}
	if strings.TrimSpace(target) == "" {
		return fmt.Errorf("permission rule %q target is empty", rule)
	}
	return nil
}

// validateToolName ensures hooks and permission prefixes use a predictable charset.
func validateToolName(name string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("tool name is empty")
	}
	if !toolNamePattern.MatchString(name) {
		return fmt.Errorf("tool name %q must match %s", name, toolNamePattern.String())
	}
	return nil
}

// validateToolPattern accepts literal tool names, wildcard "*", and arbitrary regex patterns.
// Selector in pkg/core/hooks compiles the provided string, so we enforce regex validity here
// while still allowing the catch-all wildcard used in configs.
func validateToolPattern(pattern string) error {
	pattern = strings.TrimSpace(pattern)
	if pattern == "" {
		return errors.New("tool pattern is empty")
	}
	if pattern == "*" {
		return nil
	}
	if _, err := regexp.Compile(pattern); err != nil {
		return fmt.Errorf("tool pattern %q is not a valid regexp: %w", pattern, err)
	}
	return nil
}

func validateHooksConfig(h *HooksConfig) []error {
	if h == nil {
		return nil
	}
	var errs []error
	errs = append(errs, validateHookMap("hooks.preToolUse", h.PreToolUse)...)
	errs = append(errs, validateHookMap("hooks.postToolUse", h.PostToolUse)...)
	errs = append(errs, validateHookMap("hooks.permissionRequest", h.PermissionRequest)...)
	errs = append(errs, validateHookMap("hooks.sessionStart", h.SessionStart)...)
	errs = append(errs, validateHookMap("hooks.sessionEnd", h.SessionEnd)...)
	errs = append(errs, validateHookMap("hooks.subagentStart", h.SubagentStart)...)
	errs = append(errs, validateHookMap("hooks.subagentStop", h.SubagentStop)...)
	return errs
}

func validateHookMap(label string, hooks map[string]string) []error {
	if len(hooks) == 0 {
		return nil
	}
	keys := make([]string, 0, len(hooks))
	for k := range hooks {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	var errs []error
	for _, tool := range keys {
		cmd := hooks[tool]
		if err := validateToolPattern(tool); err != nil {
			errs = append(errs, fmt.Errorf("%s[%s]: %w", label, tool, err))
		}
		if strings.TrimSpace(cmd) == "" {
			errs = append(errs, fmt.Errorf("%s[%s]: command is empty", label, tool))
		}
	}
	return errs
}

func validateSandboxConfig(s *SandboxConfig) []error {
	if s == nil {
		return nil
	}
	var errs []error
	for i, cmd := range s.ExcludedCommands {
		if strings.TrimSpace(cmd) == "" {
			errs = append(errs, fmt.Errorf("sandbox.excludedCommands[%d] is empty", i))
		}
	}
	if s.Network != nil {
		if s.Network.HTTPProxyPort != nil {
			if err := validatePortRange(*s.Network.HTTPProxyPort); err != nil {
				errs = append(errs, fmt.Errorf("sandbox.network.httpProxyPort: %w", err))
			}
		}
		if s.Network.SocksProxyPort != nil {
			if err := validatePortRange(*s.Network.SocksProxyPort); err != nil {
				errs = append(errs, fmt.Errorf("sandbox.network.socksProxyPort: %w", err))
			}
		}
	}
	return errs
}

func validateMCPConfig(cfg *MCPConfig, legacy []string) []error {
	var errs []error
	if len(legacy) > 0 {
		errs = append(errs, errors.New("mcpServers is deprecated; migrate to mcp.servers map"))
	}
	if cfg == nil || len(cfg.Servers) == 0 {
		return errs
	}
	names := make([]string, 0, len(cfg.Servers))
	for name := range cfg.Servers {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			errs = append(errs, errors.New("mcp.servers has an empty name"))
			continue
		}
		entry := cfg.Servers[name]
		serverType := strings.ToLower(strings.TrimSpace(entry.Type))
		if serverType == "" {
			serverType = "stdio"
		}
		if entry.TimeoutSeconds < 0 {
			errs = append(errs, fmt.Errorf("mcp.servers[%s].timeoutSeconds must be >=0", name))
		}
		switch serverType {
		case "stdio":
			if strings.TrimSpace(entry.Command) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].command is required for type stdio", name))
			}
		case "http", "sse":
			if strings.TrimSpace(entry.URL) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].url is required for type %s", name, serverType))
			}
		default:
			errs = append(errs, fmt.Errorf("mcp.servers[%s].type %q is not supported", name, entry.Type))
		}
		for k := range entry.Headers {
			if strings.TrimSpace(k) == "" {
				errs = append(errs, fmt.Errorf("mcp.servers[%s].headers contains empty key", name))
				break
			}
		}
	}
	return errs
}

// validatePortRange expects a TCP/UDP port in the inclusive 1-65535 range.
func validatePortRange(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d out of range (1-65535)", port)
	}
	return nil
}

func validatePluginsConfig(enabled map[string]bool, marketplaces map[string]MarketplaceSource) []error {
	var errs []error
	if len(enabled) > 0 {
		keys := make([]string, 0, len(enabled))
		for k := range enabled {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, key := range keys {
			if err := validatePluginKey(key); err != nil {
				errs = append(errs, fmt.Errorf("enabledPlugins[%s]: %w", key, err))
			}
		}
	}
	if len(marketplaces) > 0 {
		names := make([]string, 0, len(marketplaces))
		for name := range marketplaces {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			src := marketplaces[name]
			if err := validateMarketplaceSource(&src); err != nil {
				errs = append(errs, fmt.Errorf("extraKnownMarketplaces[%s]: %w", name, err))
			}
		}
	}
	return errs
}

// validatePluginKey enforces the plugin-name@marketplace-name syntax.
func validatePluginKey(key string) error {
	key = strings.TrimSpace(key)
	if key == "" {
		return errors.New("plugin key is empty")
	}
	parts := strings.Split(key, "@")
	if len(parts) != 2 {
		return fmt.Errorf("plugin key %q must be formatted as plugin@marketplace", key)
	}
	plugin, market := parts[0], parts[1]
	if plugin == "" || market == "" {
		return fmt.Errorf("plugin key %q must include non-empty plugin and marketplace", key)
	}
	if !pluginSegmentPattern.MatchString(plugin) {
		return fmt.Errorf("plugin segment %q has invalid characters", plugin)
	}
	if !pluginSegmentPattern.MatchString(market) {
		return fmt.Errorf("marketplace segment %q has invalid characters", market)
	}
	return nil
}

// validateMarketplaceSource validates marketplace source type only, as requested.
func validateMarketplaceSource(source *MarketplaceSource) error {
	if source == nil {
		return errors.New("marketplace source is nil")
	}
	switch source.Source {
	case "github", "git", "directory":
		return nil
	case "":
		return errors.New("marketplace source is empty")
	default:
		return fmt.Errorf("unsupported marketplace source %q", source.Source)
	}
}

func validateStatusLineConfig(cfg *StatusLineConfig) []error {
	if cfg == nil {
		return nil
	}
	var errs []error
	typ := strings.TrimSpace(cfg.Type)
	switch typ {
	case "command":
		if strings.TrimSpace(cfg.Command) == "" {
			errs = append(errs, errors.New("statusLine.command is required when type=command"))
		}
	case "template":
		if strings.TrimSpace(cfg.Template) == "" {
			errs = append(errs, errors.New("statusLine.template is required when type=template"))
		}
	case "":
		errs = append(errs, errors.New("statusLine.type is required"))
	default:
		errs = append(errs, fmt.Errorf("statusLine.type %q is not supported", cfg.Type))
	}
	if cfg.IntervalSeconds < 0 {
		errs = append(errs, errors.New("statusLine.intervalSeconds cannot be negative"))
	}
	if cfg.TimeoutSeconds < 0 {
		errs = append(errs, errors.New("statusLine.timeoutSeconds cannot be negative"))
	}
	return errs
}

func validateForceLoginConfig(method, org string) []error {
	rawOrg := org
	method = strings.TrimSpace(method)
	org = strings.TrimSpace(org)
	if method == "" {
		return nil
	}

	var errs []error
	if method != "claudeai" && method != "console" {
		errs = append(errs, fmt.Errorf("forceLoginMethod must be \"claudeai\" or \"console\", got %q", method))
	}
	if rawOrg != "" && org == "" {
		errs = append(errs, errors.New("forceLoginOrgUUID cannot be blank"))
	}
	return errs
}
