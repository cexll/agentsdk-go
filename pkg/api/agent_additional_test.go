package api

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/agent"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
	"github.com/cexll/agentsdk-go/pkg/runtime/tasks"
	"github.com/cexll/agentsdk-go/pkg/sandbox"
	"github.com/cexll/agentsdk-go/pkg/tool"
	toolbuiltin "github.com/cexll/agentsdk-go/pkg/tool/builtin"
)

func TestRunStreamProducesEvents(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "stream"}}}}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ch, err := rt.RunStream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	var types []string
	for evt := range ch {
		types = append(types, evt.Type)
	}
	if len(types) == 0 {
		t.Fatal("expected at least one event")
	}

	find := func(name string) int {
		t.Helper()
		for i, typ := range types {
			if typ == name {
				return i
			}
		}
		t.Fatalf("expected %s event in sequence: %v", name, types)
		return -1
	}

	agentStartIdx := find(EventAgentStart)
	agentStopIdx := find(EventAgentStop)
	if agentStartIdx >= agentStopIdx {
		t.Fatalf("expected %s before %s: %v", EventAgentStart, EventAgentStop, types)
	}

	iterStartIdx := find(EventIterationStart)
	iterStopIdx := find(EventIterationStop)
	if !(agentStartIdx < iterStartIdx && iterStartIdx < agentStopIdx) {
		t.Fatalf("expected iteration to run within agent lifecycle: %v", types)
	}
	if !(iterStartIdx < iterStopIdx && iterStopIdx < agentStopIdx) {
		t.Fatalf("expected iteration_stop before agent_stop: %v", types)
	}

	messageStartIdx := find(EventMessageStart)
	messageStopIdx := find(EventMessageStop)
	if !(iterStartIdx < messageStartIdx && messageStartIdx < messageStopIdx && messageStopIdx < iterStopIdx) {
		t.Fatalf("expected message lifecycle nested within iteration: %v", types)
	}

	contentStartIdx := find(EventContentBlockStart)
	contentDeltaIdx := find(EventContentBlockDelta)
	contentStopIdx := find(EventContentBlockStop)
	if !(messageStartIdx < contentStartIdx && contentStartIdx < contentDeltaIdx && contentDeltaIdx < contentStopIdx && contentStopIdx < messageStopIdx) {
		t.Fatalf("expected content block to stream between message start/stop: %v", types)
	}

	messageDeltaIdx := find(EventMessageDelta)
	if !(contentStopIdx < messageDeltaIdx && messageDeltaIdx < messageStopIdx) {
		t.Fatalf("expected message_delta between content_block_stop and message_stop: %v", types)
	}
}

func TestRunStreamRejectsEmptyPromptFallback(t *testing.T) {
	rt := &Runtime{opts: Options{ProjectRoot: t.TempDir()}, mode: ModeContext{EntryPoint: EntryPointCLI}, histories: newHistoryStore(0)}
	if _, err := rt.RunStream(context.Background(), Request{Prompt: "   "}); err == nil {
		t.Fatal("expected empty prompt error")
	}
}

func TestHistoryStoreEvictsOldestSession(t *testing.T) {
	store := newHistoryStore(2)
	store.Get("first")
	time.Sleep(100 * time.Microsecond)
	store.Get("second")
	time.Sleep(100 * time.Microsecond)
	store.Get("third") // triggers eviction of "first"

	store.mu.Lock()
	_, hasFirst := store.data["first"]
	_, hasSecond := store.data["second"]
	_, hasThird := store.data["third"]
	store.mu.Unlock()

	if hasFirst {
		t.Fatal("expected oldest session to be evicted")
	}
	if !hasSecond || !hasThird {
		t.Fatalf("expected newer sessions to remain, got second=%t third=%t", hasSecond, hasThird)
	}
}

