package main

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

func buildSkills() []api.SkillRegistration {
	reg := []api.SkillRegistration{}

	reg = append(reg, api.SkillRegistration{
		Definition: skills.Definition{
			Name:        "guardrail",
			Description: "阻断高危生产操作，只在严重等级高时触发。",
			Priority:    20,
			MutexKey:    "incident",
			Matchers: []skills.Matcher{
				skills.TagMatcher{Require: map[string]string{"env": "prod", "severity": "high"}},
				skills.KeywordMatcher{Any: []string{"incident", "breach", "告警", "事故"}},
			},
		},
		Handler: skills.HandlerFunc(func(ctx context.Context, ac skills.ActivationContext) (skills.Result, error) {
			select {
			case <-ctx.Done():
				return skills.Result{}, ctx.Err()
			case <-time.After(60 * time.Millisecond):
			}
			return skills.Result{
				Output: fmt.Sprintf("已冻结高危指令，env=%s severity=%s", ac.Tags["env"], ac.Tags["severity"]),
				Metadata: map[string]any{
					"action":     "halt",
					"request_id": ac.Metadata["request_id"],
					"severity":   ac.Tags["severity"],
				},
			}, nil
		}),
	})

	reg = append(reg, api.SkillRegistration{
		Definition: skills.Definition{
			Name:        "log_summary",
			Description: "提炼 noisy 日志并输出一句话总结。",
			Priority:    10,
			Matchers: []skills.Matcher{
				skills.KeywordMatcher{Any: []string{"log", "日志", "error"}},
				channelMatcher("cli", 0.62),
			},
		},
		Handler: skills.HandlerFunc(func(ctx context.Context, ac skills.ActivationContext) (skills.Result, error) {
			select {
			case <-ctx.Done():
				return skills.Result{}, ctx.Err()
			case <-time.After(40 * time.Millisecond):
			}
			return skills.Result{
				Output:   fmt.Sprintf("日志概要：%s（渠道=%v）", ac.Prompt, ac.Channels),
				Metadata: map[string]any{"summary_tokens": 24},
			}, nil
		}),
	})

	reg = append(reg, api.SkillRegistration{
		Definition: skills.Definition{
			Name:                  "add_note",
			Description:           "手动添加备注，演示 DisableAutoActivation 用法。",
			DisableAutoActivation: true,
		},
		Handler: skills.HandlerFunc(func(ctx context.Context, ac skills.ActivationContext) (skills.Result, error) {
			select {
			case <-ctx.Done():
				return skills.Result{}, ctx.Err()
			default:
			}
			return skills.Result{
				Output:   fmt.Sprintf("已记录备注：%s", ac.Prompt),
				Metadata: map[string]any{"manual": true},
			}, nil
		}),
	})

	return reg
}

func channelMatcher(channel string, score float64) skills.Matcher {
	target := strings.ToLower(strings.TrimSpace(channel))
	return skills.MatcherFunc(func(ac skills.ActivationContext) skills.MatchResult {
		for _, ch := range ac.Channels {
			if strings.ToLower(strings.TrimSpace(ch)) == target {
				return skills.MatchResult{Matched: true, Score: score, Reason: "channel:" + target}
			}
		}
		return skills.MatchResult{}
	})
}
