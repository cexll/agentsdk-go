package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

const (
	defaultAddr       = ":8080"
	defaultModel      = "claude-3-5-sonnet-20241022"
	defaultRunTimeout = 60 * time.Minute // 60分钟，适配 codex 等长时间任务
)

func main() {
	addr := envOr("AGENTSDK_HTTP_ADDR", defaultAddr)
	modelName := envOr("AGENTSDK_MODEL", defaultModel)
	requireAPIKey()

	projectRoot, err := api.ResolveProjectRoot()
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	runtime, err := api.New(context.Background(), api.Options{
		EntryPoint:   api.EntryPointPlatform,
		ProjectRoot:  projectRoot,
		ModelFactory: &modelpkg.AnthropicProvider{ModelName: modelName},
		Timeout:      defaultRunTimeout,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer runtime.Close()

	// Concurrency note:
	// Runtime serializes execution per SessionID. In HTTP servers, avoid reusing a single shared
	// session_id across concurrent requests, or you may hit api.ErrConcurrentExecution. Use a
	// request-id (stateless) or a user/client session-id (stateful) to isolate work.
	staticDir := filepath.Join(projectRoot, "examples", "03-http", "static")
	srv := &httpServer{
		runtime:        runtime,
		defaultTimeout: defaultRunTimeout,
		staticDir:      staticDir,
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)

	server := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	go func() {
		log.Printf("HTTP agent server listening on %s", addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Fatalf("server stopped unexpectedly: %v", err)
		}
	}()

	sigCtx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	<-sigCtx.Done()

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Fatalf("graceful shutdown failed: %v", err)
	}
	log.Println("server exited cleanly")
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func requireAPIKey() {
	if strings.TrimSpace(os.Getenv("ANTHROPIC_AUTH_TOKEN")) == "" && strings.TrimSpace(os.Getenv("ANTHROPIC_API_KEY")) == "" {
		log.Fatal("ANTHROPIC_AUTH_TOKEN or ANTHROPIC_API_KEY is required")
	}
}
