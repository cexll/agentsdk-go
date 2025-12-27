//go:build !demo_simple && !demo_llm

package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
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

	ctx := context.Background()

	// 初始化 runtime（自动包含 AskUserQuestion 工具）
	runtime, err := api.New(ctx, api.Options{
		ProjectRoot:  ".",
		ModelFactory: provider,
		EntryPoint:   api.EntryPointCLI,
	})
	if err != nil {
		log.Fatalf("初始化 runtime 失败: %v", err)
	}
	defer runtime.Close()

	fmt.Println("=== AskUserQuestion 工具 Demo ===")
	fmt.Println()

	// 场景 1: 单个问题，单选
	fmt.Println("【场景 1】单个技术选型问题")
	fmt.Println("----------------------------------------")
	result1, err := runtime.Run(ctx, api.Request{
		Prompt: `你正在帮我设计一个新项目的技术架构。

使用 AskUserQuestion 工具询问我：
- 问题："我们应该使用哪种数据库？"
- Header: "数据库"
- 三个选项：
  1. PostgreSQL - 功能强大的关系型数据库
  2. MongoDB - 灵活的文档型数据库
  3. Redis - 高性能内存数据库
- 单选模式`,
		SessionID: "demo-1",
	})
	if err != nil {
		log.Printf("场景 1 执行出错: %v\n", err)
	} else if result1.Result != nil {
		fmt.Printf("输出:\n%s\n", result1.Result.Output)
	}
	fmt.Println()

	// 场景 2: 多个问题
	fmt.Println("【场景 2】配置部署环境（多个问题）")
	fmt.Println("----------------------------------------")
	result2, err := runtime.Run(ctx, api.Request{
		Prompt: `你正在帮我配置部署环境。

使用 AskUserQuestion 工具同时询问我三个问题：

1. "选择部署环境？"
   Header: "环境"
   选项：Staging（测试环境）、Production（生产环境）
   单选

2. "需要启用哪些功能？"
   Header: "功能"
   选项：缓存、监控、日志、备份
   多选

3. "选择部署区域？"
   Header: "区域"
   选项：US-East（美国东部）、EU-West（欧洲西部）
   单选`,
		SessionID: "demo-2",
	})
	if err != nil {
		log.Printf("场景 2 执行出错: %v\n", err)
	} else if result2.Result != nil {
		fmt.Printf("输出:\n%s\n", result2.Result.Output)
	}
	fmt.Println()

	// 场景 3: 多选问题
	fmt.Println("【场景 3】功能选择（多选）")
	fmt.Println("----------------------------------------")
	result3, err := runtime.Run(ctx, api.Request{
		Prompt: `你正在帮我配置项目功能。

使用 AskUserQuestion 工具询问我：
- 问题："需要集成哪些第三方服务？"
- Header: "集成服务"
- 四个选项：
  1. Stripe - 支付处理
  2. SendGrid - 邮件发送
  3. Twilio - 短信服务
  4. AWS S3 - 文件存储
- 多选模式（允许选择多个）`,
		SessionID: "demo-3",
	})
	if err != nil {
		log.Printf("场景 3 执行出错: %v\n", err)
	} else if result3.Result != nil {
		fmt.Printf("输出:\n%s\n", result3.Result.Output)
	}
	fmt.Println()

	// 场景 4: 实际决策场景
	fmt.Println("【场景 4】实际开发决策")
	fmt.Println("----------------------------------------")
	result4, err := runtime.Run(ctx, api.Request{
		Prompt: `我正在开发一个用户认证功能，但不确定应该使用哪种认证方式。

请先分析各种认证方式的优劣，然后使用 AskUserQuestion 工具询问我的选择偏好。
问题应该包含 2-3 个常见的认证方案作为选项。`,
		SessionID: "demo-4",
	})
	if err != nil {
		log.Printf("场景 4 执行出错: %v\n", err)
	} else if result4.Result != nil {
		fmt.Printf("输出:\n%s\n", result4.Result.Output)
	}
	fmt.Println()

	fmt.Println("=== Demo 完成 ===")
	fmt.Println()
	fmt.Println("注意：")
	fmt.Println("- 工具会返回问题结构，但不会等待实际用户输入")
	fmt.Println("- 在实际应用中，你需要实现 UI 来收集用户选择")
	fmt.Println("- 可以通过 'answers' 参数传递用户的选择结果")
}
