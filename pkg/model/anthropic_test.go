package model

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"reflect"
	"testing"
	"time"

	anthropicsdk "github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/anthropics/anthropic-sdk-go/packages/ssestream"
	"github.com/anthropics/anthropic-sdk-go/shared/constant"
)

func TestCompleteBuildsRequestAndParsesToolUse(t *testing.T) {
	var seen anthropicsdk.MessageNewParams
	mock := &fakeMessages{
		newFn: func(ctx context.Context, params anthropicsdk.MessageNewParams) (*anthropicsdk.Message, error) {
			seen = params
			msg := anthropicsdk.Message{
				Role: constant.Assistant("assistant"),
				Content: []anthropicsdk.ContentBlockUnion{
					{Type: "text", Text: "done"},
					{Type: "tool_use", ID: "call-1", Name: "search", Input: json.RawMessage(`{"q":"go"}`)},
				},
				Usage: anthropicsdk.Usage{InputTokens: 10, OutputTokens: 3},
			}
			msg.StopReason = "end_turn"
			return &msg, nil
		},
	}

	m := &anthropicModel{
		msgs:       mock,
		model:      anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // use deprecated constant in test
		maxTokens:  256,
		maxRetries: 0,
		system:     "base-system",
	}

	req := Request{
		System: "inline-system",
		Messages: []Message{
			{Role: "system", Content: "extra"},
			{Role: "user", Content: "hello"},
			{Role: "assistant", ToolCalls: []ToolCall{{ID: "call-1", Name: "search", Arguments: map[string]any{"q": "go"}}}},
			{Role: "tool", Content: `{"ok":true}`, ToolCalls: []ToolCall{{ID: "call-1"}}},
		},
		Tools: []ToolDefinition{{
			Name:        "search",
			Description: "desc",
			Parameters:  map[string]any{"type": "object", "properties": map[string]any{"q": map[string]any{"type": "string"}}},
		}},
		MaxTokens: 64,
	}

	resp, err := m.Complete(context.Background(), req)
	if err != nil {
		t.Fatalf("complete returned error: %v", err)
	}

	if got := int(seen.MaxTokens); got != 64 {
		t.Fatalf("max tokens mismatch: %d", got)
	}
	if len(seen.System) != 3 { // base-system + inline-system + inline role system
		t.Fatalf("expected 3 system blocks, got %d", len(seen.System))
	}
	if len(seen.Messages) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(seen.Messages))
	}
	if len(seen.Tools) != 1 || seen.Tools[0].OfTool == nil || seen.Tools[0].OfTool.Name != "search" {
		t.Fatalf("tool conversion failed: %+v", seen.Tools)
	}

	if resp.Message.Content != "done" {
		t.Fatalf("content mismatch: %q", resp.Message.Content)
	}
	if len(resp.Message.ToolCalls) != 1 {
		t.Fatalf("expected tool call parsed")
	}
	if resp.Message.ToolCalls[0].Name != "search" || resp.Message.ToolCalls[0].Arguments["q"] != "go" {
		t.Fatalf("tool call parsing wrong: %+v", resp.Message.ToolCalls[0])
	}
	if resp.Usage.InputTokens != 10 || resp.Usage.OutputTokens != 3 || resp.Usage.TotalTokens != 13 {
		t.Fatalf("usage mismatch: %+v", resp.Usage)
	}
	if resp.StopReason != "end_turn" {
		t.Fatalf("stop reason mismatch: %q", resp.StopReason)
	}
}

func TestAnthropic_DefaultMaxRetries(t *testing.T) {
	cases := []struct {
		name       string
		maxRetries int
	}{
		{name: "zero uses default", maxRetries: 0},
		{name: "negative uses default", maxRetries: -3},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			mdl, err := NewAnthropic(AnthropicConfig{APIKey: "k", MaxRetries: tc.maxRetries})
			if err != nil {
				t.Fatalf("new anthropic model: %v", err)
			}
			am, ok := mdl.(*anthropicModel)
			if !ok {
				t.Fatalf("expected anthropicModel, got %T", mdl)
			}
			if am.maxRetries != 10 {
				t.Fatalf("expected default maxRetries=10, got %d", am.maxRetries)
			}
		})
	}
}

func TestRetryOnTransientError(t *testing.T) {
	calls := 0
	mock := &fakeMessages{
		newFn: func(ctx context.Context, params anthropicsdk.MessageNewParams) (*anthropicsdk.Message, error) {
			calls++
			if calls == 1 {
				return nil, tempNetErr{}
			}
			msg := anthropicsdk.Message{Role: constant.Assistant("assistant"), Content: []anthropicsdk.ContentBlockUnion{{Type: "text", Text: "ok"}}}
			return &msg, nil
		},
	}
	m := &anthropicModel{
		msgs:       mock,
		model:      anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // use deprecated constant in test
		maxTokens:  32,
		maxRetries: 1,
	}
	resp, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}})
	if err != nil {
		t.Fatalf("expected retry to succeed, got %v", err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 attempts, got %d", calls)
	}
	if resp.Message.Content != "ok" {
		t.Fatalf("unexpected content: %q", resp.Message.Content)
	}
}

