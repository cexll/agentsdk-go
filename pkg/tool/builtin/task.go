package toolbuiltin

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

const taskToolDescription = `Launch a new agent to handle complex, multi-step tasks autonomously. 

The Task tool launches specialized agents (subprocesses) that autonomously handle complex tasks. Each agent type has specific capabilities and tools available to it.

Available agent types and the tools they have access to:
- general-purpose: Full-access agent for multi-step research, coding, and remediation. Default model: sonnet. (Tools: all)
- explore: Fast read-only explorer for globbing, grep, and focused file reads when you need quick answers. Default model: haiku. (Tools: Glob, Grep, Read only)
- plan: Planner agent that produces multi-step strategies and implementation outlines. Default model: sonnet. (Tools: all)


When using the Task tool, you must specify a subagent_type parameter to select which agent type to use.

When NOT to use the Task tool:
- If you want to read a specific file path, use the Read or Glob tool instead of the Task tool, to find the match more quickly
- If you are searching for a specific class definition like "class Foo", use the Glob tool instead, to find the match more quickly
- If you are searching for code within a specific file or set of 2-3 files, use the Read tool instead of the Task tool, to find the match more quickly
- Other tasks that are not related to the agent descriptions above


Usage notes:
- Launch multiple agents concurrently whenever possible, to maximize performance; to do that, use a single message with multiple tool uses
- When the agent is done, it will return a single message back to you. The result returned by the agent is not visible to the user. To show the user the result, you should send a text message back to the user with a concise summary of the result.
- Each agent invocation is stateless. You will not be able to send additional messages to the agent, nor will the agent be able to communicate with you outside of its final report. Therefore, your prompt should contain a highly detailed task description for the agent to perform autonomously and you should specify exactly what information the agent should return back to you in its final and only message to you.
- Agents with "access to current context" can see the full conversation history before the tool call. When using these agents, you can write concise prompts that reference earlier context (e.g., "investigate the error discussed above") instead of repeating information. The agent will receive all prior messages and understand the context.
- The agent's outputs should generally be trusted
- Clearly tell the agent whether you expect it to write code or just to do research (search, file reads, web fetches, etc.), since it is not aware of the user's intent
- If the agent description mentions that it should be used proactively, then you should try your best to use it without the user having to ask for it first. Use your judgement.
- If the user specifies that they want you to run agents "in parallel", you MUST send a single message with multiple Task tool use content blocks. For example, if you need to launch both a code-reviewer agent and a test-runner agent in parallel, send a single message with both tool calls.

Example usage:

<example_agent_descriptions>
"code-reviewer": use this agent after you are done writing a signficant piece of code
"greeting-responder": use this agent when to respond to user greetings with a friendly joke
</example_agent_description>

<example>
user: "Please write a function that checks if a number is prime"
assistant: Sure let me write a function that checks if a number is prime
assistant: First let me use the Write tool to write a function that checks if a number is prime
assistant: I'm going to use the Write tool to write the following code:
<code>
function isPrime(n) {
  if (n <= 1) return false
  for (let i = 2; i * i <= n; i++) {
    if (n % i === 0) return false
  }
  return true
}
</code>
<commentary>
Since a signficant piece of code was written and the task was completed, now use the code-reviewer agent to review the code
</commentary>
assistant: Now let me use the code-reviewer agent to review the code
assistant: Uses the Task tool to launch the code-reviewer agent 
</example>

<example>
user: "Hello"
<commentary>
Since the user is greeting, use the greeting-responder agent to respond with a friendly joke
</commentary>
assistant: "I'm going to use the Task tool to launch the greeting-responder agent"
</example>
`

var taskSchema = &tool.JSONSchema{
	Type: "object",
	Properties: map[string]interface{}{
		"description": map[string]interface{}{
			"type":        "string",
			"description": "A short (3-5 word) description of the task",
		},
		"prompt": map[string]interface{}{
			"type":        "string",
			"description": "The task for the agent to perform",
		},
		"subagent_type": map[string]interface{}{
			"type":        "string",
			"description": "The type/name of specialized agent to use for this task (built-in or custom registered subagent).",
		},
		"model": map[string]interface{}{
			"type":        "string",
			"description": "Optional model to use for this agent. If not specified, inherits from parent. Prefer haiku for quick, straightforward tasks to minimize cost and latency.",
			"enum": []string{
				taskModelSonnet,
				taskModelOpus,
				taskModelHaiku,
			},
		},
		"resume": map[string]interface{}{
			"type":        "string",
			"description": "Optional resume/session identifier forwarded to the subagent handler (exposed as session_id). Resume semantics depend on the handler implementation.",
		},
	},
	Required: []string{"description", "prompt", "subagent_type"},
}

