# Trace System 架构文档

## 1. 概述
agentsdk-go 采用双层 trace 体系，在不同抽象层面捕获可观测性数据：
- **HTTP Layer Trace**：位于最外层 HTTP server，零侵入记录完整的请求/响应循环（含 SSE 流），主要服务 API 集成与网络疑难排查。
- **Middleware Layer Trace**：挂载在 agent/middleware 链的 6 个拦截点上，输出模型与工具调用的全部上下文与性能指标，用于深入理解 Agent 执行路径。
两条链路可单独启用，也可以并行开启实现端到端追踪。

## 2. HTTP Layer Trace
### 2.1 功能
- 捕获 HTTP 请求的 method、URL、headers（统一小写）以及 JSON/二进制请求体，必要时自动解析 body 为结构化 JSON。
- 捕获完整响应：status_code、headers、body_raw；若响应为 SSE，`body_raw` 保留原始 event: 行，便于重放。
- 自动脱敏敏感头（`authorization`、`x-api-key` 等），仅保留首尾 5 个字符或用 `*` 填充短 token。
- 支持 streaming：自定义 `responseRecorder` 透传 `http.Flusher`/`http.Hijacker`，既不影响实时推送也能收集片段并标记 `...(truncated)`。
- 可配置 Body 捕获上限（默认 1 MiB），避免日志放大；limit=0 可禁用 body，limit<0 则捕获完整负载。

### 2.2 输出格式
HTTP trace 写到 `.claude-trace/log-YYYY-MM-DD-HH-MM-SS.jsonl`，每行一个完整事件。下面示例参考 `.claude-trace/log-2025-11-19-08-46-48.jsonl`：
```json
{
  "request": {
    "timestamp": 1763542011.267,
    "method": "POST",
    "url": "http://localhost:23000/v1/messages?beta=true",
    "headers": {
      "accept": "application/json",
      "anthropic-version": "2023-06-01",
      "content-type": "application/json",
      "user-agent": "claude-cli/2.0.34 (external, cli)",
      "x-api-key": "sk-bf68d...419e"
    },
    "body": {
      "model": "claude-haiku-4-5-20251001",
      "messages": [
        {
          "role": "user",
          "content": [
            {
              "type": "text",
              "text": "Please write a 5-10 word title for the following conversation:..."
            }
          ]
        }
      ],
      "system": [
        {"type": "text", "text": "You are Claude Code, Anthropic's official CLI for Claude."},
        {"type": "text", "text": "Summarize this coding conversation in under 50 characters."}
      ],
      "tools": [],
      "metadata": {
        "user_id": "user_9a40d06fc56208d37837a869f78d4328467bc633cdaf025ca626316642e2fea8_account__session_6ef651a3-6819-4bda-ac34-59ba978b80a6"
      },
      "max_tokens": 32000,
      "stream": true
    }
  },
  "response": {
    "timestamp": 1763542013.862,
    "status_code": 200,
    "headers": {
      "content-type": "text/event-stream",
      "cache-control": "no-cache",
      "connection": "keep-alive",
      "date": "Wed, 19 Nov 2025 08:46:53 GMT"
    },
    "body_raw": "event: message_start\ndata: {\"type\":\"message_start\",...}"
  },
  "logged_at": "2025-11-19T08:46:54.078Z"
}
```
> 提示：示例 body_raw 仅展示前几帧 SSE 文本，实际文件保留完整 event stream。

### 2.3 使用方法
在自定义 HTTP server 中包裹最外层 mux：
```go
httpTraceDir := filepath.Join(projectRoot, ".claude-trace")
writer, err := middleware.NewFileHTTPTraceWriter(httpTraceDir)
if err != nil {
    log.Printf("HTTP trace disabled: %v", err)
} else {
    httpTrace := middleware.NewHTTPTraceMiddleware(
        writer,
        middleware.WithHTTPTraceMaxBodyBytes(2<<20),
    )
    handler := httpTrace.Wrap(mux) // mux 为业务 handler
    server := &http.Server{Addr: ":8080", Handler: handler}
    log.Printf("HTTP trace writing to %s", writer.Path())
    go server.ListenAndServe()
}
```
该中间件与现有 router/otel 链兼容，不要求应用代码感知 trace。

### 2.4 配置选项
- **输出目录**：`NewFileHTTPTraceWriter(dir)` 默认 `.claude-trace`；可指定绝对/相对路径或接入共享卷。
- **Body 大小限制**：通过 `WithHTTPTraceMaxBodyBytes(limit)` 控制；limit=0 不抓取正文，limit<0 表示无限制。
- **自定义 writer**：实现 `HTTPTraceWriter` 接口即可落地到对象存储、队列或第三方 SIEM。
- **测试友好**：`WithHTTPTraceClock` 支持注入假时钟，便于比对 deterministic 日志。

