package agent

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"strings"
	"sync/atomic"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/workflow"
)

// ForkOption customizes how Fork clones the agent.
type ForkOption func(*forkConfig)

type forkConfig struct {
	toolWhitelist map[string]struct{}
}

// WithForkToolWhitelist restricts cloned agents to the provided tool names.
func WithForkToolWhitelist(names ...string) ForkOption {
	return func(cfg *forkConfig) {
		if cfg.toolWhitelist == nil {
			cfg.toolWhitelist = map[string]struct{}{}
		}
		for _, name := range names {
			trimmed := strings.TrimSpace(name)
			if trimmed == "" {
				continue
			}
			cfg.toolWhitelist[trimmed] = struct{}{}
		}
	}
}

// SubAgentManager delegates execution to forked agents while managing session forks.
type SubAgentManager struct {
	parent  Agent
	runCtx  RunContext
	session session.Session
	bus     *event.EventBus
	prefix  string
	seq     atomic.Uint64
}

// SubAgentManagerOption configures manager behavior.
type SubAgentManagerOption func(*SubAgentManager)

// NewSubAgentManager wires delegation support on top of parent.
func NewSubAgentManager(parent Agent, opts ...SubAgentManagerOption) (*SubAgentManager, error) {
	if parent == nil {
		return nil, errors.New("subagent: parent agent is nil")
	}
	manager := &SubAgentManager{
		parent: parent,
		runCtx: RunContext{},
		prefix: "subagent",
	}
	for _, opt := range opts {
		if opt != nil {
			opt(manager)
		}
	}
	manager.runCtx = manager.runCtx.Normalize()
	return manager, nil
}

// WithSubAgentBaseRunContext overrides the default run context for delegations.
func WithSubAgentBaseRunContext(rc RunContext) SubAgentManagerOption {
	return func(m *SubAgentManager) {
		m.runCtx = rc
	}
}

// WithSubAgentSession sets the default session used for forking when requests omit one.
func WithSubAgentSession(sess session.Session) SubAgentManagerOption {
	return func(m *SubAgentManager) {
		m.session = sess
	}
}

// WithSubAgentEventBus enables event replay onto the provided bus.
func WithSubAgentEventBus(bus *event.EventBus) SubAgentManagerOption {
	return func(m *SubAgentManager) {
		m.bus = bus
	}
}

// WithSubAgentIDPrefix customizes generated session identifiers for forks.
func WithSubAgentIDPrefix(prefix string) SubAgentManagerOption {
	return func(m *SubAgentManager) {
		if strings.TrimSpace(prefix) != "" {
			m.prefix = strings.TrimSpace(prefix)
		}
	}
}

// Delegate satisfies workflow.SubAgentExecutor by forking a child agent per request.
func (m *SubAgentManager) Delegate(ctx context.Context, req workflow.SubAgentRequest) (workflow.SubAgentResult, error) {
	if m == nil {
		return workflow.SubAgentResult{}, errors.New("subagent: manager is nil")
	}
	if m.parent == nil {
		return workflow.SubAgentResult{}, errors.New("subagent: parent agent is nil")
	}
	instruction := strings.TrimSpace(req.Instruction)
	if instruction == "" {
		return workflow.SubAgentResult{}, errors.New("subagent: instruction is empty")
	}

	var opts []ForkOption
	if len(req.ToolWhitelist) > 0 {
		opts = append(opts, WithForkToolWhitelist(req.ToolWhitelist...))
	}
	child, err := m.parent.Fork(opts...)
	if err != nil {
		return workflow.SubAgentResult{}, err
	}

	sourceSession := req.Session
	if sourceSession == nil {
		sourceSession = m.session
	}
	childSession, sessionID, err := m.resolveSession(sourceSession, req)
	if err != nil {
		return workflow.SubAgentResult{}, err
	}

	runCtx := m.runCtx
	if sessionID != "" {
		runCtx.SessionID = sessionID
	}
	runCtx = runCtx.Normalize()
	if ctx == nil {
		ctx = context.Background()
	}
	childCtx := WithRunContext(ctx, runCtx)

	res, runErr := child.Run(childCtx, instruction)
	result := workflow.SubAgentResult{
		ID:            req.ID,
		SessionID:     runCtx.SessionID,
		SharedSession: req.ShareSession,
		Session:       childSession,
		Metadata:      cloneMetadata(req.Metadata),
	}
	if res != nil {
		result.Output = res.Output
		result.StopReason = res.StopReason
		result.Events = cloneEvents(res.Events)
		result.ToolCalls = exportToolCalls(res.ToolCalls)
		result.Usage = exportUsage(res.Usage)
	}

	if req.ShareEventBus && m.bus != nil {
		for _, evt := range result.Events {
			_ = m.bus.Emit(evt)
		}
	}

	if runErr != nil {
		result.Error = runErr.Error()
		return result, runErr
	}
	return result, nil
}

func (m *SubAgentManager) resolveSession(source session.Session, req workflow.SubAgentRequest) (session.Session, string, error) {
	if source == nil {
		id := strings.TrimSpace(req.SessionID)
		if id == "" {
			id = m.nextSessionName("")
		}
		return nil, id, nil
	}
	if req.ShareSession {
		id := source.ID()
		if trimmed := strings.TrimSpace(req.SessionID); trimmed != "" {
			id = trimmed
		}
		return source, id, nil
	}
	targetID := strings.TrimSpace(req.SessionID)
	if targetID == "" {
		targetID = m.nextSessionName(source.ID())
	}
	child, err := source.Fork(targetID)
	if err != nil {
		return nil, "", fmt.Errorf("subagent: fork session: %w", err)
	}
	return child, child.ID(), nil
}

func (m *SubAgentManager) nextSessionName(base string) string {
	seq := m.seq.Add(1)
	prefix := strings.TrimSpace(base)
	if prefix == "" {
		prefix = m.prefix
	}
	if prefix == "" {
		prefix = "subagent"
	}
	return fmt.Sprintf("%s-%06d", prefix, seq)
}

func cloneMetadata(src map[string]any) map[string]any {
	if len(src) == 0 {
		return nil
	}
	return maps.Clone(src)
}

func cloneEvents(events []event.Event) []event.Event {
	if len(events) == 0 {
		return nil
	}
	cloned := make([]event.Event, len(events))
	copy(cloned, events)
	return cloned
}

func exportToolCalls(calls []ToolCall) []workflow.SubAgentToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]workflow.SubAgentToolCall, 0, len(calls))
	for _, call := range calls {
		out = append(out, workflow.SubAgentToolCall{
			Name:     call.Name,
			Params:   maps.Clone(call.Params),
			Output:   call.Output,
			Error:    call.Error,
			Duration: call.Duration,
			Metadata: maps.Clone(call.Metadata),
		})
	}
	return out
}

func exportUsage(u TokenUsage) workflow.SubAgentUsage {
	return workflow.SubAgentUsage{
		InputTokens:  u.InputTokens,
		OutputTokens: u.OutputTokens,
		TotalTokens:  u.TotalTokens,
		CacheTokens:  u.CacheTokens,
	}
}
