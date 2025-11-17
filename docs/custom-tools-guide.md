# 如何自定义 Tools 和 System Prompt - 快速参考

## 🚀 快速开始

### 最小示例（5 步）

```go
package main

import (
    "context"
    "fmt"
    "github.com/cexll/agentsdk-go/pkg/agent"
    "github.com/cexll/agentsdk-go/pkg/model/anthropic"
    "github.com/cexll/agentsdk-go/pkg/session"
    "github.com/cexll/agentsdk-go/pkg/tool"
)

func main() {
    ctx := context.Background()

    // 1️⃣ 创建模型 + 设置 System Prompt
    model := anthropic.NewSDKModel(apiKey, "claude-3-5-sonnet-20241022", 2048)
    model.SetSystem("你是一个专业助手，使用提供的工具完成任务")

    // 2️⃣ 创建 Session
    sess, _ := session.NewMemorySession("my-session")

    // 3️⃣ 创建 Agent
    ag, _ := agent.New(
        agent.Config{Name: "assistant"},
        agent.WithModel(model),
        agent.WithSession(sess),
    )

    // 4️⃣ 注册工具
    ag.AddTool(&MyCustomTool{})

    // 5️⃣ 运行
    result, _ := ag.Run(ctx, "你的任务")
    fmt.Println(result.Output)
}
```

---

## 🛠️ 自定义工具模板

```go
type MyTool struct{}

func (t *MyTool) Name() string {
    return "my_tool"
}

func (t *MyTool) Description() string {
    return "工具描述（LLM 会看到）"
}

func (t *MyTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "param1": map[string]interface{}{
                "type":        "string",
                "description": "参数说明",
            },
        },
        Required: []string{"param1"},
    }
}

func (t *MyTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    value := params["param1"].(string)

    // 你的业务逻辑

    return &tool.ToolResult{
        Success: true,
        Data:    map[string]interface{}{"result": value},
    }, nil
}
```

---

## 📋 核心 API

### Model 设置

```go
// 创建模型
model := anthropic.NewSDKModel(apiKey, modelName, maxTokens)

// 自定义 baseURL（如 Kimi）
model := anthropic.NewSDKModelWithBaseURL(apiKey, modelName, baseURL, maxTokens)

// 设置 System Prompt
model.SetSystem("你的系统提示词")
```

### Agent 配置

```go
ag, _ := agent.New(
    agent.Config{
        Name:        "agent-name",
        Description: "agent 描述",
        DefaultContext: agent.RunContext{
            SessionID:     "session-id",
            MaxIterations: 10,
        },
    },
    agent.WithModel(model),       // 必需
    agent.WithSession(session),   // 必需
    agent.WithTelemetry(tm),      // 可选
)
```

### 工具注册

```go
// 自定义工具
ag.AddTool(&MyTool{})

// 内置工具
import "github.com/cexll/agentsdk-go/pkg/tool/builtin"

ag.AddTool(toolbuiltin.NewBashTool())
ag.AddTool(toolbuiltin.NewFileTool())
```

---

## ⚡ 常见用例

### 1. 计算器工具

```go
type CalculatorTool struct{}

func (t *CalculatorTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    op := params["operation"].(string)
    a := params["a"].(float64)
    b := params["b"].(float64)

    var result float64
    switch op {
    case "add":
        result = a + b
    case "multiply":
        result = a * b
    }

    return &tool.ToolResult{
        Success: true,
        Data:    map[string]interface{}{"result": result},
    }, nil
}
```

### 2. HTTP API 调用工具

```go
type APITool struct {
    httpClient *http.Client
}

func (t *APITool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    url := params["url"].(string)

    resp, err := t.httpClient.Get(url)
    if err != nil {
        return &tool.ToolResult{
            Success: false,
            Error:   fmt.Errorf("API call failed: %w", err),
        }, nil
    }
    defer resp.Body.Close()

    data, _ := io.ReadAll(resp.Body)
    return &tool.ToolResult{
        Success: true,
        Data:    string(data),
    }, nil
}
```

### 3. 数据库查询工具

```go
type DBTool struct {
    db *sql.DB
}

func (t *DBTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    query := params["query"].(string)

    rows, err := t.db.QueryContext(ctx, query)
    if err != nil {
        return &tool.ToolResult{
            Success: false,
            Error:   fmt.Errorf("query failed: %w", err),
        }, nil
    }
    defer rows.Close()

    // 处理结果...

    return &tool.ToolResult{
        Success: true,
        Data:    results,
    }, nil
}
```

---

## 🎯 System Prompt 最佳实践

### 结构化模板

```go
const systemPrompt = `你是 [角色名称]。

## 核心能力
- [能力 1]：使用 [工具名] 实现 [功能]
- [能力 2]：...

## 行为准则
- 始终使用工具而不是凭记忆
- 提供清晰的推理步骤
- 结果要包含单位和说明

## 限制
- 不要执行危险命令
- 不要访问敏感文件
- 拒绝违规请求

## 输出格式
请按以下格式回复：
1. 分析任务
2. 使用工具
3. 总结结果`
```

### 示例：专业领域助手

```go
// 数据分析助手
const dataAnalystPrompt = `你是专业的数据分析助手。

工具：
- calculator: 数学计算
- file_operation: 读取 CSV/JSON
- bash_execute: 运行数据处理脚本

原则：
- 先探索数据结构
- 验证数据有效性
- 给出可视化建议`

// 代码审查助手
const codeReviewPrompt = `你是严格的代码审查员。

工具：
- file_operation: 读取代码文件
- bash_execute: 运行测试

检查项：
- 代码风格一致性
- 安全漏洞
- 性能问题
- 测试覆盖率`
```

---

## ⚠️ 注意事项

### 必需项

- ✅ 必须设置 Model：`agent.WithModel(model)`
- ✅ 必须设置 Session：`agent.WithSession(sess)`
- ✅ ToolResult.Error 是 `error` 类型，不是 `string`

### 错误处理

```go
func (t *MyTool) Execute(ctx context.Context, params map[string]interface{}) (*tool.ToolResult, error) {
    // ❌ 错误：Error 不能是 string
    return &tool.ToolResult{
        Success: false,
        Error:   "something went wrong",  // 错误！
    }, nil

    // ✅ 正确：Error 是 error 类型
    return &tool.ToolResult{
        Success: false,
        Error:   fmt.Errorf("something went wrong"),  // 正确
    }, nil

    // ✅ 也可以：返回 error
    return nil, fmt.Errorf("fatal error")
}
```

### 工具 Schema 类型

支持的 JSON Schema 类型：
- `string`
- `number` / `integer`
- `boolean`
- `object`
- `array`
- `enum`（限制可选值）

```go
Schema: &tool.JSONSchema{
    Type: "object",
    Properties: map[string]interface{}{
        "format": map[string]interface{}{
            "type": "string",
            "enum": []string{"json", "xml", "csv"},  // 限制选项
        },
        "count": map[string]interface{}{
            "type":    "integer",
            "minimum": 1,
            "maximum": 100,
        },
    },
}
```

---

## 📖 完整示例

查看 `examples/custom-tools/` 获取：
- **main.go**: 完整可运行示例
- **README.md**: 详细文档
- **run.sh**: 快速运行脚本

运行方式：
```bash
export ANTHROPIC_API_KEY="your-key"
cd examples/custom-tools
./run.sh
```

---

## 🔗 相关文档

- [Tool 接口定义](../../pkg/tool/tool.go)
- [Agent 配置](../../pkg/agent/)
- [Model 接口](../../pkg/model/)
- [Session 管理](../../pkg/session/)
- [内置工具](../../pkg/tool/builtin/)
