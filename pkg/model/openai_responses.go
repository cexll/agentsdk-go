package model

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/param"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/openai/openai-go/shared"
)

// openaiResponsesModel implements Model using OpenAI's Responses API.
type openaiResponsesModel struct {
	responses   openaiResponsesService
	model       string
	maxTokens   int
	maxRetries  int
	system      string
	temperature *float64
}

type openaiResponsesService interface {
	New(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error)
	NewStreaming(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
}

// NewOpenAIResponses constructs an OpenAI model using the Responses API.
func NewOpenAIResponses(cfg OpenAIConfig) (Model, error) {
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

	return &openaiResponsesModel{
		responses:   &client.Responses,
		model:       modelName,
		maxTokens:   maxTokens,
		maxRetries:  retries,
		system:      strings.TrimSpace(cfg.System),
		temperature: cfg.Temperature,
	}, nil
}

// Complete issues a non-streaming completion using Responses API.
func (m *openaiResponsesModel) Complete(ctx context.Context, req Request) (*Response, error) {
	recordModelRequest(ctx, req)
	var resp *Response
	err := m.doWithRetry(ctx, func(ctx context.Context) error {
		params := m.buildResponsesParams(req)

		response, err := m.responses.New(ctx, params)
		if err != nil {
			return err
		}

		resp = convertResponsesAPIResponse(response)
		recordModelResponse(ctx, resp)
		return nil
	})
	return resp, err
}

// CompleteStream issues a streaming completion using Responses API.
func (m *openaiResponsesModel) CompleteStream(ctx context.Context, req Request, cb StreamHandler) error {
	if cb == nil {
		return errors.New("stream callback required")
	}

	recordModelRequest(ctx, req)

	return m.doWithRetry(ctx, func(ctx context.Context) error {
		params := m.buildResponsesParams(req)

		stream := m.responses.NewStreaming(ctx, params)
		if stream == nil {
			return errors.New("openai responses stream not available")
		}
		defer stream.Close()

		var (
			accumulatedContent strings.Builder
			accumulatedCalls   = make(map[string]*responsesToolCallAccumulator)
			finalUsage         Usage
			finalResponse      *responses.Response
		)

		for stream.Next() {
			event := stream.Current()

			// Use Type field to determine event type
			switch event.Type {
			case "response.output_text.delta":
				// Text delta - use Delta field
				if delta := event.Delta.OfString; delta != "" {
					accumulatedContent.WriteString(delta)
					if err := cb(StreamResult{Delta: delta}); err != nil {
						return err
					}
				}

			case "response.function_call_arguments.delta":
				// Function call argument delta - use direct fields
				if event.ItemID != "" {
					acc, ok := accumulatedCalls[event.ItemID]
					if !ok {
						acc = &responsesToolCallAccumulator{id: event.ItemID}
						accumulatedCalls[event.ItemID] = acc
					}
					acc.arguments.WriteString(event.Arguments)
				}

			case "response.function_call_arguments.done":
				// Function call complete - use direct fields
				if event.ItemID != "" {
					acc, ok := accumulatedCalls[event.ItemID]
					if !ok {
						acc = &responsesToolCallAccumulator{id: event.ItemID}
						accumulatedCalls[event.ItemID] = acc
					}
					// Name comes from output_item.added event, not here
					acc.arguments.Reset()
					acc.arguments.WriteString(event.Arguments)
				}

			case "response.output_item.added":
				// New output item - capture function call info
				if event.Item.Type == "function_call" && event.Item.ID != "" {
					acc, ok := accumulatedCalls[event.Item.ID]
					if !ok {
						acc = &responsesToolCallAccumulator{id: event.Item.ID}
						accumulatedCalls[event.Item.ID] = acc
					}
					acc.name = event.Item.Name
					acc.callID = event.Item.CallID
				}

			case "response.completed":
				// Response complete
				finalResponse = &event.Response
				if finalResponse.Usage.TotalTokens > 0 {
					finalUsage = convertResponsesUsage(finalResponse.Usage)
				}
			}
		}

		if err := stream.Err(); err != nil {
			return err
		}

		// Emit completed tool calls
		var toolCalls []ToolCall
		for _, acc := range accumulatedCalls {
			tc := acc.toToolCall()
			if tc != nil {
				toolCalls = append(toolCalls, *tc)
				if err := cb(StreamResult{ToolCall: tc}); err != nil {
					return err
				}
			}
		}

		// Determine stop reason
		stopReason := "stop"
		if finalResponse != nil && finalResponse.Status != "" {
			stopReason = string(finalResponse.Status)
		}
		if len(toolCalls) > 0 {
			stopReason = "tool_calls"
		}

		resp := &Response{
			Message: Message{
				Role:      "assistant",
				Content:   accumulatedContent.String(),
				ToolCalls: toolCalls,
			},
			Usage:      finalUsage,
			StopReason: stopReason,
		}
		recordModelResponse(ctx, resp)
		return cb(StreamResult{Final: true, Response: resp})
	})
}

type responsesToolCallAccumulator struct {
	id        string
	callID    string
	name      string
	arguments strings.Builder
}

func (a *responsesToolCallAccumulator) toToolCall() *ToolCall {
	if a.name == "" {
		return nil
	}
	id := a.callID
	if id == "" {
		id = a.id
	}
	return &ToolCall{
		ID:        id,
		Name:      a.name,
		Arguments: parseJSONArgs(a.arguments.String()),
	}
}

func (m *openaiResponsesModel) buildResponsesParams(req Request) responses.ResponseNewParams {
	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		maxTokens = m.maxTokens
	}

	modelName := m.selectModel(req.Model)

	params := responses.ResponseNewParams{
		Model:           shared.ResponsesModel(modelName),
		MaxOutputTokens: openai.Int(int64(maxTokens)),
		Input:           buildResponsesInput(req.Messages),
	}

	// Set system instructions
	if sys := m.system; sys != "" {
		params.Instructions = openai.String(sys)
	}
	if sys := strings.TrimSpace(req.System); sys != "" {
		params.Instructions = openai.String(sys)
	}

	// Add tools
	if len(req.Tools) > 0 {
		params.Tools = convertToolsToResponsesAPI(req.Tools)
	}

	// Set temperature
	if m.temperature != nil {
		params.Temperature = openai.Float(*m.temperature)
	}
	if req.Temperature != nil {
		params.Temperature = openai.Float(*req.Temperature)
	}

	if sessionID := strings.TrimSpace(req.SessionID); sessionID != "" {
		params.User = openai.String(sessionID)
	}

	return params
}

