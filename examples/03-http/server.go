package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

const (
	maxBodyBytes     = 1 << 20
	streamPingPeriod = 15 * time.Second
)

type httpServer struct {
	runtime        *api.Runtime
	defaultTimeout time.Duration
	staticDir      string
}

func (s *httpServer) registerRoutes(mux *http.ServeMux) {
	// API routes
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/run", s.handleRun)
	mux.HandleFunc("/v1/run/stream", s.handleStream)

	// Static files
	fs := http.FileServer(http.Dir(s.staticDir))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Root redirect to static/index.html
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/static/index.html", http.StatusMovedPermanently)
			return
		}
		http.NotFound(w, r)
	})
}

func (s *httpServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		s.writeJSON(w, http.StatusMethodNotAllowed, errorResponse{"only GET supported"})
		return
	}
	s.writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *httpServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, errorResponse{"only POST supported"})
		return
	}

	var req runRequest
	if err := s.decode(r, &req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}
	if req.Prompt == "" {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{"prompt is required"})
		return
	}

	// Runtime serializes per SessionID. If multiple HTTP requests share a session_id concurrently,
	// one of them can fail with api.ErrConcurrentExecution (treat it as "session busy").
	// Use request-id (stateless) or user/client session-id (stateful) as session_id to isolate work.
	sessionID := req.ensureSessionID()
	ctx, cancel := s.requestContext(r.Context(), req.TimeoutMs)
	defer cancel()

	resp, err := s.runtime.Run(ctx, api.Request{
		Prompt:    req.Prompt,
		SessionID: sessionID,
	})
	if err != nil {
		s.writeJSON(w, http.StatusBadGateway, errorResponse{err.Error()})
		return
	}
	result := resp.Result
	if result == nil {
		s.writeJSON(w, http.StatusInternalServerError, errorResponse{"agent response is empty"})
		return
	}

	s.writeJSON(w, http.StatusOK, runResponse{
		SessionID:  sessionID,
		Output:     result.Output,
		StopReason: result.StopReason,
		Usage:      result.Usage,
		ToolCalls:  result.ToolCalls,
	})
}

func (s *httpServer) handleStream(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.writeJSON(w, http.StatusMethodNotAllowed, errorResponse{"only POST supported"})
		return
	}

	var req runRequest
	if err := s.decode(r, &req); err != nil {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{err.Error()})
		return
	}
	if req.Prompt == "" {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{"prompt is required"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeJSON(w, http.StatusInternalServerError, errorResponse{"streaming unsupported"})
		return
	}

	// Same as /v1/run: isolate concurrent requests with distinct session_id values to avoid
	// per-session concurrency conflicts (api.ErrConcurrentExecution).
	sessionID := req.ensureSessionID()
	ctx, cancel := s.requestContext(r.Context(), req.TimeoutMs)
	defer cancel()

	events, err := s.runtime.RunStream(ctx, api.Request{
		Prompt:    req.Prompt,
		SessionID: sessionID,
	})
	if err != nil {
		s.writeJSON(w, http.StatusBadGateway, errorResponse{err.Error()})
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	ticker := time.NewTicker(streamPingPeriod)
	defer ticker.Stop()

	for {
		select {
		case event, ok := <-events:
			if !ok {
				return
			}
			payload, err := json.Marshal(event)
			if err != nil {
				return
			}
			fmt.Fprintf(w, "data: %s\n\n", payload)
			flusher.Flush()
		case <-ticker.C:
			fmt.Fprint(w, "data: {\"type\":\"ping\"}\n\n")
			flusher.Flush()
		case <-ctx.Done():
			return
		}
	}
}

func (s *httpServer) decode(r *http.Request, dest any) error {
	if r.Body == nil {
		return errors.New("request body is empty")
	}
	defer r.Body.Close()

	reader := io.LimitReader(r.Body, maxBodyBytes)
	dec := json.NewDecoder(reader)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dest); err != nil {
		if errors.Is(err, io.EOF) {
			return errors.New("request body is empty")
		}
		return err
	}
	if dec.More() {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func (s *httpServer) requestContext(parent context.Context, timeoutMs int) (context.Context, context.CancelFunc) {
	timeout := s.defaultTimeout
	if timeoutMs > 0 {
		timeout = time.Duration(timeoutMs) * time.Millisecond
	}
	if timeout <= 0 {
		timeout = defaultRunTimeout
	}
	return context.WithTimeout(parent, timeout)
}

func (s *httpServer) writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type runRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
	TimeoutMs int    `json:"timeout_ms"`
}

func (r *runRequest) ensureSessionID() string {
	if r.SessionID == "" {
		r.SessionID = fmt.Sprintf("session-%d", time.Now().UnixNano())
	}
	return r.SessionID
}

type runResponse struct {
	SessionID  string              `json:"session_id"`
	Output     string              `json:"output"`
	StopReason string              `json:"stop_reason"`
	Usage      modelpkg.Usage      `json:"usage"`
	ToolCalls  []modelpkg.ToolCall `json:"tool_calls"`
}

type errorResponse struct {
	Error string `json:"error"`
}
