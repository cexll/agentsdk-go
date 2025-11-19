package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"sync"
	"testing"
	"time"
)

func TestSTDIOTransportIntegration(t *testing.T) {
	if os.Getenv("MCP_STDIO_HELPER") == "1" {
		runSTDIOHelper()
		os.Exit(0)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	opts := STDIOOptions{
		Args:           []string{"-test.run", "TestSTDIOTransportIntegration"},
		Env:            append(os.Environ(), "MCP_STDIO_HELPER=1"),
		StartupTimeout: 200 * time.Millisecond,
	}
	transport, err := NewSTDIOTransport(ctx, os.Args[0], opts)
	if err != nil {
		t.Fatalf("new stdio transport: %v", err)
	}
	client := NewClient(transport)
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := client.InvokeTool(ctx, "echo", map[string]interface{}{"text": "hello"})
	if err != nil {
		t.Fatalf("invoke: %v", err)
	}
	if string(res.Content) != "\"echo:hello\"" {
		t.Fatalf("unexpected content: %s", string(res.Content))
	}
}

func runSTDIOHelper() {
	dec := json.NewDecoder(bufio.NewReader(os.Stdin))
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)

	for {
		var req Request
		if err := dec.Decode(&req); err != nil {
			if err == io.EOF {
				return
			}
			fmt.Fprintf(os.Stderr, "decode error: %v", err)
			return
		}
		resp := Response{JSONRPC: jsonRPCVersion, ID: req.ID}
		switch req.Method {
		case "tools/list":
			resp.Result = mustRaw(ToolListResult{Tools: []ToolDescriptor{{Name: "echo", Description: "Echo stdio"}}})
		case "tools/call":
			var params ToolCallParams
			raw, err := json.Marshal(req.Params)
			if err != nil {
				fmt.Fprintf(os.Stderr, "marshal params: %v", err)
				return
			}
			if err := json.Unmarshal(raw, &params); err != nil {
				fmt.Fprintf(os.Stderr, "unmarshal params: %v", err)
				return
			}
			text := ""
			if params.Arguments != nil {
				if v, ok := params.Arguments["text"].(string); ok {
					text = v
				}
			}
			resp.Result = mustRaw(ToolCallResult{Content: json.RawMessage(fmt.Sprintf("\"echo:%s\"", text))})
		default:
			resp.Error = &Error{Code: -32601, Message: "unknown method"}
		}
		if err := enc.Encode(resp); err != nil {
			fmt.Fprintf(os.Stderr, "encode error: %v", err)
			return
		}
	}
}

func mustRaw(v interface{}) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}

func TestSSETransportIntegration(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()

	harness := newSSEHarness()
	defer harness.Close()

	transport, err := NewSSETransport(ctx, SSEOptions{
		BaseURL:           harness.URL(),
		HeartbeatInterval: 50 * time.Millisecond,
		HeartbeatTimeout:  200 * time.Millisecond,
	})
	if err != nil {
		t.Fatalf("new sse transport: %v", err)
	}
	client := NewClient(transport)
	defer client.Close()

	tools, err := client.ListTools(ctx)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools) != 1 || tools[0].Name != "echo" {
		t.Fatalf("unexpected tools: %+v", tools)
	}

	res, err := client.InvokeTool(ctx, "echo", map[string]interface{}{"text": "sse"})
	if err != nil {
		t.Fatalf("invoke sse: %v", err)
	}
	if got := string(res.Content); got != "\"sse:sse\"" {
		t.Fatalf("unexpected response: %s", got)
	}

	// Force reconnection and ensure subsequent calls still work.
	_, err = client.InvokeTool(ctx, "echo", map[string]interface{}{"text": "again", "reconnect": true})
	if err != nil {
		t.Fatalf("invoke after reconnect: %v", err)
	}
}

type sseHarness struct {
	srv       *httptest.Server
	mu        sync.Mutex
	stream    chan []byte
	closeNext bool
}

func newSSEHarness() *sseHarness {
	h := &sseHarness{}
	mux := http.NewServeMux()
	mux.HandleFunc("/events", h.handleEvents)
	mux.HandleFunc("/rpc", h.handleRPC)
	h.srv = httptest.NewServer(mux)
	return h
}

func (h *sseHarness) URL() string {
	return h.srv.URL
}

func (h *sseHarness) Close() {
	h.srv.Close()
}

func (h *sseHarness) handleEvents(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming unsupported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")

	ch := make(chan []byte, 8)
	h.setStream(ch)
	ticker := time.NewTicker(80 * time.Millisecond)
	defer func() {
		ticker.Stop()
		h.setStream(nil)
	}()

	for {
		select {
		case payload, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
			if h.consumeCloseSignal() {
				return
			}
		case <-ticker.C:
			fmt.Fprintf(w, "event: heartbeat\ndata: {}\n\n")
			flusher.Flush()
		case <-r.Context().Done():
			return
		}
	}
}

func (h *sseHarness) handleRPC(w http.ResponseWriter, r *http.Request) {
	defer r.Body.Close()
	var req Request
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	resp := Response{JSONRPC: jsonRPCVersion, ID: req.ID}
	switch req.Method {
	case "tools/list":
		resp.Result = mustRaw(ToolListResult{Tools: []ToolDescriptor{{Name: "echo", Description: "Echo sse"}}})
	case "tools/call":
		var params ToolCallParams
		raw, err := json.Marshal(req.Params)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		if err := json.Unmarshal(raw, &params); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		reconnect := false
		text := ""
		if params.Arguments != nil {
			if v, ok := params.Arguments["text"].(string); ok {
				text = v
			}
			if v, ok := params.Arguments["reconnect"].(bool); ok {
				reconnect = v
			}
		}
		resp.Result = mustRaw(ToolCallResult{Content: json.RawMessage(fmt.Sprintf("\"sse:%s\"", text))})
		if reconnect {
			h.triggerClose()
		}
	default:
		resp.Error = &Error{Code: -32601, Message: "unknown method"}
	}
	h.send(resp)
	w.WriteHeader(http.StatusAccepted)
}

func (h *sseHarness) setStream(ch chan []byte) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stream != nil && h.stream != ch {
		close(h.stream)
	}
	h.stream = ch
}

func (h *sseHarness) send(resp Response) {
	payload := mustRaw(resp)
	h.mu.Lock()
	ch := h.stream
	h.mu.Unlock()
	if ch == nil {
		return
	}
	ch <- payload
}

func (h *sseHarness) triggerClose() {
	h.mu.Lock()
	h.closeNext = true
	h.mu.Unlock()
}

func (h *sseHarness) consumeCloseSignal() bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closeNext {
		h.closeNext = false
		return true
	}
	return false
}
