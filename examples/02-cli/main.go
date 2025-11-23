package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

const defaultModel = "claude-sonnet-4-5-20250929"

func main() {
	sessionID := flag.String("session-id", envOrDefault("SESSION_ID", "demo-session"), "session identifier to keep chat history")
	projectRoot := flag.String("project-root", ".", "project root directory (default: current directory)")
	enableMCP := flag.Bool("enable-mcp", true, "enable MCP servers from .claude/settings.json (auto-loaded)")
	flag.Parse()

	// Resolve project root path
	absRoot, err := filepath.Abs(*projectRoot)
	if err != nil {
		log.Fatalf("resolve project root: %v", err)
	}

	provider := &modelpkg.AnthropicProvider{ModelName: defaultModel}

	opts := api.Options{
		EntryPoint:   api.EntryPointCLI,
		ProjectRoot:  absRoot,
		ModelFactory: provider,
	}

	if !*enableMCP {
		// Empty slice tells the SDK to skip auto-loading MCP servers from settings.
		opts.MCPServers = []string{}
	}

	rt, err := api.New(context.Background(), opts)
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("Type 'exit' to quit.")
	if *enableMCP {
		fmt.Println("MCP auto-load enabled; SDK will read .claude/settings.json. Use --enable-mcp=false to disable.")
	}
	fmt.Println()

	scanner := bufio.NewScanner(os.Stdin)
	for {
		fmt.Print("You> ")
		if !scanner.Scan() {
			break
		}
		input := strings.TrimSpace(scanner.Text())
		if input == "" {
			continue
		}
		if input == "exit" {
			break
		}

		resp, err := rt.Run(context.Background(), api.Request{
			Prompt:    input,
			SessionID: *sessionID,
		})
		if err != nil {
			log.Fatalf("run: %v", err)
		}
		if resp.Result != nil && resp.Result.Output != "" {
			fmt.Printf("\nAssistant> %s\n\n", resp.Result.Output)
		}
	}

	if err := scanner.Err(); err != nil {
		log.Printf("read input: %v", err)
	}
}

func envOrDefault(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}