func TestAnthropic_NetworkErrorRetry(t *testing.T) {
	cases := []struct {
		name        string
		maxRetries  int
		failures    int
		expectCalls int
		expectErr   bool
	}{
		{name: "retries transient network error", maxRetries: 2, failures: 1, expectCalls: 2, expectErr: false},
		{name: "stops after max retries", maxRetries: 2, failures: 3, expectCalls: 3, expectErr: true},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			model := &anthropicModel{maxRetries: tc.maxRetries}
			runErr := model.doWithRetry(context.Background(), func(context.Context) error {
				calls++
				if calls <= tc.failures {
					return tempNetErr{}
				}
				return nil
			})

			if tc.expectErr {
				var netErr net.Error
				if runErr == nil || !errors.As(runErr, &netErr) {
					t.Fatalf("expected network error, got %v", runErr)
				}
			} else if runErr != nil {
				t.Fatalf("expected retry to succeed, got %v", runErr)
			}
			if calls != tc.expectCalls {
				t.Fatalf("expected %d attempts, got %d", tc.expectCalls, calls)
			}
		})
	}
}

func TestAnthropic_400ErrorRetry(t *testing.T) {
	const maxRetries = 3
	errBadRequest := &anthropicsdk.Error{StatusCode: http.StatusBadRequest}

	cases := []struct {
		name string
	}{
		{name: "bad request is retried"},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			model := &anthropicModel{maxRetries: maxRetries}
			err := model.doWithRetry(context.Background(), func(context.Context) error {
				calls++
				return errBadRequest
			})
			if !errors.Is(err, errBadRequest) {
				t.Fatalf("expected final error %v, got %v", errBadRequest, err)
			}
			if retries := calls - 1; retries != maxRetries {
				t.Fatalf("expected %d retries (calls=%d)", maxRetries, calls)
			}
		})
	}
}

func TestAnthropic_401NoRetry(t *testing.T) {
	cases := []struct {
		name       string
		maxRetries int
	}{
		{name: "unauthorized stops immediately", maxRetries: 5},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			unauthorized := &anthropicsdk.Error{StatusCode: http.StatusUnauthorized}
			model := &anthropicModel{maxRetries: tc.maxRetries}
			err := model.doWithRetry(context.Background(), func(context.Context) error {
				calls++
				return unauthorized
			})
			if !errors.Is(err, unauthorized) {
				t.Fatalf("expected unauthorized error, got %v", err)
			}
			if calls != 1 {
				t.Fatalf("expected no retries, got %d calls", calls)
			}
		})
	}
}

