package config

import (
	"errors"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/plugins"
)

// Settings models the full contents of .claude/settings.json.
// All optional booleans use *bool so nil means "unset" and caller defaults apply.
type Settings struct {
	APIKeyHelper               string                       `json:"apiKeyHelper,omitempty"`               // /bin/sh script that returns an API key for outbound model calls.
	CleanupPeriodDays          int                          `json:"cleanupPeriodDays,omitempty"`          // Days to retain chat history locally (default 30).
	CompanyAnnouncements       []string                     `json:"companyAnnouncements,omitempty"`       // Startup announcements rotated randomly.
	Env                        map[string]string            `json:"env,omitempty"`                        // Environment variables applied to every session.
	IncludeCoAuthoredBy        *bool                        `json:"includeCoAuthoredBy,omitempty"`        // Whether to append "co-authored-by Claude" to commits/PRs.
	Permissions                *PermissionsConfig           `json:"permissions,omitempty"`                // Tool permission rules and defaults.
	Hooks                      *HooksConfig                 `json:"hooks,omitempty"`                      // Hook commands to run around tool execution.
	DisableAllHooks            *bool                        `json:"disableAllHooks,omitempty"`            // Force-disable all hooks.
	Model                      string                       `json:"model,omitempty"`                      // Override default model id.
	StatusLine                 *StatusLineConfig            `json:"statusLine,omitempty"`                 // Custom status line settings.
	OutputStyle                string                       `json:"outputStyle,omitempty"`                // Optional named output style.
	MCP                        *MCPConfig                   `json:"mcp,omitempty"`                        // MCP server definitions keyed by name.
	LegacyMCPServers           []string                     `json:"mcpServers,omitempty"`                 // Deprecated list format; kept for migration errors.
	ForceLoginMethod           string                       `json:"forceLoginMethod,omitempty"`           // Restrict login to "claudeai" or "console".
	ForceLoginOrgUUID          string                       `json:"forceLoginOrgUUID,omitempty"`          // Org UUID to auto-select during login when set.
	Sandbox                    *SandboxConfig               `json:"sandbox,omitempty"`                    // Bash sandbox configuration.
	EnableAllProjectMCPServers *bool                        `json:"enableAllProjectMcpServers,omitempty"` // Auto-approve all project .mcp.json servers.
	EnabledMCPJSONServers      []string                     `json:"enabledMcpjsonServers,omitempty"`      // Allowlist of project MCP servers.
	DisabledMCPJSONServers     []string                     `json:"disabledMcpjsonServers,omitempty"`     // Denylist of project MCP servers.
	AllowedMcpServers          []MCPServerRule              `json:"allowedMcpServers,omitempty"`          // Managed allowlist of user-configurable MCP servers.
	DeniedMcpServers           []MCPServerRule              `json:"deniedMcpServers,omitempty"`           // Managed denylist of user-configurable MCP servers.
	EnabledPlugins             map[string]bool              `json:"enabledPlugins,omitempty"`             // Plugin enable/disable map keyed by plugin id.
	ExtraKnownMarketplaces     map[string]MarketplaceSource `json:"extraKnownMarketplaces,omitempty"`     // Additional plugin marketplaces by name.
	AWSAuthRefresh             string                       `json:"awsAuthRefresh,omitempty"`             // Script to refresh AWS SSO credentials.
	AWSCredentialExport        string                       `json:"awsCredentialExport,omitempty"`        // Script that prints JSON AWS credentials.
}

// PermissionsConfig defines per-tool permission rules.
type PermissionsConfig struct {
	Allow                        []string `json:"allow,omitempty"`                        // Rules that auto-allow tool use.
	Ask                          []string `json:"ask,omitempty"`                          // Rules that require confirmation.
	Deny                         []string `json:"deny,omitempty"`                         // Rules that block tool use.
	AdditionalDirectories        []string `json:"additionalDirectories,omitempty"`        // Extra working directories Claude may access.
	DefaultMode                  string   `json:"defaultMode,omitempty"`                  // Default permission mode when opening Claude Code.
	DisableBypassPermissionsMode string   `json:"disableBypassPermissionsMode,omitempty"` // Set to "disable" to forbid bypassPermissions mode.
}

// HooksConfig maps hook matchers to shell commands. For tool-related events the
// matcher is applied to the tool name; for subagent-related events it matches
// the subagent name. Session hooks ignore matcher values other than "*" since
// there is no name to match.
type HooksConfig struct {
	PreToolUse        map[string]string `json:"PreToolUse,omitempty"`        // Commands run before specific tools.
	PostToolUse       map[string]string `json:"PostToolUse,omitempty"`       // Commands run after specific tools.
	PermissionRequest map[string]string `json:"PermissionRequest,omitempty"` // Commands run when a tool requests permission.
	SessionStart      map[string]string `json:"SessionStart,omitempty"`      // Commands run when a session starts.
	SessionEnd        map[string]string `json:"SessionEnd,omitempty"`        // Commands run when a session ends.
	SubagentStart     map[string]string `json:"SubagentStart,omitempty"`     // Commands run when a subagent starts.
	SubagentStop      map[string]string `json:"SubagentStop,omitempty"`      // Commands run when a subagent stops.
}