func (m *openaiResponsesModel) selectModel(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	return m.model
}

func (m *openaiResponsesModel) doWithRetry(ctx context.Context, fn func(context.Context) error) error {
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

func buildResponsesInput(msgs []Message) responses.ResponseNewParamsInputUnion {
	if len(msgs) == 0 {
		return responses.ResponseNewParamsInputUnion{
			OfString: param.Opt[string]{Value: "."},
		}
	}

	if hasOpenAIImageContentBlocks(msgs) {
		items := buildResponsesMultimodalInput(msgs)
		if len(items) > 0 {
			return responses.ResponseNewParamsInputUnion{
				OfInputItemList: items,
			}
		}
	}

	// Build a combined prompt from all user messages
	// The Responses API works best with simple string input
	var sb strings.Builder
	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		if role == "system" {
			// System messages are handled via Instructions parameter
			continue
		}
		if role == "user" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}
			sb.WriteString(msg.Content)
		}
	}

	content := sb.String()
	if strings.TrimSpace(content) == "" {
		content = "."
	}

	return responses.ResponseNewParamsInputUnion{
		OfString: param.Opt[string]{Value: content},
	}
}

func hasOpenAIImageContentBlocks(msgs []Message) bool {
	for _, msg := range msgs {
		for _, block := range msg.ContentBlocks {
			if block.Type != ContentBlockImage {
				continue
			}
			if openAIImageURL(block) != "" {
				return true
			}
		}
	}
	return false
}

