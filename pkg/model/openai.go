package model

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/shared"
)

// OpenAIConfig configures the OpenAI-backed Model.
type OpenAIConfig struct {
	APIKey       string
	BaseURL      string // Optional: for Azure or proxies
	Model        string // e.g., "gpt-4o", "gpt-4-turbo"
	MaxTokens    int
	MaxRetries   int
	System       string
	Temperature  *float64
	HTTPClient   *http.Client
	UseResponses bool // true = /responses API, false = /chat/completions
}

type openaiChatCompletions interface {
	New(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) (*openai.ChatCompletion, error)
	NewStreaming(ctx context.Context, params openai.ChatCompletionNewParams, opts ...option.RequestOption) *ssestream.Stream[openai.ChatCompletionChunk]
}

type openaiModel struct {
	completions openaiChatCompletions
	model       string
	maxTokens   int
	maxRetries  int
	system      string
	temperature *float64
}

const (
	defaultOpenAIModel      = "gpt-4o"
	defaultOpenAIMaxTokens  = 4096
	defaultOpenAIMaxRetries = 10
)

// NewOpenAI constructs a production-ready OpenAI-backed Model.
func NewOpenAI(cfg OpenAIConfig) (Model, error) {
	apiKey := strings.TrimSpace(cfg.APIKey)
	if apiKey == "" {
		return nil, errors.New("openai: api key required")
	}

	opts := []option.RequestOption{
		option.WithAPIKey(apiKey),
	}
	if cfg.BaseURL != "" {
		opts = append(opts, option.WithBaseURL(cfg.BaseURL))
	}
	if cfg.HTTPClient != nil {
		opts = append(opts, option.WithHTTPClient(cfg.HTTPClient))
	}

	client := openai.NewClient(opts...)
	maxTokens := cfg.MaxTokens
	if maxTokens <= 0 {
		maxTokens = defaultOpenAIMaxTokens
	}
	retries := cfg.MaxRetries
	if retries <= 0 {
		retries = defaultOpenAIMaxRetries
	}

	modelName := strings.TrimSpace(cfg.Model)
	if modelName == "" {
		modelName = defaultOpenAIModel
	}

	return &openaiModel{
		completions: &client.Chat.Completions,
		model:       modelName,
		maxTokens:   maxTokens,
		maxRetries:  retries,
		system:      strings.TrimSpace(cfg.System),
		temperature: cfg.Temperature,
	}, nil
}

// Complete issues a non-streaming completion.
func (m *openaiModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)
	var resp *Response
	err := m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		completion, err := m.completions.New(ctx, params)
		if err != nil {
			return err
		}

		resp = convertOpenAIResponse(completion)
		recordModelResponse(ctx, resp)
		return nil
	})
	return resp, err
}

// CompleteStream issues a streaming completion, forwarding deltas to cb.
func (m *openaiModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}

	recordModelRequest(ctx, req)

	return m.doWithRetry(ctx, func(ctx context.Context) error {
		params, err := m.buildParams(req)
		if err != nil {
			return err
		}

		// Enable usage reporting in stream
		params.StreamOptions = openai.ChatCompletionStreamOptionsParam{
			IncludeUsage: openai.Bool(true),
		}

		stream := m.completions.NewStreaming(ctx, params)
		if stream == nil {
			return errors.New("openai stream not available")
		}
		defer stream.Close()

		var (
			accumulatedContent strings.Builder
			accumulatedReasoning strings.Builder
			accumulatedCalls   = make(map[int]*toolCallAccumulator)
			finalUsage         Usage
			finishReason       string
		)

		for stream.Next() {
			chunk := stream.Current()

			// Capture usage from final chunk
			if chunk.Usage.TotalTokens > 0 {
				finalUsage = convertOpenAIUsage(chunk.Usage)
			}

			for _, choice := range chunk.Choices {
				if choice.FinishReason != "" {
					finishReason = string(choice.FinishReason)
				}

				delta := choice.Delta

				// Handle reasoning_content from thinking models
				if raw := delta.RawJSON(); raw != "" {
					var dp map[string]json.RawMessage
					if err := json.Unmarshal([]byte(raw), &dp); err == nil {
						if rc, ok := dp["reasoning_content"]; ok {
							var s string
							if json.Unmarshal(rc, &s) == nil {
								accumulatedReasoning.WriteString(s)
							}
						}
					}
				}

				// Handle text content
				if delta.Content != "" {
					accumulatedContent.WriteString(delta.Content)
					if err := cb(StreamResult{Delta: delta.Content}); err != nil {
						return err
					}
				}

				// Handle tool calls
				for _, tc := range delta.ToolCalls {
					idx := int(tc.Index)
					acc, ok := accumulatedCalls[idx]
					if !ok {
						acc = &toolCallAccumulator{}
						accumulatedCalls[idx] = acc
					}

					if tc.ID != "" {
						acc.id = tc.ID
					}
					if tc.Function.Name != "" {
						acc.name = tc.Function.Name
					}
					acc.arguments.WriteString(tc.Function.Arguments)
				}
			}
		}

		if err := stream.Err(); err != nil {
			return err
		}

		// Emit completed tool calls in order (sort by index to preserve order)
		var indices []int
		for idx := range accumulatedCalls {
			indices = append(indices, idx)
		}
		sort.Ints(indices)

		var toolCalls []ToolCall
		for _, idx := range indices {
			acc := accumulatedCalls[idx]
			tc := acc.toToolCall()
			if tc != nil {
				toolCalls = append(toolCalls, *tc)
				if err := cb(StreamResult{ToolCall: tc}); err != nil {
					return err
				}
			}
		}

		resp := &Response{
			Message: Message{
				Role:      "assistant",
				Content:   accumulatedContent.String(),
				ToolCalls: toolCalls,
				ReasoningContent: accumulatedReasoning.String(),
			},
			Usage:      finalUsage,
			StopReason: finishReason,
		}
		recordModelResponse(ctx, resp)
		return cb(StreamResult{Final: true, Response: resp})
	})
}