## 3. Middleware Layer Trace
### 3.1 功能
- **六个拦截点**：`before_agent → before_model → after_model → before_tool → after_tool → after_agent` 全覆盖 agent 生命周期。
- **模型输入/输出快照**：`model_request` 保存 messages/system/tools/max_tokens，`model_response` 保存 content/tool_calls/usage/stop_reason/stream 元数据。
- **工具链路**：记录 `tool_call`（id/name/input/path）与 `tool_result`（content/error/is_error），方便重放。
- **性能指标**：自动打点 `duration_ms`（模型、工具、agent 三类时长），并统计 `iteration`、`session_id`、错误信息。
- **汇总统计**：累积 total tokens、total duration，并写入 HTML Viewer 的指标卡与徽章。

### 3.2 输出格式
- **JSONL**：`.trace/log-2025-11-19T14:26:28Z.jsonl` 等文件存储机器可读事件：
  ```json
  {"timestamp":"2025-11-19T22:26:28.109153+08:00","stage":"before_agent","iteration":0,"session_id":"web-chat-session","input":{"Iteration":0,"Values":{}},"duration_ms":0}
  {"timestamp":"2025-11-19T22:26:35.606617+08:00","stage":"after_model","iteration":0,"session_id":"web-chat-session","output":{"Content":"你好！...","Done":true},"model_response":{"content":"你好！...","usage":{"input_tokens":2870,"output_tokens":214,"total_tokens":3084}},"duration_ms":7494}
  ```
  所有字段均在单行 JSON 中，方便 `jq`/`rg`/SIEM 消化。
- **HTML Viewer**：同名 `.html` 由 `traceHTMLTemplate` 渲染。核心能力：
  - 顶部仪表盘展示 Total Tokens / Total Duration / Events。
  - Timeline 以 `<details>` 展示事件，附带 stage badge、duration/token/error 徽章。
  - JSON 高亮 + 折叠（Model Request/Response 默认折叠，可展开查看 messages/system/tool_calls）。
  - 底部 footer 标注生成时间及对应 JSONL 文件，便于跳转日志。

### 3.3 使用方法
将 TraceMiddleware 注入 agent middleware 链（`pkg/agent/agent.go`）：
```go
traceMW := middleware.NewTraceMiddleware(filepath.Join(projectRoot, ".trace"))
chain := middleware.NewChain([]middleware.Middleware{traceMW})
agent, err := agent.New(modelProvider, toolExec, agent.Options{Middleware: chain})
if err != nil { log.Fatal(err) }
result, err := agent.Run(ctx, agentCtx)
```
若已有其它 middleware，可传入切片初始化或使用 `chain.Use(traceMW)` 追加。

## 4. 对比说明
| 特性 | HTTP Trace | Middleware Trace |
|------|-----------|-----------------|
| 目的 | 调试 HTTP 交互、验证签名、排查网络/篮筐 | 调试 Agent 执行流程、prompt 质量、工具编排 |
| 输出目录 | `.claude-trace/` | `.trace/` |
| 格式 | JSONL（原始 HTTP 事件） | JSONL + HTML Viewer |
| 粒度 | 每次 HTTP request/response（含 streaming 片段） | 每个 middleware stage（可跨多次模型/工具） |
| 捕获数据 | method/url/headers/body_raw + 脱敏密钥 | messages/system/tools/usage、tool call/result、metrics |
| Streaming 支持 | 原样写入 SSE/Chunked，保留事件顺序 | 通过 `model_response.stream` 字段引用上游流事件 |
| 可视化 | 需外部工具（jq、tail、SIEM） | 内建交互式 HTML，含统计面板与 JSON 高亮 |
| 典型消费者 | API/平台团队、网络层 SRE | Prompt 工程、Agent/Tool 开发者、应用层 SRE |

## 5. 最佳实践
- 开发/测试环境默认开启两种 trace，配合 `tail -f` + HTML 查看器快速定位问题。
- 生产环境应按需开启：HTTP trace 只在疑难票据阶段打开，Middleware trace 可在少量会话上采样，以免写 I/O 过大。
- 通过 logrotate/cron 定期清理 `.claude-trace/` 与 `.trace/`（建议保留最近 7 天或 <5 GB）。
- 统一将 trace 目录加入 `.gitignore`，防止意外提交。
- 在下游分析前再次脱敏（尤其是 request.body 和 tool 参数），或为敏感字段提供自定义 `HTTPTraceWriter`/`sanitizePayload` wrapper。

## 6. 故障排查
- **没有生成 `.claude-trace` 文件**：确认 HTTP middleware 包裹在最外层 mux；若 server 提前返回错误，日志会写在 stderr。
- **SSE/长响应被截断**：检查 `WithHTTPTraceMaxBodyBytes` 是否足够，必要时设置为负数并监控磁盘。
- **HTML Viewer 不更新**：TraceMiddleware 每次事件会 rewrite HTML；若看到旧时间戳，检查 `.trace` 目录写权限或磁盘满。
- **JSONL 体积过大**：结合 `limit=0` 关闭 HTTP body，或在 Middleware trace 中仅对关键 session 设置 `trace.session_id`，避免长跑任务写入所有事件。
- **工具结果缺失**：确保工具执行后将结果写入 `State.ToolResult`（SDK 默认已写）；如有自定义 middleware 修改 `State`，请勿覆盖 `ToolResult`。
