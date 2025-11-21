package main

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/core/events"
	coremw "github.com/cexll/agentsdk-go/pkg/core/middleware"
)

type hookBundle struct {
	handlers []any
	mw       []coremw.Middleware
	tracker  *demoHooks
}

func buildHooks(logger *slog.Logger, timeout time.Duration) hookBundle {
	hooks := newDemoHooks(logger)
	mw := []coremw.Middleware{logEventMiddleware(logger), timingMiddleware(logger)}
	return hookBundle{handlers: []any{hooks}, mw: mw, tracker: hooks}
}

type demoHooks struct {
	logger *slog.Logger
	mu     sync.Mutex
	counts map[events.EventType]int
}

func newDemoHooks(logger *slog.Logger) *demoHooks {
	return &demoHooks{logger: logger, counts: map[events.EventType]int{}}
}

func (h *demoHooks) snapshot() map[events.EventType]int {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make(map[events.EventType]int, len(h.counts))
	for k, v := range h.counts {
		out[k] = v
	}
	return out
}

func (h *demoHooks) record(t events.EventType) {
	h.mu.Lock()
	h.counts[t]++
	h.mu.Unlock()
}

func (h *demoHooks) PreToolUse(ctx context.Context, payload events.ToolUsePayload) error {
	h.logger.Info("hook PreToolUse", "tool", payload.Name, "params", payload.Params)
	h.record(events.PreToolUse)
	return ctx.Err()
}

func (h *demoHooks) PostToolUse(ctx context.Context, payload events.ToolResultPayload) error {
	h.logger.Info("hook PostToolUse", "tool", payload.Name, "duration", payload.Duration, "err", payload.Err)
	h.record(events.PostToolUse)
	return ctx.Err()
}

func (h *demoHooks) UserPromptSubmit(ctx context.Context, payload events.UserPromptPayload) error {
	h.logger.Info("hook UserPrompt", "prompt", payload.Prompt)
	h.record(events.UserPromptSubmit)
	return ctx.Err()
}

func (h *demoHooks) Stop(ctx context.Context, payload events.StopPayload) error {
	h.logger.Info("hook Stop", "reason", payload.Reason)
	h.record(events.Stop)
	return ctx.Err()
}

func logEventMiddleware(logger *slog.Logger) coremw.Middleware {
	return func(next coremw.Handler) coremw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			logger.Info("hook middleware", "event", evt.Type, "id", evt.ID)
			return next(ctx, evt)
		}
	}
}

func timingMiddleware(logger *slog.Logger) coremw.Middleware {
	return func(next coremw.Handler) coremw.Handler {
		return func(ctx context.Context, evt events.Event) error {
			start := time.Now()
			err := next(ctx, evt)
			logger.Info("hook timing", "event", evt.Type, "took", time.Since(start))
			return err
		}
	}
}
