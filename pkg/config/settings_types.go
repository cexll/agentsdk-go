package config

import (
	"errors"
	"strings"
)

// Settings models the full contents of .claude/settings.json.
// All optional booleans use *bool so nil means "unset" and caller defaults apply.
type Settings struct {
	APIKeyHelper         string             `json:"apiKeyHelper,omitempty"`         // /bin/sh script that returns an API key for outbound model calls.
	CleanupPeriodDays    *int               `json:"cleanupPeriodDays,omitempty"`    // Days to retain chat history locally (default 30). Set to 0 to disable.
	CompanyAnnouncements []string           `json:"companyAnnouncements,omitempty"` // Startup announcements rotated randomly.
	Env                  map[string]string  `json:"env,omitempty"`                  // Environment variables applied to every session.
	IncludeCoAuthoredBy  *bool              `json:"includeCoAuthoredBy,omitempty"`  // Whether to append "co-authored-by Claude" to commits/PRs.
	Permissions          *PermissionsConfig `json:"permissions,omitempty"`          // Tool permission rules and defaults.
	DisallowedTools      []string           `json:"disallowedTools,omitempty"`      // Tool blacklist; disallowed tools are not registered.
	Hooks                *HooksConfig       `json:"hooks,omitempty"`                // Hook commands to run around tool execution.
	DisableAllHooks      *bool              `json:"disableAllHooks,omitempty"`      // Force-disable all hooks.
	Model                string             `json:"model,omitempty"`                // Override default model id.
	StatusLine           *StatusLineConfig  `json:"statusLine,omitempty"`           // Custom status line settings.
	OutputStyle          string             `json:"outputStyle,omitempty"`          // Optional named output style.
	MCP                  *MCPConfig         `json:"mcp,omitempty"`                  // MCP server definitions keyed by name.
	LegacyMCPServers     []string           `json:"mcpServers,omitempty"`           // Deprecated list format; kept for migration errors.
	ForceLoginMethod     string             `json:"forceLoginMethod,omitempty"`     // Restrict login to "claudeai" or "console".
	ForceLoginOrgUUID    string             `json:"forceLoginOrgUUID,omitempty"`    // Org UUID to auto-select during login when set.
	Sandbox              *SandboxConfig     `json:"sandbox,omitempty"`              // Bash sandbox configuration.
	BashOutput           *BashOutputConfig  `json:"bashOutput,omitempty"`           // Thresholds for spooling bash output to disk.
	ToolOutput           *ToolOutputConfig  `json:"toolOutput,omitempty"`           // Thresholds for persisting large tool outputs to disk.
	AllowedMcpServers    []MCPServerRule    `json:"allowedMcpServers,omitempty"`    // Managed allowlist of user-configurable MCP servers.
	DeniedMcpServers     []MCPServerRule    `json:"deniedMcpServers,omitempty"`     // Managed denylist of user-configurable MCP servers.
	AWSAuthRefresh       string             `json:"awsAuthRefresh,omitempty"`       // Script to refresh AWS SSO credentials.
	AWSCredentialExport  string             `json:"awsCredentialExport,omitempty"`  // Script that prints JSON AWS credentials.
	RespectGitignore     *bool              `json:"respectGitignore,omitempty"`     // Whether Glob/Grep tools should respect .gitignore patterns.
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

// HookDefinition describes a single hook action bound to a matcher entry.
// Supports command (shell), prompt (LLM), and agent hook types per the Claude Code spec.
type HookDefinition struct {
	Type          string `json:"type"`                    // "command" (default), "prompt", or "agent"
	Command       string `json:"command,omitempty"`       // Shell command (type=command)
	Prompt        string `json:"prompt,omitempty"`        // LLM prompt (type=prompt)
	Model         string `json:"model,omitempty"`         // Model override (type=prompt/agent)
	Timeout       int    `json:"timeout,omitempty"`       // Per-hook timeout in seconds (0 = use default)
	Async         bool   `json:"async,omitempty"`         // Fire-and-forget execution
	Once          bool   `json:"once,omitempty"`          // Execute only once per session
	StatusMessage string `json:"statusMessage,omitempty"` // Status message shown during execution
}

// HookMatcherEntry pairs a matcher pattern with one or more hook definitions.
type HookMatcherEntry struct {
	Matcher string           `json:"matcher"`
	Hooks   []HookDefinition `json:"hooks"`
}

// HooksConfig maps event types to matcher entries. For tool-related events the
// matcher is applied to the tool name; for session events it matches source/reason;
// for notification it matches type; for subagent events it matches agent type.
//
// Supports both Claude Code official format (array of HookMatcherEntry) and
// SDK simplified format (map[string]string) via custom UnmarshalJSON.
type HooksConfig struct {
	PreToolUse         []HookMatcherEntry `json:"PreToolUse,omitempty"`
	PostToolUse        []HookMatcherEntry `json:"PostToolUse,omitempty"`
	PostToolUseFailure []HookMatcherEntry `json:"PostToolUseFailure,omitempty"`
	PermissionRequest  []HookMatcherEntry `json:"PermissionRequest,omitempty"`
	SessionStart       []HookMatcherEntry `json:"SessionStart,omitempty"`
	SessionEnd         []HookMatcherEntry `json:"SessionEnd,omitempty"`
	SubagentStart      []HookMatcherEntry `json:"SubagentStart,omitempty"`
	SubagentStop       []HookMatcherEntry `json:"SubagentStop,omitempty"`
	Stop               []HookMatcherEntry `json:"Stop,omitempty"`
	Notification       []HookMatcherEntry `json:"Notification,omitempty"`
	UserPromptSubmit   []HookMatcherEntry `json:"UserPromptSubmit,omitempty"`
	PreCompact         []HookMatcherEntry `json:"PreCompact,omitempty"`
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

// BashOutputConfig configures when bash output is spooled to disk.
type BashOutputConfig struct {
	SyncThresholdBytes  *int `json:"syncThresholdBytes,omitempty"`  // Spool sync output to disk after exceeding this many bytes.
	AsyncThresholdBytes *int `json:"asyncThresholdBytes,omitempty"` // Spool async output to disk after exceeding this many bytes.
}

// ToolOutputConfig configures when tool output is persisted to disk.
type ToolOutputConfig struct {
	DefaultThresholdBytes int            `json:"defaultThresholdBytes,omitempty"` // Persist output to disk after exceeding this many bytes (0 = SDK default).
	PerToolThresholdBytes map[string]int `json:"perToolThresholdBytes,omitempty"` // Optional per-tool thresholds keyed by canonical tool name.
}

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
	cleanupPeriodDays := 30
	syncThresholdBytes := 30_000
	asyncThresholdBytes := 1024 * 1024
	return Settings{
		CleanupPeriodDays:   intPtr(cleanupPeriodDays),
		IncludeCoAuthoredBy: boolPtr(true),
		DisableAllHooks:     boolPtr(false),
		RespectGitignore:    boolPtr(true),
		BashOutput: &BashOutputConfig{
			SyncThresholdBytes:  &syncThresholdBytes,
			AsyncThresholdBytes: &asyncThresholdBytes,
		},
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

// intPtr helps encode optional integers.
func intPtr(v int) *int { return &v }
