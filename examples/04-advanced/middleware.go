package main

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"strconv"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/agent"
	"github.com/cexll/agentsdk-go/pkg/middleware"
)

type middlewareBundle struct {
	items    []middleware.Middleware
	monitor  *monitoringMiddleware
	traceDir string
}

func buildMiddlewares(cfg runConfig, logger *slog.Logger) middlewareBundle {
	monitor := newMonitoringMiddleware(cfg.slowThreshold, logger)
	items := []middleware.Middleware{
		newLoggingMiddleware(logger),
		newRateLimitMiddleware(cfg.rps, cfg.burst, cfg.concurrent),
		newSettingsMiddleware(cfg.prompt, cfg.owner, logger), // MUST be before security check
		newSecurityMiddleware(nil, logger),
		monitor,
	}
	if cfg.enableTrace {
		items = append(items, middleware.NewTraceMiddleware(cfg.traceDir))
	}
	return middlewareBundle{items: items, monitor: monitor, traceDir: cfg.traceDir}
}

// loggingMiddleware prints structured request/response logs.
type loggingMiddleware struct {
	logger *slog.Logger
}

func newLoggingMiddleware(logger *slog.Logger) middleware.Middleware {
	return &loggingMiddleware{logger: logger}
}

func (m *loggingMiddleware) Name() string { return "logging" }

func (m *loggingMiddleware) BeforeAgent(_ context.Context, st *middleware.State) error {
	reqID := genRequestID()
	if st.Values == nil {
		st.Values = map[string]any{}
	}
	st.Values[requestIDKey] = reqID
	st.Values[startedAtKey] = time.Now()
	m.logger.Info("agent request start", "request_id", reqID)
	return nil
}

func (m *loggingMiddleware) BeforeModel(_ context.Context, st *middleware.State) error {
	m.logger.Info("before model", "request_id", readString(st.Values, requestIDKey), "iteration", st.Iteration)
	return nil
}

func (m *loggingMiddleware) AfterModel(_ context.Context, st *middleware.State) error {
	out, _ := st.ModelOutput.(*agent.ModelOutput)
	reqID := readString(st.Values, requestIDKey)
	if out == nil {
		m.logger.Warn("model returned nil output", "request_id", reqID, "iteration", st.Iteration)
		return nil
	}
	m.logger.Info("after model", "request_id", reqID, "content", clampPreview(out.Content, 64), "tool_calls", len(out.ToolCalls))
	return nil
}

func (m *loggingMiddleware) BeforeTool(_ context.Context, st *middleware.State) error {
	call, _ := st.ToolCall.(agent.ToolCall)
	m.logger.Info("before tool", "request_id", readString(st.Values, requestIDKey), "tool", call.Name)
	return nil
}

func (m *loggingMiddleware) AfterTool(_ context.Context, st *middleware.State) error {
	res, _ := st.ToolResult.(agent.ToolResult)
	m.logger.Info("after tool", "request_id", readString(st.Values, requestIDKey), "tool", res.Name, "output", clampPreview(res.Output, 64))
	return nil
}

func (m *loggingMiddleware) AfterAgent(_ context.Context, st *middleware.State) error {
	started := nowOr(st.Values[startedAtKey], time.Now())
	elapsed := time.Since(started)
	flags, _ := st.Values[securityFlagsKey].([]string)
	m.logger.Info("agent request done", "request_id", readString(st.Values, requestIDKey), "iterations", st.Iteration+1, "elapsed", elapsed, "security_flags", flags)
	return nil
}

// rateLimitMiddleware enforces a token bucket and concurrency guard.
type rateLimitMiddleware struct {
	ratePerSec float64
	burst      float64
	tokens     float64
	lastRefill time.Time
	concurrent chan struct{}
}

func newRateLimitMiddleware(rps, burst, maxConcurrent int) *rateLimitMiddleware {
	if rps <= 0 {
		rps = 5
	}
	if burst <= 0 {
		burst = rps
	}
	if maxConcurrent <= 0 {
		maxConcurrent = 2
	}
	return &rateLimitMiddleware{
		ratePerSec: float64(rps),
		burst:      float64(burst),
		tokens:     float64(burst),
		lastRefill: time.Now(),
		concurrent: make(chan struct{}, maxConcurrent),
	}
}

