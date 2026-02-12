package security

import (
	"errors"
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
)

// PermissionAction represents the enforcement outcome for a tool invocation.
type PermissionAction string

const (
	PermissionUnknown PermissionAction = "unknown"
	PermissionAllow   PermissionAction = "allow"
	PermissionAsk     PermissionAction = "ask"
	PermissionDeny    PermissionAction = "deny"
)

// PermissionDecision captures the matched rule and derived target string.
type PermissionDecision struct {
	Action PermissionAction
	Rule   string
	Tool   string
	Target string
}

// PermissionAudit records executed decisions for later inspection.
type PermissionAudit struct {
	Tool      string
	Target    string
	Rule      string
	Action    PermissionAction
	Timestamp time.Time
}

// PermissionMatcher evaluates tool calls against allow/ask/deny rules.
type PermissionMatcher struct {
	allow []*permissionRule
	ask   []*permissionRule
	deny  []*permissionRule
}

type permissionRule struct {
	raw       string
	tool      string
	toolMatch func(string) bool
	match     func(string) bool
}

// NewPermissionMatcher builds a matcher from the provided permissions config.
// A nil config yields a nil matcher and no error.
func NewPermissionMatcher(cfg *config.PermissionsConfig) (*PermissionMatcher, error) {
	if cfg == nil {
		return nil, nil
	}

	build := func(rules []string) ([]*permissionRule, error) {
		var compiled []*permissionRule
		for _, rule := range rules {
			r, err := compilePermissionRule(rule)
			if err != nil {
				return nil, err
			}
			compiled = append(compiled, r)
		}
		sort.SliceStable(compiled, func(i, j int) bool { return compiled[i].raw < compiled[j].raw })
		return compiled, nil
	}

	allow, err := build(cfg.Allow)
	if err != nil {
		return nil, err
	}
	ask, err := build(cfg.Ask)
	if err != nil {
		return nil, err
	}
	deny, err := build(cfg.Deny)
	if err != nil {
		return nil, err
	}

	return &PermissionMatcher{allow: allow, ask: ask, deny: deny}, nil
}

// Match resolves the decision for a tool invocation. Priority: deny > ask > allow.
func (m *PermissionMatcher) Match(toolName string, params map[string]any) PermissionDecision {
	if m == nil {
		return PermissionDecision{Action: PermissionAllow, Tool: toolName}
	}

	tool := strings.TrimSpace(toolName)
	target := deriveTarget(tool, params)

	if decision, ok := m.matchRules(tool, target, m.deny, PermissionDeny); ok {
		return decision
	}
	if decision, ok := m.matchRules(tool, target, m.ask, PermissionAsk); ok {
		return decision
	}
	if decision, ok := m.matchRules(tool, target, m.allow, PermissionAllow); ok {
		return decision
	}
	return PermissionDecision{Action: PermissionUnknown, Tool: tool, Target: target}
}

func (m *PermissionMatcher) matchRules(tool, target string, rules []*permissionRule, action PermissionAction) (PermissionDecision, bool) {
	for _, rule := range rules {
		if rule.toolMatch != nil {
			if !rule.toolMatch(tool) {
				continue
			}
		} else if !strings.EqualFold(rule.tool, tool) {
			continue
		}
		if rule.match(target) {
			return PermissionDecision{Action: action, Rule: rule.raw, Tool: tool, Target: target}, true
		}
	}
	return PermissionDecision{}, false
}

func compilePermissionRule(rule string) (*permissionRule, error) {
	trimmed := strings.TrimSpace(rule)
	if trimmed == "" {
		return nil, errors.New("permission rule is empty")
	}

	// Path rule: bare glob/regex patterns containing separators or "." match any tool target.
	if !strings.ContainsRune(trimmed, '(') && (strings.Contains(trimmed, "/") || strings.Contains(trimmed, "\\") || strings.Contains(trimmed, ".")) {
		matcher, err := compilePattern(trimmed)
		if err != nil {
			return nil, fmt.Errorf("compile path rule %q: %w", rule, err)
		}
		return &permissionRule{
			raw:       trimmed,
			tool:      "*",
			toolMatch: func(string) bool { return true },
			match:     matcher,
		}, nil
	}

	// Tool name rule: match tool.Name directly (exact or glob).
	if !strings.ContainsRune(trimmed, '(') {
		toolMatcher, err := compileToolMatcher(trimmed)
		if err != nil {
			return nil, fmt.Errorf("compile tool rule %q: %w", rule, err)
		}
		return &permissionRule{
			raw:       trimmed,
			tool:      trimmed,
			toolMatch: toolMatcher,
			match:     func(string) bool { return true },
		}, nil
	}

	open := strings.IndexRune(trimmed, '(')
	if !strings.HasSuffix(trimmed, ")") {
		return nil, fmt.Errorf("permission rule %q malformed", rule)
	}
	tool := strings.TrimSpace(trimmed[:open])
	pattern := strings.TrimSuffix(trimmed[open+1:], ")")
	matcher, err := compilePattern(pattern)
	if err != nil {
		return nil, fmt.Errorf("compile rule %q: %w", rule, err)
	}
	return &permissionRule{
		raw:       trimmed,
		tool:      tool,
		toolMatch: func(name string) bool { return strings.EqualFold(tool, name) },
		match:     matcher,
	}, nil
}