func TestHistoryStoreUpdatesLRUOnAccess(t *testing.T) {
	store := newHistoryStore(2)
	store.Get("alpha")
	time.Sleep(100 * time.Microsecond)
	store.Get("beta")
	time.Sleep(100 * time.Microsecond)
	store.Get("alpha") // refresh alpha as most recently used
	time.Sleep(100 * time.Microsecond)
	store.Get("gamma") // should evict beta

	store.mu.Lock()
	_, hasAlpha := store.data["alpha"]
	_, hasBeta := store.data["beta"]
	_, hasGamma := store.data["gamma"]
	store.mu.Unlock()

	if hasBeta {
		t.Fatal("expected beta to be evicted after alpha refresh")
	}
	if !hasAlpha || !hasGamma {
		t.Fatalf("expected alpha and gamma to remain, got alpha=%t gamma=%t", hasAlpha, hasGamma)
	}
}

func TestHistoryStoreCreatesNewSession(t *testing.T) {
	store := newHistoryStore(1)
	hist := store.Get("new")
	if hist == nil {
		t.Fatal("expected history to be created")
	}
	if got := store.Get("new"); got != hist {
		t.Fatal("expected retrieving existing session to return same history")
	}
}

func TestExecuteCommandsIgnoresPlainText(t *testing.T) {
	rt := &Runtime{}
	cmds, prompt, err := rt.executeCommands(context.Background(), "free text", &Request{Prompt: "free text"})
	if err != nil {
		t.Fatalf("execute commands: %v", err)
	}
	if len(cmds) != 0 || prompt != "free text" {
		t.Fatalf("unexpected command handling: %v %q", cmds, prompt)
	}
}

func TestExecuteCommandsUnknownCommand(t *testing.T) {
	exec := commands.NewExecutor()
	rt := &Runtime{cmdExec: exec}
	_, _, err := rt.executeCommands(context.Background(), "/unknown", &Request{Prompt: "/unknown"})
	if !errors.Is(err, commands.ErrUnknownCommand) {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestRegisterHelpersRejectNilHandlers(t *testing.T) {
	if _, err := registerSkills([]SkillRegistration{{Definition: skills.Definition{Name: "x"}}}); err == nil {
		t.Fatal("expected skill error")
	}
	if _, err := registerCommands([]CommandRegistration{{Definition: commands.Definition{Name: "x"}}}); err == nil {
		t.Fatal("expected command error")
	}
	if _, err := registerSubagents([]SubagentRegistration{{Definition: subagents.Definition{Name: "x"}}}); err == nil {
		t.Fatal("expected subagent error")
	}
}

func TestRegisterSubagentsEmpty(t *testing.T) {
	mgr, err := registerSubagents(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr != nil {
		t.Fatalf("expected nil manager, got %#v", mgr)
	}
}

func TestRegisterSubagentsRegistersHandlers(t *testing.T) {
	registrations := []SubagentRegistration{
		{Definition: subagents.Definition{Name: "ops"}, Handler: subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
			return subagents.Result{Output: "ok"}, nil
		})},
		{Definition: subagents.Definition{Name: "triage"}, Handler: subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
			return subagents.Result{Output: "ok"}, nil
		})},
	}
	mgr, err := registerSubagents(registrations)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if mgr == nil {
		t.Fatal("expected non-nil manager")
	}
	defs := mgr.List()
	if len(defs) != 2 {
		t.Fatalf("expected two subagents, got %+v", defs)
	}
	seen := map[string]struct{}{}
	for _, def := range defs {
		seen[def.Name] = struct{}{}
	}
	for _, expected := range []string{"ops", "triage"} {
		if _, ok := seen[expected]; !ok {
			t.Fatalf("missing subagent %s in %+v", expected, defs)
		}
	}
}

func TestRegisterSubagentsDuplicateDefinition(t *testing.T) {
	handler := subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
		return subagents.Result{}, nil
	})
	_, err := registerSubagents([]SubagentRegistration{
		{Definition: subagents.Definition{Name: "dup"}, Handler: handler},
		{Definition: subagents.Definition{Name: "dup"}, Handler: handler},
	})
	if !errors.Is(err, subagents.ErrDuplicateSubagent) {
		t.Fatalf("expected duplicate error, got %v", err)
	}
}

