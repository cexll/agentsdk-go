# 自定义工具开发指南

本文档说明如何为 agentsdk-go 开发自定义工具。

## 工具接口

所有工具必须实现 `tool.Tool` 接口：

```go
type Tool interface {
    Name() string
    Description() string
    Schema() *JSONSchema
    Execute(ctx context.Context, params map[string]any) (*ToolResult, error)
}
```

### 接口方法说明

- `Name()` - 返回工具的唯一标识符，仅包含字母、数字和下划线
- `Description()` - 返回工具的功能描述，模型将根据此描述决定何时调用
- `Schema()` - 返回 JSON Schema，定义工具的输入参数
- `Execute()` - 执行工具逻辑，返回执行结果

## 基础示例

### 最简单的工具

```go
package main

import (
    "context"
    "github.com/cexll/agentsdk-go/pkg/tool"
)

type EchoTool struct{}

func (t *EchoTool) Name() string {
    return "echo"
}

func (t *EchoTool) Description() string {
    return "返回输入的文本"
}

func (t *EchoTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "text": map[string]interface{}{
                "type":        "string",
                "description": "要回显的文本",
            },
        },
        Required: []string{"text"},
    }
}

func (t *EchoTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    text := params["text"].(string)

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: text,
    }, nil
}
```

### 注册和使用

```go
package main

import (
    "context"
    "log"
    "os"

    "github.com/cexll/agentsdk-go/pkg/api"
    "github.com/cexll/agentsdk-go/pkg/model"
)

func main() {
    ctx := context.Background()

    // 创建模型提供者
    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    // 创建自定义工具
    echoTool := &EchoTool{}

    // 初始化运行时并注册工具
    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
        CustomTools:   []tool.Tool{echoTool},
    })
    if err != nil {
        log.Fatal(err)
    }
    defer runtime.Close()

    // 执行任务
    result, err := runtime.Run(ctx, api.Request{
        Prompt:    "使用 echo 工具回显 'Hello, World!'",
        SessionID: "demo",
    })
    if err != nil {
        log.Fatal(err)
    }

    log.Printf("结果: %s", result.Output)
}
```

## 实用示例

### 计算器工具

```go
package main

import (
    "context"
    "fmt"

    "github.com/cexll/agentsdk-go/pkg/tool"
)

type CalculatorTool struct{}

func (t *CalculatorTool) Name() string {
    return "calculator"
}

func (t *CalculatorTool) Description() string {
    return "执行基本数学运算（加、减、乘、除）"
}

func (t *CalculatorTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "operation": map[string]interface{}{
                "type":        "string",
                "enum":        []string{"add", "subtract", "multiply", "divide"},
                "description": "运算类型",
            },
            "a": map[string]interface{}{
                "type":        "number",
                "description": "第一个操作数",
            },
            "b": map[string]interface{}{
                "type":        "number",
                "description": "第二个操作数",
            },
        },
        Required: []string{"operation", "a", "b"},
    }
}

func (t *CalculatorTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    operation := params["operation"].(string)
    a := params["a"].(float64)
    b := params["b"].(float64)

    var result float64
    var err error

    switch operation {
    case "add":
        result = a + b
    case "subtract":
        result = a - b
    case "multiply":
        result = a * b
    case "divide":
        if b == 0 {
            return nil, fmt.Errorf("除数不能为零")
        }
        result = a / b
    default:
        return nil, fmt.Errorf("不支持的运算: %s", operation)
    }

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: fmt.Sprintf("%f %s %f = %f", a, operation, b, result),
    }, nil
}
```

### HTTP API 调用工具

```go
package main

import (
    "context"
    "encoding/json"
    "fmt"
    "io"
    "net/http"
    "time"

    "github.com/cexll/agentsdk-go/pkg/tool"
)

type HTTPTool struct {
    client *http.Client
}

func NewHTTPTool() *HTTPTool {
    return &HTTPTool{
        client: &http.Client{
            Timeout: 30 * time.Second,
        },
    }
}

func (t *HTTPTool) Name() string {
    return "http_get"
}

func (t *HTTPTool) Description() string {
    return "发送 HTTP GET 请求并返回响应内容"
}

func (t *HTTPTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "url": map[string]interface{}{
                "type":        "string",
                "description": "请求的 URL",
            },
            "headers": map[string]interface{}{
                "type":        "object",
                "description": "请求头（可选）",
            },
        },
        Required: []string{"url"},
    }
}

func (t *HTTPTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    url := params["url"].(string)

    req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
    if err != nil {
        return nil, fmt.Errorf("创建请求失败: %w", err)
    }

    // 添加自定义请求头
    if headers, ok := params["headers"].(map[string]interface{}); ok {
        for key, value := range headers {
            req.Header.Set(key, fmt.Sprint(value))
        }
    }

    resp, err := t.client.Do(req)
    if err != nil {
        return nil, fmt.Errorf("请求失败: %w", err)
    }
    defer resp.Body.Close()

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, fmt.Errorf("读取响应失败: %w", err)
    }

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: fmt.Sprintf("Status: %d\n\n%s", resp.StatusCode, string(body)),
        Metadata: map[string]any{
            "status_code": resp.StatusCode,
            "headers":     resp.Header,
        },
    }, nil
}
```

