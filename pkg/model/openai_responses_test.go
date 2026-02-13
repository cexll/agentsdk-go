package model

import (
	"context"
	"errors"
	"net/http"
	"testing"

	"github.com/openai/openai-go"
	"github.com/openai/openai-go/option"
	"github.com/openai/openai-go/packages/ssestream"
	"github.com/openai/openai-go/responses"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockOpenAIResponses implements openaiResponsesService for testing
type mockOpenAIResponses struct {
	newFunc        func(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error)
	streamFunc     func(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion]
	capturedParams responses.ResponseNewParams
}

func (m *mockOpenAIResponses) New(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error) {
	m.capturedParams = body
	if m.newFunc != nil {
		return m.newFunc(ctx, body, opts...)
	}
	return nil, errors.New("mock: New not implemented")
}

func (m *mockOpenAIResponses) NewStreaming(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) *ssestream.Stream[responses.ResponseStreamEventUnion] {
	m.capturedParams = body
	if m.streamFunc != nil {
		return m.streamFunc(ctx, body, opts...)
	}
	return nil
}

func TestNewOpenAIResponses(t *testing.T) {
	tests := []struct {
		name    string
		cfg     OpenAIConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid config",
			cfg: OpenAIConfig{
				APIKey: "sk-test-key",
				Model:  "gpt-4o",
			},
			wantErr: false,
		},
		{
			name: "missing API key",
			cfg: OpenAIConfig{
				Model: "gpt-4o",
			},
			wantErr: true,
			errMsg:  "openai: api key required",
		},
		{
			name: "whitespace API key",
			cfg: OpenAIConfig{
				APIKey: "   ",
			},
			wantErr: true,
			errMsg:  "openai: api key required",
		},
		{
			name: "empty API key",
			cfg: OpenAIConfig{
				APIKey: "",
			},
			wantErr: true,
			errMsg:  "openai: api key required",
		},
		{
			name: "default model when empty",
			cfg: OpenAIConfig{
				APIKey: "sk-test",
			},
			wantErr: false,
		},
		{
			name: "with custom base URL",
			cfg: OpenAIConfig{
				APIKey:  "sk-test",
				BaseURL: "https://custom.api.com",
			},
			wantErr: false,
		},
		{
			name: "with all options",
			cfg: OpenAIConfig{
				APIKey:      "sk-test",
				BaseURL:     "https://custom.api.com",
				Model:       "gpt-4-turbo",
				MaxTokens:   8192,
				MaxRetries:  5,
				System:      "You are a helpful assistant",
				Temperature: func() *float64 { v := 0.7; return &v }(),
				HTTPClient:  &http.Client{},
			},
			wantErr: false,
		},
		{
			name: "default max tokens when zero",
			cfg: OpenAIConfig{
				APIKey:    "sk-test",
				MaxTokens: 0,
			},
			wantErr: false,
		},
		{
			name: "default max retries when zero",
			cfg: OpenAIConfig{
				APIKey:     "sk-test",
				MaxRetries: 0,
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mdl, err := NewOpenAIResponses(tt.cfg)
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
				assert.Nil(t, mdl)
			} else {
				require.NoError(t, err)
				assert.NotNil(t, mdl)
			}
		})
	}
}