type toolCallAccumulator struct {
	id        string
	name      string
	arguments strings.Builder
}

func (a *toolCallAccumulator) toToolCall() *ToolCall {
	if a.id == "" || a.name == "" {
		return nil
	}
	return &ToolCall{
		ID:        a.id,
		Name:      a.name,
		Arguments: parseJSONArgs(a.arguments.String()),
	}
}

func parseJSONArgs(raw string) map[string]any {
	if raw == "" {
		return nil
	}
	var args map[string]any
	if err := json.Unmarshal([]byte(raw), &args); err != nil {
		return map[string]any{"raw": raw}
	}
	return args
}

func (m *openaiModel) buildParams(req Request) (openai.ChatCompletionNewParams, error) {
	messages := convertMessagesToOpenAI(req.Messages, m.system, req.System)

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	modelName := m.selectModel(req.Model)

	params := openai.ChatCompletionNewParams{
		Model:               shared.ChatModel(modelName),
		MaxCompletionTokens: openai.Int(int64(maxTokens)),
		Messages:            messages,
	}

	if len(req.Tools) > 0 {
		tools := convertToolsToOpenAI(req.Tools)
		params.Tools = tools
	}

	if m.temperature != nil {
		params.Temperature = openai.Float(*m.temperature)
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		params.User = openai.String(sessionID)
	}

	return params, nil
}

func (m *openaiModel) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
	attempts := 0
	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !isOpenAIRetryable(err) || attempts >= m.maxRetries {
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

func isOpenAIRetryable(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var apiErr *openai.Error
	if errors.As(err, &apiErr) {
		// Don't retry authentication errors
		return apiErr.StatusCode != http.StatusUnauthorized
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		if netErr.Timeout() {
			return true
		}
		//nolint:staticcheck // Temporary is deprecated but retained for transient errors
		return netErr.Temporary()
	}
	return true
}

func (m *openaiModel) selectModel(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	return m.model
}

func convertMessagesToOpenAI(msgs []Message, defaults ...string) []openai.ChatCompletionMessageParamUnion {
	var result []openai.ChatCompletionMessageParamUnion

	// Add system messages from defaults
	for _, sys := range defaults {
		if trimmed := strings.TrimSpace(sys); trimmed != "" {
			result = append(result, openai.SystemMessage(trimmed))
		}
	}

	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "system":
			if trimmed := strings.TrimSpace(msg.Content); trimmed != "" {
				result = append(result, openai.SystemMessage(trimmed))
			}
		case "assistant":
			result = append(result, buildOpenAIAssistantMessage(msg))
		case "tool":
			result = append(result, buildOpenAIToolResults(msg)...)
		default: // user
			content := msg.Content
			if strings.TrimSpace(content) == "" {
				content = "."
			}
			result = append(result, openai.UserMessage(content))
		}
	}

	if len(result) == 0 {
		result = append(result, openai.UserMessage("."))
	}

	return result
}

