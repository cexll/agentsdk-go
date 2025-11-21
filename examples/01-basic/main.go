package main

import (
	"context"
	"fmt"
	"log"

	"github.com/cexll/agentsdk-go/pkg/api"
	"github.com/cexll/agentsdk-go/pkg/middleware"
	modelpkg "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
	// 创建 Anthropic provider
	provider := &modelpkg.AnthropicProvider{ModelName: "claude-sonnet-4-5-20250929"}

	// 初始化运行时，使用默认配置
	traceMW := middleware.NewTraceMiddleware(".trace")
	rt, err := api.New(context.Background(), api.Options{
		ModelFactory: provider,
		Middleware:   []middleware.Middleware{traceMW},
	})
	if err != nil {
		log.Fatalf("build runtime: %v", err)
	}
	defer rt.Close()

	// 发起一次同步调用，固定提示词
	resp, err := rt.Run(context.Background(), api.Request{Prompt: "你好"})
	if err != nil {
		log.Fatalf("run: %v", err)
	}
	if resp.Result != nil {
		fmt.Println(resp.Result.Output)
	}
}
