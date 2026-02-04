package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
)

func TestAnthropicRequestOptionsEnvAndEmpty(t *testing.T) {
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED", "")
	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	m := &anthropicModel{}
	if opts := m.requestOptions(); len(opts) == 0 {
		t.Fatalf("expected env api key options")
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth-token")
	if opts := m.requestOptions(); len(opts) == 0 {
		t.Fatalf("expected auth token options")
	}

	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	m = &anthropicModel{}
	if opts := m.requestOptions(); opts != nil {
		t.Fatalf("expected nil options when no headers")
	}
}

func TestNewAnthropicWithOptions(t *testing.T) {
	client := &http.Client{}
	model, err := NewAnthropic(AnthropicConfig{
		APIKey:     "key",
		BaseURL:    "https://example.com",
		HTTPClient: client,
		MaxTokens:  1,
		MaxRetries: 1,
	})
	if err != nil || model == nil {
		t.Fatalf("expected model, got %v", err)
	}
}

func TestAnthropicCompleteBuildParamsError(t *testing.T) {
	msgs := &fakeMessages{newMsg: &anthropicsdk.Message{}}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolDefinition{{Name: "bad", Parameters: map[string]any{"bad": make(chan int)}}},
	}
	if _, err := m.Complete(context.Background(), req); err == nil {
		t.Fatalf("expected build params error")
	}
}

func TestAnthropicCompleteNewError(t *testing.T) {
	msgs := &fakeMessages{newErr: errors.New("boom")}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	if _, err := m.Complete(context.Background(), req); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected new error, got %v", err)
	}
}

func TestAnthropicCompleteStreamNilCallback(t *testing.T) {
	msgs := &fakeMessages{}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	if err := m.CompleteStream(context.Background(), Request{}, nil); err == nil {
		t.Fatalf("expected callback error")
	}
}

func TestAnthropicCompleteStreamBuildParamsError(t *testing.T) {
	msgs := &fakeMessages{stream: buildStream(t, []string{`{"type":"message_start","message":{}}`})}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{
		Messages: []Message{{Role: "user", Content: "hi"}},
		Tools:    []ToolDefinition{{Name: "bad", Parameters: map[string]any{"bad": make(chan int)}}},
	}
	if err := m.CompleteStream(context.Background(), req, func(StreamResult) error { return nil }); err == nil {
		t.Fatalf("expected build params error")
	}
}

func TestAnthropicCompleteStreamNilStream(t *testing.T) {
	msgs := &fakeMessages{stream: nil}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := m.CompleteStream(context.Background(), req, func(StreamResult) error { return nil }); err == nil {
		t.Fatalf("expected nil stream error")
	}
}

func TestAnthropicCompleteStreamAccumulateError(t *testing.T) {
	events := []string{
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
	}
	msgs := &fakeMessages{stream: buildStream(t, events)}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := m.CompleteStream(context.Background(), req, func(StreamResult) error { return nil }); err == nil {
		t.Fatalf("expected accumulate error")
	}
}

func TestAnthropicCompleteStreamStreamErr(t *testing.T) {
	stream := ssestream.NewStream[anthropicsdk.MessageStreamEventUnion](&fakeDecoder{err: errors.New("stream")}, nil)
	msgs := &fakeMessages{stream: stream}
	m := &anthropicModel{msgs: msgs, model: mapModelName(""), maxTokens: 1, configuredAPIKey: "key"}
	req := Request{Messages: []Message{{Role: "user", Content: "hi"}}}
	if err := m.CompleteStream(context.Background(), req, func(StreamResult) error { return nil }); err == nil || !strings.Contains(err.Error(), "stream") {
		t.Fatalf("expected stream error, got %v", err)
	}
}

func TestConvertMessagesCoverage(t *testing.T) {
	msgs := []Message{
		{Role: "system", Content: "sys"},
		{Role: "system", Content: " "},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "", Name: "t"}}},
		{Role: "tool", Content: "tool", ToolCalls: []ToolCall{{ID: "", Result: "err"}}},
		{Role: "user", Content: ""},
	}
	system, params, err := convertMessages(msgs, true, "base")
	if err != nil || len(system) == 0 || len(params) == 0 {
		t.Fatalf("unexpected convert result %v %v", system, err)
	}
}

func TestBuildAssistantAndToolResultsCoverage(t *testing.T) {
	blocks := buildAssistantContent(Message{Content: "", ToolCalls: []ToolCall{{ID: "", Name: ""}}})
	if len(blocks) == 0 {
		t.Fatalf("expected fallback block")
	}
	blocks = buildToolResults(Message{Content: "fallback", ToolCalls: []ToolCall{{ID: "", Result: ""}}})
	if len(blocks) == 0 {
		t.Fatalf("expected fallback tool result")
	}
}

func TestToolResultIsErrorVariants(t *testing.T) {
	if toolResultIsError(`{"error":false}`) {
		t.Fatalf("expected false")
	}
	if !toolResultIsError(`{"error":1}`) {
		t.Fatalf("expected true for non-nil error")
	}
	if toolResultIsError(`{"error":}`) {
		t.Fatalf("expected json parse false")
	}
}

func TestEncodeSchemaDefaults(t *testing.T) {
	schema, err := encodeSchema(map[string]any{})
	if err != nil || schema.Type != "object" {
		t.Fatalf("expected object schema, got %v err %v", schema, err)
	}
	schema, err = encodeSchema(map[string]any{"properties": map[string]any{}})
	if err != nil || schema.Type != "object" {
		t.Fatalf("expected default type, got %v err %v", schema, err)
	}
}

func TestConvertToolsAndDecodeJSONCoverage(t *testing.T) {
	tools, err := convertTools([]ToolDefinition{{Name: ""}, {Name: "t", Parameters: map[string]any{}}})
	if err != nil || len(tools) != 1 {
		t.Fatalf("unexpected tools %v err %v", tools, err)
	}
	if got := decodeJSON(json.RawMessage(`not json`)); got["raw"] == "" {
		t.Fatalf("expected raw payload")
	}
	if got := decodeJSON(json.RawMessage(`123`)); got["value"] == nil {
		t.Fatalf("expected scalar value")
	}
}

func TestToolCallFromBlockInvalid(t *testing.T) {
	if toolCallFromBlock(anthropicsdk.ContentBlockUnion{Type: "text"}) != nil {
		t.Fatalf("expected nil for non tool_use")
	}
	if toolCallFromBlock(anthropicsdk.ContentBlockUnion{Type: "tool_use", ID: " ", Name: "x"}) != nil {
		t.Fatalf("expected nil for empty id")
	}
	if toolCallFromBlock(anthropicsdk.ContentBlockUnion{Type: "tool_use", ID: "id", Name: " "}) != nil {
		t.Fatalf("expected nil for empty name")
	}
	if extractToolCall(anthropicsdk.Message{}) != nil {
		t.Fatalf("expected nil for empty content")
	}
}