func buildOpenAIAssistantMessage(msg Message) openai.ChatCompletionMessageParamUnion {
	assistantParam := openai.ChatCompletionAssistantMessageParam{}

	// Set content
	content := msg.Content
	if strings.TrimSpace(content) == "" {
		content = "."
	}
	assistantParam.Content = openai.ChatCompletionAssistantMessageParamContentUnion{
		OfString: openai.String(content),
	}

	// Add tool calls if present
	if len(msg.ToolCalls) > 0 {
		var toolCalls []openai.ChatCompletionMessageToolCallParam
		for _, call := range msg.ToolCalls {
			id := strings.TrimSpace(call.ID)
			name := strings.TrimSpace(call.Name)
			if id == "" || name == "" {
				continue
			}

			argsJSON, _ := json.Marshal(call.Arguments) //nolint:errcheck
			toolCalls = append(toolCalls, openai.ChatCompletionMessageToolCallParam{
				ID: id,
				Function: openai.ChatCompletionMessageToolCallFunctionParam{
					Name:      name,
					Arguments: string(argsJSON),
				},
			})
		}
		assistantParam.ToolCalls = toolCalls
	}

	// Pass through reasoning_content for thinking models
	if msg.ReasoningContent != "" {
		assistantParam.SetExtraFields(map[string]any{
			"reasoning_content": msg.ReasoningContent,
		})
	}

	return openai.ChatCompletionMessageParamUnion{
		OfAssistant: &assistantParam,
	}
}

func buildOpenAIToolResults(msg Message) []openai.ChatCompletionMessageParamUnion {
	if len(msg.ToolCalls) == 0 {
		return []openai.ChatCompletionMessageParamUnion{
			openai.ToolMessage(msg.Content, ""),
		}
	}

	var results []openai.ChatCompletionMessageParamUnion
	for _, call := range msg.ToolCalls {
		id := strings.TrimSpace(call.ID)
		if id == "" {
			continue
		}
		content := call.Result
		if strings.TrimSpace(content) == "" {
			content = msg.Content
		}
		results = append(results, openai.ToolMessage(content, id))
	}

	if len(results) == 0 {
		results = append(results, openai.ToolMessage(msg.Content, ""))
	}

	return results
}

func convertToolsToOpenAI(tools []ToolDefinition) []openai.ChatCompletionToolParam {
	var result []openai.ChatCompletionToolParam
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		tool := openai.ChatCompletionToolParam{
			Function: shared.FunctionDefinitionParam{
				Name:       name,
				Parameters: convertToFunctionParameters(def.Parameters),
			},
		}
		if desc := strings.TrimSpace(def.Description); desc != "" {
			tool.Function.Description = openai.Opt(desc)
		}

		result = append(result, tool)
	}
	return result
}

func convertToFunctionParameters(params map[string]any) shared.FunctionParameters {
	if len(params) == 0 {
		return shared.FunctionParameters{
			"type": "object",
		}
	}

	// Ensure type is set
	result := make(shared.FunctionParameters, len(params)+1)
	for k, v := range params {
		result[k] = v
	}
	if _, ok := result["type"]; !ok {
		result["type"] = "object"
	}
	return result
}

func convertOpenAIResponse(completion *openai.ChatCompletion) *Response {
	if completion == nil || len(completion.Choices) == 0 {
		return &Response{
			Message: Message{Role: "assistant"},
		}
	}

	choice := completion.Choices[0]
	msg := choice.Message

	var toolCalls []ToolCall
	for _, tc := range msg.ToolCalls {
		toolCalls = append(toolCalls, ToolCall{
			ID:        tc.ID,
			Name:      tc.Function.Name,
			Arguments: parseJSONArgs(tc.Function.Arguments),
		})
	}

	var reasoningContent string
	if raw := msg.RawJSON(); raw != "" {
		var parsed map[string]json.RawMessage
		if err := json.Unmarshal([]byte(raw), &parsed); err == nil {
			if rc, ok := parsed["reasoning_content"]; ok {
				_ = json.Unmarshal(rc, &reasoningContent)
			}
		}
	}

	return &Response{
		Message: Message{
			Role:      "assistant",
			Content:   msg.Content,
			ToolCalls: toolCalls,
			ReasoningContent: reasoningContent,
		},
		Usage:      convertOpenAIUsage(completion.Usage),
		StopReason: choice.FinishReason,
	}
}

func convertOpenAIUsage(usage openai.CompletionUsage) Usage {
	return Usage{
		InputTokens:  int(usage.PromptTokens),
		OutputTokens: int(usage.CompletionTokens),
		TotalTokens:  int(usage.TotalTokens),
	}
}