func TestRegisterSkillsSuccess(t *testing.T) {
	reg, err := registerSkills([]SkillRegistration{{Definition: skills.Definition{Name: "ops"}, Handler: skills.HandlerFunc(func(context.Context, skills.ActivationContext) (skills.Result, error) {
		return skills.Result{Output: "ok"}, nil
	})}})
	if err != nil {
		t.Fatalf("register skills: %v", err)
	}
	if reg == nil {
		t.Fatal("expected registry")
	}
	if _, ok := reg.Get("ops"); !ok {
		t.Fatalf("expected skill to be registered")
	}
}

func TestRegisterSkillsEmpty(t *testing.T) {
	reg, err := registerSkills(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if reg == nil {
		t.Fatal("expected registry instance")
	}
	if _, ok := reg.Get("missing"); ok {
		t.Fatalf("expected no skills to be registered")
	}
}

func TestRegisterCommandsSuccess(t *testing.T) {
	exec, err := registerCommands([]CommandRegistration{{Definition: commands.Definition{Name: "ping"}, Handler: commands.HandlerFunc(func(context.Context, commands.Invocation) (commands.Result, error) {
		return commands.Result{Output: "pong"}, nil
	})}})
	if err != nil {
		t.Fatalf("register commands: %v", err)
	}
	if exec == nil {
		t.Fatal("expected executor")
	}
	results, err := exec.Execute(context.Background(), []commands.Invocation{{Name: "ping"}})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	if len(results) != 1 || results[0].Output != "pong" {
		t.Fatalf("unexpected command result: %+v", results)
	}
}

func TestRegisterCommandsEmpty(t *testing.T) {
	exec, err := registerCommands(nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if exec == nil {
		t.Fatal("expected executor instance")
	}
	if _, err := exec.Execute(context.Background(), []commands.Invocation{{Name: "missing"}}); !errors.Is(err, commands.ErrUnknownCommand) {
		t.Fatalf("expected unknown command error, got %v", err)
	}
}

func TestRegisterMCPServersNoop(t *testing.T) {
	registry := tool.NewRegistry()
	mgr := sandbox.NewManager(nil, sandbox.NewDomainAllowList(), nil)
	if err := registerMCPServers(context.Background(), registry, mgr, nil); err != nil {
		t.Fatalf("register MCP servers: %v", err)
	}
}

func TestDefaultSessionIDUsesEntrypoint(t *testing.T) {
	id := defaultSessionID("")
	if len(id) == 0 || id[:3] != string(defaultEntrypoint) {
		t.Fatalf("unexpected default session id: %s", id)
	}
}

func TestToolWhitelistDeniesExecution(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{
		{Message: model.Message{Role: "assistant", ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Arguments: map[string]any{"text": "hi"}},
		}}},
		{Message: model.Message{Role: "assistant", Content: "done"}},
	}}
	echo := &echoTool{}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Tools: []tool.Tool{echo}})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	defer rt.Close()

	resp, err := rt.Run(context.Background(), Request{Prompt: "call", ToolWhitelist: []string{"other"}})
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if resp == nil || resp.Result == nil || resp.Result.Output != "done" {
		t.Fatalf("unexpected response: %+v", resp)
	}
	if echo.calls != 0 {
		t.Fatalf("expected tool to be blocked by whitelist, got %d executions", echo.calls)
	}
}

