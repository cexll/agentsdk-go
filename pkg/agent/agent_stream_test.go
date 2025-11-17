package agent

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
	"github.com/cexll/agentsdk-go/pkg/session"
	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestRunStreamLongFlows(t *testing.T) {
	server := newStreamingServer(t, 10*time.Millisecond, 5)
	tests := []struct {
		name               string
		streamBuffer       int
		initialDrainDelay  time.Duration
		cancelOnStage      string
		expectBackpressure bool
		expectStopped      bool
	}{
		{
			name:               "long_run_with_backpressure_recovery",
			streamBuffer:       2,
			initialDrainDelay:  40 * time.Millisecond,
			expectBackpressure: true,
		},
		{
			name:          "context_cancel_mid_execution",
			streamBuffer:  4,
			cancelOnStage: "tool:longhttp",
			expectStopped: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			t.Cleanup(cancel)
			sess, err := session.NewMemorySession(fmt.Sprintf("stream-long-%s", tt.name))
			if err != nil {
				t.Fatalf("session: %v", err)
			}
			t.Cleanup(func() { _ = sess.Close() })
			agent, err := New(
				Config{
					Name:         "stream-long",
					StreamBuffer: tt.streamBuffer,
					DefaultContext: RunContext{
						SessionID: "stream-session",
					},
				},
				WithModel(newMockModel()),
				WithSession(sess),
			)
			if err != nil {
				t.Fatalf("new agent: %v", err)
			}
			longTool := &httpStreamTool{
				name:   "longhttp",
				url:    server.URL,
				client: &http.Client{Timeout: time.Second},
			}
			if err := agent.AddTool(longTool); err != nil {
				t.Fatalf("add tool: %v", err)
			}
			stream, err := agent.RunStream(ctx, "tool:longhttp {}")
			if err != nil {
				t.Fatalf("run stream: %v", err)
			}
			events := collectStreamEvents(t, stream, tt.initialDrainDelay, func(evt event.Event) {
				if tt.cancelOnStage == "" || evt.Type != event.EventProgress {
					return
				}
				data, ok := evt.Data.(event.ProgressData)
				if !ok {
					return
				}
				if data.Stage == tt.cancelOnStage && data.Message == "started" {
					cancel()
				}
			})
			if len(events) == 0 {
				t.Fatal("expected streamed events")
			}
			if tt.expectBackpressure {
				if !hasProgress(events, "backpressure", "throttled") {
					t.Fatalf("missing throttled backpressure event: %+v", events)
				}
				if !hasProgress(events, "backpressure", "recovered") {
					t.Fatalf("missing recovered backpressure event: %+v", events)
				}
				if !hasEventType(events, event.EventCompletion) {
					t.Fatalf("expected completion event, got none: %+v", events)
				}
			}
			if tt.expectStopped {
				if !hasProgress(events, "stopped", "") {
					t.Fatalf("missing stopped event: %+v", events)
				}
			} else if hasProgress(events, "stopped", "") {
				t.Fatalf("unexpected stopped event: %+v", events)
			}
		})
	}
}

type httpStreamTool struct {
	name   string
	url    string
	client *http.Client
}

func (h *httpStreamTool) Name() string             { return h.name }
func (h *httpStreamTool) Description() string      { return "http stream tool" }
func (h *httpStreamTool) Schema() *tool.JSONSchema { return nil }

func (h *httpStreamTool) Execute(ctx context.Context, _ map[string]any) (*tool.ToolResult, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, h.url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	return &tool.ToolResult{Output: strings.TrimSpace(string(body))}, nil
}

func collectStreamEvents(t *testing.T, ch <-chan event.Event, delay time.Duration, onEvent func(event.Event)) []event.Event {
	t.Helper()
	if delay > 0 {
		time.Sleep(delay)
	}
	var events []event.Event
	timeout := time.NewTimer(2 * time.Second)
	defer timeout.Stop()
	for {
		select {
		case evt, ok := <-ch:
			if !ok {
				return events
			}
			events = append(events, evt)
			if onEvent != nil {
				onEvent(evt)
			}
		case <-timeout.C:
			t.Fatal("timed out waiting for stream events")
		}
	}
}

func hasProgress(events []event.Event, stage, message string) bool {
	for _, evt := range events {
		if evt.Type != event.EventProgress {
			continue
		}
		data, ok := evt.Data.(event.ProgressData)
		if !ok {
			continue
		}
		if data.Stage != stage {
			continue
		}
		if message == "" || data.Message == message {
			return true
		}
	}
	return false
}

func hasEventType(events []event.Event, typ event.EventType) bool {
	for _, evt := range events {
		if evt.Type == typ {
			return true
		}
	}
	return false
}

func newStreamingServer(t *testing.T, delay time.Duration, chunks int) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		flusher, _ := w.(http.Flusher)
		for i := 0; i < chunks; i++ {
			select {
			case <-r.Context().Done():
				return
			default:
			}
			fmt.Fprintf(w, "chunk-%d\n", i)
			if flusher != nil {
				flusher.Flush()
			}
			time.Sleep(delay)
		}
	})
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	return server
}