const (
	taskModelSonnet = "sonnet"
	taskModelOpus   = "opus"
	taskModelHaiku  = "haiku"
)

var modelAliasMap = map[string]string{
	taskModelSonnet: "claude-sonnet-4-5-20250929",
	taskModelOpus:   "claude-opus-4-20250514",
	taskModelHaiku:  "claude-3-5-haiku-20241022",
}

// TaskRunner executes a validated task invocation.
type TaskRunner func(context.Context, TaskRequest) (*tool.ToolResult, error)

// TaskRequest carries normalized parameters gathered from the tool input.
type TaskRequest struct {
	Description  string
	Prompt       string
	SubagentType string
	Model        string
	Resume       string
}

// TaskTool launches specialized subagents through the runtime.
type TaskTool struct {
	mu     sync.RWMutex
	runner TaskRunner
}

// NewTaskTool constructs an instance with no runner attached.
func NewTaskTool() *TaskTool {
	return &TaskTool{}
}

func (t *TaskTool) Name() string { return "Task" }

func (t *TaskTool) Description() string { return taskToolDescription }

func (t *TaskTool) Schema() *tool.JSONSchema { return taskSchema }

// SetRunner wires the runtime callback that executes task invocations.
func (t *TaskTool) SetRunner(runner TaskRunner) {
	t.mu.Lock()
	t.runner = runner
	t.mu.Unlock()
}

func (t *TaskTool) getRunner() TaskRunner {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.runner
}

func (t *TaskTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	if ctx == nil {
		return nil, errors.New("context is nil")
	}
	runner := t.getRunner()
	if runner == nil {
		return nil, errors.New("task runner is not configured")
	}
	payload, err := parseTaskParams(params)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return runner(ctx, payload)
}

func parseTaskParams(params map[string]interface{}) (TaskRequest, error) {
	if params == nil {
		return TaskRequest{}, errors.New("params is nil")
	}

	description, err := requiredString(params, "description")
	if err != nil {
		return TaskRequest{}, err
	}
	if err := validateTaskDescription(description); err != nil {
		return TaskRequest{}, err
	}

	prompt, err := requiredString(params, "prompt")
	if err != nil {
		return TaskRequest{}, err
	}

	subagentType, err := requiredString(params, "subagent_type")
	if err != nil {
		return TaskRequest{}, err
	}
	subagentType = strings.ToLower(subagentType)

	modelName, err := optionalModel(params)
	if err != nil {
		return TaskRequest{}, err
	}
	resumeID, err := optionalResume(params)
	if err != nil {
		return TaskRequest{}, err
	}

	return TaskRequest{
		Description:  description,
		Prompt:       prompt,
		SubagentType: subagentType,
		Model:        modelName,
		Resume:       resumeID,
	}, nil
}

func validateTaskDescription(desc string) error {
	words := strings.Fields(desc)
	if len(words) < 3 || len(words) > 5 {
		return fmt.Errorf("description must be 3-5 words, got %d", len(words))
	}
	return nil
}

func requiredString(params map[string]interface{}, key string) (string, error) {
	raw, ok := params[key]
	if !ok {
		return "", fmt.Errorf("%s is required", key)
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("%s must be string: %w", key, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s cannot be empty", key)
	}
	return value, nil
}

func optionalModel(params map[string]interface{}) (string, error) {
	raw, ok := params["model"]
	if !ok {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("model must be string: %w", err)
	}
	value = strings.ToLower(strings.TrimSpace(value))
	if value == "" {
		return "", nil
	}
	mapped, ok := modelAliasMap[value]
	if !ok {
		return "", fmt.Errorf("model %q is not supported", value)
	}
	return mapped, nil
}

func optionalResume(params map[string]interface{}) (string, error) {
	raw, ok := params["resume"]
	if !ok {
		return "", nil
	}
	value, err := coerceString(raw)
	if err != nil {
		return "", fmt.Errorf("resume must be string: %w", err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", errors.New("resume cannot be empty")
	}
	return value, nil
}
