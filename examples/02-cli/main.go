package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

const defaultModel = "claude-sonnet-4-5-20250929"

func main() {
	sessionID := flag.String("session-id", envOrDefault("SESSION_ID", "demo-session"), "session identifier to keep chat history")
	settingsPath := flag.String("settings-path", "", "path to .claude/settings.json for sandbox/tools config")
	flag.Parse()

	provider := &modelpkg.AnthropicProvider{ModelName: defaultModel}

	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		SettingsPath: *settingsPath,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("Type 'exit' to quit.")

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
