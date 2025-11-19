package api

import (
	"context"
	"encoding/json"

	"github.com/cexll/agentsdk-go/pkg/agent"
	"github.com/cexll/agentsdk-go/pkg/middleware"
)

// progressEmitter centralises guarded writes to the event channel so the
// middleware hooks stay terse and ordered.
type progressEmitter struct {
	ch chan<- StreamEvent
}

func (e progressEmitter) emit(evt StreamEvent) {
	if e.ch == nil {
		return
	}
	e.ch <- evt
}

// newProgressMiddleware surfaces Anthropic-compatible SSE progress events at
// each middleware interception point. The event ordering mirrors Anthropic's
// streaming payloads while adding agent/tool lifecycle markers.
func newProgressMiddleware(events chan<- StreamEvent) middleware.Funcs {
	em := progressEmitter{ch: events}

	textBlock := func(idx int, content string) {
		if content == "" {
			return
		}
		em.emit(StreamEvent{Type: EventContentBlockStart, Index: &idx, ContentBlock: &ContentBlock{Type: "text"}})
		for _, r := range content {
			em.emit(StreamEvent{Type: EventContentBlockDelta, Index: &idx, Delta: &Delta{Type: "text_delta", Text: string(r)}})
		}
		em.emit(StreamEvent{Type: EventContentBlockStop, Index: &idx})
	}

	toolBlock := func(idx int, call agent.ToolCall) {
		em.emit(StreamEvent{Type: EventContentBlockStart, Index: &idx, ContentBlock: &ContentBlock{Type: "tool_use", ID: call.ID, Name: call.Name}})
		raw, err := json.Marshal(call.Input)
		if err != nil {
			raw = []byte("{}")
		}
		for _, chunk := range chunkString(string(raw), 10) {
			em.emit(StreamEvent{Type: EventContentBlockDelta, Index: &idx, Delta: &Delta{Type: "input_json_delta", PartialJSON: json.RawMessage(chunk)}})
		}
		em.emit(StreamEvent{Type: EventContentBlockStop, Index: &idx})
	}

	return middleware.Funcs{
		Identifier: "progress",

		OnBeforeAgent: func(context.Context, *middleware.State) error {
			em.emit(StreamEvent{Type: EventAgentStart})
			return nil
		},

		OnBeforeModel: func(_ context.Context, st *middleware.State) error {
			iter := st.Iteration
			em.emit(StreamEvent{Type: EventIterationStart, Iteration: &iter})
			em.emit(StreamEvent{Type: EventMessageStart, Message: &Message{Role: "assistant"}})
			return nil
		},

		OnAfterModel: func(_ context.Context, st *middleware.State) error {
			out, ok := st.ModelOutput.(*agent.ModelOutput)
			if !ok || out == nil {
				return nil
			}

			idx := 0
			text := out.Content
			textBlock(idx, text)
			if text != "" {
				idx++
			}

			for _, call := range out.ToolCalls {
				toolBlock(idx, call)
				idx++
			}

			reason := "end_turn"
			if len(out.ToolCalls) > 0 {
				reason = "tool_use"
			}
			em.emit(StreamEvent{Type: EventMessageDelta, Delta: &Delta{StopReason: reason}, Usage: &Usage{}})
			em.emit(StreamEvent{Type: EventMessageStop})
			return nil
		},

		OnBeforeTool: func(_ context.Context, st *middleware.State) error {
			call, ok := st.ToolCall.(agent.ToolCall)
			if !ok {
				return nil
			}
			iter := st.Iteration
			em.emit(StreamEvent{Type: EventToolExecutionStart, ToolUseID: call.ID, Name: call.Name, Iteration: &iter})
			return nil
		},

		OnAfterTool: func(_ context.Context, st *middleware.State) error {
			call, ok := st.ToolCall.(agent.ToolCall)
			if !ok {
				return nil
			}
			res, ok := st.ToolResult.(agent.ToolResult)
			if !ok {
				return nil
			}

			if res.Output != "" {
				em.emit(StreamEvent{Type: EventToolExecutionOutput, ToolUseID: call.ID, Name: call.Name, Output: res.Output})
			}

			payload := map[string]any{}
			if res.Output != "" {
				payload["output"] = res.Output
			}
			if len(res.Metadata) > 0 {
				payload["metadata"] = res.Metadata
			}
			em.emit(StreamEvent{Type: EventToolExecutionResult, ToolUseID: call.ID, Name: call.Name, Output: payload})
			return nil
		},

		OnAfterAgent: func(_ context.Context, st *middleware.State) error {
			iter := st.Iteration
			em.emit(StreamEvent{Type: EventIterationStop, Iteration: &iter})
			em.emit(StreamEvent{Type: EventAgentStop})
			return nil
		},
	}
}

// chunkString splits s into roughly equal sized pieces without dropping
// remainder characters to support streaming partial JSON/tool output.
func chunkString(s string, size int) []string {
	if size <= 0 || s == "" {
		return nil
	}
	out := make([]string, 0, (len(s)+size-1)/size)
	for start := 0; start < len(s); start += size {
		end := start + size
		if end > len(s) {
			end = len(s)
		}
		out = append(out, s[start:end])
	}
	return out
}