func TestStreamDeltasAndToolUse(t *testing.T) {
	events := []ssestream.Event{
		mkEvent(anthropicsdk.MessageStartEvent{
			Type:    constant.MessageStart("message_start"),
			Message: anthropicsdk.Message{Role: constant.Assistant("assistant")},
		}),
		mkEvent(anthropicsdk.ContentBlockStartEvent{
			Type:  constant.ContentBlockStart("content_block_start"),
			Index: 0,
			ContentBlock: anthropicsdk.ContentBlockStartEventContentBlockUnion{
				Type: "text",
				Text: "",
			},
		}),
		mkEvent(anthropicsdk.ContentBlockDeltaEvent{
			Type:  constant.ContentBlockDelta("content_block_delta"),
			Index: 0,
			Delta: anthropicsdk.RawContentBlockDeltaUnion{Type: "text_delta", Text: "hel"},
		}),
		mkEvent(anthropicsdk.ContentBlockDeltaEvent{
			Type:  constant.ContentBlockDelta("content_block_delta"),
			Index: 0,
			Delta: anthropicsdk.RawContentBlockDeltaUnion{Type: "text_delta", Text: "lo"},
		}),
		mkEvent(anthropicsdk.ContentBlockStopEvent{Type: constant.ContentBlockStop("content_block_stop"), Index: 0}),
		mkEvent(anthropicsdk.ContentBlockStartEvent{
			Type:  constant.ContentBlockStart("content_block_start"),
			Index: 1,
			ContentBlock: anthropicsdk.ContentBlockStartEventContentBlockUnion{
				Type: "tool_use",
				ID:   "tool-1",
				Name: "search",
			},
		}),
		mkEvent(anthropicsdk.ContentBlockDeltaEvent{
			Type:  constant.ContentBlockDelta("content_block_delta"),
			Index: 1,
			Delta: anthropicsdk.RawContentBlockDeltaUnion{Type: "input_json_delta", PartialJSON: `{"q":"doc"}`},
		}),
		mkEvent(anthropicsdk.ContentBlockStopEvent{Type: constant.ContentBlockStop("content_block_stop"), Index: 1}),
		mkEvent(anthropicsdk.MessageDeltaEvent{
			Type:  constant.MessageDelta("message_delta"),
			Delta: anthropicsdk.MessageDeltaEventDelta{StopReason: "tool_use"},
			Usage: anthropicsdk.MessageDeltaUsage{
				InputTokens:              9,
				OutputTokens:             3,
				CacheCreationInputTokens: 1,
				CacheReadInputTokens:     2,
			},
		}),
		mkEvent(anthropicsdk.MessageStopEvent{Type: constant.MessageStop("message_stop")}),
	}

	decoder := &sequenceDecoder{events: events}
	stream := ssestream.NewStream[anthropicsdk.MessageStreamEventUnion](decoder, nil)

	mock := &fakeMessages{
		streamFn: func(ctx context.Context, params anthropicsdk.MessageNewParams) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
			return stream
		},
		countFn: func(ctx context.Context, params anthropicsdk.MessageCountTokensParams) (*anthropicsdk.MessageTokensCount, error) {
			return &anthropicsdk.MessageTokensCount{InputTokens: 9}, nil
		},
	}

	m := &anthropicModel{
		msgs:       mock,
		model:      anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // use deprecated constant in test
		maxTokens:  32,
		maxRetries: 0,
	}

	var deltas []string
	var tools []*ToolCall
	var final *Response
	err := m.CompleteStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, func(sr StreamResult) error {
		if sr.Delta != "" {
			deltas = append(deltas, sr.Delta)
		}
		if sr.ToolCall != nil {
			tools = append(tools, sr.ToolCall)
		}
		if sr.Final {
			final = sr.Response
		}
		return nil
	})
	if err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	if want := []string{"hel", "lo"}; len(deltas) != len(want) || deltas[0] != want[0] || deltas[1] != want[1] {
		t.Fatalf("deltas mismatch: %v", deltas)
	}
	if len(tools) != 1 || tools[0].Name != "search" || tools[0].Arguments["q"] != "doc" {
		t.Fatalf("tool use not surfaced: %+v", tools)
	}
	if final == nil {
		t.Fatal("missing final response")
	}
	if final.Message.Content != "hello" {
		t.Fatalf("final content mismatch: %q", final.Message.Content)
	}
	if final.Usage.InputTokens != 9 || final.Usage.OutputTokens != 3 || final.Usage.CacheCreationTokens != 1 || final.Usage.CacheReadTokens != 2 {
		t.Fatalf("usage mismatch: %+v", final.Usage)
	}
}

func TestProviderEnvFallbackAndCache(t *testing.T) {
	setEnv(t, "ANTHROPIC_API_KEY", "env-key")
	p := &AnthropicProvider{CacheTTL: time.Minute}
	first, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("first provide failed: %v", err)
	}
	second, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("second provide failed: %v", err)
	}
	if first != second {
		t.Fatalf("expected cached model instance")
	}
}

func TestProviderMissingAPIKey(t *testing.T) {
	setEnv(t, "ANTHROPIC_AUTH_TOKEN", "")
	setEnv(t, "ANTHROPIC_API_KEY", "")
	p := &AnthropicProvider{}
	if _, err := p.Model(context.Background()); err == nil {
		t.Fatalf("expected error when api key is missing")
	}
}