func TestRuntimeToolExecutorPopulatesUsage(t *testing.T) {
	rp := &recordingPolicy{}
	sb := sandbox.NewManager(nil, nil, rp)
	reg := tool.NewRegistry()
	if err := reg.Register(&echoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	exec := tool.NewExecutor(reg, sb)
	rtExec := &runtimeToolExecutor{executor: exec, hooks: &runtimeHookAdapter{}, host: "localhost"}

	_, err := rtExec.Execute(context.Background(), agent.ToolCall{Name: "echo", Input: map[string]any{"text": "hi"}}, agent.NewContext())
	if err != nil {
		t.Fatalf("execute tool: %v", err)
	}
	if rp.lastUse.MemoryBytes == 0 {
		t.Fatal("expected memory usage to be recorded")
	}
}

func TestRuntimeToolExecutorEnforcesResourceLimits(t *testing.T) {
	limiter := sandbox.NewResourceLimiter(sandbox.ResourceLimits{MaxMemoryBytes: 1})
	sb := sandbox.NewManager(nil, nil, limiter)
	reg := tool.NewRegistry()
	if err := reg.Register(&echoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	exec := tool.NewExecutor(reg, sb)
	rtExec := &runtimeToolExecutor{executor: exec, hooks: &runtimeHookAdapter{}, host: "localhost"}

	_, err := rtExec.Execute(context.Background(), agent.ToolCall{Name: "echo", Input: map[string]any{"text": "hi"}}, agent.NewContext())
	if err == nil {
		t.Fatal("expected resource limit error")
	}
	if !errors.Is(err, sandbox.ErrResourceExceeded) {
		t.Fatalf("expected ErrResourceExceeded, got %v", err)
	}
}

func TestAvailableToolsNilRegistry(t *testing.T) {
	if defs := availableTools(nil, nil); defs != nil {
		t.Fatalf("expected nil definitions, got %+v", defs)
	}
}

func TestAvailableToolsFiltersWhitelist(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(&echoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	defs := availableTools(reg, map[string]struct{}{"missing": {}})
	if len(defs) != 0 {
		t.Fatalf("expected tools filtered out, got %v", defs)
	}
}

func TestAvailableToolsReturnsDefinitions(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(&echoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	defs := availableTools(reg, nil)
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("unexpected tool defs: %+v", defs)
	}
	if defs[0].Description == "" {
		t.Fatal("expected description to be populated")
	}
}

func TestAvailableToolsWhitelistAllowsMatch(t *testing.T) {
	reg := tool.NewRegistry()
	if err := reg.Register(&echoTool{}); err != nil {
		t.Fatalf("register tool: %v", err)
	}
	defs := availableTools(reg, map[string]struct{}{"echo": {}})
	if len(defs) != 1 || defs[0].Name != "echo" {
		t.Fatalf("expected whitelisted tool, got %+v", defs)
	}
}

func TestSchemaToMap(t *testing.T) {
	schema := &tool.JSONSchema{Type: "object", Required: []string{"x"}, Properties: map[string]any{"x": map[string]any{"type": "string"}}}
	mapped := schemaToMap(schema)
	reqField, ok := mapped["required"].([]string)
	if mapped["type"] != "object" || !ok || len(reqField) != 1 {
		t.Fatalf("unexpected map: %+v", mapped)
	}
}

func TestHistoryStoreCreatesOnce(t *testing.T) {
	store := newHistoryStore(0)
	a := store.Get("s1")
	b := store.Get("s1")
	if a != b {
		t.Fatal("expected same history instance")
	}
}

func TestExecuteSubagentBranches(t *testing.T) {
	rt := &Runtime{}
	// nil manager returns original prompt
	res, out, err := rt.executeSubagent(context.Background(), "p", skills.ActivationContext{}, &Request{Prompt: "p"})
	if err != nil || res != nil || out != "p" {
		t.Fatalf("unexpected result: res=%v out=%q err=%v", res, out, err)
	}

	// no matching subagent with empty target suppresses error
	rt.subMgr = subagents.NewManager()
	// without WithTaskDispatch, executeSubagent treats unauthorized dispatch as no-op
	res, out, err = rt.executeSubagent(context.Background(), "p", skills.ActivationContext{}, &Request{Prompt: "p"})
	if err != nil || res != nil || out != "p" {
		t.Fatalf("expected no-op for unauthorized dispatch, got res=%v out=%q err=%v", res, out, err)
	}
	res, out, err = rt.executeSubagent(subagents.WithTaskDispatch(context.Background()), "p", skills.ActivationContext{}, &Request{Prompt: "p"})
	if err != nil || res != nil || out != "p" {
		t.Fatalf("no-match branch failed: res=%v out=%q err=%v", res, out, err)
	}

	// unknown explicit target returns error
	_, _, err = rt.executeSubagent(subagents.WithTaskDispatch(context.Background()), "p", skills.ActivationContext{}, &Request{Prompt: "p", TargetSubagent: "missing"})
	if err == nil {
		t.Fatal("expected error for unknown subagent")
	}
}

func TestExecuteSubagentSuccess(t *testing.T) {
	mgr := subagents.NewManager()
	err := mgr.Register(subagents.Definition{Name: "ops"}, subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
		return subagents.Result{
			Output: "new-prompt",
			Metadata: map[string]any{
				"api.prompt_override": "new-prompt",
				"api.tags":            map[string]string{"sub": "1"},
			},
		}, nil
	}))
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	rt := &Runtime{subMgr: mgr}
	req := &Request{Prompt: "p"}
	res, out, err := rt.executeSubagent(subagents.WithTaskDispatch(context.Background()), "p", skills.ActivationContext{}, req)
	if err != nil || res == nil || out != "new-prompt" {
		t.Fatalf("unexpected result: res=%v out=%q err=%v", res, out, err)
	}
	if req.Tags["sub"] != "1" {
		t.Fatalf("expected tags propagated, got %+v", req.Tags)
	}
}

func TestConvertRunResultHelpers(t *testing.T) {
	if got := convertRunResult(runResult{}); got != nil {
		t.Fatalf("expected nil result, got %+v", got)
	}
	out := &agent.ModelOutput{Content: "ok", ToolCalls: []agent.ToolCall{{Name: "t", Input: map[string]any{"x": 1}}}}
	res := convertRunResult(runResult{output: out})
	if res == nil || len(res.ToolCalls) != 1 {
		t.Fatalf("unexpected converted result: %+v", res)
	}
	v, ok := res.ToolCalls[0].Arguments["x"].(int)
	if !ok || v != 1 {
		t.Fatalf("unexpected converted result: %+v", res)
	}
}

func TestConvertMessagesClonesToolCalls(t *testing.T) {
	msgs := []message.Message{
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []message.ToolCall{{ID: "c1", Name: "echo", Arguments: map[string]any{"count": 1}}}},
	}
	converted := convertMessages(msgs)
	if len(converted) != 2 || len(converted[1].ToolCalls) != 1 {
		t.Fatalf("unexpected conversion: %+v", converted)
	}
	msgs[1].ToolCalls[0].Arguments["count"] = 41
	count, ok := converted[1].ToolCalls[0].Arguments["count"].(int)
	if !ok || count != 1 {
		t.Fatal("expected arguments to be cloned")
	}
	converted[1].ToolCalls[0].Arguments["count"] = 7
	if orig, ok := msgs[1].ToolCalls[0].Arguments["count"].(int); !ok || orig != 41 {
		t.Fatal("expected clone not to mutate source")
	}
	if convertMessages(nil) != nil {
		t.Fatal("expected nil input to return nil")
	}
}

