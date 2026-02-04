package model

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

type fakeDecoder struct {
	events []ssestream.Event
	idx    int
	err    error
}

func (d *fakeDecoder) Next() bool {
	if d.idx >= len(d.events) {
		return false
	}
	d.idx++
	return true
}

func (d *fakeDecoder) Event() ssestream.Event {
	if d.idx == 0 || d.idx > len(d.events) {
		return ssestream.Event{}
	}
	return d.events[d.idx-1]
}

func (d *fakeDecoder) Close() error { return nil }
func (d *fakeDecoder) Err() error   { return d.err }

type fakeMessages struct {
	newParams   anthropicsdk.MessageNewParams
	countParams anthropicsdk.MessageCountTokensParams
	newMsg      *anthropicsdk.Message
	newErr      error
	stream      *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	countResp   *anthropicsdk.MessageTokensCount
	countErr    error
}

func (f *fakeMessages) New(ctx context.Context, params anthropicsdk.MessageNewParams, _ ...option.RequestOption) (*anthropicsdk.Message, error) {
	f.newParams = params
	return f.newMsg, f.newErr
}

func (f *fakeMessages) NewStreaming(ctx context.Context, params anthropicsdk.MessageNewParams, _ ...option.RequestOption) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
	f.newParams = params
	return f.stream
}

func (f *fakeMessages) CountTokens(ctx context.Context, params anthropicsdk.MessageCountTokensParams, _ ...option.RequestOption) (*anthropicsdk.MessageTokensCount, error) {
	f.countParams = params
	return f.countResp, f.countErr
}

func buildStream(t *testing.T, raw []string) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
	t.Helper()
	events := make([]ssestream.Event, 0, len(raw))
	for _, item := range raw {
		var meta struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(item), &meta); err != nil {
			t.Fatalf("parse event: %v", err)
		}
		events = append(events, ssestream.Event{Type: meta.Type, Data: []byte(item)})
	}
	return ssestream.NewStream[anthropicsdk.MessageStreamEventUnion](&fakeDecoder{events: events}, nil)
}

