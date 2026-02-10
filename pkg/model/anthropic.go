package model

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"strings"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/param"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

// AnthropicConfig wires a plain anthropic-sdk-go client into the Model interface.
type AnthropicConfig struct {
	APIKey      string
	BaseURL     string
	Model       string
	MaxTokens   int
	MaxRetries  int
	System      string
	Temperature *float64
	HTTPClient  *http.Client
}

type anthropicMessages interface {
	New(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) (*anthropicsdk.Message, error)
	NewStreaming(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	CountTokens(ctx context.Context, params anthropicsdk.MessageCountTokensParams, opts ...option.RequestOption) (*anthropicsdk.MessageTokensCount, error)
}

type anthropicModel struct {
	msgs             anthropicMessages
	model            anthropicsdk.Model
	maxTokens        int
	maxRetries       int
	system           string
	temperature      *float64
	configuredAPIKey string
}

var anthropicPredefinedHeaders = map[string]string{
	"accept":         "application/json",
	"anthropic-beta": "interleaved-thinking-2025-05-14,fine-grained-tool-streaming-2025-05-14",
	"anthropic-dangerous-direct-browser-access": "true",
	"anthropic-version":                         "2023-06-01",
	"content-type":                              "application/json",
	"user-agent":                                "claude-cli/2.0.34 (external, cli)",
	"x-app":                                     "cli",
	"x-stainless-arch":                          "arm64",
	"x-stainless-helper-method":                 "stream",
	"x-stainless-lang":                          "js",
	"x-stainless-os":                            "MacOS",
	"x-stainless-package-version":               "0.68.0",
	"x-stainless-retry-count":                   "0",
	"x-stainless-runtime":                       "node",
	"x-stainless-runtime-version":               "v22.20.0",
	"x-stainless-timeout":                       "600",
}

func anthropicCustomHeadersEnabled() bool {
	return strings.EqualFold(strings.TrimSpace(os.Getenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED")), "true")
}

func newAnthropicHeaders(defaults, overrides map[string]string) map[string]string {
	merge := func(dst map[string]string, src map[string]string) {
		for k, v := range src {
			norm := strings.ToLower(strings.TrimSpace(k))
			if norm == "" || norm == "x-api-key" {
				continue
			}
			dst[norm] = v
		}
	}

	merged := make(map[string]string)
	if anthropicCustomHeadersEnabled() {
		merge(merged, anthropicPredefinedHeaders)
	}
	merge(merged, defaults)
	merge(merged, overrides)

	if len(merged) == 0 {
		return nil
	}
	return merged
}

func (m *anthropicModel) requestOptions() []option.RequestOption {
	headers := newAnthropicHeaders(nil, nil)

	apiKey := strings.TrimSpace(m.configuredAPIKey)
	if apiKey == "" {
		if envKey := strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")); envKey != "" {
			apiKey = envKey
		} else if authToken := strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")); authToken != "" {
			apiKey = authToken
		}
	}
	if apiKey != "" {
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["x-api-key"] = apiKey
	}

	if len(headers) == 0 {
		return nil
	}
	opts := make([]option.RequestOption, 0, len(headers))
	for key, value := range headers {
		if value == "" {
			continue
		}
		opts = append(opts, option.WithHeader(key, value))
	}
	return opts
}

// NewAnthropic constructs a production-ready Anthropic-backed Model.
func NewAnthropic(cfg AnthropicConfig) (Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("anthropic: api key required")
	}

	opts := []option.RequestOption{
		// Explicitly set the API key so it overrides any ANTHROPIC_AUTH_TOKEN
		// or ANTHROPIC_API_KEY from the environment (DefaultClientOptions).
		option.WithAPIKey(apiKey),
		// Also set auth token for providers that require Authorization: Bearer
		// (e.g. DeepSeek's Anthropic-compatible endpoint).
		option.WithAuthToken(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}

	client := anthropicsdk.NewClient(opts...)
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = 4096
	}
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = 10
	}

	return &anthropicModel{
		msgs:             &client.Messages,
		model:            mapModelName(cfg.Model),
		maxTokens:        maxTokens,
		maxRetries:       retries,
		system:           strings.TrimSpace(cfg.System),
		temperature:      cfg.Temperature,
		configuredAPIKey: apiKey,
	}, nil
}