### 数据库查询工具

```go
package main

import (
    "context"
    "database/sql"
    "encoding/json"
    "fmt"

    "github.com/cexll/agentsdk-go/pkg/tool"
)

type DatabaseTool struct {
    db *sql.DB
}

func NewDatabaseTool(db *sql.DB) *DatabaseTool {
    return &DatabaseTool{db: db}
}

func (t *DatabaseTool) Name() string {
    return "db_query"
}

func (t *DatabaseTool) Description() string {
    return "执行只读 SQL 查询并返回结果"
}

func (t *DatabaseTool) Schema() *tool.JSONSchema {
    return &tool.JSONSchema{
        Type: "object",
        Properties: map[string]interface{}{
            "query": map[string]interface{}{
                "type":        "string",
                "description": "SQL 查询语句（仅支持 SELECT）",
            },
            "limit": map[string]interface{}{
                "type":        "integer",
                "description": "返回结果数量限制（默认 100）",
                "minimum":     1,
                "maximum":     1000,
            },
        },
        Required: []string{"query"},
    }
}

func (t *DatabaseTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    query := params["query"].(string)

    // 安全检查：仅允许 SELECT 查询
    if !isSelectQuery(query) {
        return nil, fmt.Errorf("仅支持 SELECT 查询")
    }

    limit := 100
    if l, ok := params["limit"].(float64); ok {
        limit = int(l)
    }

    rows, err := t.db.QueryContext(ctx, query)
    if err != nil {
        return nil, fmt.Errorf("查询失败: %w", err)
    }
    defer rows.Close()

    columns, err := rows.Columns()
    if err != nil {
        return nil, fmt.Errorf("获取列信息失败: %w", err)
    }

    var results []map[string]interface{}
    count := 0

    for rows.Next() && count < limit {
        values := make([]interface{}, len(columns))
        valuePtrs := make([]interface{}, len(columns))
        for i := range values {
            valuePtrs[i] = &values[i]
        }

        if err := rows.Scan(valuePtrs...); err != nil {
            return nil, fmt.Errorf("扫描行失败: %w", err)
        }

        row := make(map[string]interface{})
        for i, col := range columns {
            row[col] = values[i]
        }
        results = append(results, row)
        count++
    }

    output, err := json.MarshalIndent(results, "", "  ")
    if err != nil {
        return nil, fmt.Errorf("序列化结果失败: %w", err)
    }

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: string(output),
        Metadata: map[string]any{
            "row_count":    count,
            "column_count": len(columns),
        },
    }, nil
}

func isSelectQuery(query string) bool {
    // 简单的安全检查
    // 生产环境应使用更严格的验证
    return len(query) >= 6 && query[:6] == "SELECT"
}
```

## JSON Schema 详解

### 基础类型

```go
// 字符串
map[string]interface{}{
    "type":        "string",
    "description": "描述",
    "minLength":   1,
    "maxLength":   100,
}

// 数字
map[string]interface{}{
    "type":        "number",
    "description": "描述",
    "minimum":     0,
    "maximum":     100,
}

// 整数
map[string]interface{}{
    "type":        "integer",
    "description": "描述",
}

// 布尔值
map[string]interface{}{
    "type":        "boolean",
    "description": "描述",
}
```

### 枚举类型

```go
map[string]interface{}{
    "type":        "string",
    "enum":        []string{"option1", "option2", "option3"},
    "description": "从预定义选项中选择",
}
```

### 对象类型

```go
map[string]interface{}{
    "type": "object",
    "properties": map[string]interface{}{
        "name": map[string]interface{}{
            "type": "string",
        },
        "age": map[string]interface{}{
            "type": "integer",
        },
    },
    "required": []string{"name"},
}
```

### 数组类型

