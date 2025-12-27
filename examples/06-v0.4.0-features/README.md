# v0.4.0 新特性演示

本示例演示 agentsdk-go v0.4.0 版本的所有主要新增功能。

## 新增功能概览

v0.4.0 引入了 8 个重要特性，显著增强了 SDK 的功能性、可观测性和安全性：

### 1. Rules Configuration (规则配置)
从 `.claude/rules/` 目录加载项目规则：
- 支持 markdown 格式，文件名前缀表示优先级（如 `01-xxx.md`）
- 规则自动按优先级排序并合并到系统提示
- 支持热重载，文件变化时自动更新

### 2. Token Statistics (Token 统计)
自动跟踪每次请求的 token 使用情况：
- 输入/输出 tokens
- 总计 tokens
- 缓存创建和读取 tokens
- 通过 `response.Result.Usage` 访问
- 支持自定义回调函数 `api.Options.TokenCallback`

### 3. Auto Compact (自动压缩)
当对话上下文超过阈值时自动触发压缩：
- 可配置的触发阈值（默认 80%）
- 保留最近 N 条消息（默认 5 条）
- 使用指定模型生成摘要（建议使用 Haiku 节省成本）
- 防止上下文溢出，降低长对话成本

### 4. Async Bash (异步 Bash)
Bash 工具新增 `background` 参数支持后台执行：
- 非阻塞命令执行
- Agent 可继续处理其他任务
- 适合构建、测试、部署等长时间运行的任务

### 5. DisallowedTools (禁用工具)
在配置文件或运行时指定禁用的工具列表：
- 运行时阻止特定工具调用
- 增强安全性，无需修改代码
- 适用于只读或受限环境

### 6. Multi-model Support (多模型支持)
为不同子代理绑定不同模型：
- 成本优化：简单任务使用便宜模型
- 性能调优：复杂推理使用强大模型
- 灵活的模型层级选择（Low/Mid/High）
- 详见 `examples/05-multimodel`

### 7. Hooks System Extension (钩子系统扩展)
新增 4 个钩子事件类型：
- `PermissionRequest` - 请求敏感操作权限
- `SessionStart/End` - 跟踪会话生命周期
- `SubagentStart/Stop` - 监控子代理执行
- `PreToolUse` 增强 - 现在可以修改工具输入

所有钩子以 shell 命令方式运行（stdin JSON，exit code 表示决策）

### 8. OpenTelemetry Integration (OpenTelemetry 集成)
分布式追踪支持：
- 请求级 UUID 追踪
- Span 在 agent/model/tool 调用间传播
- 集成 Jaeger、Zipkin 等追踪系统
- 需要 `otel` build tag 启用

## 文件结构

```
examples/06-v0.4.0-features/
├── demo.go              # 完整功能演示（可在线/离线运行）
├── demo_test.go         # 单元测试验证各功能可用性
├── README.md            # 本文件
└── .claude/
    ├── settings.json    # 示例配置（compact、disallowed_tools）
    └── rules/
        ├── 01-code-style.md   # 代码风格规则
        └── 02-testing.md       # 测试规范
```

## 运行示例

### 离线演示（无需 API key）

```bash
# 运行功能介绍和配置演示
go run ./examples/06-v0.4.0-features/demo.go
```

演示会展示：
- 加载并显示 rules 配置
- 各功能的配置方式和使用示例
- 数据结构和 API 说明

### 在线测试（需要 API key）

```bash
# 设置 API key
export ANTHROPIC_API_KEY=sk-ant-your-key-here

# 运行完整演示（包括实际 API 调用）
go run ./examples/06-v0.4.0-features/demo.go
```

会额外执行：
- 创建真实 runtime 并配置所有功能
- 执行简单的 agent 任务
- 显示实际的 token 统计信息

### 运行测试

```bash
cd examples/06-v0.4.0-features
go test -v
```

测试覆盖：
- Rules 配置加载和排序
- Auto Compact 配置验证
- DisallowedTools 配置
- Multi-model 配置
- Token 统计数据结构
- OTEL 配置