func TestBuildResponsesInput(t *testing.T) {
	tests := []struct {
		name        string
		msgs        []Message
		wantContent string
	}{
		{
			name:        "empty messages returns placeholder",
			msgs:        []Message{},
			wantContent: ".",
		},
		{
			name: "single user message",
			msgs: []Message{
				{Role: "user", Content: "Hello"},
			},
			wantContent: "Hello",
		},
		{
			name: "multiple user messages joined",
			msgs: []Message{
				{Role: "user", Content: "First"},
				{Role: "user", Content: "Second"},
			},
			wantContent: "First\n\nSecond",
		},
		{
			name: "system messages ignored",
			msgs: []Message{
				{Role: "system", Content: "Be helpful"},
				{Role: "user", Content: "Hello"},
			},
			wantContent: "Hello",
		},
		{
			name: "assistant messages ignored",
			msgs: []Message{
				{Role: "user", Content: "Hello"},
				{Role: "assistant", Content: "Hi there"},
				{Role: "user", Content: "How are you?"},
			},
			wantContent: "Hello\n\nHow are you?",
		},
		{
			name: "whitespace only content returns placeholder",
			msgs: []Message{
				{Role: "user", Content: "   "},
			},
			wantContent: ".",
		},
		{
			name: "tool messages ignored",
			msgs: []Message{
				{Role: "user", Content: "Call tool"},
				{Role: "tool", Content: "tool result"},
			},
			wantContent: "Call tool",
		},
		{
			name: "mixed case roles",
			msgs: []Message{
				{Role: "USER", Content: "Hello"},
				{Role: "User", Content: "World"},
			},
			wantContent: "Hello\n\nWorld",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildResponsesInput(tt.msgs)
			assert.Equal(t, tt.wantContent, result.OfString.Value)
		})
	}
}

func TestBuildResponsesInput_MultimodalUserMessage(t *testing.T) {
	msgs := []Message{
		{
			Role:    "user",
			Content: "describe this",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockText, Text: "in detail"},
				{Type: ContentBlockImage, MediaType: "image/png", Data: "YWJj"},
			},
		},
	}

	result := buildResponsesInput(msgs)
	require.Len(t, result.OfInputItemList, 1)
	item := result.OfInputItemList[0]
	require.NotNil(t, item.OfMessage)
	require.Equal(t, responses.EasyInputMessageRoleUser, item.OfMessage.Role)

	parts := item.OfMessage.Content.OfInputItemContentList
	require.Len(t, parts, 3)

	require.NotNil(t, parts[0].OfInputText)
	assert.Equal(t, "describe this", parts[0].OfInputText.Text)

	require.NotNil(t, parts[1].OfInputText)
	assert.Equal(t, "in detail", parts[1].OfInputText.Text)

	require.NotNil(t, parts[2].OfInputImage)
	require.True(t, parts[2].OfInputImage.ImageURL.Valid())
	assert.Equal(t, "data:image/png;base64,YWJj", parts[2].OfInputImage.ImageURL.Value)
}

func TestBuildResponsesInput_MultimodalImageURL(t *testing.T) {
	msgs := []Message{
		{
			Role: "user",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockImage, URL: "https://example.com/vision.png"},
			},
		},
	}

	result := buildResponsesInput(msgs)
	require.Len(t, result.OfInputItemList, 1)
	item := result.OfInputItemList[0]
	require.NotNil(t, item.OfMessage)
	parts := item.OfMessage.Content.OfInputItemContentList
	require.Len(t, parts, 1)
	require.NotNil(t, parts[0].OfInputImage)
	require.True(t, parts[0].OfInputImage.ImageURL.Valid())
	assert.Equal(t, "https://example.com/vision.png", parts[0].OfInputImage.ImageURL.Value)
}

func TestBuildResponsesInput_MultimodalToolMessagePreserved(t *testing.T) {
	msgs := []Message{
		{
			Role:    "user",
			Content: "describe this",
			ContentBlocks: []ContentBlock{
				{Type: ContentBlockImage, MediaType: "image/png", Data: "YWJj"},
			},
		},
		{
			Role:    "assistant",
			Content: "I'll call a tool",
		},
		{
			Role:    "tool",
			Content: "tool result data",
		},
		{
			Role:    "user",
			Content: "thanks",
		},
	}

	result := buildResponsesInput(msgs)
	// All four messages should be present: user, assistant, tool (as user), user
	require.Len(t, result.OfInputItemList, 4)

	// First item: user with image
	require.NotNil(t, result.OfInputItemList[0].OfMessage)
	assert.Equal(t, responses.EasyInputMessageRoleUser, result.OfInputItemList[0].OfMessage.Role)

	// Second item: assistant
	require.NotNil(t, result.OfInputItemList[1].OfMessage)
	assert.Equal(t, responses.EasyInputMessageRoleAssistant, result.OfInputItemList[1].OfMessage.Role)

	// Third item: tool mapped to user
	require.NotNil(t, result.OfInputItemList[2].OfMessage)
	assert.Equal(t, responses.EasyInputMessageRoleUser, result.OfInputItemList[2].OfMessage.Role)
	toolParts := result.OfInputItemList[2].OfMessage.Content.OfInputItemContentList
	require.Len(t, toolParts, 1)
	require.NotNil(t, toolParts[0].OfInputText)
	assert.Equal(t, "tool result data", toolParts[0].OfInputText.Text)

	// Fourth item: user
	require.NotNil(t, result.OfInputItemList[3].OfMessage)
	assert.Equal(t, responses.EasyInputMessageRoleUser, result.OfInputItemList[3].OfMessage.Role)
}

