# Security Guide

This document explains agentsdk-go security mechanisms, configuration, and best practices.

## Security Architecture

The SDK uses a three-layer defense model:

1. **Sandbox** – filesystem and network access control  
2. **Validator** – command and parameter validation  
3. **Approval Queue** – human-in-the-loop approvals  

These layers cooperate with the 6 middleware hook points to enforce checks at critical stages.

## Sandbox Isolation

### Capabilities

- Filesystem allowlist
- Symlink resolution (prevents path traversal)
- Network allowlist

### Implementation

- `pkg/sandbox/` – sandbox manager  
- `pkg/security/sandbox.go` – sandbox core  
- `pkg/security/resolver.go` – path resolver

### Configuration Example (`.claude/settings.json`)

```json
{
  "sandbox": {
    "enabled": true,
    "allowed_paths": [
      "/tmp",
      "./workspace",
      "/var/lib/agent/data"
    ],
    "network_allow": [
      "*.anthropic.com",
      "api.example.com"
    ]
  }
}
```

### Code Example

```go
import (
    "github.com/cexll/agentsdk-go/pkg/security"
)

// Create sandbox
sandbox := security.NewSandbox(workDir)

// Allow paths
sandbox.Allow("/var/lib/agent/runtime")
sandbox.Allow(filepath.Join(workDir, ".cache"))

// Validate path
if err := sandbox.ValidatePath(targetPath); err != nil {
    return fmt.Errorf("path denied: %w", err)
}

// Validate command
if err := sandbox.ValidateCommand(command); err != nil {
    return fmt.Errorf("command denied: %w", err)
}
```

### Best Practices

1. Declare all allowed paths in config; avoid runtime adds  
2. Use absolute paths; avoid ambiguities from relatives  
3. Review sandbox config regularly; remove unused paths  
4. Call `ValidatePath` for every tool execution, not just at startup

## Command Validation

### Capabilities

Checks before execution:

- Dangerous commands (`dd`, `mkfs`, `fdisk`, `shutdown`, …)  
- Dangerous arguments (e.g., `--no-preserve-root`)  
- Dangerous patterns (`rm -rf`, `rm -r`)  
- Shell metacharacters (in Platform mode)  
- Command length limits

### Implementation

- `pkg/security/validator.go` – command validator  
- `pkg/security/validator_full_test.go` – validator tests

### Default Blocks

#### Destructive Commands

- `dd` – raw disk writes  
- `mkfs`, `mkfs.ext4` – filesystem format  
- `fdisk`, `parted` – partition editing  
- `shutdown`, `reboot`, `halt`, `poweroff` – power control  
- `mount` – mount filesystem

#### Dangerous Deletes

- `rm -rf` / `rm -fr` – recursive force delete  
- `rm -r` / `rm --recursive` – recursive delete  
- `rmdir -p` – recursive directory delete

#### Shell Metacharacters (Platform)

- `|`, `;`, `&` – command chaining  
- `>`, `<` – redirection  
- `` ` `` – command substitution

### Code Example

```go
import (
    "github.com/cexll/agentsdk-go/pkg/security"
)

validator := security.NewValidator()

if err := validator.Validate(command); err != nil {
    log.Printf("command blocked: %v", err)
    return err
}

// Allow shell metachars (CLI only)
validator.AllowShellMeta(true)
```

### Custom Rules

```go
validator.BanCommand("kubectl", "cluster ops require approval")
validator.BanCommand("helm", "helm ops require approval")

validator.BanArgument("--force")
validator.BanArgument("--insecure")

validator.BanFragment("sudo rm")
```

### Best Practices

1. Combine with JSON Schema to validate tool params  
2. Run validation in `BeforeTool` middleware  
3. Audit log blocked commands  
4. Sync with org blacklists regularly  
5. Enforce approvals for high-risk commands

## Approval Queue

### Capabilities

- Create/manage approval requests  
- Session-level allowlist (TTL)  
- Decision recording  

### Implementation

- `pkg/security/approval.go` – approval queue  
- `pkg/security/approval_test.go` – tests

### Code Example

```go
import (
    "github.com/cexll/agentsdk-go/pkg/security"
)

queue, err := security.NewApprovalQueue("/var/lib/agent/approvals")
if err != nil {
    return err
}

request, err := queue.Request(sessionID, command, []string{path})
if err != nil {
    return err
}