func TestCloneArgumentsCopiesMap(t *testing.T) {
	args := map[string]any{"a": 1}
	dup := cloneArguments(args)
	v, ok := dup["a"].(int)
	if !ok || v != 1 {
		t.Fatalf("unexpected clone: %+v", dup)
	}
	dup["a"] = 2
	if orig, ok := args["a"].(int); !ok || orig != 1 {
		t.Fatal("mutation leaked to original map")
	}
	if cloneArguments(nil) != nil {
		t.Fatal("expected nil input to return nil")
	}
}

func TestNewTrimmerHelper(t *testing.T) {
	rt := &Runtime{opts: Options{TokenLimit: 0}}
	if rt.newTrimmer() != nil {
		t.Fatal("expected nil trimmer when limit is zero")
	}
	rt.opts.TokenLimit = 10
	if rt.newTrimmer() == nil {
		t.Fatal("expected non-nil trimmer")
	}
}

func TestResolveModelPrefersFactory(t *testing.T) {
	mdl := &stubModel{}
	called := false
	resolved, err := resolveModel(context.Background(), Options{ModelFactory: ModelFactoryFunc(func(context.Context) (model.Model, error) {
		called = true
		return mdl, nil
	})})
	if err != nil || resolved != mdl || !called {
		t.Fatalf("resolveModel factory branch failed: m=%v err=%v called=%v", resolved, err, called)
	}
	explicit := &stubModel{}
	resolved, err = resolveModel(context.Background(), Options{Model: explicit})
	if err != nil || resolved != explicit {
		t.Fatalf("expected explicit model to be returned, got m=%v err=%v", resolved, err)
	}
	if _, err := resolveModel(context.Background(), Options{}); !errors.Is(err, ErrMissingModel) {
		t.Fatalf("expected ErrMissingModel, got %v", err)
	}
}