// Complete issues a non-streaming completion.
func (m *anthropicModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)
	var resp *Response
	headerOpts := m.requestOptions()
	err := m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		msg, err := m.msgs.New(ctx, params, headerOpts...)
		if err != nil {
			return err
		}

		usage := convertUsage(msg.Usage)
		resp = &Response{
			Message:    convertResponseMessage(*msg),
			Usage:      usage,
			StopReason: string(msg.StopReason),
		}
		recordModelResponse(ctx, resp)
		return nil
	})
	return resp, err
}

// CompleteStream issues a streaming completion, forwarding deltas to cb.
func (m *anthropicModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}

	recordModelRequest(ctx, req)

	headerOpts := m.requestOptions()
	return m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		// Pre-count input tokens for accurate usage; ignore errors (non-fatal).
		var usage Usage
		if count, err := m.msgs.CountTokens(ctx, m.countParams(params)); err == nil && count != nil {
			usage.InputTokens = int(count.InputTokens)
			usage.TotalTokens = usage.InputTokens
		}

		stream := m.msgs.NewStreaming(ctx, params, headerOpts...)
		if stream == nil {
			return errors.New("anthropic stream not available")
		}
		defer stream.Close()

		var final anthropicsdk.Message

		for stream.Next() {
			event := stream.Current()
			// Keep aggregate message for the final output.
			if err := final.Accumulate(event); err != nil {
				return fmt.Errorf("accumulate stream: %w", err)
			}

			switch ev := event.AsAny().(type) {
			case anthropicsdk.ContentBlockDeltaEvent:
				if text := ev.Delta.AsTextDelta().Text; text != "" {
					if err := cb(StreamResult{Delta: text}); err != nil {
						return err
					}
				}
			case anthropicsdk.ContentBlockStopEvent:
				if tool := extractToolCall(final); tool != nil {
					if err := cb(StreamResult{ToolCall: tool}); err != nil {
						return err
					}
				}
			case anthropicsdk.MessageDeltaEvent:
				usage.CacheCreationTokens = int(ev.Usage.CacheCreationInputTokens)
				usage.CacheReadTokens = int(ev.Usage.CacheReadInputTokens)
				usage.InputTokens = int(ev.Usage.InputTokens)
				usage.OutputTokens = int(ev.Usage.OutputTokens)
				usage.TotalTokens = usage.InputTokens + usage.OutputTokens
			}
		}

		if err := stream.Err(); err != nil {
			return err
		}

		resp := &Response{
			Message:    convertResponseMessage(final),
			Usage:      usageFromFallback(final.Usage, usage),
			StopReason: string(final.StopReason),
		}
		recordModelResponse(ctx, resp)
		return cb(StreamResult{Final: true, Response: resp})
	})
}

func (m *anthropicModel) buildParams(req Request) (anthropicsdk.MessageNewParams, error) {
	systemBlocks, messageParams, err := convertMessages(req.Messages, req.EnablePromptCache, m.system, req.System)
	if err != nil {
		return anthropicsdk.MessageNewParams{}, err
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	params := anthropicsdk.MessageNewParams{
		Model:     m.selectModel(req.Model),
		MaxTokens: int64(maxTokens),
		Messages:  messageParams,
	}

	if len(systemBlocks) > 0 {
		params.System = systemBlocks
	}

	if len(req.Tools) > 0 {
		tools, err := convertTools(req.Tools)
		if err != nil {
			return anthropicsdk.MessageNewParams{}, err
		}
		params.Tools = tools
	}

	if m.temperature != nil {
		params.Temperature = param.NewOpt(*m.temperature)
	}
	if req.Temperature != nil {
		params.Temperature = param.NewOpt(*req.Temperature)
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		params.Metadata = anthropicsdk.MetadataParam{
			UserID: param.NewOpt(sessionID),
		}
	}

	return params, nil
}

func (m *anthropicModel) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := 0
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		// Check context before deciding to retry
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isRetryable(err) || attempts >= m.maxRetries {
			return err
		}
		attempts++
		backoff := time.Duration(attempts*attempts) * 100 * time.Millisecond
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(backoff):
		}
	}
}

func isRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *anthropicsdk.Error
	if errors.As(err, &apiErr) {
		return apiErr.StatusCode != http.StatusUnauthorized
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		//nolint:staticcheck // Temporary is deprecated but retained to treat non-timeout transient errors as retryable (tests rely on this behaviour).
		return netErr.Temporary()
	}
	return true
}

func (m *anthropicModel) selectModel(override string) anthropicsdk.Model {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return mapModelName(trimmed)
	}
	return m.model
}

func (m *anthropicModel) countParams(params anthropicsdk.MessageNewParams) anthropicsdk.MessageCountTokensParams {
	cp := anthropicsdk.MessageCountTokensParams{
		Messages: params.Messages,
		Model:    params.Model,
	}
	if len(params.System) > 0 {
		cp.System = anthropicsdk.MessageCountTokensParamsSystemUnion{OfTextBlockArray: params.System}
	}
	if len(params.Tools) > 0 {
		cp.Tools = convertCountTools(params.Tools)
	}
	return cp
}

func convertCountTools(tools []anthropicsdk.ToolUnionParam) []anthropicsdk.MessageCountTokensToolUnionParam {
	out := make([]anthropicsdk.MessageCountTokensToolUnionParam, 0, len(tools))
	for _, tool := range tools {
		if tool.OfTool != nil {
			out = append(out, anthropicsdk.MessageCountTokensToolUnionParam{OfTool: tool.OfTool})
		}
	}
	return out
}

func convertMessages(msgs []Message, enableCache bool, defaults ...string) ([]anthropicsdk.TextBlockParam, []anthropicsdk.MessageParam, error) {
	var systemBlocks []anthropicsdk.TextBlockParam
	for _, sys := range defaults {
		if trimmed := strings.TrimSpace(sys); trimmed != "" {
			systemBlocks = append(systemBlocks, anthropicsdk.TextBlockParam{Text: trimmed})
		}
	}

	messageParams := make([]anthropicsdk.MessageParam, 0, len(msgs))
	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				systemBlocks = append(systemBlocks, anthropicsdk.TextBlockParam{Text: trimmed})
			}
			continue
		case "assistant":
			content := buildAssistantContent(msg)
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role:    anthropicsdk.MessageParamRoleAssistant,
				Content: content,
			})
		case "tool":
			content := buildToolResults(msg)
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role:    anthropicsdk.MessageParamRoleUser,
				Content: content,
			})
		default:
			content := msg.Content
			if strings.TrimSpace(content) == "" {
				content = "."
			}
			messageParams = append(messageParams, anthropicsdk.MessageParam{
				Role: anthropicsdk.MessageParamRoleUser,
				Content: []anthropicsdk.ContentBlockParamUnion{
					anthropicsdk.NewTextBlock(content),
				},
			})
		}
	}

	if len(messageParams) == 0 {
		messageParams = append(messageParams, anthropicsdk.MessageParam{
			Role: anthropicsdk.MessageParamRoleUser,
			Content: []anthropicsdk.ContentBlockParamUnion{
				anthropicsdk.NewTextBlock("."),
			},
		})
	}

	// Apply cache control if enabled
	if enableCache {
		// Mark the last system block for caching
		if len(systemBlocks) > 0 {
			systemBlocks[len(systemBlocks)-1].CacheControl = anthropicsdk.NewCacheControlEphemeralParam()
		}

		// Mark the last 2-3 user messages for caching to optimize multi-turn conversations
		userMsgCount := 0
		for i := len(messageParams) - 1; i >= 0 && userMsgCount < 3; i-- {
			if messageParams[i].Role == anthropicsdk.MessageParamRoleUser && len(messageParams[i].Content) > 0 {
				// Mark the last content block in this message
				lastIdx := len(messageParams[i].Content) - 1
				block := &messageParams[i].Content[lastIdx]

				// Only cache text blocks
				if text := block.GetText(); text != nil && *text != "" {
					messageParams[i].Content[lastIdx] = anthropicsdk.ContentBlockParamUnion{
						OfText: &anthropicsdk.TextBlockParam{
							Text:         *text,
							CacheControl: anthropicsdk.NewCacheControlEphemeralParam(),
						},
					}
				}
				userMsgCount++
			}
		}
	}

	return systemBlocks, messageParams, nil
}