func TestHelperBranches(t *testing.T) {
	if !isRetryable(&anthropicsdk.Error{StatusCode: http.StatusTooManyRequests}) {
		t.Fatal("expected 429 to be retryable")
	}
	if !isRetryable(&anthropicsdk.Error{StatusCode: http.StatusBadRequest}) {
		t.Fatal("expected 400 to be retryable")
	}
	if isRetryable(&anthropicsdk.Error{StatusCode: http.StatusUnauthorized}) {
		t.Fatal("expected 401 to not be retryable")
	}

	usage := usageFromFallback(anthropicsdk.Usage{InputTokens: 2, OutputTokens: 3, CacheCreationInputTokens: 1, CacheReadInputTokens: 1}, Usage{})
	if usage.TotalTokens != 5 || usage.CacheCreationTokens != 1 || usage.CacheReadTokens != 1 {
		t.Fatalf("fallback usage incorrect: %+v", usage)
	}
	usageTracked := usageFromFallback(anthropicsdk.Usage{InputTokens: 5, OutputTokens: 5}, Usage{InputTokens: 1, OutputTokens: 1})
	if usageTracked.TotalTokens != 2 {
		t.Fatalf("tracked usage should be preserved: %+v", usageTracked)
	}

	countTools := convertCountTools([]anthropicsdk.ToolUnionParam{{OfTool: &anthropicsdk.ToolParam{Name: "x"}}})
	if len(countTools) != 1 || countTools[0].OfTool == nil {
		t.Fatalf("convertCountTools failed: %+v", countTools)
	}

	raw := decodeJSON([]byte(`"str"`))
	if raw["value"] != "str" {
		t.Fatalf("decodeJSON scalar failed: %+v", raw)
	}
	rawBad := decodeJSON([]byte(`{invalid`))
	if _, ok := rawBad["raw"]; !ok {
		t.Fatalf("decodeJSON error path not handled: %+v", rawBad)
	}

	if got := mapModelName(string(anthropicsdk.ModelClaude3_5HaikuLatest)); got != anthropicsdk.ModelClaude3_5HaikuLatest {
		t.Fatalf("mapModelName expected haiku got %s", got)
	}
	if got := mapModelName("unknown"); got != anthropicsdk.ModelClaudeSonnet4_5_20250929 {
		t.Fatalf("mapModelName default mismatch: %s", got)
	}

	sys, msgs, err := convertMessages(nil, "sys")
	if err != nil {
		t.Fatalf("convertMessages error: %v", err)
	}
	if len(sys) != 1 || len(msgs) != 1 || msgs[0].Role != anthropicsdk.MessageParamRoleUser {
		t.Fatalf("convertMessages placeholder incorrect: sys=%d msgs=%d", len(sys), len(msgs))
	}
}

func TestProviderFuncAndMustProvider(t *testing.T) {
	called := false
	fn := ProviderFunc(func(ctx context.Context) (Model, error) {
		called = true
		return noopModel{}, nil
	})
	if _, err := fn.Model(context.Background()); err != nil || !called {
		t.Fatalf("provider func not invoked: err=%v called=%v", err, called)
	}

	if MustProvider(fn) == nil {
		t.Fatal("must provider returned nil")
	}
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic on provider error")
		}
	}()
	MustProvider(ProviderFunc(func(context.Context) (Model, error) { return nil, errors.New("boom") }))
}

func TestAdditionalBranches(t *testing.T) {
	m := &anthropicModel{model: anthropicsdk.ModelClaude3_5HaikuLatest}
	if got := m.selectModel("claude-sonnet-4-5"); got != anthropicsdk.ModelClaudeSonnet4_5 {
		t.Fatalf("override model failed: %s", got)
	}
	if got := m.selectModel(""); got != anthropicsdk.ModelClaude3_5HaikuLatest {
		t.Fatalf("selectModel should fall back")
	}

	if _, err := encodeSchema(map[string]any{"bad": func() {}}); err == nil {
		t.Fatal("expected encodeSchema to fail on non-marshalable value")
	}

	errBlocks := buildToolResults(Message{Content: `{"error":"boom"}`, ToolCalls: []ToolCall{{ID: "id"}}})
	if ptr := errBlocks[0].GetIsError(); ptr == nil || !*ptr {
		t.Fatalf("tool result should mark error: %+v", errBlocks[0])
	}

	cp := m.countParams(anthropicsdk.MessageNewParams{
		Model: anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // use deprecated constant in test
		System: []anthropicsdk.TextBlockParam{
			{Text: "sys"},
		},
		Tools: []anthropicsdk.ToolUnionParam{{OfTool: &anthropicsdk.ToolParam{Name: "t"}}},
	})
	if !(len(cp.System.OfTextBlockArray) == 1 && len(cp.Tools) == 1) {
		t.Fatalf("count params conversion failed: %+v", cp)
	}

	out, ok := cloneValue(map[string]any{"ary": []any{map[string]any{"k": "v"}}}).(map[string]any)
	if !ok {
		t.Fatalf("expected map clone, got %T", cloneValue(map[string]any{"ary": []any{map[string]any{"k": "v"}}}))
	}
	ary, ok := out["ary"].([]any)
	if !ok || len(ary) != 1 {
		t.Fatalf("expected ary slice, got %#v", out["ary"])
	}
	elem, ok := ary[0].(map[string]any)
	if !ok || elem["k"] != "v" {
		t.Fatalf("cloneValue lost data: %+v", out)
	}
}

