# 自定义工具与内置工具选择指南

本文档说明如何在 agentsdk-go 中选择性启用内置工具并注册自定义工具，兼顾向后兼容与简单性。

## 工具接口

所有自定义工具必须实现 `tool.Tool`：

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

## 选项字段与优先级

- `Options.Tools []tool.Tool`  
  旧字段，**非空时完全接管工具集**，忽略其他工具相关选项（保持向后兼容）。
- `Options.EnabledBuiltinTools []string`  
  控制内置工具白名单（大小写不敏感）：  
  - `nil`（默认）：注册全部内置工具  
  - 空切片：禁用全部内置工具  
  - 非空：仅启用列出的内置工具  
  可用名称（小写/下划线）：`bash`, `file_read`, `file_write`, `file_edit`, `grep`, `glob`, `web_fetch`, `web_search`, `bash_output`, `todo_write`, `skill`, `slash_command`, `task`（Task 仅在 CLI/Platform 时自动可用）。
- `Options.CustomTools []tool.Tool`  
  当 `Tools` 为空时，附加自定义工具（会跳过 nil）。

优先级：`Tools` > (`EnabledBuiltinTools` 过滤 + `CustomTools` 追加)。

## 内置工具白名单示例

```go
opts := api.Options{
    ProjectRoot:         ".",
    ModelFactory:        provider,
    EnabledBuiltinTools: []string{"bash", "grep", "file_read"}, // 仅启用这几个
}
rt, _ := api.New(context.Background(), opts)
```

## 禁用全部内置工具

```go
opts := api.Options{
    ProjectRoot:         ".",
    ModelFactory:        provider,
    EnabledBuiltinTools: []string{},      // 不注册任何内置工具
    CustomTools:         []tool.Tool{&EchoTool{}}, // 只用自定义
}
```

## 追加自定义工具

```go
opts := api.Options{
    ProjectRoot:  ".",
    ModelFactory: provider,
    // nil 表示内置全开
    CustomTools: []tool.Tool{&EchoTool{}},
}
```

## 组合（部分内置 + 自定义）

```go
opts := api.Options{
    ProjectRoot:         ".",
    ModelFactory:        provider,
    EnabledBuiltinTools: []string{"bash", "file_read"},
    CustomTools:         []tool.Tool{&CalculatorTool{}},
}
```

## 自定义工具示例：Echo

```go
type EchoTool struct{}

func (t *EchoTool) Name() string        { return "echo" }
func (t *EchoTool) Description() string { return "返回输入的文本" }
func (t *EchoTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]any{
            "text": map[string]any{"type": "string", "description": "要回显的文本"},
        },
        Required: []string{"text"},
    }
}
func (t *EchoTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    return &tool.ToolResult{Output: fmt.Sprint(params["text"])}, nil
}
```

## 注意事项

- 名称匹配大小写不敏感，`-` / 空格会被视为 `_`。建议直接使用文档列出的下划线小写形式。
- Task 工具仅在 CLI/Platform 入口自动附加；CI 入口不会注册。
- MCP 工具注册独立于上述配置，始终全量注册。  
- 重名工具会保留第一个并记录警告日志，避免同名冲突。
