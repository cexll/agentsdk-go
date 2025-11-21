package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
	"github.com/cexll/agentsdk-go/pkg/runtime/subagents"
)

func buildSubagents() []api.SubagentRegistration {
	regs := []api.SubagentRegistration{}

	regs = append(regs, registrationFromBuiltin(subagents.TypeGeneralPurpose, nil, generalPurposeHandler))

	regs = append(regs, registrationFromBuiltin(subagents.TypeExplore, []skills.Matcher{
		skills.KeywordMatcher{Any: []string{"log", "trace", "grep", "read"}},
		skills.TraitMatcher{Traits: []string{"fast"}},
	}, exploreHandler))

	regs = append(regs, registrationFromBuiltin(subagents.TypePlan, []skills.Matcher{
		skills.KeywordMatcher{Any: []string{"plan", "roadmap", "步骤", "拆解"}},
	}, planHandler))

	regs = append(regs, api.SubagentRegistration{
		Definition: subagents.Definition{
			Name:        "deploy_guard",
			Description: "Blocks risky production deploys before handing off to planning/ops agents.",
			Priority:    5,
			MutexKey:    "ops",
			BaseContext: subagents.Context{
				Model:         subagents.ModelHaiku,
				ToolWhitelist: []string{"bash", "read"},
				Metadata:      map[string]any{"role": "sre"},
			},
			Matchers: []skills.Matcher{
				skills.KeywordMatcher{Any: []string{"deploy", "上线", "发布", "rollout"}},
				skills.TagMatcher{Require: map[string]string{"env": "prod"}},
			},
		},
		Handler: subagents.HandlerFunc(deployGuardHandler),
	})

	return regs
}

func registrationFromBuiltin(name string, matchers []skills.Matcher, handler subagents.HandlerFunc) api.SubagentRegistration {
	def, ok := subagents.BuiltinDefinition(name)
	if !ok {
		panic("missing builtin subagent: " + name)
	}
	if len(matchers) > 0 {
		def.Matchers = matchers
	}
	return api.SubagentRegistration{Definition: def, Handler: handler}
}

func generalPurposeHandler(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
	if err := ctx.Err(); err != nil {
		return subagents.Result{}, err
	}
	model := preferModel(subCtx, subagents.ModelSonnet)
	summary := fmt.Sprintf("general-purpose agent (%s) handling: %s", model, req.Instruction)
	return subagents.Result{Output: summary, Metadata: map[string]any{"model": model, "tools": subCtx.ToolList()}}, nil
}

func exploreHandler(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
	if err := ctx.Err(); err != nil {
		return subagents.Result{}, err
	}
	tools := subCtx.ToolList()
	joined := strings.Join(tools, ", ")
	if joined == "" {
		joined = "(no tool limits)"
	}
	output := fmt.Sprintf("explore agent scanning: %s using tools: %s", req.Instruction, joined)
	meta := map[string]any{"model": preferModel(subCtx, subagents.ModelHaiku), "tools": tools}
	return subagents.Result{Output: output, Metadata: meta}, nil
}

func planHandler(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
	if err := ctx.Err(); err != nil {
		return subagents.Result{}, err
	}
	steps := []string{"clarify objective and constraints", "split work into three executable tasks", "route execution to general-purpose agent after approval"}
	output := fmt.Sprintf("plan agent (%s) drafted steps for: %s\n- %s", preferModel(subCtx, subagents.ModelSonnet), req.Instruction, strings.Join(steps, "\n- "))
	return subagents.Result{Output: output, Metadata: map[string]any{"steps": len(steps), "tools": subCtx.ToolList()}}, nil
}

func deployGuardHandler(ctx context.Context, subCtx subagents.Context, req subagents.Request) (subagents.Result, error) {
	if err := ctx.Err(); err != nil {
		return subagents.Result{}, err
	}
	output := fmt.Sprintf("deploy_guard stopped production rollout; forwarding context to planner. tools=%v", subCtx.ToolList())
	return subagents.Result{Output: output, Metadata: map[string]any{"blocked": true, "model": preferModel(subCtx, subagents.ModelHaiku), "tools": subCtx.ToolList()}}, nil
}

func preferModel(subCtx subagents.Context, fallback string) string {
	model := strings.TrimSpace(subCtx.Model)
	if model != "" {
		return model
	}
	return fallback
}
