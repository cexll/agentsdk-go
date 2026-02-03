package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cexll/agentsdk-go/pkg/api"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	provider := &modelpkg.AnthropicProvider{ModelName: "claude-sonnet-4-5-20250929"}

	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	// 测试 Task 系统：创建多个任务并设置依赖关系
	prompt := `请帮我测试 Task 系统的完整功能：

1. 使用 TaskCreate 创建 3 个任务：
   - 任务 A: "读取配置文件" (subject: "Read config", description: "Load settings.json", activeForm: "Reading config")
   - 任务 B: "验证配置" (subject: "Validate config", description: "Check config validity", activeForm: "Validating config")
   - 任务 C: "启动服务" (subject: "Start service", description: "Initialize server", activeForm: "Starting service")

2. 使用 TaskUpdate 设置依赖关系：
   - 任务 B 依赖任务 A (B blockedBy A)
   - 任务 C 依赖任务 B (C blockedBy B)

3. 使用 TaskList 列出所有任务和依赖关系

4. 使用 TaskUpdate 将任务 A 标记为 in_progress，然后标记为 completed

5. 再次使用 TaskList 验证任务 B 是否自动解除阻塞

6. 使用 TaskGet 获取任务 B 的详细信息

请按顺序执行这些操作，并在每步后说明结果。`

	resp, err := rt.Run(context.Background(), api.Request{
		Prompt: prompt,
	})
	if err != nil {
		log.Fatalf("run: %v", err)
	}

	if resp.Result != nil {
		fmt.Println("\n=== Task System Test Result ===")
		fmt.Println(resp.Result.Output)
	}
}