func compileToolMatcher(pattern string) (func(string) bool, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return nil, errors.New("empty tool pattern")
	}
	// Exact match fast path.
	if !strings.ContainsAny(trimmed, "*?") && !strings.HasPrefix(strings.ToLower(trimmed), "regex:") && !strings.HasPrefix(strings.ToLower(trimmed), "regexp:") {
		lower := strings.ToLower(trimmed)
		return func(name string) bool { return strings.ToLower(strings.TrimSpace(name)) == lower }, nil
	}

	// Glob or regex with case-insensitive comparison.
	matcher, err := compilePattern(strings.ToLower(trimmed))
	if err != nil {
		return nil, err
	}
	return func(name string) bool {
		return matcher(strings.ToLower(strings.TrimSpace(name)))
	}, nil
}

func compilePattern(pattern string) (func(string) bool, error) {
	trimmed := strings.TrimSpace(pattern)
	if trimmed == "" {
		return nil, errors.New("empty permission pattern")
	}

	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "regex:") || strings.HasPrefix(lower, "regexp:") {
		expr := strings.TrimSpace(trimmed[strings.Index(trimmed, ":")+1:])
		re, err := regexp.Compile(expr)
		if err != nil {
			return nil, err
		}
		return re.MatchString, nil
	}

	normalizedPattern := normalizeGlobSlashes(trimmed)
	regex := globToRegex(normalizedPattern)
	re, err := regexp.Compile("^" + regex + "$")
	if err != nil {
		return nil, err
	}
	return func(target string) bool {
		return re.MatchString(normalizeGlobSlashes(target))
	}, nil
}

func globToRegex(glob string) string {
	var b strings.Builder
	for i := 0; i < len(glob); i++ {
		switch glob[i] {
		case '*':
			if i+1 < len(glob) && glob[i+1] == '*' {
				b.WriteString(".*")
				i++
			} else {
				b.WriteString(".*")
			}
		case '?':
			b.WriteString(".")
		case '.', '+', '(', ')', '|', '^', '$', '{', '}', '[', ']', '\\':
			b.WriteString("\\")
			b.WriteByte(glob[i])
		default:
			b.WriteByte(glob[i])
		}
	}
	return b.String()
}

func normalizeGlobSlashes(input string) string {
	return strings.ReplaceAll(input, "\\", "/")
}

func deriveTarget(tool string, params map[string]any) string {
	switch strings.ToLower(strings.TrimSpace(tool)) {
	case "bash":
		cmd := firstString(params, "command")
		name, args := splitCommandNameArgs(cmd)
		if name == "" {
			return strings.TrimSpace(cmd)
		}
		if args == "" {
			return name + ":"
		}
		return name + ":" + args
	case "read", "write", "edit":
		if p := firstString(params, "file_path", "path"); p != "" {
			return filepath.Clean(p)
		}
	case "taskcreate", "taskget", "taskupdate", "tasklist":
		if id := firstString(params, "task_id", "id"); id != "" {
			return id
		}
	}
	if p := firstString(params, "path", "file", "target"); p != "" {
		return filepath.Clean(p)
	}
	return firstString(params)
}

func firstString(params map[string]any, keys ...string) string {
	if params == nil {
		return ""
	}
	if len(keys) == 0 {
		for _, v := range params {
			if s := coerceToString(v); s != "" {
				return s
			}
		}
		return ""
	}
	for _, key := range keys {
		if v, ok := params[key]; ok {
			if s := coerceToString(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func coerceToString(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case []byte:
		return strings.TrimSpace(string(val))
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	default:
		return ""
	}
}

func splitCommandNameArgs(cmd string) (string, string) {
	trimmed := strings.TrimSpace(cmd)
	if trimmed == "" {
		return "", ""
	}
	fields := strings.Fields(trimmed)
	if len(fields) == 0 {
		return "", ""
	}
	name := fields[0]
	if len(fields) == 1 {
		return name, ""
	}
	return name, strings.Join(fields[1:], " ")
}