func buildAssistantContent(msg Message) []anthropicsdk.ContentBlockParamUnion {
	blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, 1+len(msg.ToolCalls))
	// Prepend thinking block if reasoning content is present
	if msg.ReasoningContent != "" {
		blocks = append(blocks, anthropicsdk.NewThinkingBlock("", msg.ReasoningContent))
	}
	if strings.TrimSpace(msg.Content) != "" {
		blocks = append(blocks, anthropicsdk.NewTextBlock(msg.Content))
	}
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		name := strings.TrimSpace(call.Name)
		if id == "" || name == "" {
			continue
		}
		blocks = append(blocks, anthropicsdk.NewToolUseBlock(id, cloneValue(call.Arguments), name))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicsdk.NewTextBlock("."))
	}
	return blocks
}

func buildToolResults(msg Message) []anthropicsdk.ContentBlockParamUnion {
	if len(msg.ToolCalls) == 0 {
		return []anthropicsdk.ContentBlockParamUnion{
			anthropicsdk.NewTextBlock(msg.Content),
		}
	}

	blocks := make([]anthropicsdk.ContentBlockParamUnion, 0, len(msg.ToolCalls))
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		text := call.Result
		if strings.TrimSpace(text) == "" {
			text = msg.Content
		}
		blocks = append(blocks, anthropicsdk.NewToolResultBlock(id, text, toolResultIsError(text)))
	}
	if len(blocks) == 0 {
		blocks = append(blocks, anthropicsdk.NewTextBlock(msg.Content))
	}
	return blocks
}

func toolResultIsError(text string) bool {
	trimmed := strings.TrimSpace(text)
	if !strings.HasPrefix(trimmed, "{") || !strings.HasSuffix(trimmed, "}") {
		return false
	}

	var payload map[string]any
	if err := json.Unmarshal([]byte(trimmed), &payload); err != nil {
		return false
	}

	val, ok := payload["error"]
	if !ok {
		return false
	}

	switch t := val.(type) {
	case bool:
		return t
	case string:
		return strings.TrimSpace(t) != ""
	default:
		return t != nil
	}
}

func convertTools(tools []ToolDefinition) ([]anthropicsdk.ToolUnionParam, error) {
	out := make([]anthropicsdk.ToolUnionParam, 0, len(tools))
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		schema, err := encodeSchema(def.Parameters)
		if err != nil {
			return nil, fmt.Errorf("tool %s schema: %w", name, err)
		}

		tool := anthropicsdk.ToolParam{
			Name:        name,
			InputSchema: schema,
		}
		if strings.TrimSpace(def.Description) != "" {
			tool.Description = anthropicsdk.String(def.Description)
		}

		out = append(out, anthropicsdk.ToolUnionParam{OfTool: &tool})
	}
	return out, nil
}

func encodeSchema(raw map[string]any) (anthropicsdk.ToolInputSchemaParam, error) {
	if len(raw) == 0 {
		return anthropicsdk.ToolInputSchemaParam{Type: "object"}, nil
	}
	data, err := json.Marshal(raw)
	if err != nil {
		return anthropicsdk.ToolInputSchemaParam{}, err
	}
	var schema anthropicsdk.ToolInputSchemaParam
	if err := json.Unmarshal(data, &schema); err != nil {
		return anthropicsdk.ToolInputSchemaParam{}, err
	}
	if schema.Type == "" {
		schema.Type = "object"
	}
	return schema, nil
}

func convertResponseMessage(msg anthropicsdk.Message) Message {
	var textParts []string
	var thinkingParts []string
	var toolCalls []ToolCall
	for _, block := range msg.Content {
		if tc := toolCallFromBlock(block); tc != nil {
			toolCalls = append(toolCalls, *tc)
			continue
		}
		if block.Type == "thinking" && block.Thinking != "" {
			thinkingParts = append(thinkingParts, block.Thinking)
			continue
		}
		if text := block.Text; text != "" {
			textParts = append(textParts, text)
		}
	}

	role := strings.TrimSpace(string(msg.Role))
	if role == "" {
		role = "assistant"
	}
	return Message{
		Role:             role,
		Content:          strings.Join(textParts, ""),
		ToolCalls:        toolCalls,
		ReasoningContent: strings.Join(thinkingParts, ""),
	}
}