func buildResponsesMultimodalInput(msgs []Message) responses.ResponseInputParam {
	items := make(responses.ResponseInputParam, 0, len(msgs))
	for _, msg := range msgs {
		role := strings.ToLower(strings.TrimSpace(msg.Role))
		switch role {
		case "user":
			parts := buildResponsesInputParts(msg)
			if len(parts) == 0 {
				continue
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: parts,
					},
				},
			})
		case "assistant":
			parts := buildResponsesInputParts(msg)
			if len(parts) == 0 {
				continue
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleAssistant,
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: parts,
					},
				},
			})
		case "system", "developer":
			parts := buildResponsesInputParts(msg)
			if len(parts) == 0 {
				continue
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleDeveloper,
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: parts,
					},
				},
			})
		case "tool":
			// Tool results: include as user message with text content.
			// The Responses API EasyInputMessage doesn't have a tool role.
			parts := buildResponsesInputParts(msg)
			if len(parts) == 0 {
				continue
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: parts,
					},
				},
			})
		default:
			// Unknown roles: map to user
			parts := buildResponsesInputParts(msg)
			if len(parts) == 0 {
				continue
			}
			items = append(items, responses.ResponseInputItemUnionParam{
				OfMessage: &responses.EasyInputMessageParam{
					Role: responses.EasyInputMessageRoleUser,
					Content: responses.EasyInputMessageContentUnionParam{
						OfInputItemContentList: parts,
					},
				},
			})
		}
	}
	return items
}

func buildResponsesInputParts(msg Message) responses.ResponseInputMessageContentListParam {
	parts := make(responses.ResponseInputMessageContentListParam, 0, len(msg.ContentBlocks)+1)
	if text := strings.TrimSpace(msg.Content); text != "" {
		parts = append(parts, responses.ResponseInputContentUnionParam{
			OfInputText: &responses.ResponseInputTextParam{
				Text: text,
			},
		})
	}
	for _, block := range msg.ContentBlocks {
		switch block.Type {
		case ContentBlockText:
			if text := strings.TrimSpace(block.Text); text != "" {
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputText: &responses.ResponseInputTextParam{
						Text: text,
					},
				})
			}
		case ContentBlockImage:
			if imageURL := openAIImageURL(block); imageURL != "" {
				parts = append(parts, responses.ResponseInputContentUnionParam{
					OfInputImage: &responses.ResponseInputImageParam{
						Detail:   responses.ResponseInputImageDetailAuto,
						ImageURL: param.NewOpt(imageURL),
					},
				})
			}
		}
	}
	if len(parts) == 0 {
		parts = append(parts, responses.ResponseInputContentUnionParam{
			OfInputText: &responses.ResponseInputTextParam{
				Text: ".",
			},
		})
	}
	return parts
}

func convertToolsToResponsesAPI(tools []ToolDefinition) []responses.ToolUnionParam {
	var result []responses.ToolUnionParam
	for _, def := range tools {
		name := strings.TrimSpace(def.Name)
		if name == "" {
			continue
		}

		tool := responses.ToolUnionParam{
			OfFunction: &responses.FunctionToolParam{
				Name:       name,
				Parameters: convertToFunctionParameters(def.Parameters),
			},
		}
		if desc := strings.TrimSpace(def.Description); desc != "" {
			tool.OfFunction.Description = openai.String(desc)
		}

		result = append(result, tool)
	}
	return result
}

func convertResponsesAPIResponse(resp *responses.Response) *Response {
	if resp == nil {
		return &Response{
			Message: Message{Role: "assistant"},
		}
	}

	var content strings.Builder
	var toolCalls []ToolCall

	// Extract content and tool calls from output items
	// ResponseOutputItemUnion uses Type field for discrimination
	for _, item := range resp.Output {
		switch item.Type {
		case "message":
			// Extract text from content parts
			for _, part := range item.Content {
				if part.Type == "output_text" {
					content.WriteString(part.Text)
				}
			}
		case "function_call":
			// Extract function call
			toolCalls = append(toolCalls, ToolCall{
				ID:        item.CallID,
				Name:      item.Name,
				Arguments: parseJSONArgs(item.Arguments),
			})
		}
	}

	// Determine stop reason
	stopReason := string(resp.Status)
	if len(toolCalls) > 0 {
		stopReason = "tool_calls"
	}

	return &Response{
		Message: Message{
			Role:      "assistant",
			Content:   content.String(),
			ToolCalls: toolCalls,
		},
		Usage:      convertResponsesUsage(resp.Usage),
		StopReason: stopReason,
	}
}

func convertResponsesUsage(usage responses.ResponseUsage) Usage {
	return Usage{
		InputTokens:  int(usage.InputTokens),
		OutputTokens: int(usage.OutputTokens),
		TotalTokens:  int(usage.TotalTokens),
	}
}
