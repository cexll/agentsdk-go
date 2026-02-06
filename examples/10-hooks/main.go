package main

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"runtime"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/core/events"
	"github.com/cexll/agentsdk-go/pkg/core/hooks"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	// 获取当前源文件所在目录作为示例根目录
	_, currentFile, _, _ := runtime.Caller(0)
	exampleDir := filepath.Dir(currentFile)
	scriptsDir := filepath.Join(exampleDir, "scripts")

	// 方式一：通过 TypedHooks 代码配置 (推荐用于动态配置)
	//
	// 新特性:
	//   - Async: 异步执行，不阻塞主流程
	//   - Once:  每个 session 只执行一次
	//   - Timeout: 自定义超时 (默认 600s)
	//   - StatusMessage: 执行时显示的状态信息
	typedHooks := []hooks.ShellHook{
		{
			Event:   events.PreToolUse,
			Command: filepath.Join(scriptsDir, "pre_tool.sh"),
		},
		{
			Event:   events.PostToolUse,
			Command: filepath.Join(scriptsDir, "post_tool.sh"),
			Async:   true, // 异步执行，不阻塞工具调用
		},
	}

	// 创建 provider
	provider := &modelpkg.AnthropicProvider{
		ModelName: "claude-sonnet-4-5-20250514",
	}

	// 初始化运行时
	// hooks 会在 agent 执行工具时自动触发，无需手动 Publish
	// 方式二：通过 .claude/settings.json 配置 hooks (见 .claude/settings.json)
	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		ProjectRoot:  exampleDir,
		TypedHooks:   typedHooks,
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	fmt.Println("=== Hooks 示例 ===")
	fmt.Println("已注册 hooks:")
	fmt.Println("  - PreToolUse  (同步，验证工具调用)")
	fmt.Println("  - PostToolUse (异步，记录执行结果)")
	fmt.Println()
	fmt.Println("退出码语义 (Claude Code 规范):")
	fmt.Println("  exit 0  = 成功，解析 stdout JSON")
	fmt.Println("  exit 2  = 阻塞错误，stderr 为错误信息")
	fmt.Println("  其他    = 非阻塞，记录 stderr 并继续")
	fmt.Println()

	// 执行 agent 调用 - hooks 会自动触发
	fmt.Println(">>> 执行 Agent 调用")
	fmt.Println("    当 agent 调用工具时，hooks 会自动执行")
	fmt.Println()

	resp, err := rt.Run(context.Background(), api.Request{
		Prompt: "请用 pwd 命令显示当前目录",
	})
	if err != nil {
		log.Printf("run error: %v", err)
	} else if resp.Result != nil {
		fmt.Printf("\n输出: %s\n", resp.Result.Output)
	}

	fmt.Println("\n=== 示例结束 ===")
}
