package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

const (
	defaultAddr        = ":8080"
	defaultModel       = "claude-3-5-sonnet-20241022"
	defaultRunTimeout  = 45 * time.Second
	defaultMaxSessions = 500
	minimalConfig      = "version: v0.0.1\ndescription: agentsdk-go CLI example\nenvironment: {}\n"
)

func main() {
	projectRoot, cleanup, err := resolveProjectRoot()
	if err != nil {
		log.Fatalf("init project root: %v", err)
	}
	if cleanup != nil {
		defer cleanup()
	}

	addr := getEnv("AGENTSDK_HTTP_ADDR", defaultAddr)
	modelName := getEnv("AGENTSDK_MODEL", defaultModel)
	defaultTimeout := getDuration("AGENTSDK_DEFAULT_TIMEOUT", defaultRunTimeout)
	maxSessions := getInt("AGENTSDK_MAX_SESSIONS", defaultMaxSessions)

	mode := api.ModeContext{
		EntryPoint: api.EntryPointPlatform,
		Platform: &api.PlatformContext{
			Organization: "agentsdk-go",
			Project:      "http-example",
			Environment:  "dev",
		},
	}

	opts := api.Options{
		EntryPoint:   api.EntryPointPlatform,
		ProjectRoot:  projectRoot,
		Mode:         mode,
		ModelFactory: &modelpkg.AnthropicProvider{ModelName: modelName},
		MaxSessions:  maxSessions,
	}

	rt, err := api.New(context.Background(), opts)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	server := &http.Server{
		Addr:              addr,
		Handler:           buildMux(rt, mode, defaultTimeout),
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

func buildMux(rt *api.Runtime, mode api.ModeContext, defaultTimeout time.Duration) *http.ServeMux {
	srv := &httpServer{
		runtime:        rt,
		mode:           mode,
		defaultTimeout: defaultTimeout,
	}
	mux := http.NewServeMux()
	srv.registerRoutes(mux)
	return mux
}

func getEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}

func getInt(key string, fallback int) int {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	val, err := strconv.Atoi(raw)
	if err != nil || val <= 0 {
		return fallback
	}
	return val
}

func getDuration(key string, fallback time.Duration) time.Duration {
	raw := strings.TrimSpace(os.Getenv(key))
	if raw == "" {
		return fallback
	}
	if dur, err := time.ParseDuration(raw); err == nil {
		return dur
	}
	if ms, err := strconv.Atoi(raw); err == nil {
		return time.Duration(ms) * time.Millisecond
	}
	return fallback
}

func resolveProjectRoot() (string, func(), error) {
	if root := strings.TrimSpace(os.Getenv("AGENTSDK_PROJECT_ROOT")); root != "" {
		return root, nil, nil
	}
	tmp, err := os.MkdirTemp("", "agentsdk-http-*")
	if err != nil {
		return "", nil, err
	}
	cleanup := func() { _ = os.RemoveAll(tmp) }
	if err := scaffoldMinimalConfig(tmp); err != nil {
		cleanup()
		return "", nil, err
	}
	return tmp, cleanup, nil
}

func scaffoldMinimalConfig(root string) error {
	claudeDir := filepath.Join(root, ".claude")
	if err := os.MkdirAll(claudeDir, 0o755); err != nil {
		return err
	}
	configPath := filepath.Join(claudeDir, "config.yaml")
	if _, err := os.Stat(configPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	return os.WriteFile(configPath, []byte(minimalConfig), 0o644)
}