type recordingPolicy struct {
	lastUse sandbox.ResourceUsage
}

func (r *recordingPolicy) Limits() sandbox.ResourceLimits {
	return sandbox.ResourceLimits{}
}

func (r *recordingPolicy) Validate(usage sandbox.ResourceUsage) error {
	r.lastUse = usage
	return nil
}

func TestTaskRunnerDispatchesBuiltinTypes(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{}
	type record struct {
		ctx  subagents.Context
		meta subagents.Context
		req  subagents.Request
	}
	records := map[string]record{}
	regs := make([]SubagentRegistration, 0, 3)
	targets := []string{subagents.TypeGeneralPurpose, subagents.TypeExplore, subagents.TypePlan}
	for _, name := range targets {
		def, ok := subagents.BuiltinDefinition(name)
		if !ok {
			t.Fatalf("missing builtin definition %s", name)
		}
		if name == subagents.TypePlan {
			def.BaseContext.Model = ""
		}
		subType := name
		regs = append(regs, SubagentRegistration{
			Definition: def,
			Handler: subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
				if subType == subagents.TypeExplore && subCtx.Allows("bash") {
					t.Fatalf("explore subagent should not allow bash")
				}
				metaCtx, _ := subagents.FromContext(ctx)
				records[subType] = record{ctx: subCtx, meta: metaCtx, req: req}
				return subagents.Result{Output: subType + "-done"}, nil
			}),
		})
	}

	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Subagents: regs})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	runner := rt.taskRunner()
	cases := []struct {
		payload toolbuiltin.TaskRequest
		expect  string
	}{
		{payload: toolbuiltin.TaskRequest{Description: "General dispatch check", Prompt: "general run", SubagentType: subagents.TypeGeneralPurpose}, expect: subagents.TypeGeneralPurpose + "-done"},
		{payload: toolbuiltin.TaskRequest{Description: "Explore isolation proof", Prompt: "explore run", SubagentType: subagents.TypeExplore}, expect: subagents.TypeExplore + "-done"},
		{payload: toolbuiltin.TaskRequest{Description: "Plan resume validation", Prompt: "plan run", SubagentType: subagents.TypePlan, Model: "claude-opus-4-20250514", Resume: "plan-session-42"}, expect: subagents.TypePlan + "-done"},
	}

	for _, tc := range cases {
		res, err := runner(context.Background(), tc.payload)
		if err != nil {
			t.Fatalf("runner(%s): %v", tc.payload.SubagentType, err)
		}
		if res == nil || res.Output != tc.expect {
			t.Fatalf("unexpected result for %s: %+v", tc.payload.SubagentType, res)
		}
		data, ok := res.Data.(map[string]any)
		if !ok || data == nil || data["subagent"] != tc.payload.SubagentType {
			t.Fatalf("missing subagent metadata for %s: %+v", tc.payload.SubagentType, res.Data)
		}
	}

	if len(records) != len(cases) {
		t.Fatalf("expected %d subagent invocations, got %d", len(cases), len(records))
	}

	gp := records[subagents.TypeGeneralPurpose]
	if gp.ctx.Model != subagents.ModelSonnet {
		t.Fatalf("expected sonnet model for general-purpose, got %s", gp.ctx.Model)
	}
	if gp.req.Instruction != "general run" {
		t.Fatalf("expected prompt forwarded, got %q", gp.req.Instruction)
	}
	if entry, ok := gp.req.Metadata["entrypoint"].(EntryPoint); !ok || entry != EntryPointCLI {
		t.Fatalf("expected entrypoint metadata propagated, got %+v", gp.req.Metadata["entrypoint"])
	}

	explore := records[subagents.TypeExplore]
	tools := explore.meta.ToolList()
	if len(tools) != 3 {
		t.Fatalf("expected explore whitelist of 3 tools, got %v", tools)
	}
	if explore.meta.Allows("bash") {
		t.Fatal("explore context should block bash")
	}
	if desc, ok := explore.req.Metadata["task.description"].(string); !ok || desc != "Explore isolation proof" {
		t.Fatalf("expected description propagated, got %q", explore.req.Metadata["task.description"])
	}

	plan := records[subagents.TypePlan]
	if plan.meta.SessionID != "plan-session-42" {
		t.Fatalf("expected resume session propagated, got %q", plan.meta.SessionID)
	}
	if metaSession, ok := plan.req.Metadata["session_id"].(string); !ok || metaSession != "plan-session-42" {
		t.Fatalf("expected session metadata forwarded, got %q", plan.req.Metadata["session_id"])
	}
	if plan.meta.Model != subagents.ModelSonnet {
		t.Fatalf("expected plan model to stay at default, got %s", plan.meta.Model)
	}
	if val, ok := plan.meta.Metadata["task.model"].(string); !ok || val != "claude-opus-4-20250514" {
		t.Fatalf("expected task.model metadata, got %q", plan.meta.Metadata["task.model"])
	}
}

