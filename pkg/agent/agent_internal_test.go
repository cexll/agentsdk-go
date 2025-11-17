package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/session"
)

// stubModel captures the incoming messages and returns a preset response.
type stubModel struct {
	resp         model.Message
	err          error
	capturedMsgs []model.Message
}

func (m *stubModel) Generate(_ context.Context, msgs []model.Message) (model.Message, error) {
	m.capturedMsgs = append([]model.Message(nil), msgs...)
	if m.err != nil {
		return model.Message{}, m.err
	}
	return m.resp, nil
}

func (m *stubModel) GenerateStream(context.Context, []model.Message, model.StreamCallback) error {
	return errors.New("not implemented")
}

// mustMemorySession is a lightweight helper to build an in-memory session.
func mustMemorySession(t *testing.T, id string) *session.MemorySession {
	t.Helper()
	sess, err := session.NewMemorySession(id)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sess.Close() })
	return sess
}

func TestGenerateResponse_AssistantMessagePersisted(t *testing.T) {
	ctx := context.Background()
	sess := mustMemorySession(t, "gen-success")
	modelResp := model.Message{Role: "assistant", Content: "  hello world  "}
	stub := &stubModel{resp: modelResp}
	agent := &basicAgent{model: stub, session: sess}

	out, err := agent.generateResponse(ctx, "ping", RunContext{})
	require.NoError(t, err)
	assert.Equal(t, "hello world", out)

	// Model receives normalized user message.
	require.Len(t, stub.capturedMsgs, 1)
	assert.Equal(t, "user", stub.capturedMsgs[0].Role)
	assert.Equal(t, "ping", stub.capturedMsgs[0].Content)

	// Session should contain user then assistant.
	history, err := sess.List(session.Filter{})
	require.NoError(t, err)
	require.Len(t, history, 2)
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, "assistant", history[1].Role)
	assert.Equal(t, "hello world", history[1].Content)
	assert.Nil(t, history[1].ToolCalls)
}

func TestGenerateResponse_DefaultRoleAndToolCalls(t *testing.T) {
	ctx := context.Background()
	sess := mustMemorySession(t, "gen-tools")
	toolArgs := map[string]any{"k": "v"}
	stub := &stubModel{resp: model.Message{
		Role:    "  ", // triggers role fallback to assistant
		Content: "with tools",
		ToolCalls: []model.ToolCall{
			{ID: "c1", Name: "echo", Arguments: toolArgs},
			{ID: "c2", Name: "sum", Arguments: map[string]any{"a": 1, "b": 2}},
		},
	}}
	agent := &basicAgent{model: stub, session: sess}

	out, err := agent.generateResponse(ctx, "tool please", RunContext{})
	require.NoError(t, err)
	assert.Equal(t, "with tools", out)

	history, err := sess.List(session.Filter{})
	require.NoError(t, err)
	require.Len(t, history, 2)

	assistantMsg := history[1]
	assert.Equal(t, "assistant", assistantMsg.Role) // role fallback verified
	require.Len(t, assistantMsg.ToolCalls, 2)
	assert.Equal(t, "c1", assistantMsg.ToolCalls[0].ID)
	assert.Equal(t, "echo", assistantMsg.ToolCalls[0].Name)
	assert.Equal(t, toolArgs, assistantMsg.ToolCalls[0].Arguments)
	assert.Equal(t, "c2", assistantMsg.ToolCalls[1].ID)
	assert.Equal(t, "sum", assistantMsg.ToolCalls[1].Name)
}

func TestGenerateResponse_ErrorsOnEmptyContent(t *testing.T) {
	ctx := context.Background()
	sess := mustMemorySession(t, "gen-empty")
	stub := &stubModel{resp: model.Message{Content: "   "}}
	agent := &basicAgent{model: stub, session: sess}

	_, err := agent.generateResponse(ctx, "noop", RunContext{})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "empty response")

	// Only the user message should have been appended before failing.
	history, listErr := sess.List(session.Filter{})
	require.NoError(t, listErr)
	require.Len(t, history, 1)
	assert.Equal(t, "user", history[0].Role)
}

func TestBuildModelMessages_EmptyHistory(t *testing.T) {
	sess := mustMemorySession(t, "build-empty")
	agent := &basicAgent{session: sess}

	msgs, err := agent.buildModelMessages("hello")
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	assert.Equal(t, model.Message{Role: "user", Content: "hello"}, msgs[0])

	history, err := sess.List(session.Filter{})
	require.NoError(t, err)
	require.Len(t, history, 1)
	assert.Equal(t, "user", history[0].Role)
	assert.Equal(t, "hello", history[0].Content)
}

func TestBuildModelMessages_RoleNormalizationAndConversion(t *testing.T) {
	sess := mustMemorySession(t, "build-roles")
	// Preload history with mixed roles and tool calls.
	require.NoError(t, sess.Append(session.Message{Role: "SYSTEM", Content: "policy"}))
	require.NoError(t, sess.Append(session.Message{
		Role:    "MODEL", // should become assistant
		Content: "prior reply",
	}))
	require.NoError(t, sess.Append(session.Message{
		Role:    "tool",
		Content: "ignored content",
		ToolCalls: []session.ToolCall{{
			ID:        "t1",
			Name:      "  calc  ",
			Arguments: map[string]any{"x": 1},
		}},
	}))
	agent := &basicAgent{session: sess}

	msgs, err := agent.buildModelMessages("next question")
	require.NoError(t, err)
	require.Len(t, msgs, 4)

	assert.Equal(t, "system", msgs[0].Role)
	assert.Equal(t, "assistant", msgs[1].Role)
	assert.Equal(t, "tool", msgs[2].Role)
	require.Len(t, msgs[2].ToolCalls, 1)
	assert.Equal(t, "calc", msgs[2].ToolCalls[0].Name) // normalized name trims spaces
	assert.Equal(t, map[string]any{"x": 1}, msgs[2].ToolCalls[0].Arguments)

	// Newly appended user input.
	assert.Equal(t, model.Message{Role: "user", Content: "next question"}, msgs[3])

	history, err := sess.List(session.Filter{})
	require.NoError(t, err)
	require.Len(t, history, 4)
	assert.Equal(t, "user", history[3].Role) // buildModelMessages appends the user turn
}

func TestBuildModelMessages_UnknownRolesFallbackToUser(t *testing.T) {
	sess := mustMemorySession(t, "build-unknown-role")
	require.NoError(t, sess.Append(session.Message{Role: "weird", Content: "??"}))
	agent := &basicAgent{session: sess}

	msgs, err := agent.buildModelMessages("ok")
	require.NoError(t, err)
	require.Len(t, msgs, 2)

	assert.Equal(t, "user", msgs[0].Role) // normalized from unknown
	assert.Equal(t, "user", msgs[1].Role)
}
