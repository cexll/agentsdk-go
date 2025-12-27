# AskUserQuestion 工具演示

## 📌 概述

这个目录包含 `AskUserQuestion` 工具的多个演示示例。

本目录通过 build tag 提供 3 种演示模式：
- 默认（无 tag）：`main.go`
- `demo_simple`：纯工具测试（无需 API key）
- `demo_llm`：LLM 集成测试（需要 API key）

## 🎯 Demo 文件

### 1. demo_simple.go - 独立工具测试（推荐）
**不需要 API Key**，直接测试工具本身的功能。

```bash
go run -tags demo_simple .
```

展示：
- 单个问题单选
- 多个问题组合
- 多选问题
- 预填答案

### 2. demo_llm.go - LLM 集成测试
需要 API Key，测试 LLM 是否会主动调用 AskUserQuestion 工具。

```bash
export ANTHROPIC_API_KEY=sk-ant-your-key
go run -tags demo_llm .
```

### 3. main.go - 完整集成示例
需要 API Key，展示在实际 AI Agent 场景中使用 AskUserQuestion。

```bash
export ANTHROPIC_API_KEY=sk-ant-your-key
go run .
```

## ⚠️ 当前发现

### LLM 工具调用行为

经过测试发现，即使明确要求 LLM "使用 AskUserQuestion 工具"，LLM 往往会：
1. **描述问题**而不是调用工具
2. 说"我已经创建了问题"，但实际没有调用
3. StopReason 显示为 `end_turn` 而不是 `tool_use`

**可能原因：**
1. **工具描述的定位**：AskUserQuestion 的 description 说明它用于"在执行过程中询问用户"，LLM 可能认为当前场景不需要实际调用
2. **提示词措辞**：提示词再明确，LLM 也可能将"使用工具"理解为"描述使用工具"
3. **模型行为**：LLM 倾向于用自然语言交互而非工具调用

## ✅ 工具本身功能

工具实现是完全正确的：
- ✅ 正确注册到 runtime
- ✅ Schema 完整准确
- ✅ 参数验证工作正常
- ✅ 输出格式正确
- ✅ 测试覆盖率 ≥90%

## 💡 实际使用建议

在实际应用中，AskUserQuestion 工具更适合：

1. **程序化调用**
   ```go
   tool := toolbuiltin.NewAskUserQuestionTool()
   result, _ := tool.Execute(ctx, params)
   ```

2. **明确的决策点**
   当代码逻辑判断需要用户输入时，直接调用工具

3. **前端集成**
   前端 UI 根据工具返回的结构化数据渲染问卷

4. **工作流编排**
   在预定义的工作流中插入询问步骤

## 📊 工具输出示例

```
1 question(s)
1. [语言] 选择主要开发语言？ (single-select)
   1) Python - 丰富的 AI/ML 生态，大量现成库
   2) TypeScript - 类型安全，适合全栈开发
   3) Go - 高性能，适合分布式系统
```

结构化数据：
```json
{
  "questions": [
    {
      "question": "选择主要开发语言？",
      "header": "语言",
      "options": [
        {"label": "Python", "description": "..."},
        {"label": "TypeScript", "description": "..."}
      ],
      "multiSelect": false
    }
  ]
}
```

## 🔧 运行环境

```bash
# 初始化模块
cd examples/08-askuserquestion
go mod tidy

# 运行无需 API Key 的demo
go run -tags demo_simple .

# 运行需要 API Key 的demo
export ANTHROPIC_API_KEY=sk-ant-xxx
go run -tags demo_llm .
```

## 📝 总结

- **工具实现**：完全符合规范 ✅
- **单元测试**：覆盖率 ≥90% ✅
- **直接调用**：功能正常 ✅
- **LLM 主动调用**：较难触发 ⚠️

AskUserQuestion 是一个功能完整的工具，更适合在明确的程序逻辑点使用，而非依赖 LLM 的自主判断。