func TestAdditionalBranchesII(t *testing.T) {
	setEnv(t, "ANTHROPIC_AUTH_TOKEN", "")
	setEnv(t, "ANTHROPIC_API_KEY", "")
	if err := (&anthropicModel{}).CompleteStream(context.Background(), Request{}, nil); err == nil {
		t.Fatal("expected error when callback is nil")
	}

	blocks := buildAssistantContent(Message{})
	if blocks[0].OfText == nil {
		t.Fatal("assistant fallback text missing")
	}

	toolBlocks := buildToolResults(Message{Content: "plain"})
	if toolBlocks[0].OfText == nil {
		t.Fatal("tool result fallback text missing")
	}

	schema, err := encodeSchema(nil)
	if err != nil || schema.Type != "object" {
		t.Fatalf("encodeSchema default failed: %v %+v", err, schema)
	}

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	errRetry := (&anthropicModel{maxRetries: 1}).doWithRetry(ctx, func(context.Context) error {
		calls++
		cancel()
		return tempNetErr{}
	})
	if !errors.Is(errRetry, context.Canceled) || calls != 1 {
		t.Fatalf("expected context cancel from retry, got %v (calls=%d)", errRetry, calls)
	}

	cfgModel, err := NewAnthropic(AnthropicConfig{APIKey: "k"})
	if err != nil {
		t.Fatalf("new anthropic failed: %v", err)
	}
	am, ok := cfgModel.(*anthropicModel)
	if !ok {
		t.Fatalf("expected *anthropicModel, got %T", cfgModel)
	}
	if am.maxTokens != 4096 || am.maxRetries != 10 {
		t.Fatalf("defaults not applied: %+v", am)
	}

	if val := (&AnthropicProvider{APIKey: "abc"}).resolveAPIKey(); val != "abc" {
		t.Fatalf("resolveAPIKey should prefer explicit value, got %s", val)
	}
}

func TestResolveAPIKeyPriority(t *testing.T) {
	t.Run("auth token wins", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "  auth-token  ")
		t.Setenv("ANTHROPIC_API_KEY", "api-key")
		val := (&AnthropicProvider{APIKey: "cfg-key"}).resolveAPIKey()
		if val != "auth-token" {
			t.Fatalf("expected ANTHROPIC_AUTH_TOKEN to win, got %s", val)
		}
	})

	t.Run("api key when auth token missing", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "  api-key  ")
		val := (&AnthropicProvider{APIKey: "cfg-key"}).resolveAPIKey()
		if val != "api-key" {
			t.Fatalf("expected ANTHROPIC_API_KEY to win, got %s", val)
		}
	})

	t.Run("fallback to explicit config", func(t *testing.T) {
		t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
		t.Setenv("ANTHROPIC_API_KEY", "")
		val := (&AnthropicProvider{APIKey: " cfg-key "}).resolveAPIKey()
		if val != "cfg-key" {
			t.Fatalf("expected explicit APIKey to be used, got %s", val)
		}
	})
}

func TestStreamUnavailable(t *testing.T) {
	mock := &fakeMessages{
		streamFn: func(context.Context, anthropicsdk.MessageNewParams) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
			return nil
		},
		countFn: func(context.Context, anthropicsdk.MessageCountTokensParams) (*anthropicsdk.MessageTokensCount, error) {
			return nil, errors.New("boom")
		},
	}
	modelName := anthropicsdk.ModelClaude3_7SonnetLatest //nolint:staticcheck // use deprecated constant in test
	err := (&anthropicModel{msgs: mock, model: modelName, maxTokens: 1}).CompleteStream(
		context.Background(),
		Request{Messages: []Message{{Role: "user", Content: "hi"}}},
		func(StreamResult) error { return nil },
	)
	if err == nil {
		t.Fatal("expected stream creation error")
	}
}

func TestMustProviderNilPanics(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for nil provider")
		}
	}()
	MustProvider(nil)
}

func TestNewAnthropicBranches(t *testing.T) {
	mdl, err := NewAnthropic(AnthropicConfig{
		APIKey:     "k",
		BaseURL:    "http://example.com",
		HTTPClient: &http.Client{},
		MaxRetries: -1,
	})
	if err != nil || mdl == nil {
		t.Fatalf("unexpected error: %v", err)
	}

	_, errParams := (&anthropicModel{}).buildParams(Request{
		Tools: []ToolDefinition{{Name: "t", Parameters: map[string]any{"bad": func() {}}}},
	})
	if errParams == nil {
		t.Fatal("expected buildParams to fail on invalid tool schema")
	}
}

func TestSessionIDMetadataPropagation(t *testing.T) {
	m := &anthropicModel{
		model:     anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // use deprecated constant in test
		maxTokens: 16,
	}

	req := Request{
		SessionID: " session-123 ",
		Messages:  []Message{{Role: "user", Content: "hi"}},
	}

	params, err := m.buildParams(req)
	if err != nil {
		t.Fatalf("buildParams returned error: %v", err)
	}
	if !params.Metadata.UserID.Valid() {
		t.Fatal("metadata user id missing")
	}
	if got := params.Metadata.UserID.Value; got != "session-123" {
		t.Fatalf("user id mismatch: %q", got)
	}

	noSessionParams, err := m.buildParams(Request{Messages: []Message{{Role: "user", Content: "ping"}}})
	if err != nil {
		t.Fatalf("buildParams returned error for empty session: %v", err)
	}
	if noSessionParams.Metadata.UserID.Valid() {
		t.Fatal("metadata should be omitted when no session id")
	}
}