func TestConvertToolsToResponsesAPI(t *testing.T) {
	tests := []struct {
		name    string
		tools   []ToolDefinition
		wantLen int
	}{
		{
			name:    "empty tools",
			tools:   []ToolDefinition{},
			wantLen: 0,
		},
		{
			name:    "nil tools",
			tools:   nil,
			wantLen: 0,
		},
		{
			name: "single tool",
			tools: []ToolDefinition{
				{
					Name:        "get_weather",
					Description: "Get weather for a location",
					Parameters: map[string]any{
						"type": "object",
						"properties": map[string]any{
							"location": map[string]any{"type": "string"},
						},
					},
				},
			},
			wantLen: 1,
		},
		{
			name: "skip empty name",
			tools: []ToolDefinition{
				{Name: "", Description: "ignored"},
				{Name: "valid", Description: "kept"},
			},
			wantLen: 1,
		},
		{
			name: "skip whitespace name",
			tools: []ToolDefinition{
				{Name: "   ", Description: "ignored"},
				{Name: "valid", Description: "kept"},
			},
			wantLen: 1,
		},
		{
			name: "multiple tools",
			tools: []ToolDefinition{
				{Name: "tool1", Description: "First tool"},
				{Name: "tool2", Description: "Second tool"},
				{Name: "tool3", Description: "Third tool"},
			},
			wantLen: 3,
		},
		{
			name: "tool without description",
			tools: []ToolDefinition{
				{Name: "simple_tool"},
			},
			wantLen: 1,
		},
		{
			name: "tool with nil parameters",
			tools: []ToolDefinition{
				{Name: "tool", Parameters: nil},
			},
			wantLen: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertToolsToResponsesAPI(tt.tools)
			assert.Len(t, result, tt.wantLen)

			for i, tool := range result {
				require.NotNil(t, tool.OfFunction)
				assert.NotEmpty(t, tool.OfFunction.Name)

				// Find the corresponding input tool
				var inputTool *ToolDefinition
				validIdx := 0
				for j := range tt.tools {
					if tt.tools[j].Name != "" && tt.tools[j].Name != "   " {
						if validIdx == i {
							inputTool = &tt.tools[j]
							break
						}
						validIdx++
					}
				}

				if inputTool != nil && inputTool.Description != "" {
					assert.NotNil(t, tool.OfFunction.Description)
				}
			}
		})
	}
}