func TestAnthropicCompleteWithStubMessages(t *testing.T) {
	msg := anthropicsdk.Message{
		Role: constant.Assistant("assistant"),
		Content: []anthropicsdk.ContentBlockUnion{
			{Type: "text", Text: "hello"},
			{Type: "tool_use", ID: "toolu_1", Name: "calc", Input: json.RawMessage(`{"a":1}`)},
		},
		Usage:      anthropicsdk.Usage{InputTokens: 2, OutputTokens: 3},
		StopReason: anthropicsdk.StopReason("end_turn"),
	}
	msgs := &fakeMessages{newMsg: &msg}
	m := &anthropicModel{
		msgs:             msgs,
		model:            mapModelName(""),
		maxTokens:        16,
		configuredAPIKey: "key",
	}

	req := Request{
		Messages:  []Message{{Role: "user", Content: "hi"}},
		Tools:     []ToolDefinition{{Name: "calc", Parameters: map[string]any{"type": "object"}}},
		SessionID: "sess",
	}
	resp, err := m.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if resp.Message.Content != "hello" {
		t.Fatalf("unexpected content %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 || resp.Message.ToolCalls[0].Name != "calc" {
		t.Fatalf("unexpected tool calls %+v", resp.Message.ToolCalls)
	}
	if resp.Usage.TotalTokens == 0 {
		t.Fatalf("expected usage to be populated")
	}
	if msgs.newParams.MaxTokens == 0 {
		t.Fatalf("expected params populated")
	}
}

func TestAnthropicCompleteStreamWithStubMessages(t *testing.T) {
	events := []string{
		`{"type":"message_start","message":{}}`,
		`{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		`{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}`,
		`{"type":"content_block_stop","index":0}`,
		`{"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"calc","input":{"a":1}}}`,
		`{"type":"content_block_stop","index":1}`,
		`{"type":"message_delta","usage":{"input_tokens":3,"output_tokens":2,"cache_creation_input_tokens":1,"cache_read_input_tokens":1}}`,
		`{"type":"message_stop"}`,
	}
	msgs := &fakeMessages{
		stream:    buildStream(t, events),
		countResp: &anthropicsdk.MessageTokensCount{InputTokens: 3},
	}
	m := &anthropicModel{
		msgs:             msgs,
		model:            mapModelName(""),
		maxTokens:        16,
		system:           "sys",
		configuredAPIKey: "key",
	}
	req := Request{
		Messages:          []Message{{Role: "user", Content: "hello"}},
		Tools:             []ToolDefinition{{Name: "calc", Description: "tool", Parameters: map[string]any{"type": "object"}}},
		EnablePromptCache: true,
		System:            "sys2",
		SessionID:         "sess",
	}

	var deltas []string
	var toolCalls []ToolCall
	var final *Response
	err := m.CompleteStream(context.Background(), req, func(res StreamResult) error {
		if res.Delta != "" {
			deltas = append(deltas, res.Delta)
		}
		if res.ToolCall != nil {
			toolCalls = append(toolCalls, *res.ToolCall)
		}
		if res.Final {
			final = res.Response
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream: %v", err)
	}
	if len(deltas) == 0 || deltas[0] != "hi" {
		t.Fatalf("unexpected deltas %v", deltas)
	}
	if len(toolCalls) != 1 || toolCalls[0].Name != "calc" {
		t.Fatalf("unexpected tool calls %v", toolCalls)
	}
	if final == nil || final.Usage.TotalTokens == 0 {
		t.Fatalf("expected final response with usage")
	}
}

func TestAnthropicHelpers(t *testing.T) {
	if !toolResultIsError(`{"error":true}`) {
		t.Fatalf("expected error=true")
	}
	if toolResultIsError(`{"error":""}`) {
		t.Fatalf("expected empty error to be false")
	}
	if !toolResultIsError(`{"error":"boom"}`) {
		t.Fatalf("expected error string to be true")
	}
	if toolResultIsError("nope") {
		t.Fatalf("expected non-json to be false")
	}

	if _, err := encodeSchema(map[string]any{"bad": make(chan int)}); err == nil {
		t.Fatalf("expected encode error")
	}
	schema, err := encodeSchema(map[string]any{"type": "object"})
	if err != nil || schema.Type == "" {
		t.Fatalf("expected schema type set, err=%v", err)
	}
	empty, err := encodeSchema(nil)
	if err != nil || empty.Type != "object" {
		t.Fatalf("expected empty schema default, got %+v err=%v", empty, err)
	}

	original := map[string]any{"a": []any{map[string]any{"b": 1}}}
	cloned := cloneValue(original).(map[string]any)
	original["a"].([]any)[0].(map[string]any)["b"] = 2
	if cloned["a"].([]any)[0].(map[string]any)["b"].(int) == 2 {
		t.Fatalf("expected deep clone")
	}

	usage := usageFromFallback(anthropicsdk.Usage{InputTokens: 1, OutputTokens: 2}, Usage{})
	if usage.TotalTokens == 0 {
		t.Fatalf("expected fallback usage")
	}
	tracked := usageFromFallback(anthropicsdk.Usage{}, Usage{InputTokens: 1, OutputTokens: 1})
	if tracked.TotalTokens != 2 {
		t.Fatalf("expected tracked total tokens")
	}

	if mapModelName("unknown") == "" {
		t.Fatalf("expected default model mapping")
	}
	if mapModelName(string(supportedAnthropicModels[0])) != supportedAnthropicModels[0] {
		t.Fatalf("expected known model mapping")
	}

	if !isRetryable(netErr{timeout: true}) {
		t.Fatalf("expected timeout retryable")
	}
	if !isRetryable(netErr{temporary: true}) {
		t.Fatalf("expected temporary retryable")
	}
	if isRetryable(context.Canceled) {
		t.Fatalf("expected canceled to be non-retryable")
	}
	if isRetryable(&anthropicsdk.Error{StatusCode: http.StatusUnauthorized}) {
		t.Fatalf("expected unauthorized to be non-retryable")
	}

	systemBlocks, messages, err := convertMessages([]Message{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "u1"},
		{Role: "assistant", Content: "a1", ToolCalls: []ToolCall{{ID: "toolu_2", Name: "t", Arguments: map[string]any{"x": "y"}}}},
		{Role: "tool", Content: "ok", ToolCalls: []ToolCall{{ID: "toolu_2", Result: `{"error":true}`}}},
	}, true, "defaults")
	if err != nil || len(systemBlocks) == 0 || len(messages) == 0 {
		t.Fatalf("convert messages failed: %v", err)
	}
	cached := false
	for _, msg := range messages {
		if msg.Role == anthropicsdk.MessageParamRoleUser {
			for _, block := range msg.Content {
				if block.OfText != nil && block.OfText.CacheControl.Type != "" {
					cached = true
				}
			}
		}
	}
	if !cached {
		t.Fatalf("expected cache control to be set")
	}

	tools, err := convertTools([]ToolDefinition{{Name: "demo", Description: "desc", Parameters: map[string]any{"type": "object"}}})
	if err != nil || len(tools) != 1 {
		t.Fatalf("convert tools failed: %v", err)
	}
}

func TestDoWithRetry(t *testing.T) {
	model := &anthropicModel{maxRetries: 2}
	attempts := 0
	if err := model.doWithRetry(context.Background(), func(context.Context) error {
		attempts++
		if attempts < 2 {
			return errors.New("retry")
		}
		return nil
	}); err != nil {
		t.Fatalf("expected retry success, got %v", err)
	}
	if attempts != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	attempts = 0
	if err := model.doWithRetry(ctx, func(context.Context) error {
		attempts++
		return context.Canceled
	}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context canceled, got %v", err)
	}
	if attempts != 1 {
		t.Fatalf("expected single attempt on canceled context, got %d", attempts)
	}

	modelZero := &anthropicModel{maxRetries: 0}
	attempts = 0
	if err := modelZero.doWithRetry(context.Background(), func(context.Context) error {
		attempts++
		return errors.New("fail")
	}); err == nil {
		t.Fatalf("expected error with zero retries")
	}
	if attempts != 1 {
		t.Fatalf("expected 1 attempt, got %d", attempts)
	}
}

type netErr struct {
	timeout   bool
	temporary bool
}

func (n netErr) Error() string   { return "net" }
func (n netErr) Timeout() bool   { return n.timeout }
func (n netErr) Temporary() bool { return n.temporary }

func TestAnthropicHeaderHelpers(t *testing.T) {
	t.Setenv("ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	headers := newAnthropicHeaders(map[string]string{"X-Test": "v", "x-api-key": "bad"}, map[string]string{"X-Other": "o"})
	if headers["x-test"] != "v" || headers["x-other"] != "o" {
		t.Fatalf("unexpected headers %v", headers)
	}
	if _, ok := headers["x-api-key"]; ok {
		t.Fatalf("x-api-key should not be included")
	}

	t.Setenv("ANTHROPIC_API_KEY", "env-key")
	m := &anthropicModel{configuredAPIKey: ""}
	if opts := m.requestOptions(); len(opts) == 0 {
		t.Fatalf("expected request options with env key")
	}

	m = &anthropicModel{configuredAPIKey: "cfg-key"}
	if opts := m.requestOptions(); len(opts) == 0 {
		t.Fatalf("expected request options with configured key")
	}
}

func TestAnthropicSelectModelAndExtractToolCall(t *testing.T) {
	m := &anthropicModel{model: mapModelName("claude-3-5-haiku-20241022")}
	if got := m.selectModel(" "); got != m.model {
		t.Fatalf("expected default model")
	}
	if got := m.selectModel("claude-3-5-haiku-20241022"); string(got) == "" {
		t.Fatalf("expected mapped model")
	}

	msg := anthropicsdk.Message{
		Content: []anthropicsdk.ContentBlockUnion{
			{Type: "text", Text: "hi"},
			{Type: "tool_use", ID: "id1", Name: "tool", Input: json.RawMessage(`{"a":1}`)},
		},
	}
	call := extractToolCall(msg)
	if call == nil || call.Name != "tool" {
		t.Fatalf("expected tool call")
	}
	msg.Content = nil
	if call := extractToolCall(msg); call != nil {
		t.Fatalf("expected nil tool call")
	}
}

func TestBuildToolResultsFallbacks(t *testing.T) {
	msg := Message{
		Content: "fallback",
		ToolCalls: []ToolCall{
			{ID: "id1", Result: ""},
			{ID: "id2", Result: "ok"},
			{ID: "", Result: "skip"},
		},
	}
	blocks := buildToolResults(msg)
	if len(blocks) == 0 {
		t.Fatalf("expected tool result blocks")
	}

	msg.ToolCalls = nil
	blocks = buildToolResults(msg)
	if len(blocks) != 1 {
		t.Fatalf("expected single block fallback")
	}
}