resolved, err := queue.Wait(context.Background(), request.ID)
if err != nil {
    return err
}
if resolved.State != security.ApprovalApproved {
    return fmt.Errorf("approval denied")
}
return executeCommand(command)
```

### Decisions

```go
// Approve (with whitelist TTL)
err := queue.Approve(requestID, approverID, 3600) // 1h whitelist
if err != nil {
    return err
}

// Deny
err = queue.Deny(requestID, approverID, "policy violation")
if err != nil {
    return err
}
```

### Best Practices

1. Create and back up the approval storage path before deploy  
2. Set short TTLs; avoid permanent bypass  
3. Log all approval actions  
4. Enforce approval timeouts; auto-deny expired  
5. Cap whitelist TTL and re-approve regularly

> 运行时可通过 `api.Options{ApprovalQueue: ..., ApprovalWait: true}` 启用阻塞式审批。

## Middleware Security Interception

### Hook Overview

Six checkpoints:

1. `BeforeAgent` – request validation, rate limiting, blacklist  
2. `BeforeModel` – prompt injection detection, sensitive-word filter  
3. `AfterModel` – output review, secret redaction  
4. `BeforeTool` – tool permission check, param validation  
5. `AfterTool` – result review, error sanitization  
6. `AfterAgent` – audit logging, compliance checks

### BeforeAgent: Request Validation

Threats: session abuse (DoS), overlong prompts, malicious IPs.

```go
beforeAgentGuard := middleware.Middleware{
    BeforeAgent: func(ctx context.Context, req *middleware.AgentRequest) (*middleware.AgentRequest, error) {
        if blacklist.Contains(req.RemoteAddr) {
            return nil, fmt.Errorf("IP blocked: %s", req.RemoteAddr)
        }
        if !rateLimiter.Allow(req.SessionID) {
            return nil, fmt.Errorf("too many requests")
        }
        if len(req.Input) > maxInputLength {
            return nil, fmt.Errorf("input too long")
        }
        return req, nil
    },
}
```

### BeforeModel: Prompt Safety

Threats: prompt injection, sensitive data leakage, control chars.

```go
beforeModelScan := middleware.Middleware{
    BeforeModel: func(ctx context.Context, msgs []message.Message) ([]message.Message, error) {
        for _, msg := range msgs {
            content := msg.Content
            if containsInjection(content) {
                audit.Log(ctx, "prompt_injection_detected", content)
                return nil, fmt.Errorf("prompt injection detected")
            }
            if secrets := detectSecrets(content); len(secrets) > 0 {
                audit.Log(ctx, "secrets_in_prompt", secrets)
                return nil, fmt.Errorf("input contains secrets")
            }
            msg.Content = filterSensitiveWords(content)
        }
        return msgs, nil
    },
}
```

### AfterModel: Output Review

Threats: dangerous commands, secret leakage, malicious URLs.

```go
afterModelReview := middleware.Middleware{
    AfterModel: func(ctx context.Context, output *agent.ModelOutput) (*agent.ModelOutput, error) {
        content := output.Content
        if dangerous := detectDangerousCommand(content); dangerous != "" {
            approvalQueue.Request(sessionID, dangerous, nil)
            return nil, fmt.Errorf("model suggested dangerous command: %s", dangerous)
        }
        cleaned := redactSecrets(content)
        if cleaned != content {
            audit.Log(ctx, "model_output_redacted", "secrets_found")
            output.Content = cleaned
        }
        return output, nil
    },
}
```

### BeforeTool: Permission Check

Threats: unauthorized tool use, parameter tampering, recursive bypass.

```go
beforeToolGuard := middleware.Middleware{
    BeforeTool: func(ctx context.Context, call *middleware.ToolCall) (*middleware.ToolCall, error) {
        if !toolRegistry.Exists(call.Name) {
            return nil, fmt.Errorf("unknown tool: %s", call.Name)
        }
        if !rbac.CanInvoke(identity, call.Name) {
            audit.Log(ctx, "unauthorized_tool_call", call.Name)
            return nil, fmt.Errorf("not authorized to call: %s", call.Name)
        }
        if err := validateParams(call); err != nil {
            return nil, fmt.Errorf("param validation failed: %w", err)
        }
        if path, ok := call.Params["path"].(string); ok {
            if err := sandbox.ValidatePath(path); err != nil {
                return nil, fmt.Errorf("path denied: %w", err)
            }
        }
        return call, nil
    },
}
```

### AfterTool: Result Review

Threats: secret leakage, error info disclosure, oversized output.

```go
afterToolReview := middleware.Middleware{
    AfterTool: func(ctx context.Context, result *middleware.ToolResult) (*middleware.ToolResult, error) {
        if secrets := detectSecrets(result.Output); len(secrets) > 0 {
            result.Output = redactSecrets(result.Output)
            audit.Log(ctx, "tool_output_redacted", "secrets_found")
        }
        if result.Error != nil {
            logSecurityError(ctx, result.Error)
            result.Error = errors.New("tool execution failed")
        }
        if len(result.Output) > maxOutputLength {
            result.Output = result.Output[:maxOutputLength] + "...(truncated)"
        }
        return result, nil
    },
}
```

## Deployment Checklist

### Config

- [ ] Sandbox configured with all required allow paths  
- [ ] Command validator enabled and configured  
- [ ] Approval queue storage path created with permissions  
- [ ] Security handlers registered at all middleware hooks  
- [ ] Middleware timeouts < request timeout  
- [ ] Network allowlist configured

### Tests

```bash
go test ./pkg/security/... -v
go test ./pkg/middleware/... -v
go test ./test/integration/security/... -v
```

## Common Vulnerabilities

### Path Traversal

Mitigation:

1. Call `Sandbox.ValidatePath` on all path params  
2. Re-validate in `BeforeTool`  
3. Use absolute paths, resolve symlinks  
4. Restrict allowed prefixes

Test:

```bash
go test ./pkg/security -run TestSandbox_PathTraversal
```

### Prompt Injection

Mitigation:

1. Detect patterns in `BeforeModel`  
2. Maintain injection signature list  
3. Log suspected injections  
4. Require approval for high-risk inputs

Detection example:

```go
func containsInjection(input string) bool {
    patterns := []string{
        "ignore previous instructions",
        "ignore above",
        "disregard all",
        "system prompt",
    }
    lower := strings.ToLower(input)
    for _, pattern := range patterns {
        if strings.Contains(lower, pattern) {
            return true
        }
    }
    return false
}
```

### Secret Leakage

Mitigation:

1. Scan secrets in `BeforeModel` and `AfterModel`  
2. Clean tool output in `AfterTool`  
3. Regex common patterns  
4. Keep pre-redaction data in encrypted storage

Patterns:

```go
var secretPatterns = []*regexp.Regexp{
    regexp.MustCompile(`sk-[a-zA-Z0-9]{48}`),              // API Keys
    regexp.MustCompile(`[0-9]{4}-[0-9]{4}-[0-9]{4}-[0-9]{4}`), // Credit Cards
    regexp.MustCompile(`ghp_[a-zA-Z0-9]{36}`),             // GitHub Tokens
    regexp.MustCompile(`xox[baprs]-[a-zA-Z0-9-]+`),        // Slack Tokens
}
```

### Command Injection

- Validate all commands with `Validator.Validate`  
- Block shell metachars (Platform mode)  
- Use parameterized execution, not string concatenation  
- Limit command length

### Privilege Escalation

- Enforce RBAC in `BeforeTool`  
- Require approval for privileged ops  
- Limit recursion depth  
- Log all authorization decisions

## Security Incident Response

### Detect

Middleware errors should trigger alerts:

```go
if err != nil {
    alert.Send(alert.SecurityEvent{
        Stage:     "before_tool",
        Error:     err.Error(),
        SessionID: sessionID,
        Timestamp: time.Now(),
    })
}
```

### Contain

```go
approvalQueue.RevokeAll()
approvalQueue.SetGlobalApprovalRequired(true)
```

### Analyze

```bash
audit-export --since 1h --output /tmp/audit.json
audit-analyze /tmp/audit.json --detect-anomalies
```

### Recover

1. Patch detection logic  
2. Run regression tests  
3. Gradually restore service  
4. Watch for anomaly metrics

### Postmortem

1. Record incident timeline  
2. Root-cause analysis  
3. Update security config  
4. Refine detection rules  
5. Update documentation

## Best Practices

### Development

1. Enable all security checks by default  
2. Define JSON Schemas for every tool  
3. Cover security cases in unit tests  
4. Use static analysis

### Deployment

1. Manage policies via config files  
2. Enable all monitoring metrics  
3. Configure alert rules  
4. Prepare incident response playbooks

### Operations

1. Review audit logs regularly  
2. Update blacklists and validators  
3. Run red/blue exercises  
4. Stay current with security patches

### Audit

1. Use append-only storage for audit logs  
2. Link audit records to approval decisions  
3. Back up audit data regularly  
4. Enforce audit log integrity checks
