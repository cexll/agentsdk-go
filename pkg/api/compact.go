package api

import (
	"context"
	"errors"
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

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

	PreserveInitial  bool `json:"preserve_initial"`   // keep initial messages when compacting
	InitialCount     int  `json:"initial_count"`      // keep first N messages from the compacted prefix
	PreserveUserText bool `json:"preserve_user_text"` // keep recent user messages from the compacted prefix
	UserTextTokens   int  `json:"user_text_tokens"`   // token budget for preserved user messages

	MaxRetries    int           `json:"max_retries"`
	RetryDelay    time.Duration `json:"retry_delay"`
	FallbackModel string        `json:"fallback_model"`

	// RolloutDir enables compact event persistence when non-empty.
	// The directory is resolved relative to Options.ProjectRoot unless absolute.
	RolloutDir string `json:"rollout_dir"`
}

const (
	defaultCompactThreshold   = 0.8
	defaultCompactPreserve    = 5
	defaultClaudeContextLimit = 200000
	summaryMaxTokens          = 1024
)

var errNoCompaction = errors.New("api: nothing to compact")

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
	if cfg.InitialCount < 0 {
		cfg.InitialCount = 0
	}
	if cfg.PreserveInitial && cfg.InitialCount == 0 {
		cfg.InitialCount = 1
	}
	if cfg.UserTextTokens < 0 {
		cfg.UserTextTokens = 0
	}
	if cfg.MaxRetries < 0 {
		cfg.MaxRetries = 0
	}
	if cfg.RetryDelay < 0 {
		cfg.RetryDelay = 0
	}
	cfg.FallbackModel = strings.TrimSpace(cfg.FallbackModel)
	cfg.RolloutDir = strings.TrimSpace(cfg.RolloutDir)
	return cfg
}

type compactor struct {
	cfg     CompactConfig
	model   model.Model
	limit   int
	hooks   *corehooks.Executor
	rollout *RolloutWriter
	mu      sync.Mutex
}

func newCompactor(projectRoot string, cfg CompactConfig, mdl model.Model, tokenLimit int, hooks *corehooks.Executor) *compactor {
	cfg = cfg.withDefaults()
	if !cfg.Enabled {
		return nil
	}
	limit := tokenLimit
	if limit <= 0 {
		limit = defaultClaudeContextLimit
	}
	rollout := newRolloutWriter(projectRoot, cfg.RolloutDir)
	return &compactor{
		cfg:     cfg,
		model:   mdl,
		limit:   limit,
		hooks:   hooks,
		rollout: rollout,
	}
}

func (c *compactor) shouldCompact(msgCount, tokenCount int) bool {
	if c == nil || !c.cfg.Enabled {
		return false
	}
	if msgCount <= c.cfg.PreserveCount {
		return false
	}
	if tokenCount <= 0 || c.limit <= 0 {
		return false
	}
	ratio := float64(tokenCount) / float64(c.limit)
	return ratio >= c.cfg.Threshold
}

type compactResult struct {
	summary       string
	originalMsgs  int
	preservedMsgs int
	tokensBefore  int
	tokensAfter   int
}