func TestProviderFuncNil(t *testing.T) {
	var fn ProviderFunc
	if _, err := fn.Model(context.Background()); err == nil {
		t.Fatal("expected error for nil provider func")
	}
}

func TestExtractToolCallNil(t *testing.T) {
	if tc := extractToolCall(anthropicsdk.Message{}); tc != nil {
		t.Fatalf("expected nil tool call, got %+v", tc)
	}
}

func TestCustomHeadersDisabled(t *testing.T) {
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "false")
	defaults := map[string]string{"User-Agent": "custom-client"}
	overrides := map[string]string{"X-App": "user-app"}
	if anthropicCustomHeadersEnabled() {
		t.Fatal("custom headers gate should be disabled")
	}
	headers := newAnthropicHeaders(defaults, overrides)
	if headers == nil {
		t.Fatal("expected headers when defaults provided")
	}
	if _, ok := headers["accept"]; ok {
		t.Fatal("predefined headers should be skipped when gate is off")
	}
	if headers["user-agent"] != "custom-client" {
		t.Fatalf("defaults not applied: %+v", headers)
	}
	if headers["x-app"] != "user-app" {
		t.Fatalf("user override missing, got %+v", headers)
	}
	if _, ok := headers["x-api-key"]; ok {
		t.Fatalf("x-api-key should be handled by requestOptions, got %+v", headers)
	}
}

func TestCustomHeadersEnabled(t *testing.T) {
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", " TrUe ")
	if !anthropicCustomHeadersEnabled() {
		t.Fatal("custom headers gate should be enabled")
	}
	headers := newAnthropicHeaders(nil, nil)
	if headers == nil {
		t.Fatal("expected predefined headers when gate is on")
	}
	if got, want := len(headers), len(anthropicPredefinedHeaders); got != want {
		t.Fatalf("header count mismatch: got %d want %d", got, want)
	}
	for key, value := range anthropicPredefinedHeaders {
		if headers[key] != value {
			t.Fatalf("predefined header %s mismatch: %q", key, headers[key])
		}
	}
	if _, ok := headers["x-api-key"]; ok {
		t.Fatalf("x-api-key should be handled by requestOptions, got %+v", headers)
	}
}

func TestCustomHeadersMergePriority(t *testing.T) {
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	defaults := map[string]string{"Content-Type": "text/plain", "User-Agent": "ua-default", "X-App": "default-app"}
	overrides := map[string]string{"CONTENT-TYPE": "user-type", "X-App": "user-app", "extra": "1"}
	headers := newAnthropicHeaders(defaults, overrides)
	if headers["content-type"] != "user-type" {
		t.Fatalf("user header should win: %+v", headers)
	}
	if headers["user-agent"] != "ua-default" {
		t.Fatalf("defaults should override predefined: %+v", headers)
	}
	if headers["x-app"] != "user-app" {
		t.Fatalf("user override missing: %+v", headers)
	}
	if headers["extra"] != "1" {
		t.Fatalf("user extra header missing: %+v", headers)
	}
	if headers["anthropic-version"] != anthropicPredefinedHeaders["anthropic-version"] {
		t.Fatalf("predefined headers should remain: %+v", headers)
	}
}

func TestCustomHeadersNeverInjectAPIKey(t *testing.T) {
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	setEnv(t, "ANTHROPIC_AUTH_TOKEN", "auth-token")
	setEnv(t, "ANTHROPIC_API_KEY", "real-key")

	overrides := map[string]string{"X-API-Key": "user"}
	headers := newAnthropicHeaders(nil, overrides)
	if _, ok := headers["x-api-key"]; ok {
		t.Fatalf("x-api-key should be handled by requestOptions, got %+v", headers)
	}

	if err := os.Setenv("ANTHROPIC_API_KEY", ""); err != nil {
		t.Fatalf("set env: %v", err)
	}
	headers = newAnthropicHeaders(map[string]string{"Accept": "application/json"}, overrides)
	if _, ok := headers["x-api-key"]; ok {
		t.Fatalf("x-api-key should be handled by requestOptions, got %+v", headers)
	}
}

