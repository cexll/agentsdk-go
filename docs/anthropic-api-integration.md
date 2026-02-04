# Anthropic API SDK 对接文档

本文档记录 agentsdk-go 与 Anthropic API SDK 的参数对接状态，帮助开发者了解当前支持的功能和待实现的特性。

## 版本信息

- **Anthropic SDK**: `github.com/anthropics/anthropic-sdk-go v1.18.0`
- **对接位置**: `pkg/model/anthropic.go`
- **最后更新**: 2025-12-29

## 对接状态总览

| 参数 | 状态 | 实现位置 | 说明 |
|------|------|----------|------|
| Model | ✅ | `buildParams:275` | 模型选择 |
| MaxTokens | ✅ | `buildParams:276` | 最大生成 tokens |
| Messages | ✅ | `buildParams:277` | 对话消息 |
| System | ✅ | `buildParams:280-282` | 系统提示词 |
| Tools | ✅ | `buildParams:284-290` | 工具定义 |
| Temperature | ✅ | `buildParams:292-297` | 温度参数 (0.0-1.0) |
| Metadata.UserID | ✅ | `buildParams:299-303` | 用户标识 (从 SessionID 映射) |
| EnablePromptCache | ✅ | `convertMessages` | Prompt 缓存控制 (v0.6.1+) |
| **TopK** | ❌ | - | 采样参数：从前 K 个选项中采样 |
| **TopP** | ❌ | - | 核采样参数：累积概率阈值 |
| **StopSequences** | ❌ | - | 自定义停止序列 |
| **ToolChoice** | ❌ | - | 工具使用策略控制 |
| **Thinking** | ❌ | - | Extended Thinking 配置 |

## 未对接参数详解

### 1. TopK (top_k)

**类型**: `int64`

**用途**: 从前 K 个最可能的 token 中采样，用于移除"长尾"低概率响应。

**使用场景**:
- 需要更确定性的输出
- 限制模型的创造性
- 高级采样控制

**技术细节**: [How to sample from language models](https://towardsdatascience.com/how-to-sample-from-language-models-682bceb97277)

**推荐**: 高级用例，通常只需使用 `Temperature` 即可。

---

### 2. TopP (top_p)

**类型**: `float64`

**范围**: 0.0 - 1.0

**用途**: 核采样（Nucleus Sampling），从累积概率达到 P 的最小 token 集合中采样。

**使用场景**:
- 平衡确定性和多样性
- 替代 Temperature 的采样控制
- 更精细的输出控制

**推荐**: 与 `Temperature` 二选一使用，不建议同时设置。

**示例值**:
- `0.9` - 较保守，高质量输出
- `0.95` - 平衡
- `0.99` - 更多样化

---

### 3. StopSequences (stop_sequences)

**类型**: `[]string`

**用途**: 定义停止序列，当模型生成这些序列时立即停止。

**使用场景**:
- 格式化输出控制
- 防止模型生成不需要的内容
- 实现特定的对话模式

**示例**:
```go
StopSequences: []string{
    "\n\nHuman:",     // 防止模型模拟用户输入
    "```",           // 在代码块结束时停止
    "[END]",         // 自定义结束标记
}
```

**限制**: 最多 4 个停止序列。

---

### 4. ToolChoice (tool_choice)

**类型**: `ToolChoiceUnionParam`

**用途**: 控制模型如何使用提供的工具。

**选项**:

1. **`auto`** (默认)
   - 模型自主决定是否使用工具
   - 适合大多数场景

2. **`any`**
   - 强制模型必须使用某个工具
   - 适合必须调用工具的场景

3. **`tool`**
   - 强制使用特定工具
   - 需要指定工具名称
   ```json
   {
     "type": "tool",
     "name": "get_weather"
   }
   ```

4. **`none`**
   - 禁用所有工具
   - 即使提供了工具定义也不使用

**使用场景**:
- Agent 工作流控制
- 强制工具调用
- 多步骤任务编排

**重要性**: ⭐⭐⭐⭐⭐ (对 Agent 场景至关重要)

---

### 5. Thinking (thinking) 🆕

**类型**: `ThinkingConfigParamUnion`

**用途**: 启用 Claude 的 Extended Thinking 功能，显示模型的思考过程。

**配置**:
```json
{
  "type": "enabled",
  "budget_tokens": 2048
}
```

**要求**:
- 最少 1024 tokens 预算
- 计入 `max_tokens` 限制
- 仅支持特定模型

**响应格式**:
```json
{
  "content": [
    {
      "type": "thinking",
      "thinking": "Let me analyze this step by step..."
    },
    {
      "type": "text",
      "text": "Based on my analysis..."
    }
  ]
}
```

**使用场景**:
- 复杂推理任务
- 需要透明度的决策
- 调试模型行为

**文档**: [Extended Thinking Guide](https://docs.claude.com/en/docs/build-with-claude/extended-thinking)

## 对接优先级建议

### 🔴 高优先级

#### 1. ToolChoice
- **原因**: Agent 场景的核心功能，控制工具使用策略
- **影响**: 无法实现强制工具调用、工具选择控制
- **工作量**: 中等（需要类型转换和验证）

#### 2. StopSequences
- **原因**: 常用于格式化输出控制
- **影响**: 无法精确控制生成内容的边界
- **工作量**: 低（简单的字符串数组）

### 🟡 中优先级

#### 3. TopP
- **原因**: 与 Temperature 互补的采样控制
- **影响**: 缺少一种常用的采样策略
- **工作量**: 低（简单的浮点数参数）

#### 4. TopK
- **原因**: 高级采样控制
- **影响**: 缺少精细的采样控制
- **工作量**: 低（简单的整数参数）

### 🟢 低优先级

#### 5. Thinking
- **原因**: 新功能，特定场景使用
- **影响**: 无法使用 Extended Thinking 功能
- **工作量**: 中等（需要处理新的响应格式）

## 实现建议

### 扩展 model.Request

```go
// pkg/model/interface.go
type Request struct {
    Messages           []Message
    Tools              []ToolDefinition
    System             string
    Model              string
    SessionID          string
    MaxTokens          int
    Temperature        *float64
    EnablePromptCache  bool

    // 新增参数
    TopK              *int64          // 采样参数
    TopP              *float64        // 核采样参数
    StopSequences     []string        // 停止序列
    ToolChoice        *ToolChoice     // 工具选择策略
    ThinkingConfig    *ThinkingConfig // Extended Thinking 配置
}

