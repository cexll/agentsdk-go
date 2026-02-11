//go:build ignore
// +build ignore

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
	fmt.Println("=== 测试文件系统优先级 ===")
	fmt.Println()

	// 创建一个假的 provider（不会真正调用 API）
	provider := &model.AnthropicProvider{
		APIKey:    "sk-test-key",
		ModelName: "claude-sonnet-4-5-20250929",
	}

	// 创建 Runtime，传入嵌入的文件系统
	runtime, err := api.New(context.Background(), api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EmbedFS:      claudeFS,
	})
	if err != nil {
		log.Fatalf("创建 runtime 失败: %v", err)
	}
	defer runtime.Close()

	fmt.Println("✓ Runtime 创建成功")
	fmt.Println("✓ 嵌入的 .claude 目录已加载")
	fmt.Println()
	fmt.Println("说明:")
	fmt.Println("- 如果本地存在 .claude/settings.local.json，它会覆盖嵌入的配置")
	fmt.Println("- 如果本地不存在，则使用嵌入的 .claude/settings.json")
	fmt.Println()

	// 检查是否存在本地覆盖文件
	if _, err := os.Stat(".claude/settings.local.json"); err == nil {
		fmt.Println("✓ 检测到本地 .claude/settings.local.json，将优先使用本地配置")
	} else {
		fmt.Println("✓ 未检测到本地覆盖文件，使用嵌入的配置")
	}
}