func TestAnthropicRequestOptionsAPIKeyPriority(t *testing.T) {
	tests := []struct {
		name           string
		configuredKey  string
		envAuthToken   string
		envAPIKey      string
		expectedAPIKey string
	}{
		{
			name:           "configured APIKey wins over env vars",
			configuredKey:  "sk-config-key",
			envAuthToken:   "sk-env-auth-token",
			envAPIKey:      "sk-env-api-key",
			expectedAPIKey: "sk-config-key",
		},
		{
			name:           "fallback to ANTHROPIC_AUTH_TOKEN",
			configuredKey:  "",
			envAuthToken:   "sk-env-auth-token",
			envAPIKey:      "sk-env-api-key",
			expectedAPIKey: "sk-env-auth-token",
		},
		{
			name:           "fallback to ANTHROPIC_API_KEY",
			configuredKey:  "",
			envAuthToken:   "",
			envAPIKey:      "sk-env-api-key",
			expectedAPIKey: "sk-env-api-key",
		},
		{
			name:           "no key when none provided",
			configuredKey:  "",
			envAuthToken:   "",
			envAPIKey:      "",
			expectedAPIKey: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			setEnv(t, "ANTHROPIC_AUTH_TOKEN", tt.envAuthToken)
			setEnv(t, "ANTHROPIC_API_KEY", tt.envAPIKey)
			setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "false")

			m := &anthropicModel{configuredAPIKey: tt.configuredKey}
			opts := m.requestOptions()
			headers := headersFromOptions(t, opts)

			got := headers.Get("x-api-key")
			if got != tt.expectedAPIKey {
				t.Fatalf("x-api-key mismatch: got %q want %q", got, tt.expectedAPIKey)
			}
		})
	}
}

func TestCustomHeadersUserOverridePredefined(t *testing.T) {
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	overrides := map[string]string{"X-App": "user-app", "Anthropic-Version": "2099-01-01"}
	headers := newAnthropicHeaders(nil, overrides)
	if headers["x-app"] != "user-app" {
		t.Fatalf("expected user x-app, got %+v", headers)
	}
	if headers["anthropic-version"] != "2099-01-01" {
		t.Fatalf("expected overridden anthropic-version: %+v", headers)
	}
	if headers["accept"] != anthropicPredefinedHeaders["accept"] {
		t.Fatalf("unrelated predefined headers should remain: %+v", headers)
	}
}

func TestCustomHeadersCompleteAppliesHeaders(t *testing.T) {
	setEnv(t, "ANTHROPIC_AUTH_TOKEN", "")
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	setEnv(t, "ANTHROPIC_API_KEY", "complete-key")
	mock := &fakeMessages{
		newFn: func(context.Context, anthropicsdk.MessageNewParams) (*anthropicsdk.Message, error) {
			msg := anthropicsdk.Message{
				Role:    constant.Assistant("assistant"),
				Content: []anthropicsdk.ContentBlockUnion{{Type: "text", Text: "ok"}},
			}
			return &msg, nil
		},
	}
	m := &anthropicModel{
		msgs:       mock,
		model:      anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // compat coverage
		maxTokens:  32,
		maxRetries: 0,
	}
	if _, err := m.Complete(context.Background(), Request{Messages: []Message{{Role: "user", Content: "ping"}}}); err != nil {
		t.Fatalf("complete failed: %v", err)
	}
	assertHeadersApplied(t, mock.newOpts, "complete-key")
}

func TestCustomHeadersCompleteStreamAppliesHeaders(t *testing.T) {
	setEnv(t, "ANTHROPIC_AUTH_TOKEN", "")
	setEnv(t, "ANTHROPIC_CUSTOM_HEADERS_ENABLED", "true")
	setEnv(t, "ANTHROPIC_API_KEY", "stream-key")
	decoder := &sequenceDecoder{}
	stream := ssestream.NewStream[anthropicsdk.MessageStreamEventUnion](decoder, nil)
	mock := &fakeMessages{
		streamFn: func(context.Context, anthropicsdk.MessageNewParams) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
			return stream
		},
	}
	m := &anthropicModel{
		msgs:       mock,
		model:      anthropicsdk.ModelClaude3_7SonnetLatest, //nolint:staticcheck // compat coverage
		maxTokens:  32,
		maxRetries: 0,
	}
	if err := m.CompleteStream(context.Background(), Request{Messages: []Message{{Role: "user", Content: "hi"}}}, func(StreamResult) error { return nil }); err != nil {
		t.Fatalf("stream failed: %v", err)
	}
	assertHeadersApplied(t, mock.streamOpts, "stream-key")
}

func setEnv(t *testing.T, key, value string) {
	t.Helper()
	original, had := os.LookupEnv(key)
	if err := os.Setenv(key, value); err != nil {
		t.Fatalf("set env %s: %v", key, err)
	}
	t.Cleanup(func() {
		var err error
		if had {
			err = os.Setenv(key, original)
		} else {
			err = os.Unsetenv(key)
		}
		if err != nil {
			t.Fatalf("restore env %s: %v", key, err)
		}
	})
}