```go
map[string]interface{}{
    "type": "array",
    "items": map[string]interface{}{
        "type": "string",
    },
    "minItems": 1,
    "maxItems": 10,
}
```

## 错误处理

### 返回错误的方式

工具可以通过两种方式报告错误：

1. 返回 error（致命错误，中断执行）：

```go
func (t *MyTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    if invalidInput {
        return nil, fmt.Errorf("输入参数无效")
    }
    // ...
}
```

2. 在 ToolResult 中包含错误信息（非致命错误，继续执行）：

```go
func (t *MyTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    result, err := doSomething()
    if err != nil {
        return &tool.ToolResult{
            Name:   t.Name(),
            Output: fmt.Sprintf("操作失败: %v", err),
            Metadata: map[string]any{
                "success": false,
                "error":   err.Error(),
            },
        }, nil
    }

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: result,
        Metadata: map[string]any{
            "success": true,
        },
    }, nil
}
```

### 错误处理最佳实践

1. 验证输入参数
2. 处理超时和取消
3. 记录详细的错误信息
4. 返回有用的错误消息给模型

```go
func (t *MyTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // 1. 验证参数
    value, ok := params["required_param"].(string)
    if !ok || value == "" {
        return nil, fmt.Errorf("required_param 参数缺失或无效")
    }

    // 2. 检查上下文取消
    select {
    case <-ctx.Done():
        return nil, ctx.Err()
    default:
    }

    // 3. 执行操作
    result, err := performOperation(ctx, value)
    if err != nil {
        // 记录详细错误
        log.Printf("工具执行失败: %v", err)

        // 返回用户友好的错误消息
        return nil, fmt.Errorf("无法完成操作: %w", err)
    }

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: result,
    }, nil
}
```

## 工具注册

### 注册自定义工具

```go
runtime, err := api.New(ctx, api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
    CustomTools:   []tool.Tool{
        &CalculatorTool{},
        NewHTTPTool(),
        NewDatabaseTool(db),
    },
})
```

### 与内置工具一起使用

```go
import (
    "github.com/cexll/agentsdk-go/pkg/tool/builtin"
)

runtime, err := api.New(ctx, api.Options{
    ProjectRoot:   ".",
    ModelFactory:  provider,
    CustomTools:   []tool.Tool{
        // 自定义工具
        &CalculatorTool{},

        // 内置工具
        toolbuiltin.NewBashTool(),
        toolbuiltin.NewReadTool(),
        toolbuiltin.NewWriteTool(),
        toolbuiltin.NewEditTool(),
    },
})
```

## 工具开发最佳实践

### 命名规范

1. 工具名称使用小写字母和下划线
2. 名称应清晰描述工具功能
3. 避免与内置工具冲突

```go
// 好的命名
"calculator"
"http_get"
"db_query"
"send_email"

// 不好的命名
"calc"  // 太简短
"HTTP"  // 不应使用大写
"tool1" // 不描述功能
```

### 描述编写

描述应该：

1. 清晰说明工具的功能
2. 说明何时应该使用此工具
3. 提及重要的限制或注意事项

```go
func (t *MyTool) Description() string {
    return "执行数学计算，支持加减乘除运算。" +
        "用于需要精确数值计算的场景。" +
        "注意：除法运算不支持除以零。"
}
```

### 参数设计

1. 必需参数放在 `Required` 列表中
2. 为每个参数提供清晰的描述
3. 使用合适的类型和约束
4. 考虑参数的默认值

```go
Schema: &tool.JSONSchema{
    Type: "object",
    Properties: map[string]interface{}{
        "query": map[string]interface{}{
            "type":        "string",
            "description": "搜索查询字符串",
        },
        "limit": map[string]interface{}{
            "type":        "integer",
            "description": "返回结果数量限制（默认 10）",
            "minimum":     1,
            "maximum":     100,
        },
        "format": map[string]interface{}{
            "type":        "string",
            "enum":        []string{"json", "xml", "csv"},
            "description": "输出格式（默认 json）",
        },
    },
    Required: []string{"query"},
}
```

### 性能优化

1. 设置合理的超时
2. 限制输出大小
3. 使用连接池
4. 缓存频繁访问的数据

```go
type OptimizedTool struct {
    client *http.Client
    cache  *cache.Cache
}

func (t *OptimizedTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // 设置超时
    ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
    defer cancel()

    // 检查缓存
    key := generateCacheKey(params)
    if cached, ok := t.cache.Get(key); ok {
        return cached.(*tool.ToolResult), nil
    }

    // 执行操作
    result, err := t.performOperation(ctx, params)
    if err != nil {
        return nil, err
    }

    // 限制输出大小
    if len(result.Output) > 10000 {
        result.Output = result.Output[:10000] + "...(truncated)"
    }

    // 缓存结果
    t.cache.Set(key, result, 5*time.Minute)

    return result, nil
}
```