func (c *compactor) maybeCompact(ctx context.Context, hist *message.History, sessionID string, recorder *hookRecorder) (compactResult, bool, error) {
	if c == nil || hist == nil || !c.cfg.Enabled {
		return compactResult{}, false, nil
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	msgCount := hist.Len()
	tokenCount := hist.TokenCount()
	if !c.shouldCompact(msgCount, tokenCount) {
		return compactResult{}, false, nil
	}
	snapshot := hist.All()
	if len(snapshot) <= c.cfg.PreserveCount {
		return compactResult{}, false, nil
	}

	payload := coreevents.PreCompactPayload{
		EstimatedTokens: tokenCount,
		TokenLimit:      c.limit,
		Threshold:       c.cfg.Threshold,
		PreserveCount:   c.cfg.PreserveCount,
	}
	allow, err := c.preCompact(ctx, sessionID, payload, recorder)
	if err != nil {
		return compactResult{}, false, err
	}
	if !allow {
		return compactResult{}, false, nil
	}

	res, err := c.compact(ctx, hist, snapshot, tokenCount)
	if err != nil {
		if errors.Is(err, errNoCompaction) {
			return compactResult{}, false, nil
		}
		return compactResult{}, false, err
	}
	c.postCompact(sessionID, res, recorder)
	return res, true, nil
}

func (c *compactor) preCompact(ctx context.Context, sessionID string, payload coreevents.PreCompactPayload, recorder *hookRecorder) (bool, error) {
	evt := coreevents.Event{
		Type:      coreevents.PreCompact,
		SessionID: sessionID,
		Payload:   payload,
	}
	if c.hooks == nil {
		c.record(recorder, evt)
		return true, nil
	}
	results, err := c.hooks.Execute(ctx, evt)
	c.record(recorder, evt)
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

func (c *compactor) postCompact(sessionID string, res compactResult, recorder *hookRecorder) {
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
	c.record(recorder, evt)
	if c.rollout != nil {
		if err := c.rollout.WriteCompactEvent(sessionID, res); err != nil {
			log.Printf("api: write compaction rollout: %v", err)
		}
	}
}

func (c *compactor) record(recorder *hookRecorder, evt coreevents.Event) {
	if recorder == nil {
		return
	}
	recorder.Record(evt)
}

func (c *compactor) compact(ctx context.Context, hist *message.History, snapshot []message.Message, tokensBefore int) (compactResult, error) {
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

	preservedPrefix := make([]bool, len(older))

	var initial []message.Message
	if c.cfg.PreserveInitial && c.cfg.InitialCount > 0 {
		n := c.cfg.InitialCount
		if n > len(older) {
			n = len(older)
		}
		initial = make([]message.Message, 0, n)
		for i := 0; i < n; i++ {
			preservedPrefix[i] = true
			initial = append(initial, message.CloneMessage(older[i]))
		}
	}

	var userText []message.Message
	if c.cfg.PreserveUserText && c.cfg.UserTextTokens > 0 {
		var counter message.NaiveCounter
		total := 0
		indices := make([]int, 0)
		for i := len(older) - 1; i >= 0; i-- {
			if preservedPrefix[i] {
				continue
			}
			if older[i].Role != "user" || strings.TrimSpace(older[i].Content) == "" {
				continue
			}
			cost := counter.Count(older[i])
			total += cost
			indices = append(indices, i)
			preservedPrefix[i] = true
			if total >= c.cfg.UserTextTokens {
				break
			}
		}
		if len(indices) > 0 {
			userText = make([]message.Message, 0, len(indices))
			for j := len(indices) - 1; j >= 0; j-- {
				userText = append(userText, message.CloneMessage(older[indices[j]]))
			}
		}
	}

	summarize := make([]message.Message, 0, len(older))
	for i, msg := range older {
		if preservedPrefix[i] {
			continue
		}
		summarize = append(summarize, msg)
	}
	if len(summarize) == 0 {
		return compactResult{}, errNoCompaction
	}

	req := model.Request{
		Messages:  convertMessages(summarize),
		System:    summarySystemPrompt,
		Model:     c.cfg.SummaryModel,
		MaxTokens: summaryMaxTokens,
	}
	resp, err := c.completeSummary(ctx, req)
	if err != nil {
		return compactResult{}, fmt.Errorf("api: compact summary: %w", err)
	}
	summary := strings.TrimSpace(resp.Message.Content)
	if summary == "" {
		summary = "对话摘要为空"
	}

	newMsgs := make([]message.Message, 0, len(initial)+1+len(userText)+len(kept))
	newMsgs = append(newMsgs, message.CloneMessages(initial)...)
	newMsgs = append(newMsgs, message.Message{
		Role:    "system",
		Content: fmt.Sprintf("对话摘要：\n%s", summary),
	})
	newMsgs = append(newMsgs, message.CloneMessages(userText)...)
	newMsgs = append(newMsgs, message.CloneMessages(kept)...)
	hist.Replace(newMsgs)

	tokensAfter := hist.TokenCount()
	preservedMsgs := len(initial) + len(userText) + len(kept)
	return compactResult{
		summary:       summary,
		originalMsgs:  len(snapshot),
		preservedMsgs: preservedMsgs,
		tokensBefore:  tokensBefore,
		tokensAfter:   tokensAfter,
	}, nil
}

func (c *compactor) completeSummary(ctx context.Context, req model.Request) (*model.Response, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if c == nil || c.model == nil {
		return nil, errors.New("api: summary model is nil")
	}
	attempts := 1 + c.cfg.MaxRetries
	if attempts < 1 {
		attempts = 1
	}

	var lastErr error
	for attempt := 1; attempt <= attempts; attempt++ {
		if attempt > 1 {
			if delay := c.cfg.RetryDelay; delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return nil, ctx.Err()
				case <-timer.C:
				}
			}
			if fallback := strings.TrimSpace(c.cfg.FallbackModel); fallback != "" {
				req.Model = fallback
			}
		}
		resp, err := c.model.Complete(ctx, req)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if attempts > 1 {
			log.Printf("api: compact summary attempt %d/%d failed: %v", attempt, attempts, err)
		}
	}
	return nil, lastErr
}
