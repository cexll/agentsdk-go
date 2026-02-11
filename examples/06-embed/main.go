package main

import (
	"context"
	"embed"
	"fmt"
	"log"
	"os"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/model"
)

//go:embed .claude
var claudeFS embed.FS

func main() {
	// 从环境变量获取 API Key
	apiKey := os.Getenv("ANTHROPIC_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("ANTHROPIC_AUTH_TOKEN")
	}
	if apiKey == "" {
		log.Fatal("请设置 ANTHROPIC_API_KEY 或 ANTHROPIC_AUTH_TOKEN 环境变量")
	}

	// 创建 Anthropic provider
	provider := &model.AnthropicProvider{
		APIKey:    apiKey,
		ModelName: "claude-sonnet-4-5-20250929",
	}

	// 创建 Runtime，传入嵌入的文件系统
	runtime, err := api.New(context.Background(), api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EmbedFS:      claudeFS, // 关键：传入嵌入的 .claude 目录
	})
	if err != nil {
		log.Fatalf("创建 runtime 失败: %v", err)
	}
	defer runtime.Close()

	fmt.Println("=== 嵌入文件系统示例 ===")
	fmt.Println("此示例演示如何将 .claude 目录嵌入到二进制文件中")
	fmt.Println()
	fmt.Println("嵌入的配置和技能将在运行时自动加载")
	fmt.Println("你仍然可以通过创建本地 .claude/settings.local.json 来覆盖嵌入的配置")
	fmt.Println()

	// 运行一个简单的测试
	result, err := runtime.Run(context.Background(), api.Request{
		Prompt:    "列出当前目录",
		SessionID: "embed-demo",
	})
	if err != nil {
		log.Fatalf("运行失败: %v", err)
	}

	fmt.Println("运行结果:")
	if result.Result != nil {
		fmt.Println(result.Result.Output)
	}
}
