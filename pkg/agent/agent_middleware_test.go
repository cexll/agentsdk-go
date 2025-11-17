package agent

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/cexll/agentsdk-go/pkg/middleware"
)

func TestBasicAgentUseMiddleware(t *testing.T) {
	t.Run("single middleware", func(t *testing.T) {
		t.Helper()
		agent := &basicAgent{}
		mw := newTestMiddleware("logger", 10)

		agent.UseMiddleware(mw)

		list := agent.ListMiddlewares()
		assert.Len(t, list, 1)
		assert.Same(t, mw, list[0])
	})

	t.Run("multiple prioritized middlewares", func(t *testing.T) {
		t.Helper()
		agent := &basicAgent{}
		low := newTestMiddleware("low", 1)
		high := newTestMiddleware("high", 100)
		mid := newTestMiddleware("mid", 50)

		agent.UseMiddleware(low)
		agent.UseMiddleware(high)
		agent.UseMiddleware(mid)

		list := agent.ListMiddlewares()
		assert.Len(t, list, 3)
		assert.Equal(t, []string{"high", "mid", "low"}, middlewareNames(list))
	})
}

func TestBasicAgentRemoveMiddleware(t *testing.T) {
	agent := &basicAgent{}
	high := newTestMiddleware("high", 90)
	low := newTestMiddleware("low", 10)

	agent.UseMiddleware(high)
	agent.UseMiddleware(low)

	assert.True(t, agent.RemoveMiddleware("high"), "expected to remove existing middleware")
	assert.Equal(t, []string{"low"}, middlewareNames(agent.ListMiddlewares()))

	assert.False(t, agent.RemoveMiddleware("missing"), "removing non-existent middleware should return false")
}

func TestBasicAgentListMiddlewares(t *testing.T) {
	t.Run("empty list", func(t *testing.T) {
		agent := &basicAgent{}
		list := agent.ListMiddlewares()
		assert.Len(t, list, 0)
		assert.Nil(t, list)
	})

	t.Run("sorted order and defensive copy", func(t *testing.T) {
		agent := &basicAgent{}
		agent.UseMiddleware(newTestMiddleware("low", 1))
		agent.UseMiddleware(newTestMiddleware("high", 100))

		list := agent.ListMiddlewares()
		assert.Equal(t, []string{"high", "low"}, middlewareNames(list))

		list[0] = newTestMiddleware("mutated", 200)
		assert.Equal(t, []string{"high", "low"}, middlewareNames(agent.ListMiddlewares()))
	})
}

func middlewareNames(middlewares []middleware.Middleware) []string {
	names := make([]string, len(middlewares))
	for i, mw := range middlewares {
		if mw == nil {
			continue
		}
		names[i] = mw.Name()
	}
	return names
}

type testMiddleware struct {
	name     string
	priority int
}

func newTestMiddleware(name string, priority int) *testMiddleware {
	return &testMiddleware{name: name, priority: priority}
}

func (m *testMiddleware) Name() string  { return m.name }
func (m *testMiddleware) Priority() int { return m.priority }

func (m *testMiddleware) ExecuteModelCall(ctx context.Context, req *middleware.ModelRequest, next middleware.ModelCallFunc) (*middleware.ModelResponse, error) {
	if next == nil {
		return nil, middleware.ErrMissingNext
	}
	return next(ctx, req)
}

func (m *testMiddleware) ExecuteToolCall(ctx context.Context, req *middleware.ToolCallRequest, next middleware.ToolCallFunc) (*middleware.ToolCallResponse, error) {
	if next == nil {
		return nil, middleware.ErrMissingNext
	}
	return next(ctx, req)
}

func (m *testMiddleware) OnStart(ctx context.Context) error { return nil }
func (m *testMiddleware) OnStop(ctx context.Context) error  { return nil }
