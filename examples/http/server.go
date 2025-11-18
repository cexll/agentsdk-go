package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
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
	mode           api.ModeContext
	defaultTimeout time.Duration
}

func (s *httpServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/healthz", s.handleHealth)
	mux.HandleFunc("/v1/run", s.handleRun)
	mux.HandleFunc("/v1/run/stream", s.handleStream)
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
	if strings.TrimSpace(req.Prompt) == "" {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{"prompt is required"})
		return
	}

	ctx, cancel := s.requestContext(r.Context(), req.TimeoutMs)
	defer cancel()

	resp, err := s.runtime.Run(ctx, req.toAPIRequest(s.mode))
	if err != nil {
		s.writeJSON(w, http.StatusBadGateway, errorResponse{err.Error()})
		return
	}
	payload, err := buildRunResponse(resp, req.SessionID)
	if err != nil {
		s.writeJSON(w, http.StatusInternalServerError, errorResponse{err.Error()})
		return
	}
	s.writeJSON(w, http.StatusOK, payload)
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
	if strings.TrimSpace(req.Prompt) == "" {
		s.writeJSON(w, http.StatusBadRequest, errorResponse{"prompt is required"})
		return
	}

	flusher, ok := w.(http.Flusher)
	if !ok {
		s.writeJSON(w, http.StatusInternalServerError, errorResponse{"streaming unsupported"})
		return
	}

	ctx, cancel := s.requestContext(r.Context(), req.TimeoutMs)
	defer cancel()

	events, err := s.runtime.RunStream(ctx, req.toAPIRequest(s.mode))
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
	Prompt        string            `json:"prompt"`
	SessionID     string            `json:"session_id"`
	TimeoutMs     int               `json:"timeout_ms"`
	Tags          map[string]string `json:"tags"`
	Traits        []string          `json:"traits"`
	Channels      []string          `json:"channels"`
	Metadata      map[string]any    `json:"metadata"`
	ToolWhitelist []string          `json:"tool_whitelist"`
}

func (r runRequest) toAPIRequest(base api.ModeContext) api.Request {
	req := api.Request{
		Prompt:        r.Prompt,
		SessionID:     r.SessionID,
		Traits:        copyStrings(r.Traits),
		Channels:      copyStrings(r.Channels),
		ToolWhitelist: copyStrings(r.ToolWhitelist),
		Tags:          copyStringMap(r.Tags),
		Metadata:      copyAnyMap(r.Metadata),
	}
	req.Mode = base
	return req
}

type runResponse struct {
	SessionID  string              `json:"session_id"`
	Output     string              `json:"output"`
	StopReason string              `json:"stop_reason"`
	Usage      modelpkg.Usage      `json:"usage"`
	Tags       map[string]string   `json:"tags"`
	ToolCalls  []modelpkg.ToolCall `json:"tool_calls"`
	Sandbox    api.SandboxReport   `json:"sandbox"`
}

func buildRunResponse(resp *api.Response, sessionID string) (*runResponse, error) {
	if resp == nil || resp.Result == nil {
		return nil, errors.New("agent response is empty")
	}
	tags := resp.Tags
	if tags == nil {
		tags = map[string]string{}
	}
	return &runResponse{
		SessionID:  sessionID,
		Output:     resp.Result.Output,
		StopReason: resp.Result.StopReason,
		Usage:      resp.Result.Usage,
		ToolCalls:  resp.Result.ToolCalls,
		Tags:       tags,
		Sandbox:    resp.SandboxSnapshot,
	}, nil
}

type errorResponse struct {
	Error string `json:"error"`
}

func copyStrings(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	copy(out, in)
	return out
}

func copyStringMap(in map[string]string) map[string]string {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

func copyAnyMap(in map[string]any) map[string]any {
	if len(in) == 0 {
		return nil
	}
	out := make(map[string]any, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