func assertHeadersApplied(t *testing.T, opts []option.RequestOption, apiKey string) {
	t.Helper()
	if len(opts) == 0 {
		t.Fatal("expected request options to include headers")
	}
	headers := headersFromOptions(t, opts)
	if got, want := len(headers), len(anthropicPredefinedHeaders)+1; got != want {
		t.Fatalf("header count mismatch: got %d want %d", got, want)
	}
	for key, value := range anthropicPredefinedHeaders {
		if headers.Get(key) != value {
			t.Fatalf("header %s mismatch: got %q want %q", key, headers.Get(key), value)
		}
	}
	if got := headers.Get("x-api-key"); got != apiKey {
		t.Fatalf("x-api-key mismatch: got %q want %q", got, apiKey)
	}
}

func headersFromOptions(t *testing.T, opts []option.RequestOption) http.Header {
	t.Helper()
	headers := http.Header{}
	if len(opts) == 0 {
		return headers
	}
	var cfg reflect.Value
	for _, opt := range opts {
		if opt == nil {
			continue
		}
		apply := reflect.ValueOf(opt).MethodByName("Apply")
		if !apply.IsValid() {
			t.Fatalf("request option missing Apply: %T", opt)
		}
		if apply.Type().NumIn() != 1 {
			t.Fatalf("unexpected request option signature: %T", opt)
		}
		if !cfg.IsValid() {
			argType := apply.Type().In(0)
			cfg = reflect.New(argType.Elem())
			field := cfg.Elem().FieldByName("Request")
			if !field.IsValid() {
				t.Fatal("request config missing Request field")
			}
			field.Set(reflect.ValueOf(&http.Request{Header: headers}))
		}
		out := apply.Call([]reflect.Value{cfg})
		if len(out) == 1 && !out[0].IsNil() {
			if err, ok := out[0].Interface().(error); ok && err != nil {
				t.Fatalf("apply option failed: %v", err)
			}
		}
	}
	return headers
}

type noopModel struct{}

func (noopModel) Complete(context.Context, Request) (*Response, error) { return &Response{}, nil }
func (noopModel) CompleteStream(context.Context, Request, StreamHandler) error {
	return nil
}

// --- helpers ---

type fakeMessages struct {
	newFn      func(context.Context, anthropicsdk.MessageNewParams) (*anthropicsdk.Message, error)
	streamFn   func(context.Context, anthropicsdk.MessageNewParams) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion]
	countFn    func(context.Context, anthropicsdk.MessageCountTokensParams) (*anthropicsdk.MessageTokensCount, error)
	newOpts    []option.RequestOption
	streamOpts []option.RequestOption
	countOpts  []option.RequestOption
}

func (f *fakeMessages) New(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) (*anthropicsdk.Message, error) {
	f.newOpts = append([]option.RequestOption(nil), opts...)
	if f.newFn == nil {
		return nil, errors.New("newFn not set")
	}
	return f.newFn(ctx, params)
}

func (f *fakeMessages) NewStreaming(ctx context.Context, params anthropicsdk.MessageNewParams, opts ...option.RequestOption) *ssestream.Stream[anthropicsdk.MessageStreamEventUnion] {
	f.streamOpts = append([]option.RequestOption(nil), opts...)
	if f.streamFn == nil {
		return nil
	}
	return f.streamFn(ctx, params)
}

func (f *fakeMessages) CountTokens(ctx context.Context, params anthropicsdk.MessageCountTokensParams, opts ...option.RequestOption) (*anthropicsdk.MessageTokensCount, error) {
	f.countOpts = append([]option.RequestOption(nil), opts...)
	if f.countFn == nil {
		return &anthropicsdk.MessageTokensCount{}, nil
	}
	return f.countFn(ctx, params)
}

type sequenceDecoder struct {
	events []ssestream.Event
	i      int
}

func (d *sequenceDecoder) Next() bool {
	if d.i >= len(d.events) {
		return false
	}
	d.i++
	return true
}

func (d *sequenceDecoder) Event() ssestream.Event {
	return d.events[d.i-1]
}

func (d *sequenceDecoder) Close() error { return nil }
func (d *sequenceDecoder) Err() error   { return nil }

func mkEvent(v any) ssestream.Event {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	// Attempt to read "type" field for the SSE wrapper.
	var typeProbe struct {
		Type string `json:"type"`
	}
	if err := json.Unmarshal(data, &typeProbe); err != nil {
		panic(err)
	}
	return ssestream.Event{Type: typeProbe.Type, Data: data}
}

type tempNetErr struct{}

func (tempNetErr) Error() string   { return "temp" }
func (tempNetErr) Timeout() bool   { return false }
func (tempNetErr) Temporary() bool { return true }

// Avoid "imported and not used" when running go vet in memory constrained CI.
var _ net.Error = tempNetErr{}