### 安全考虑

1. 验证所有输入参数
2. 限制文件系统访问
3. 限制网络访问
4. 避免命令注入
5. 遵循最小权限原则

```go
func (t *SafeTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    // 1. 验证输入
    path := params["path"].(string)
    if !isValidPath(path) {
        return nil, fmt.Errorf("路径验证失败")
    }

    // 2. 检查沙箱限制
    if !t.sandbox.ValidatePath(path) {
        return nil, fmt.Errorf("路径不在允许的范围内")
    }

    // 3. 使用最小权限执行
    result, err := t.executeWithLimitedPrivileges(ctx, path)
    if err != nil {
        return nil, err
    }

    // 4. 清理敏感信息
    result = sanitizeOutput(result)

    return &tool.ToolResult{
        Name:   t.Name(),
        Output: result,
    }, nil
}
```

## 测试

### 单元测试

```go
package main

import (
    "context"
    "testing"

    "github.com/cexll/agentsdk-go/pkg/tool"
)

func TestCalculatorTool_Execute(t *testing.T) {
    calc := &CalculatorTool{}

    tests := []struct {
        name    string
        params  map[string]any
        wantErr bool
    }{
        {
            name: "add",
            params: map[string]any{
                "operation": "add",
                "a":         2.0,
                "b":         3.0,
            },
            wantErr: false,
        },
        {
            name: "divide by zero",
            params: map[string]any{
                "operation": "divide",
                "a":         5.0,
                "b":         0.0,
            },
            wantErr: true,
        },
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := calc.Execute(context.Background(), tt.params)
            if (err != nil) != tt.wantErr {
                t.Errorf("Execute() error = %v, wantErr %v", err, tt.wantErr)
                return
            }
            if !tt.wantErr && result == nil {
                t.Error("Execute() returned nil result")
            }
        })
    }
}
```

### 集成测试

```go
func TestCalculatorTool_Integration(t *testing.T) {
    ctx := context.Background()

    provider := model.NewAnthropicProvider(
        model.WithAPIKey(os.Getenv("ANTHROPIC_API_KEY")),
        model.WithModel("claude-sonnet-4-5"),
    )

    runtime, err := api.New(ctx, api.Options{
        ProjectRoot:   ".",
        ModelFactory:  provider,
        CustomTools:   []tool.Tool{&CalculatorTool{}},
    })
    if err != nil {
        t.Fatal(err)
    }
    defer runtime.Close()

    result, err := runtime.Run(ctx, api.Request{
        Prompt:    "计算 15 + 27",
        SessionID: "test",
    })
    if err != nil {
        t.Fatal(err)
    }

    if result.Output == "" {
        t.Error("Expected non-empty output")
    }
}
```

## 调试技巧

### 添加日志

```go
func (t *MyTool) Execute(ctx context.Context, params map[string]any) (*tool.ToolResult, error) {
    log.Printf("[%s] 收到参数: %+v", t.Name(), params)

    result, err := t.performOperation(ctx, params)
    if err != nil {
        log.Printf("[%s] 执行失败: %v", t.Name(), err)
        return nil, err
    }

    log.Printf("[%s] 执行成功: %s", t.Name(), result.Output)
    return result, nil
}
```

### 使用元数据

```go
return &tool.ToolResult{
    Name:   t.Name(),
    Output: "操作完成",
    Metadata: map[string]any{
        "execution_time": time.Since(start).Seconds(),
        "cache_hit":      cacheHit,
        "items_processed": count,
    },
}
```

## 常见问题

### 工具未被调用

检查：

1. 工具名称是否唯一
2. 描述是否清晰
3. Schema 是否正确
4. 是否已正确注册

### 参数类型错误

使用类型断言时添加检查：

```go
value, ok := params["param"].(string)
if !ok {
    return nil, fmt.Errorf("参数 param 类型错误")
}
```

### 超时问题

确保尊重上下文超时：

```go
select {
case <-ctx.Done():
    return nil, ctx.Err()
case result := <-resultChan:
    return result, nil
}
```

## 参考资料

- [Tool 接口定义](../pkg/tool/tool.go)
- [内置工具实现](../pkg/tool/builtin/)
- [API 参考文档](api-reference.md)
- [JSON Schema 规范](https://json-schema.org/)