// ToolChoice 工具选择策略
type ToolChoice struct {
    Type string  // "auto" | "any" | "tool" | "none"
    Name string  // 当 Type="tool" 时指定工具名
}

// ThinkingConfig Extended Thinking 配置
type ThinkingConfig struct {
    Enabled      bool
    BudgetTokens int
}
```

### 修改 buildParams

```go
// pkg/model/anthropic.go
func (m *anthropicModel) buildParams(req Request) (anthropicsdk.MessageNewParams, error) {
    // ... 现有代码 ...

    // TopK
    if req.TopK != nil {
        params.TopK = param.NewOpt(*req.TopK)
    }

    // TopP
    if req.TopP != nil {
        params.TopP = param.NewOpt(*req.TopP)
    }

    // StopSequences
    if len(req.StopSequences) > 0 {
        params.StopSequences = req.StopSequences
    }

    // ToolChoice
    if req.ToolChoice != nil {
        params.ToolChoice = convertToolChoice(req.ToolChoice)
    }

    // Thinking
    if req.ThinkingConfig != nil && req.ThinkingConfig.Enabled {
        params.Thinking = anthropicsdk.ThinkingConfigParamUnion{
            OfEnabled: &anthropicsdk.ThinkingConfigEnabledParam{
                Type:         "enabled",
                BudgetTokens: int64(req.ThinkingConfig.BudgetTokens),
            },
        }
    }

    return params, nil
}
```

## 使用示例

### 示例 1: 使用 TopP 控制采样

```go
topP := 0.9
resp, err := runtime.Run(ctx, api.Request{
    Prompt:      "生成一个创意故事",
    TopP:        &topP,  // 使用核采样
    Temperature: nil,    // 不使用 Temperature
})
```

### 示例 2: 使用 StopSequences 控制输出

```go
resp, err := runtime.Run(ctx, api.Request{
    Prompt: "生成 Python 代码",
    StopSequences: []string{
        "```",           // 在代码块结束时停止
        "\n\nHuman:",    // 防止模型模拟对话
    },
})
```

### 示例 3: 强制工具调用

```go
resp, err := runtime.Run(ctx, api.Request{
    Prompt: "查询天气",
    ToolChoice: &model.ToolChoice{
        Type: "tool",
        Name: "get_weather",  // 强制使用 get_weather 工具
    },
})
```

### 示例 4: 启用 Extended Thinking

```go
resp, err := runtime.Run(ctx, api.Request{
    Prompt: "解决这个复杂的数学问题",
    ThinkingConfig: &model.ThinkingConfig{
        Enabled:      true,
        BudgetTokens: 2048,  // 分配 2048 tokens 用于思考
    },
})

// 响应中会包含 thinking 内容块
for _, block := range resp.Content {
    if block.Type == "thinking" {
        fmt.Println("思考过程:", block.Thinking)
    }
}
```

## 测试要求

每个新增参数都需要：

1. **单元测试**: 验证参数正确传递到 Anthropic SDK
2. **集成测试**: 验证实际 API 调用行为
3. **边界测试**: 验证参数验证和错误处理

覆盖率不在文档固化阈值；请根据改动风险补齐必要测试，并以 CI/本地 `go test` 结果为准。

## 参考资料

- [Anthropic API Documentation](https://docs.anthropic.com/en/api/messages)
- [Anthropic Go SDK](https://github.com/anthropics/anthropic-sdk-go)
- [Extended Thinking Guide](https://docs.claude.com/en/docs/build-with-claude/extended-thinking)
- [Tool Use Guide](https://docs.claude.com/en/docs/agents-and-tools/tool-use)
- [Sampling Parameters](https://towardsdatascience.com/how-to-sample-from-language-models-682bceb97277)

## 更新日志

### v0.6.1 (2025-12-29)
- ✅ 添加 `EnablePromptCache` 支持
- ✅ 实现双层缓存配置（model + API）

### v0.6.0 及之前
- ✅ 基础参数对接: Model, MaxTokens, Messages, System, Tools, Temperature, Metadata

## 贡献指南

如需实现未对接的参数，请：

1. 在 `pkg/model/interface.go` 中扩展 `Request` 结构
2. 在 `pkg/model/anthropic.go` 的 `buildParams` 中添加参数转换
3. 添加单元测试到 `pkg/model/anthropic_test.go`
4. 更新本文档的对接状态表
5. 提交 PR 并确保相关测试通过

---

**维护者**: agentsdk-go team
**最后审核**: 2025-12-29