func TestConvertResponsesAPIResponse(t *testing.T) {
	tests := []struct {
		name           string
		resp           *responses.Response
		wantRole       string
		wantContent    string
		wantToolCalls  int
		wantStopReason string
	}{
		{
			name:           "nil response",
			resp:           nil,
			wantRole:       "assistant",
			wantContent:    "",
			wantToolCalls:  0,
			wantStopReason: "",
		},
		{
			name: "empty output",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantContent:    "",
			wantToolCalls:  0,
			wantStopReason: "completed",
		},
		{
			name: "text response",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type: "message",
						Content: []responses.ResponseOutputMessageContentUnion{
							{
								Type: "output_text",
								Text: "Hello, world!",
							},
						},
					},
				},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantContent:    "Hello, world!",
			wantToolCalls:  0,
			wantStopReason: "completed",
		},
		{
			name: "function call response",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type:      "function_call",
						CallID:    "call_abc123",
						Name:      "get_weather",
						Arguments: `{"location":"Tokyo"}`,
					},
				},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantContent:    "",
			wantToolCalls:  1,
			wantStopReason: "tool_calls",
		},
		{
			name: "mixed text and function calls",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type: "message",
						Content: []responses.ResponseOutputMessageContentUnion{
							{
								Type: "output_text",
								Text: "Let me check the weather.",
							},
						},
					},
					{
						Type:      "function_call",
						CallID:    "call_123",
						Name:      "get_weather",
						Arguments: `{"location":"Tokyo"}`,
					},
				},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantContent:    "Let me check the weather.",
			wantToolCalls:  1,
			wantStopReason: "tool_calls",
		},
		{
			name: "multiple function calls",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type:      "function_call",
						CallID:    "call_1",
						Name:      "tool1",
						Arguments: `{"a":1}`,
					},
					{
						Type:      "function_call",
						CallID:    "call_2",
						Name:      "tool2",
						Arguments: `{"b":2}`,
					},
				},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantToolCalls:  2,
			wantStopReason: "tool_calls",
		},
		{
			name: "multiple text parts",
			resp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type: "message",
						Content: []responses.ResponseOutputMessageContentUnion{
							{
								Type: "output_text",
								Text: "Part 1",
							},
							{
								Type: "output_text",
								Text: " Part 2",
							},
						},
					},
				},
				Status: "completed",
			},
			wantRole:       "assistant",
			wantContent:    "Part 1 Part 2",
			wantStopReason: "completed",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := convertResponsesAPIResponse(tt.resp)
			require.NotNil(t, resp)
			assert.Equal(t, tt.wantRole, resp.Message.Role)
			assert.Equal(t, tt.wantContent, resp.Message.Content)
			assert.Len(t, resp.Message.ToolCalls, tt.wantToolCalls)
			assert.Equal(t, tt.wantStopReason, resp.StopReason)

			// Verify tool call details for function_call tests
			if tt.wantToolCalls > 0 {
				for _, tc := range resp.Message.ToolCalls {
					assert.NotEmpty(t, tc.ID)
					assert.NotEmpty(t, tc.Name)
				}
			}
		})
	}
}

func TestConvertResponsesUsage(t *testing.T) {
	tests := []struct {
		name  string
		usage responses.ResponseUsage
		want  Usage
	}{
		{
			name: "standard usage",
			usage: responses.ResponseUsage{
				InputTokens:  100,
				OutputTokens: 50,
				TotalTokens:  150,
			},
			want: Usage{
				InputTokens:  100,
				OutputTokens: 50,
				TotalTokens:  150,
			},
		},
		{
			name:  "zero usage",
			usage: responses.ResponseUsage{},
			want: Usage{
				InputTokens:  0,
				OutputTokens: 0,
				TotalTokens:  0,
			},
		},
		{
			name: "large values",
			usage: responses.ResponseUsage{
				InputTokens:  100000,
				OutputTokens: 50000,
				TotalTokens:  150000,
			},
			want: Usage{
				InputTokens:  100000,
				OutputTokens: 50000,
				TotalTokens:  150000,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := convertResponsesUsage(tt.usage)
			assert.Equal(t, tt.want.InputTokens, result.InputTokens)
			assert.Equal(t, tt.want.OutputTokens, result.OutputTokens)
			assert.Equal(t, tt.want.TotalTokens, result.TotalTokens)
		})
	}
}

