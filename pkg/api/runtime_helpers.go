package api

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func availableTools(registry *tool.Registry, whitelist map[string]struct{}) []model.ToolDefinition {
	if registry == nil {
		return nil
	}
	tools := registry.List()
	defs := make([]model.ToolDefinition, 0, len(tools))
	for _, impl := range tools {
		if impl == nil {
			continue
		}
		name := strings.TrimSpace(impl.Name())
		if name == "" {
			continue
		}
		canon := canonicalToolName(name)
		if canon == "" {
			continue
		}
		if len(whitelist) > 0 {
			if _, ok := whitelist[canon]; !ok {
				continue
			}
		}
		defs = append(defs, model.ToolDefinition{
			Name:        name,
			Description: strings.TrimSpace(impl.Description()),
			Parameters:  schemaToMap(impl.Schema()),
		})
	}
	sort.Slice(defs, func(i, j int) bool { return defs[i].Name < defs[j].Name })
	return defs
}

func schemaToMap(schema *tool.JSONSchema) map[string]any {
	if schema == nil {
		return nil
	}
	payload := map[string]any{}
	if schema.Type != "" {
		payload["type"] = schema.Type
	}
	if len(schema.Properties) > 0 {
		payload["properties"] = schema.Properties
	}
	if len(schema.Required) > 0 {
		payload["required"] = append([]string(nil), schema.Required...)
	}
	return payload
}

func convertMessages(msgs []message.Message) []model.Message {
	if len(msgs) == 0 {
		return nil
	}
	out := make([]model.Message, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, model.Message{
			Role:      msg.Role,
			Content:   msg.Content,
			ToolCalls: convertToolCalls(msg.ToolCalls),
		})
	}
	return out
}

func convertToolCalls(calls []message.ToolCall) []model.ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]model.ToolCall, len(calls))
	for i, call := range calls {
		out[i] = model.ToolCall{ID: call.ID, Name: call.Name, Arguments: cloneArguments(call.Arguments)}
	}
	return out
}

func cloneArguments(args map[string]any) map[string]any {
	if len(args) == 0 {
		return nil
	}
	dup := make(map[string]any, len(args))
	for k, v := range args {
		dup[k] = v
	}
	return dup
}

func registerSkills(registrations []SkillRegistration) (*skills.Registry, error) {
	reg := skills.NewRegistry()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: skill handler is nil")
		}
		if err := reg.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return reg, nil
}

func registerCommands(registrations []CommandRegistration) (*commands.Executor, error) {
	exec := commands.NewExecutor()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: command handler is nil")
		}
		if err := exec.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return exec, nil
}

func registerSubagents(registrations []SubagentRegistration) (*subagents.Manager, error) {
	if len(registrations) == 0 {
		return nil, nil
	}
	mgr := subagents.NewManager()
	for _, entry := range registrations {
		if entry.Handler == nil {
			return nil, errors.New("api: subagent handler is nil")
		}
		if err := mgr.Register(entry.Definition, entry.Handler); err != nil {
			return nil, err
		}
	}
	return mgr, nil
}

type loaderOptions struct {
	ProjectRoot string
	UserHome    string
	EnableUser  bool
}

func buildLoaderOptions(opts Options) loaderOptions {
	return loaderOptions{
		ProjectRoot: opts.ProjectRoot,
		UserHome:    "",
		EnableUser:  false,
	}
}

func buildCommandsExecutor(opts Options) (*commands.Executor, []error) {
	loader := buildLoaderOptions(opts)
	fsRegs, errs := commands.LoadFromFS(commands.LoaderOptions{
		ProjectRoot: loader.ProjectRoot,
		UserHome:    loader.UserHome,
		EnableUser:  loader.EnableUser,
	})

	merged := mergeCommandRegistrations(fsRegs, opts.Commands, &errs)

	exec := commands.NewExecutor()
	for _, reg := range merged {
		if err := exec.Register(reg.Definition, reg.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return exec, errs
}

func mergeCommandRegistrations(fsRegs []commands.CommandRegistration, manual []CommandRegistration, errs *[]error) []commands.CommandRegistration {
	merged := make([]commands.CommandRegistration, 0, len(fsRegs)+len(manual))
	index := map[string]int{}

	add := func(def commands.Definition, handler commands.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: command name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: command %s handler is nil", key))
			return
		}
		reg := commands.CommandRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = reg // manual overrides loader
			return
		}
		index[key] = len(merged)
		merged = append(merged, reg)
	}

	for _, reg := range fsRegs {
		add(reg.Definition, reg.Handler, "loader")
	}
	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	return merged
}

func buildSkillsRegistry(opts Options) (*skills.Registry, []error) {
	loader := buildLoaderOptions(opts)
	fsRegs, errs := skills.LoadFromFS(skills.LoaderOptions{
		ProjectRoot: loader.ProjectRoot,
		UserHome:    loader.UserHome,
		EnableUser:  loader.EnableUser,
	})

	merged := mergeSkillRegistrations(fsRegs, opts.Skills, &errs)

	reg := skills.NewRegistry()
	for _, entry := range merged {
		if err := reg.Register(entry.Definition, entry.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return reg, errs
}

func mergeSkillRegistrations(fsRegs []skills.SkillRegistration, manual []SkillRegistration, errs *[]error) []skills.SkillRegistration {
	merged := make([]skills.SkillRegistration, 0, len(fsRegs)+len(manual))
	index := map[string]int{}

	add := func(def skills.Definition, handler skills.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: skill name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: skill %s handler is nil", key))
			return
		}
		reg := skills.SkillRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = reg
			return
		}
		index[key] = len(merged)
		merged = append(merged, reg)
	}

	for _, reg := range fsRegs {
		add(reg.Definition, reg.Handler, "loader")
	}
	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	return merged
}