// SandboxConfig controls bash sandboxing.
type SandboxConfig struct {
	Enabled                   *bool                 `json:"enabled,omitempty"`                   // Enable filesystem/network sandboxing for bash.
	AutoAllowBashIfSandboxed  *bool                 `json:"autoAllowBashIfSandboxed,omitempty"`  // Auto-approve bash commands when sandboxed.
	ExcludedCommands          []string              `json:"excludedCommands,omitempty"`          // Commands that must run outside the sandbox.
	AllowUnsandboxedCommands  *bool                 `json:"allowUnsandboxedCommands,omitempty"`  // Whether dangerouslyDisableSandbox escape hatch is allowed.
	EnableWeakerNestedSandbox *bool                 `json:"enableWeakerNestedSandbox,omitempty"` // Allow weaker sandbox for unprivileged Docker.
	Network                   *SandboxNetworkConfig `json:"network,omitempty"`                   // Network-level sandbox knobs.
}

// SandboxNetworkConfig tunes sandbox network access.
type SandboxNetworkConfig struct {
	AllowUnixSockets  []string `json:"allowUnixSockets,omitempty"`  // Unix sockets exposed inside sandbox (SSH agent, docker socket).
	AllowLocalBinding *bool    `json:"allowLocalBinding,omitempty"` // Allow binding to localhost ports (macOS).
	HTTPProxyPort     *int     `json:"httpProxyPort,omitempty"`     // Port for custom HTTP proxy if bringing your own.
	SocksProxyPort    *int     `json:"socksProxyPort,omitempty"`    // Port for custom SOCKS5 proxy if bringing your own.
}

// MarketplaceConfig holds plugin marketplace related fields.
type MarketplaceConfig = plugins.MarketplaceConfig

// MarketplaceSource describes where a marketplace is hosted.
type MarketplaceSource = plugins.MarketplaceSource

// MCPConfig nests Model Context Protocol server definitions.
type MCPConfig struct {
	Servers map[string]MCPServerConfig `json:"servers,omitempty"`
}

// MCPServerConfig describes how to reach an MCP server.
type MCPServerConfig struct {
	Type           string            `json:"type"`              // stdio/http/sse
	Command        string            `json:"command,omitempty"` // for stdio
	Args           []string          `json:"args,omitempty"`
	URL            string            `json:"url,omitempty"` // for http/sse
	Env            map[string]string `json:"env,omitempty"`
	Headers        map[string]string `json:"headers,omitempty"`
	TimeoutSeconds int               `json:"timeoutSeconds,omitempty"` // optional per-transport timeout
}

// MCPServerRule constrains which MCP servers can be enabled.
type MCPServerRule struct {
	ServerName string `json:"serverName,omitempty"` // Name of the MCP server as declared in .mcp.json.
	URL        string `json:"url,omitempty"`        // Optional URL/endpoint to further pin the server.
}

// StatusLineConfig controls contextual status line rendering.
type StatusLineConfig struct {
	Type            string `json:"type"`                      // "command" executes a script; "template" renders a string.
	Command         string `json:"command,omitempty"`         // Executable to run when Type=command.
	Template        string `json:"template,omitempty"`        // Text template when Type=template.
	IntervalSeconds int    `json:"intervalSeconds,omitempty"` // Optional refresh interval in seconds.
	TimeoutSeconds  int    `json:"timeoutSeconds,omitempty"`  // Optional timeout for the command run.
}

// GetDefaultSettings returns Anthropic's documented defaults.
func GetDefaultSettings() Settings {
	return Settings{
		CleanupPeriodDays:   30,
		IncludeCoAuthoredBy: boolPtr(true),
		DisableAllHooks:     boolPtr(false),
		Permissions: &PermissionsConfig{
			DefaultMode: "askBeforeRunningTools",
		},
		Sandbox: &SandboxConfig{
			Enabled:                   boolPtr(false),
			AutoAllowBashIfSandboxed:  boolPtr(true),
			AllowUnsandboxedCommands:  boolPtr(true),
			EnableWeakerNestedSandbox: boolPtr(false),
			Network: &SandboxNetworkConfig{
				AllowLocalBinding: boolPtr(false),
			},
		},
	}
}

// Validate delegates to the new aggregated validator.
func (s *Settings) Validate() error { return ValidateSettings(s) }

// Validate ensures permission modes and toggles are within allowed values.
func (p *PermissionsConfig) Validate() error { return errors.Join(validatePermissionsConfig(p)...) }

// Validate ensures hook maps contain non-empty commands.
func (h *HooksConfig) Validate() error { return errors.Join(validateHooksConfig(h)...) }

// Validate checks sandbox and network constraints.
func (s *SandboxConfig) Validate() error { return errors.Join(validateSandboxConfig(s)...) }

// Validate enforces presence of a server name.
func (r MCPServerRule) Validate() error {
	if strings.TrimSpace(r.ServerName) == "" {
		return errors.New("serverName is required")
	}
	return nil
}

// Validate ensures status line config is coherent.
func (s *StatusLineConfig) Validate() error { return errors.Join(validateStatusLineConfig(s)...) }

// boolPtr helps encode optional booleans.
func boolPtr(v bool) *bool { return &v }
