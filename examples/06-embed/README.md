# 嵌入文件系统示例

此示例演示如何使用 `embed.FS` 将 `.claude` 目录嵌入到二进制文件中。

## 功能特性

- ✅ 将配置文件嵌入到二进制
- ✅ 将 skills 嵌入到二进制
- ✅ 文件系统优先策略（允许运行时覆盖）
- ✅ 零外部依赖的可执行文件

## 使用方法

### 1. 设置 API Key

```bash
export ANTHROPIC_API_KEY=sk-ant-your-key-here
```

### 2. 运行示例

```bash
go run main.go
```

### 3. 构建独立二进制

```bash
go build -o embed-demo main.go
./embed-demo
```

## 工作原理

### 嵌入文件系统

```go
//go:embed .claude
var claudeFS embed.FS
```

这行代码将整个 `.claude` 目录嵌入到编译后的二进制文件中。

### 传递给 Runtime

```go
runtime, err := api.New(context.Background(), api.Options{
    ProjectRoot:  ".",
    ModelFactory: provider,
    EmbedFS:      claudeFS,  // 传入嵌入的文件系统
})
```

### 加载优先级

1. **OS 文件系统优先**：如果本地存在 `.claude/settings.json`，优先使用
2. **嵌入 FS 回退**：如果本地不存在，使用嵌入的版本

这意味着你可以：
- 打包时提供默认配置（嵌入）
- 运行时通过创建本地文件来覆盖（OS 文件系统）

## 运行时覆盖示例

即使二进制中嵌入了配置，你仍然可以在运行时覆盖：

```bash
# 创建本地覆盖配置
mkdir -p .claude
cat > .claude/settings.local.json <<EOF
{
  "permissions": {
    "allow": ["Bash(*:*)"]
  }
}
EOF

# 运行时会使用本地配置覆盖嵌入的配置
./embed-demo
```

## 分发场景

此功能特别适合以下场景：

1. **CLI 工具分发**：用户只需下载一个二进制文件，无需额外配置
2. **CI/CD 环境**：容器镜像中只需包含二进制，无需复制配置文件
3. **企业部署**：提供默认配置，允许用户自定义覆盖

## 文件结构

```
examples/06-embed/
├── main.go                    # 示例代码
├── .claude/
│   ├── settings.json          # 嵌入的默认配置
│   └── skills/
│       └── demo/
│           └── SKILL.md       # 嵌入的技能
└── README.md                  # 本文档
```

## 注意事项

- 嵌入的内容在编译时固定，运行时不可修改
- 嵌入会增加二进制文件大小
- 建议只嵌入必要的配置和技能
- 敏感信息（如 API Key）不应嵌入，应通过环境变量传递