func TestTaskRunnerUnknownSubagentError(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{}
	def, ok := subagents.BuiltinDefinition(subagents.TypeGeneralPurpose)
	if !ok {
		t.Fatal("missing general-purpose definition")
	}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Subagents: []SubagentRegistration{{
		Definition: def,
		Handler: subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
			return subagents.Result{Output: "ok"}, nil
		}),
	}}})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	runner := rt.taskRunner()
	_, err = runner(context.Background(), toolbuiltin.TaskRequest{
		Description:  "Plan routing failure",
		Prompt:       "plan now",
		SubagentType: subagents.TypePlan,
	})
	if !errors.Is(err, subagents.ErrUnknownSubagent) {
		t.Fatalf("expected ErrUnknownSubagent, got %v", err)
	}
}

func TestTaskRunnerContextCancellation(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{}
	def, ok := subagents.BuiltinDefinition(subagents.TypeGeneralPurpose)
	if !ok {
		t.Fatal("missing general-purpose definition")
	}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Subagents: []SubagentRegistration{{
		Definition: def,
		Handler: subagents.HandlerFunc(func(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
			<-ctx.Done()
			return subagents.Result{}, ctx.Err()
		}),
	}}})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	runner := rt.taskRunner()
	_, err = runner(ctx, toolbuiltin.TaskRequest{
		Description:  "General cancellation case",
		Prompt:       "general run",
		SubagentType: subagents.TypeGeneralPurpose,
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context cancellation, got %v", err)
	}
}

func TestTaskRunnerConvertsSubagentErrorFlag(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{}
	def, ok := subagents.BuiltinDefinition(subagents.TypeGeneralPurpose)
	if !ok {
		t.Fatal("missing general-purpose definition")
	}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl, Subagents: []SubagentRegistration{{
		Definition: def,
		Handler: subagents.HandlerFunc(func(context.Context, subagents.Context, subagents.Request) (subagents.Result, error) {
			return subagents.Result{Output: "", Error: "model refused"}, nil
		}),
	}}})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	runner := rt.taskRunner()
	res, err := runner(context.Background(), toolbuiltin.TaskRequest{
		Description:  "General error report",
		Prompt:       "general run",
		SubagentType: subagents.TypeGeneralPurpose,
	})
	if err != nil {
		t.Fatalf("task runner: %v", err)
	}
	if res.Success {
		t.Fatal("expected tool result to be marked unsuccessful")
	}
	if res.Output != "subagent general-purpose completed" {
		t.Fatalf("unexpected default output: %q", res.Output)
	}
	data, ok := res.Data.(map[string]any)
	if !ok || data == nil || data["subagent"] != subagents.TypeGeneralPurpose || data["error"] != "model refused" {
		t.Fatalf("expected error metadata, got %+v", res.Data)
	}
}

func TestRunStreamRejectsEmptyPrompt(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant"}}}}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	events, err := rt.RunStream(context.Background(), Request{})
	if err == nil {
		t.Fatal("expected prompt validation error")
	}
	if events != nil {
		t.Fatal("expected nil event channel on failure")
	}
}

