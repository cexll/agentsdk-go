package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/model"
)

const (
	defaultAddr         = ":8081"
	defaultSessions     = 10
	defaultModelLatency = 50 * time.Millisecond
	maxBodyBytes        = 1 << 20
)

// This example demonstrates the Runtime concurrency rule:
// - Same SessionID: serialized (concurrent calls may return api.ErrConcurrentExecution).
// - Different SessionID: can run concurrently.
//
// In HTTP servers, do NOT reuse one global SessionID across requests. This example uses a pool of
// 10 SessionIDs to safely process many concurrent HTTP requests without hitting concurrency
// conflicts.
//
// Run:
//
//	go run examples/06-v0.4.0-features/concurrent_http.go
//
// Benchmark (GET by default):
//
//	ab -n 1000 -c 50 http://localhost:8081/v1/run
//
// Optional: force a specific session_id (to observe per-session serialization):
//
//	ab -n 200 -c 50 "http://localhost:8081/v1/run?session_id=demo"
func main() {
	logger := log.New(os.Stdout, "", log.LstdFlags|log.Lmicroseconds)

	addr := envOr("AGENTSDK_CONCURRENT_HTTP_ADDR", defaultAddr)
	sessionCount := envIntOr("AGENTSDK_CONCURRENT_HTTP_SESSIONS", defaultSessions)
	modelLatency := envDurationOr("AGENTSDK_CONCURRENT_HTTP_MODEL_LATENCY", defaultModelLatency)

	projectRoot, err := api.ResolveProjectRoot()
	if err != nil {
		logger.Printf("resolve project root failed: %v (falling back to .)", err)
		projectRoot = "."
	}

	rt, err := api.New(context.Background(), api.Options{
		EntryPoint:          api.EntryPointPlatform,
		ProjectRoot:         projectRoot,
		Model:               sleepEchoModel{latency: modelLatency},
		MaxIterations:       1,
		Timeout:             30 * time.Second,
		EnabledBuiltinTools: []string{},
		RulesEnabled:        boolPtr(false),
	})
	if err != nil {
		logger.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	srv := newHTTPServer(rt, sessionCount, logger)
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		logger.Printf("concurrent HTTP example listening on %s (session pool=%d)", addr, sessionCount)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Fatalf("server stopped unexpectedly: %v", err)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		logger.Printf("graceful shutdown failed: %v", err)
	}
	logger.Println("server exited cleanly")
}

type httpServer struct {
	rt       *api.Runtime
	sessions chan string
	logger   *log.Logger
	reqID    atomic.Uint64
}

func newHTTPServer(rt *api.Runtime, sessionCount int, logger *log.Logger) *httpServer {
	if sessionCount <= 0 {
		sessionCount = defaultSessions
	}
	if logger == nil {
		logger = log.New(io.Discard, "", 0)
	}

	pool := make(chan string, sessionCount)
	for i := 0; i < sessionCount; i++ {
		pool <- fmt.Sprintf("session-%02d", i)
	}

	return &httpServer{
		rt:       rt,
		sessions: pool,
		logger:   logger,
	}
}

func (s *httpServer) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/health", s.handleHealth)
	mux.HandleFunc("/v1/run", s.handleRun)
}

func (s *httpServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "only GET supported"})
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *httpServer) handleRun(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, errorResponse{Error: "only GET/POST supported"})
		return
	}

	start := time.Now()
	reqID := s.reqID.Add(1)

	prompt, sessionID, err := s.parseRunRequest(r)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, errorResponse{Error: err.Error(), RequestID: reqID})
		return
	}

	fromPool := false
	if sessionID == "" {
		// Avoid api.ErrConcurrentExecution by never using the same SessionID concurrently:
		// take a SessionID from a fixed-size pool and return it when done.
		select {
		case sessionID = <-s.sessions:
			fromPool = true
		case <-r.Context().Done():
			writeJSON(w, http.StatusRequestTimeout, errorResponse{
				Error:     r.Context().Err().Error(),
				RequestID: reqID,
			})
			return
		}
	}
	if fromPool {
		defer func() { s.sessions <- sessionID }()
	}

	resp, err := s.rt.Run(r.Context(), api.Request{
		Prompt:    prompt,
		SessionID: sessionID,
	})
	if err != nil {
		status := http.StatusBadGateway
		if errors.Is(err, api.ErrConcurrentExecution) {
			status = http.StatusConflict
		} else if errors.Is(err, context.DeadlineExceeded) {
			status = http.StatusGatewayTimeout
		}
		s.logger.Printf("req=%d session=%q error=%v", reqID, sessionID, err)
		writeJSON(w, status, errorResponse{Error: err.Error(), RequestID: reqID, SessionID: sessionID})
		return
	}

	output := ""
	if resp != nil && resp.Result != nil {
		output = resp.Result.Output
	}

	s.logger.Printf("req=%d session=%q ok duration=%s", reqID, sessionID, time.Since(start))
	writeJSON(w, http.StatusOK, runResponse{
		SessionID: sessionID,
		Output:    output,
	})
}

func (s *httpServer) parseRunRequest(r *http.Request) (prompt, sessionID string, _ error) {
	if r.Method == http.MethodGet {
		q := r.URL.Query()
		prompt = strings.TrimSpace(q.Get("prompt"))
		sessionID = strings.TrimSpace(q.Get("session_id"))
		if prompt == "" {
			prompt = "ping"
		}
		return prompt, sessionID, nil
	}

	var req runRequest
	if err := decodeJSON(r, &req); err != nil {
		return "", "", err
	}
	prompt = strings.TrimSpace(req.Prompt)
	if prompt == "" {
		return "", "", errors.New("prompt is required")
	}
	return prompt, strings.TrimSpace(req.SessionID), nil
}

type runRequest struct {
	Prompt    string `json:"prompt"`
	SessionID string `json:"session_id"`
}

type runResponse struct {
	SessionID string `json:"session_id"`
	Output    string `json:"output"`
}

type errorResponse struct {
	Error     string `json:"error"`
	RequestID uint64 `json:"request_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

func decodeJSON(r *http.Request, dest any) error {
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
	if err := dec.Decode(&struct{}{}); err != io.EOF {
		return errors.New("request body must contain a single JSON object")
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, payload any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(payload)
}

type sleepEchoModel struct {
	latency time.Duration
}

func (m sleepEchoModel) Complete(ctx context.Context, req model.Request) (*model.Response, error) {
	if m.latency > 0 {
		timer := time.NewTimer(m.latency)
		select {
		case <-timer.C:
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		}
	}

	lastUser := ""
	for i := len(req.Messages) - 1; i >= 0; i-- {
		if req.Messages[i].Role == "user" {
			lastUser = strings.TrimSpace(req.Messages[i].Content)
			break
		}
	}
	if lastUser == "" {
		lastUser = "ok"
	}

	return &model.Response{
		Message: model.Message{
			Role:    "assistant",
			Content: "ok: " + lastUser,
		},
		StopReason: "end_turn",
	}, nil
}

func (m sleepEchoModel) CompleteStream(ctx context.Context, req model.Request, cb model.StreamHandler) error {
	resp, err := m.Complete(ctx, req)
	if err != nil {
		return err
	}
	if cb == nil {
		return nil
	}
	return cb(model.StreamResult{Final: true, Response: resp})
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func envIntOr(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	n, err := strconv.Atoi(raw)
	if err != nil {
		return fallback
	}
	return n
}

func envDurationOr(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	d, err := time.ParseDuration(raw)
	if err != nil {
		return fallback
	}
	return d
}

func boolPtr(v bool) *bool { return &v }
