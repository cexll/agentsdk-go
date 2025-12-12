package api

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	coreevents "github.com/cexll/agentsdk-go/pkg/core/events"
	corehooks "github.com/cexll/agentsdk-go/pkg/core/hooks"
	"github.com/cexll/agentsdk-go/pkg/message"
	"github.com/cexll/agentsdk-go/pkg/model"
)

// CompactConfig controls automatic context compaction.
type CompactConfig struct {
	Enabled       bool    `json:"enabled"`
	Threshold     float64 `json:"threshold"`      // trigger ratio (default 0.8)
	PreserveCount int     `json:"preserve_count"` // keep latest N messages (default 5)
	SummaryModel  string  `json:"summary_model"`  // model tier/name used for summary
}

const (
	defaultCompactThreshold   = 0.8
	defaultCompactPreserve    = 5
	defaultClaudeContextLimit = 200000
	summaryMaxTokens          = 1024
)

const summarySystemPrompt = `你是对话上下文压缩器。
请将以下对话内容总结成一段简洁但信息密集的摘要，供后续继续对话使用。
要求：
- 保留关键事实、约束、决定、计划、姓名、数字、代码与未解决的问题。
- 删除寒暄、重复和无关细节，不要臆造新信息。
- 以客观叙述方式输出。
只输出摘要正文。`

func (c CompactConfig) withDefaults() CompactConfig {
	cfg := c
	if cfg.Threshold <= 0 || cfg.Threshold > 1 {
		cfg.Threshold = defaultCompactThreshold
	}
	if cfg.PreserveCount <= 0 {
		cfg.PreserveCount = defaultCompactPreserve
	}
	if cfg.PreserveCount < 1 {
		cfg.PreserveCount = 1
	}
	cfg.SummaryModel = strings.TrimSpace(cfg.SummaryModel)
	return cfg
}

type compactor struct {
	cfg      CompactConfig
	model    model.Model
	limit    int
	hooks    *corehooks.Executor
	recorder HookRecorder
	mu       sync.Mutex
}

func newCompactor(cfg CompactConfig, mdl model.Model, tokenLimit int, hooks *corehooks.Executor, recorder HookRecorder) *compactor {
	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		return nil
	}
	limit := tokenLimit
	if limit <= 0 {
		limit = defaultClaudeContextLimit
	}
	return &compactor{
		cfg:      cfg,
		model:    mdl,
		limit:    limit,
		hooks:    hooks,
		recorder: recorder,
	}
}

func (c *compactor) estimateTokens(msgs []message.Message) int {
	var counter message.NaiveCounter
	total := 0
	for _, msg := range msgs {
		total += counter.Count(msg)
	}
	return total
}

func (c *compactor) shouldCompact(msgs []message.Message) bool {
	if c == nil || !c.cfg.Enabled {
		return false
	}
	if len(msgs) <= c.cfg.PreserveCount {
		return false
	}
	toks := c.estimateTokens(msgs)
	if toks <= 0 || c.limit <= 0 {
		return false
	}
	ratio := float64(toks) / float64(c.limit)
	return ratio >= c.cfg.Threshold
}

type compactResult struct {
	summary       string
	originalMsgs  int
	preservedMsgs int
	tokensBefore  int
	tokensAfter   int
}

func (c *compactor) maybeCompact(ctx context.Context, hist *message.History, sessionID string) (compactResult, bool, error) {
	if c == nil || hist == nil || !c.cfg.Enabled {
		return compactResult{}, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	snapshot := hist.All()
	if !c.shouldCompact(snapshot) {
		return compactResult{}, false, nil
	}

	payload := coreevents.PreCompactPayload{
		EstimatedTokens: c.estimateTokens(snapshot),
		TokenLimit:      c.limit,
		Threshold:       c.cfg.Threshold,
		PreserveCount:   c.cfg.PreserveCount,
	}
	allow, err := c.preCompact(ctx, sessionID, payload)
	if err != nil {
		return compactResult{}, false, err
	}
	if !allow {
		return compactResult{}, false, nil
	}

	res, err := c.compact(ctx, hist, snapshot)
	if err != nil {
		return compactResult{}, false, err
	}
	c.postCompact(sessionID, res)
	return res, true, nil
}

func (c *compactor) preCompact(ctx context.Context, sessionID string, payload coreevents.PreCompactPayload) (bool, error) {
	evt := coreevents.Event{
		Type:      coreevents.PreCompact,
		SessionID: sessionID,
		Payload:   payload,
	}
	if c.hooks == nil {
		c.record(evt)
		return true, nil
	}
	results, err := c.hooks.Execute(ctx, evt)
	c.record(evt)
	if err != nil {
		return false, err
	}
	for _, res := range results {
		if res.Decision == corehooks.DecisionDeny || res.Decision == corehooks.DecisionAsk {
			return false, nil
		}
	}
	return true, nil
}

func (c *compactor) postCompact(sessionID string, res compactResult) {
	payload := coreevents.ContextCompactedPayload{
		Summary:               res.summary,
		OriginalMessages:      res.originalMsgs,
		PreservedMessages:     res.preservedMsgs,
		EstimatedTokensBefore: res.tokensBefore,
		EstimatedTokensAfter:  res.tokensAfter,
	}
	evt := coreevents.Event{
		Type:      coreevents.ContextCompacted,
		SessionID: sessionID,
		Payload:   payload,
	}
	if c.hooks != nil {
		//nolint:errcheck // context compacted events are non-critical notifications
		c.hooks.Publish(evt)
	}
	c.record(evt)
}

func (c *compactor) record(evt coreevents.Event) {
	if c.recorder == nil {
		return
	}
	c.recorder.Record(evt)
}

func (c *compactor) compact(ctx context.Context, hist *message.History, snapshot []message.Message) (compactResult, error) {
	if c.model == nil {
		return compactResult{}, errors.New("api: summary model is nil")
	}
	preserve := c.cfg.PreserveCount
	if preserve >= len(snapshot) {
		return compactResult{}, nil
	}
	cut := len(snapshot) - preserve
	older := snapshot[:cut]
	kept := snapshot[cut:]

	tokensBefore := c.estimateTokens(snapshot)

	req := model.Request{
		Messages:  convertMessages(older),
		System:    summarySystemPrompt,
		Model:     c.cfg.SummaryModel,
		MaxTokens: summaryMaxTokens,
	}
	resp, err := c.model.Complete(ctx, req)
	if err != nil {
		return compactResult{}, fmt.Errorf("api: compact summary: %w", err)
	}
	summary := strings.TrimSpace(resp.Message.Content)
	if summary == "" {
		summary = "对话摘要为空"
	}

	newMsgs := make([]message.Message, 0, 1+len(kept))
	newMsgs = append(newMsgs, message.Message{
		Role:    "system",
		Content: fmt.Sprintf("对话摘要：\n%s", summary),
	})
	for _, msg := range kept {
		newMsgs = append(newMsgs, message.CloneMessage(msg))
	}
	hist.Replace(newMsgs)

	tokensAfter := c.estimateTokens(newMsgs)
	return compactResult{
		summary:       summary,
		originalMsgs:  len(snapshot),
		preservedMsgs: len(kept),
		tokensBefore:  tokensBefore,
		tokensAfter:   tokensAfter,
	}, nil
}