func toolCallFromBlock(block anthropicsdk.ContentBlockUnion) *ToolCall {
	if block.Type != "tool_use" {
		return nil
	}
	id := strings.TrimSpace(block.ID)
	name := strings.TrimSpace(block.Name)
	if id == "" || name == "" {
		return nil
	}
	args := decodeJSON(block.Input)
	if len(args) == 0 && len(block.Input) > 0 {
		log.Printf("WARNING: tool_use %q has empty input (raw=%s) — API proxy may have stripped arguments", name, string(block.Input))
	}
	return &ToolCall{
		ID:        id,
		Name:      name,
		Arguments: args,
	}
}

func extractToolCall(msg anthropicsdk.Message) *ToolCall {
	if len(msg.Content) == 0 {
		return nil
	}
	return toolCallFromBlock(msg.Content[len(msg.Content)-1])
}

func decodeJSON(raw json.RawMessage) map[string]any {
	if len(raw) == 0 {
		return nil
	}
	var v any
	if err := json.Unmarshal(raw, &v); err != nil {
		return map[string]any{"raw": string(raw)}
	}
	if v == nil {
		return nil
	}
	if m, ok := v.(map[string]any); ok {
		return m
	}
	return map[string]any{"value": v}
}

func cloneValue(v any) any {
	switch val := v.(type) {
	case map[string]any:
		cp := make(map[string]any, len(val))
		for k, v := range val {
			cp[k] = cloneValue(v)
		}
		return cp
	case []any:
		cp := make([]any, len(val))
		for i, el := range val {
			cp[i] = cloneValue(el)
		}
		return cp
	default:
		return val
	}
}

func convertUsage(u anthropicsdk.Usage) Usage {
	input := int(u.InputTokens)
	// Usage fields already treat cache tokens as part of input; keep explicit copy.
	cacheRead := int(u.CacheReadInputTokens)
	cacheCreate := int(u.CacheCreationInputTokens)
	return Usage{
		InputTokens:         input,
		OutputTokens:        int(u.OutputTokens),
		TotalTokens:         int(u.OutputTokens) + input,
		CacheReadTokens:     cacheRead,
		CacheCreationTokens: cacheCreate,
	}
}

func usageFromFallback(final anthropicsdk.Usage, tracked Usage) Usage {
	if tracked.InputTokens == 0 && tracked.OutputTokens == 0 {
		return convertUsage(final)
	}
	if tracked.TotalTokens == 0 {
		tracked.TotalTokens = tracked.InputTokens + tracked.OutputTokens
	}
	return tracked
}

const defaultAnthropicModel = anthropicsdk.ModelClaudeSonnet4_5_20250929

var supportedAnthropicModels = []anthropicsdk.Model{
	anthropicsdk.ModelClaude3_7SonnetLatest,   //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude3_7Sonnet20250219, //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude3_5HaikuLatest,
	anthropicsdk.ModelClaude3_5Haiku20241022,
	anthropicsdk.ModelClaudeHaiku4_5,
	anthropicsdk.ModelClaudeHaiku4_5_20251001,
	anthropicsdk.ModelClaudeSonnet4_20250514,
	anthropicsdk.ModelClaudeSonnet4_0,
	anthropicsdk.ModelClaude4Sonnet20250514,
	anthropicsdk.ModelClaudeSonnet4_5,
	anthropicsdk.ModelClaudeSonnet4_5_20250929,
	anthropicsdk.ModelClaudeOpus4_0,
	anthropicsdk.ModelClaudeOpus4_20250514,
	anthropicsdk.ModelClaude4Opus20250514,
	anthropicsdk.ModelClaudeOpus4_1_20250805,
	anthropicsdk.ModelClaude3OpusLatest,      //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude_3_Opus_20240229, //nolint:staticcheck // deprecated but still accepted
	anthropicsdk.ModelClaude_3_Haiku_20240307,
}

var modelLookup = func() map[string]anthropicsdk.Model {
	lookup := make(map[string]anthropicsdk.Model, len(supportedAnthropicModels))
	for _, model := range supportedAnthropicModels {
		lookup[string(model)] = model
	}
	return lookup
}()

func mapModelName(name string) anthropicsdk.Model {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return defaultAnthropicModel
	}
	if model, ok := modelLookup[trimmed]; ok {
		return model
	}
	// Pass through unknown model names (e.g. deepseek-reasoner via proxy)
	return anthropicsdk.Model(trimmed)
}