func TestOpenAIResponsesModel_Complete(t *testing.T) {
	tests := []struct {
		name        string
		request     Request
		mockResp    *responses.Response
		mockErr     error
		wantErr     bool
		wantRole    string
		wantContent string
	}{
		{
			name: "simple completion",
			request: Request{
				Messages: []Message{
					{Role: "user", Content: "Hello"},
				},
			},
			mockResp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type: "message",
						Content: []responses.ResponseOutputMessageContentUnion{
							{
								Type: "output_text",
								Text: "Hello! How can I help?",
							},
						},
					},
				},
				Status: "completed",
				Usage: responses.ResponseUsage{
					InputTokens:  10,
					OutputTokens: 20,
					TotalTokens:  30,
				},
			},
			wantRole:    "assistant",
			wantContent: "Hello! How can I help?",
		},
		{
			name: "completion with tool calls",
			request: Request{
				Messages: []Message{
					{Role: "user", Content: "What's the weather?"},
				},
				Tools: []ToolDefinition{
					{Name: "get_weather", Description: "Get weather"},
				},
			},
			mockResp: &responses.Response{
				Output: []responses.ResponseOutputItemUnion{
					{
						Type:      "function_call",
						CallID:    "call_abc123",
						Name:      "get_weather",
						Arguments: `{"location":"Tokyo"}`,
					},
				},
				Status: "completed",
			},
			wantRole: "assistant",
		},
		{
			name: "API error",
			request: Request{
				Messages: []Message{
					{Role: "user", Content: "test"},
				},
			},
			mockErr: &openai.Error{
				StatusCode: http.StatusUnauthorized,
				Message:    "Invalid API key",
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockOpenAIResponses{
				newFunc: func(ctx context.Context, body responses.ResponseNewParams, opts ...option.RequestOption) (*responses.Response, error) {
					return tt.mockResp, tt.mockErr
				},
			}

			mdl := &openaiResponsesModel{
				responses:  mock,
				model:      "gpt-4o",
				maxTokens:  4096,
				maxRetries: 0, // No retries for testing
			}

			resp, err := mdl.Complete(context.Background(), tt.request)

			if tt.wantErr {
				require.Error(t, err)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, resp)
			assert.Equal(t, tt.wantRole, resp.Message.Role)
			if tt.wantContent != "" {
				assert.Equal(t, tt.wantContent, resp.Message.Content)
			}

			if tt.name == "completion with tool calls" {
				require.Len(t, resp.Message.ToolCalls, 1)
				assert.Equal(t, "call_abc123", resp.Message.ToolCalls[0].ID)
				assert.Equal(t, "get_weather", resp.Message.ToolCalls[0].Name)
				assert.Equal(t, "Tokyo", resp.Message.ToolCalls[0].Arguments["location"])
			}
		})
	}
}

func TestOpenAIResponsesModel_CompleteStream_NilCallback(t *testing.T) {
	mdl := &openaiResponsesModel{
		model:      "gpt-4o",
		maxTokens:  4096,
		maxRetries: 0,
	}

	err := mdl.CompleteStream(context.Background(), Request{}, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "stream callback required")
}

func TestOpenAIResponsesModel_SelectModel(t *testing.T) {
	mdl := &openaiResponsesModel{model: "gpt-4o"}

	t.Run("uses override when provided", func(t *testing.T) {
		result := mdl.selectModel("gpt-4-turbo")
		assert.Equal(t, "gpt-4-turbo", result)
	})

	t.Run("uses default when empty override", func(t *testing.T) {
		result := mdl.selectModel("")
		assert.Equal(t, "gpt-4o", result)
	})

	t.Run("trims whitespace", func(t *testing.T) {
		result := mdl.selectModel("  gpt-4  ")
		assert.Equal(t, "gpt-4", result)
	})
}