func (m *rateLimitMiddleware) Name() string { return "ratelimit" }

func (m *rateLimitMiddleware) BeforeAgent(ctx context.Context, _ *middleware.State) error {
	if err := m.waitForToken(ctx); err != nil {
		return err
	}
	select {
	case m.concurrent <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	default:
		return fmt.Errorf("ratelimit: concurrent limit reached")
	}
}

func (m *rateLimitMiddleware) AfterAgent(context.Context, *middleware.State) error {
	select {
	case <-m.concurrent:
	default:
	}
	return nil
}

func (m *rateLimitMiddleware) BeforeModel(context.Context, *middleware.State) error { return nil }
func (m *rateLimitMiddleware) AfterModel(context.Context, *middleware.State) error  { return nil }
func (m *rateLimitMiddleware) BeforeTool(context.Context, *middleware.State) error  { return nil }
func (m *rateLimitMiddleware) AfterTool(context.Context, *middleware.State) error   { return nil }

func (m *rateLimitMiddleware) waitForToken(ctx context.Context) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if m.tryConsume() {
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func (m *rateLimitMiddleware) tryConsume() bool {
	now := time.Now()
	elapsed := now.Sub(m.lastRefill).Seconds()
	if elapsed > 0 {
		m.tokens += elapsed * m.ratePerSec
		if m.tokens > m.burst {
			m.tokens = m.burst
		}
		m.lastRefill = now
	}
	if m.tokens < 1 {
		return false
	}
	m.tokens -= 1
	return true
}

// securityMiddleware performs lightweight input/output checks.
type securityMiddleware struct {
	blocked []string
	logger  *slog.Logger
}

func newSecurityMiddleware(blocked []string, logger *slog.Logger) middleware.Middleware {
	if len(blocked) == 0 {
		blocked = []string{"drop table", "rm -rf", "system.exit"}
	}
	return &securityMiddleware{blocked: blocked, logger: logger}
}

func (m *securityMiddleware) Name() string { return "security" }

func (m *securityMiddleware) BeforeAgent(_ context.Context, st *middleware.State) error {
	ctx, _ := st.Agent.(*agent.Context)
	if ctx == nil {
		return fmt.Errorf("security: missing agent context")
	}
	prompt := readString(ctx.Values, promptKey)
	if prompt == "" {
		return fmt.Errorf("security: prompt is empty")
	}
	if hit := m.detect(prompt); hit != "" {
		return fmt.Errorf("security: prompt contains blocked phrase %q", hit)
	}
	st.Values[securityFlagsKey] = []string{}
	noteFlag(st, "prompt validated")
	return nil
}

func (m *securityMiddleware) BeforeModel(_ context.Context, st *middleware.State) error {
	m.logger.Debug("prompt accepted", "request_id", readString(st.Values, requestIDKey))
	return nil
}

func (m *securityMiddleware) AfterModel(_ context.Context, st *middleware.State) error {
	out, _ := st.ModelOutput.(*agent.ModelOutput)
	if out != nil {
		if hit := m.detect(out.Content); hit != "" {
			return fmt.Errorf("security: model output blocked phrase %q", hit)
		}
	}
	return nil
}

func (m *securityMiddleware) BeforeTool(_ context.Context, st *middleware.State) error {
	call, _ := st.ToolCall.(agent.ToolCall)
	query := readStringParam(call.Input, "query")
	if strings.TrimSpace(query) == "" {
		return fmt.Errorf("security: tool %s missing query", call.Name)
	}
	noteFlag(st, "tool params ok")
	return nil
}

func (m *securityMiddleware) AfterTool(_ context.Context, st *middleware.State) error {
	res, _ := st.ToolResult.(agent.ToolResult)
	if hit := m.detect(res.Output); hit != "" {
		return fmt.Errorf("security: tool %s output blocked phrase %q", res.Name, hit)
	}
	return nil
}

func (m *securityMiddleware) AfterAgent(_ context.Context, st *middleware.State) error {
	flags, _ := st.Values[securityFlagsKey].([]string)
	m.logger.Info("security review passed", "request_id", readString(st.Values, requestIDKey), "flags", flags)
	return nil
}

func (m *securityMiddleware) detect(s string) string {
	text := strings.ToLower(s)
	for _, blocked := range m.blocked {
		if strings.Contains(text, strings.ToLower(blocked)) {
			return blocked
		}
	}
	return ""
}

func noteFlag(st *middleware.State, msg string) {
	flags, _ := st.Values[securityFlagsKey].([]string)
	flags = append(flags, msg)
	st.Values[securityFlagsKey] = flags
}

// monitoringMiddleware tracks latency across stages.
type monitoringMiddleware struct {
	threshold time.Duration
	logger    *slog.Logger
	metrics   *metricsRegistry
}

type metricsRegistry struct {
	totalRuns  int
	slowRuns   int
	maxLatency time.Duration
	lastRun    time.Duration
}

func newMonitoringMiddleware(threshold time.Duration, logger *slog.Logger) *monitoringMiddleware {
	return &monitoringMiddleware{threshold: threshold, logger: logger, metrics: &metricsRegistry{}}
}

func (m *monitoringMiddleware) Name() string { return "monitoring" }

func (m *monitoringMiddleware) BeforeAgent(_ context.Context, st *middleware.State) error {
	st.Values["monitoring.start"] = time.Now()
	return nil
}

func (m *monitoringMiddleware) BeforeModel(_ context.Context, st *middleware.State) error {
	st.Values[modelKey(st.Iteration)] = time.Now()
	return nil
}

func (m *monitoringMiddleware) AfterModel(_ context.Context, st *middleware.State) error {
	start := nowOr(st.Values[modelKey(st.Iteration)], time.Now())
	latency := time.Since(start)
	if latency > m.threshold {
		m.logger.Warn("slow model iteration", "request_id", readString(st.Values, requestIDKey), "iteration", st.Iteration, "latency", latency)
	}
	return nil
}

func (m *monitoringMiddleware) BeforeTool(_ context.Context, st *middleware.State) error {
	st.Values[toolKey(st.Iteration)] = time.Now()
	return nil
}

func (m *monitoringMiddleware) AfterTool(_ context.Context, st *middleware.State) error {
	latency := time.Since(nowOr(st.Values[toolKey(st.Iteration)], time.Now()))
	if latency > m.threshold {
		m.logger.Warn("slow tool call", "request_id", readString(st.Values, requestIDKey), "latency", latency)
	}
	return nil
}

func (m *monitoringMiddleware) AfterAgent(_ context.Context, st *middleware.State) error {
	started := nowOr(st.Values["monitoring.start"], time.Now())
	latency := time.Since(started)
	slow := latency > m.threshold
	m.metrics.record(latency, slow)
	if slow {
		m.logger.Info("request flagged as slow", "request_id", readString(st.Values, requestIDKey), "latency", latency)
	}
	return nil
}

func (reg *metricsRegistry) record(latency time.Duration, slow bool) {
	reg.totalRuns++
	reg.lastRun = latency
	if latency > reg.maxLatency {
		reg.maxLatency = latency
	}
	if slow {
		reg.slowRuns++
	}
}

func (m *monitoringMiddleware) Snapshot() (total int, slow int, max time.Duration, last time.Duration) {
	if m == nil || m.metrics == nil {
		return 0, 0, 0, 0
	}
	return m.metrics.totalRuns, m.metrics.slowRuns, m.metrics.maxLatency, m.metrics.lastRun
}

func newSettingsMiddleware(prompt, owner string, logger *slog.Logger) middleware.Middleware {
	env := map[string]string{"REQUEST_OWNER": owner}
	return middleware.Funcs{
		Identifier: "settings",
		OnBeforeAgent: func(_ context.Context, st *middleware.State) error {
			if st.Values == nil {
				st.Values = map[string]any{}
			}
			st.Values[promptKey] = prompt
			st.Values["settings.env"] = maps.Clone(env)

			if ctx, ok := st.Agent.(*agent.Context); ok && ctx != nil {
				if ctx.Values == nil {
					ctx.Values = map[string]any{}
				}
				ctx.Values[promptKey] = prompt
				ctx.Values["request_owner"] = owner
			}

			logger.Info("settings applied", "env_keys", len(env))
			return nil
		},
	}
}

func modelKey(iter int) string { return "monitoring.iter." + strconv.Itoa(iter) }
func toolKey(iter int) string  { return "monitoring.tool." + strconv.Itoa(iter) }