func TestRunStreamEmitsErrorEvent(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{err: errors.New("boom")}
	rt, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	t.Cleanup(func() { _ = rt.Close() })

	stream, err := rt.RunStream(context.Background(), Request{Prompt: "hi"})
	if err != nil {
		t.Fatalf("run stream: %v", err)
	}
	found := false
	for evt := range stream {
		if evt.Type == EventError {
			found = true
			if evt.IsError == nil || !*evt.IsError {
				t.Fatalf("expected IsError flag, got %+v", evt)
			}
		}
	}
	if !found {
		t.Fatal("expected error event")
	}
}

func TestWithStreamEmitGuardsNilInputs(t *testing.T) {
	// Nil context should be promoted to Background.
	ctx := withStreamEmit(context.TODO(), nil)
	if ctx == nil {
		t.Fatal("expected non-nil context from withStreamEmit")
	}
	if streamEmitFromContext(ctx) != nil {
		t.Fatal("nil emit function should not be stored")
	}

	// Stored emit should round-trip through context and be callable.
	var called bool
	emit := func(context.Context, StreamEvent) { called = true }
	ctx = withStreamEmit(context.Background(), emit)
	got := streamEmitFromContext(ctx)
	if got == nil {
		t.Fatal("expected emit function from context")
	}
	got(context.Background(), StreamEvent{Type: "ping"})
	if !called {
		t.Fatal("emit function was not invoked")
	}

	// Missing key should return nil safely.
	if streamEmitFromContext(context.Background()) != nil {
		t.Fatal("expected nil emit when not set")
	}
}

func TestNewRuntimeTaskStoreOwnership(t *testing.T) {
	root := newClaudeProject(t)
	mdl := &stubModel{responses: []*model.Response{{Message: model.Message{Role: "assistant", Content: "ok"}}}}

	rtDefault, err := New(context.Background(), Options{ProjectRoot: root, Model: mdl})
	if err != nil {
		t.Fatalf("runtime default task store: %v", err)
	}
	if rtDefault.taskStore == nil {
		t.Fatal("expected runtime to create default task store")
	}
	if !rtDefault.ownsTaskStore {
		t.Fatal("expected runtime to own default task store")
	}
	if err := rtDefault.Close(); err != nil {
		t.Fatalf("close runtime with default task store: %v", err)
	}

	spy := &taskStoreCloseSpy{Store: tasks.NewTaskStore()}
	rtProvided, err := New(context.Background(), Options{
		ProjectRoot: root,
		Model:       mdl,
		TaskStore:   spy,
	})
	if err != nil {
		t.Fatalf("runtime provided task store: %v", err)
	}
	if rtProvided.ownsTaskStore {
		t.Fatal("expected runtime not to own caller-provided task store")
	}
	if err := rtProvided.Close(); err != nil {
		t.Fatalf("close runtime with provided task store: %v", err)
	}
	if calls := atomic.LoadInt32(&spy.closeCalls); calls != 0 {
		t.Fatalf("expected provided task store Close not called, got %d", calls)
	}
}

func TestRuntimeCloseClosesOwnedTaskStore(t *testing.T) {
	spy := &taskStoreCloseSpy{Store: tasks.NewTaskStore()}
	rt := &Runtime{
		taskStore:     spy,
		ownsTaskStore: true,
	}

	if err := rt.Close(); err != nil {
		t.Fatalf("runtime close: %v", err)
	}
	if calls := atomic.LoadInt32(&spy.closeCalls); calls != 1 {
		t.Fatalf("expected owned task store Close called once, got %d", calls)
	}
	// closeOnce should keep TaskStore.Close idempotent.
	if err := rt.Close(); err != nil {
		t.Fatalf("runtime close second call: %v", err)
	}
	if calls := atomic.LoadInt32(&spy.closeCalls); calls != 1 {
		t.Fatalf("expected close count to remain 1, got %d", calls)
	}
}

type taskStoreCloseSpy struct {
	tasks.Store
	closeCalls int32
	closeErr   error
}

func (s *taskStoreCloseSpy) Close() error {
	atomic.AddInt32(&s.closeCalls, 1)
	return s.closeErr
}