func TestResponsesToolCallAccumulator(t *testing.T) {
	t.Run("complete tool call with callID", func(t *testing.T) {
		acc := &responsesToolCallAccumulator{
			id:     "item_123",
			callID: "call_123",
			name:   "my_tool",
		}
		acc.arguments.WriteString(`{"key":"value"}`)

		tc := acc.toToolCall()
		require.NotNil(t, tc)
		assert.Equal(t, "call_123", tc.ID) // Uses callID when present
		assert.Equal(t, "my_tool", tc.Name)
		assert.Equal(t, "value", tc.Arguments["key"])
	})

	t.Run("falls back to id when no callID", func(t *testing.T) {
		acc := &responsesToolCallAccumulator{
			id:   "item_456",
			name: "tool",
		}

		tc := acc.toToolCall()
		require.NotNil(t, tc)
		assert.Equal(t, "item_456", tc.ID)
	})

	t.Run("missing name returns nil", func(t *testing.T) {
		acc := &responsesToolCallAccumulator{id: "call_1", callID: "call_1"}
		assert.Nil(t, acc.toToolCall())
	})

	t.Run("empty arguments", func(t *testing.T) {
		acc := &responsesToolCallAccumulator{
			id:   "call_1",
			name: "tool",
		}

		tc := acc.toToolCall()
		require.NotNil(t, tc)
		assert.Nil(t, tc.Arguments)
	})
}

func TestOpenAIResponsesModel_BuildResponsesParams(t *testing.T) {
	t.Run("basic params", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
		}

		params := mdl.buildResponsesParams(req)
		assert.Equal(t, "gpt-4o", string(params.Model))
		assert.Equal(t, int64(4096), params.MaxOutputTokens.Value)
	})

	t.Run("request overrides max tokens", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
		}

		req := Request{
			Messages:  []Message{{Role: "user", Content: "Hello"}},
			MaxTokens: 1000,
		}

		params := mdl.buildResponsesParams(req)
		assert.Equal(t, int64(1000), params.MaxOutputTokens.Value)
	})

	t.Run("model system instructions", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
			system:    "Be helpful",
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
		}

		params := mdl.buildResponsesParams(req)
		require.True(t, params.Instructions.Valid())
		assert.Equal(t, "Be helpful", params.Instructions.Value)
	})

	t.Run("request overrides system instructions", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
			system:    "Be helpful",
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
			System:   "Be concise",
		}

		params := mdl.buildResponsesParams(req)
		require.True(t, params.Instructions.Valid())
		assert.Equal(t, "Be concise", params.Instructions.Value)
	})

	t.Run("with tools", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
			Tools: []ToolDefinition{
				{Name: "tool1"},
				{Name: "tool2"},
			},
		}

		params := mdl.buildResponsesParams(req)
		assert.Len(t, params.Tools, 2)
	})

	t.Run("model temperature", func(t *testing.T) {
		temp := 0.7
		mdl := &openaiResponsesModel{
			model:       "gpt-4o",
			maxTokens:   4096,
			temperature: &temp,
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
		}

		params := mdl.buildResponsesParams(req)
		require.True(t, params.Temperature.Valid())
		assert.Equal(t, 0.7, params.Temperature.Value)
	})

	t.Run("request overrides temperature", func(t *testing.T) {
		modelTemp := 0.7
		reqTemp := 0.3
		mdl := &openaiResponsesModel{
			model:       "gpt-4o",
			maxTokens:   4096,
			temperature: &modelTemp,
		}

		req := Request{
			Messages:    []Message{{Role: "user", Content: "Hello"}},
			Temperature: &reqTemp,
		}

		params := mdl.buildResponsesParams(req)
		require.True(t, params.Temperature.Valid())
		assert.Equal(t, 0.3, params.Temperature.Value)
	})

	t.Run("with session ID", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
		}

		req := Request{
			Messages:  []Message{{Role: "user", Content: "Hello"}},
			SessionID: "session-123",
		}

		params := mdl.buildResponsesParams(req)
		require.True(t, params.User.Valid())
		assert.Equal(t, "session-123", params.User.Value)
	})

	t.Run("model override in request", func(t *testing.T) {
		mdl := &openaiResponsesModel{
			model:     "gpt-4o",
			maxTokens: 4096,
		}

		req := Request{
			Messages: []Message{{Role: "user", Content: "Hello"}},
			Model:    "gpt-4-turbo",
		}

		params := mdl.buildResponsesParams(req)
		assert.Equal(t, "gpt-4-turbo", string(params.Model))
	})
}