func buildSubagentsManager(opts Options) (*subagents.Manager, []error) {
	loader := buildLoaderOptions(opts)
	projectRegs, errs := subagents.LoadFromFS(subagents.LoaderOptions{
		ProjectRoot: loader.ProjectRoot,
		UserHome:    loader.UserHome,
		EnableUser:  false,
	})

	merged := mergeSubagentRegistrations(opts.Subagents, projectRegs, &errs)
	if len(merged) == 0 {
		return nil, errs
	}

	mgr := subagents.NewManager()
	for _, reg := range merged {
		if err := mgr.Register(reg.Definition, reg.Handler); err != nil {
			errs = append(errs, err)
		}
	}
	return mgr, errs
}

func mergeSubagentRegistrations(manual []SubagentRegistration, project []subagents.SubagentRegistration, errs *[]error) []subagents.SubagentRegistration {
	merged := make([]subagents.SubagentRegistration, 0, len(manual)+len(project))
	index := map[string]int{}

	add := func(def subagents.Definition, handler subagents.Handler, source string) {
		key := strings.ToLower(strings.TrimSpace(def.Name))
		if key == "" {
			*errs = append(*errs, fmt.Errorf("api: subagent name is empty (%s)", source))
			return
		}
		if handler == nil {
			*errs = append(*errs, fmt.Errorf("api: subagent %s handler is nil", key))
			return
		}
		entry := subagents.SubagentRegistration{Definition: def, Handler: handler}
		if idx, ok := index[key]; ok {
			merged[idx] = entry
			return
		}
		index[key] = len(merged)
		merged = append(merged, entry)
	}

	for _, reg := range manual {
		add(reg.Definition, reg.Handler, "manual")
	}
	for _, reg := range project {
		add(reg.Definition, reg.Handler, "project")
	}
	return merged
}

type historyStore struct {
	mu       sync.Mutex
	data     map[string]*message.History
	lastUsed map[string]time.Time
	maxSize  int
}

func newHistoryStore(maxSize int) *historyStore {
	if maxSize <= 0 {
		maxSize = defaultMaxSessions
	}
	return &historyStore{
		data:     map[string]*message.History{},
		lastUsed: map[string]time.Time{},
		maxSize:  maxSize,
	}
}

func (s *historyStore) Get(id string) *message.History {
	if strings.TrimSpace(id) == "" {
		id = defaultSessionID(defaultEntrypoint)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := time.Now()
	if hist, ok := s.data[id]; ok {
		s.lastUsed[id] = now
		return hist
	}
	hist := message.NewHistory()
	s.data[id] = hist
	s.lastUsed[id] = now
	if len(s.data) > s.maxSize {
		s.evictOldest()
	}
	return hist
}

func (s *historyStore) evictOldest() {
	if len(s.data) <= s.maxSize {
		return
	}
	var oldestKey string
	var oldestTime time.Time
	first := true
	for id, ts := range s.lastUsed {
		if first || ts.Before(oldestTime) {
			oldestKey = id
			oldestTime = ts
			first = false
		}
	}
	if oldestKey == "" {
		return
	}
	delete(s.data, oldestKey)
	delete(s.lastUsed, oldestKey)
}

func definitionSnapshot(exec *commands.Executor, name string) commands.Definition {
	if exec == nil {
		return commands.Definition{Name: strings.ToLower(name)}
	}
	lower := strings.ToLower(strings.TrimSpace(name))
	for _, def := range exec.List() {
		if def.Name == lower {
			return def
		}
	}
	return commands.Definition{Name: lower}
}

func snapshotSandbox(mgr *sandbox.Manager) SandboxReport {
	if mgr == nil {
		return SandboxReport{}
	}
	return SandboxReport{ResourceLimits: mgr.Limits()}
}

type sessionGate struct {
	gates sync.Map // map[string]chan struct{}
}

func newSessionGate() *sessionGate {
	return &sessionGate{}
}

func (g *sessionGate) Acquire(ctx context.Context, sessionID string) error {
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		gate := make(chan struct{})
		existing, loaded := g.gates.LoadOrStore(sessionID, gate)
		if !loaded {
			if err := ctx.Err(); err != nil {
				g.Release(sessionID)
				return err
			}
			return nil
		}

		held := existing.(chan struct{}) //nolint:errcheck // sync.Map guarantees type safety for stored values
		select {
		case <-held:
			continue
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (g *sessionGate) Release(sessionID string) {
	if g == nil {
		return
	}
	existing, ok := g.gates.LoadAndDelete(sessionID)
	if !ok {
		return
	}
	close(existing.(chan struct{})) //nolint:errcheck // sync.Map guarantees type safety for stored values
}
