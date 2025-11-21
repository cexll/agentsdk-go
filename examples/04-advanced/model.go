package main

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/tool"
	"log/slog"
)

type demoModel struct {
	projectRoot string
	owner       string
	useMCP      bool
	settings    *config.Settings
	counter     int64
}

func newDemoModel(root, owner string, useMCP bool, settings *config.Settings) model.Model {
	if settings == nil {
		def := config.GetDefaultSettings()
		settings = &def
	}
	return &demoModel{projectRoot: root, owner: owner, useMCP: useMCP, settings: settings}
}

func (m *demoModel) Complete(_ context.Context, req model.Request) (*model.Response, error) {
	if m == nil {
		return nil, errors.New("demo model is nil")
	}

	prompt := lastUserPrompt(req.Messages)
	if prompt == "" {
		return nil, errors.New("demo model: prompt is empty")
	}

	logs := lastToolResult(req.Messages, "observe_logs")
	timeResult := lastToolResult(req.Messages, "get_current_time")

	// If we already have both tool results, summarise and finish.
	if logs != "" && (!m.useMCP || timeResult != "") {
		summary := fmt.Sprintf("安全报告：%s", logs)
		if timeResult != "" {
			summary += fmt.Sprintf("；当前时间：%s", timeResult)
		}
		return &model.Response{Message: model.Message{Role: "assistant", Content: summary}, StopReason: "done"}, nil
	}

	id := atomic.AddInt64(&m.counter, 1)
	calls := []model.ToolCall{
		{
			ID:        fmt.Sprintf("tool-%d", id),
			Name:      "observe_logs",
			Arguments: map[string]any{"query": prompt, "project_root": m.projectRoot, "owner": m.owner},
		},
	}
	if m.useMCP && timeResult == "" {
		calls = append(calls, model.ToolCall{ID: fmt.Sprintf("tool-time-%d", id), Name: "get_current_time", Arguments: map[string]any{"timezone": "UTC"}})
	}

	envSuffix := ""
	if val := strings.TrimSpace(m.settings.Env["ADVANCED_EXAMPLE"]); val != "" {
		envSuffix = fmt.Sprintf(" (ADVANCED_EXAMPLE=%s)", val)
	}

	return &model.Response{
		Message: model.Message{
			Role:      "assistant",
			Content:   fmt.Sprintf("收到指令：%s，准备分析项目 %s。%s", prompt, m.projectRoot, envSuffix),
			ToolCalls: calls,
		},
		StopReason: "tool_call",
	}, nil
}

func (m *demoModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb == nil {
		return nil
	}
	return cb(model.StreamResult{Response: resp, Final: true})
}

type observeLogsTool struct {
	latency  time.Duration
	logger   *slog.Logger
	settings *config.Settings
}

func newObserveLogsTool(latency time.Duration, logger *slog.Logger, settings *config.Settings) tool.Tool {
	if settings == nil {
		def := config.GetDefaultSettings()
		settings = &def
	}
	return &observeLogsTool{latency: latency, logger: logger, settings: settings}
}

func (t *observeLogsTool) Name() string { return "observe_logs" }

func (t *observeLogsTool) Description() string {
	return "读取最近的 HTTP 访问日志并返回安全摘要"
}

func (t *observeLogsTool) Schema() *tool.JSONSchema {
	return &tool.JSONSchema{
		Type: "object",
		Properties: map[string]any{
			"query":        map[string]any{"type": "string", "description": "日志过滤条件或诊断提示"},
			"project_root": map[string]any{"type": "string", "description": "项目根目录，用于解析相对路径"},
			"owner":        map[string]any{"type": "string", "description": "logical owner"},
		},
		Required: []string{"query", "project_root"},
	}
}

func (t *observeLogsTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
	select {
	case <-time.After(t.latency):
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	query := readStringParam(params, "query")
	root := readStringParam(params, "project_root")
	owner := readStringParam(params, "owner")

	output := fmt.Sprintf("已检查 %s 的最近 100 行日志，未发现高危操作；查询: %s", root, query)
	if env := strings.TrimSpace(t.settings.Env["ADVANCED_EXAMPLE"]); env != "" {
		output += fmt.Sprintf("；ADVANCED_EXAMPLE=%s", env)
	}
	if owner != "" {
		output += fmt.Sprintf("；owner=%s", owner)
	}

	t.logger.Info("tool finished", "tool", t.Name(), "latency", t.latency)
	return &tool.ToolResult{
		Success: true,
		Output:  output,
		Data: map[string]any{
			"latency_ms":   t.latency.Milliseconds(),
			"project_root": root,
		},
	}, nil
}
