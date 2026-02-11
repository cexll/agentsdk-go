# API 智能默认值配置

## 概述

SDK 现在根据 `EntryPoint` 自动配置智能默认值，消除重复配置。

## 自动配置项

### 1. 项目根目录自动解析

**新增函数**：`api.ResolveProjectRoot()`

**解析优先级**：
1. 环境变量 `AGENTSDK_PROJECT_ROOT`
2. 向上查找包含 `go.mod` 的目录
3. 当前工作目录

**自动处理**：
- 符号链接解析（macOS `/var` → `/private/var`）
- 相对路径转绝对路径

**使用方式**：

```go
// 自动解析（推荐）
rt, err := api.New(ctx, api.Options{
    EntryPoint: api.EntryPointCLI,
    // ProjectRoot 自动解析
})

// 手动指定
rt, err := api.New(ctx, api.Options{
    ProjectRoot: "/path/to/project",
})

// 环境变量
os.Setenv("AGENTSDK_PROJECT_ROOT", "/custom/path")
```

### 2. 网络白名单自动配置

**根据 EntryPoint 自动设置**：

#### 所有模式（统一默认值）
```go
所有 EntryPoint 自动允许:
- localhost, 127.0.0.1, ::1
- 0.0.0.0 (所有本机接口)
- *.local (mDNS)
- 192.168.*, 10.*, 172.16.* (私有网段)
```

> 注意：当前实现中 `defaultNetworkAllowList` 对所有 EntryPoint 返回相同的本地网络白名单。如需更严格的网络控制，请通过 `Sandbox.NetworkAllow` 显式覆盖。

**覆盖默认值**：

```go
rt, err := api.New(ctx, api.Options{
    EntryPoint: api.EntryPointCLI,
    Sandbox: api.SandboxOptions{
        NetworkAllow: []string{"example.com"}, // 覆盖默认值
    },
})
```

### 3. Shell 元字符自动配置

**CLI 模式**自动允许管道和 shell 特性：
- 管道 `|`
- 重定向 `>`, `<`
- 后台执行 `&`
- 命令链 `;`
- 命令替换 `` ` ``, `$()`

**CI/Platform 模式**严格禁止，防止命令注入。

## 迁移指南

### 修改前（繁琐）

```go
func main() {
    projectRoot, _, err := resolveProjectRoot()
    if err != nil {
        log.Fatal(err)
    }

    rt, err := api.New(ctx, api.Options{
        EntryPoint:  api.EntryPointCLI,
        ProjectRoot: projectRoot,
        Sandbox: api.SandboxOptions{
            NetworkAllow: []string{
                "localhost",
                "127.0.0.1",
                "::1",
                "0.0.0.0",
                "*.local",
                "192.168.*",
                "10.*",
                "172.16.*",
            },
        },
    })
}

func resolveProjectRoot() (string, func(), error) {
    // 50+ 行重复代码...
}
```

### 修改后（简洁）

```go
func main() {
    rt, err := api.New(ctx, api.Options{
        EntryPoint:   api.EntryPointCLI,
        ModelFactory: provider,
        // 所有配置自动设置！
    })
}
```

**代码减少**：从 ~110 行 → ~30 行（-73%）

## 安全模式对比

| 配置项 | CLI 模式 | CI/Platform 模式 |
|--------|----------|------------------|
| Shell 元字符 | ✅ 允许 | ❌ 禁止 |
| 网络访问 | ✅ 本机全网段 | ✅ 本机全网段（同 CLI） |
| 项目根目录 | 自动解析 | 自动解析 |
| 危险命令 | ❌ 禁止 `rm`/`dd` 等 | ❌ 禁止 `rm`/`dd` 等 |

## 环境变量

| 变量名 | 作用 | 示例 |
|--------|------|------|
| `AGENTSDK_PROJECT_ROOT` | 覆盖项目根目录 | `/path/to/project` |
| `ANTHROPIC_API_KEY` | Anthropic API 密钥 | `sk-ant-...` |

## 最佳实践

### ✅ 推荐：使用默认值

```go
rt, err := api.New(ctx, api.Options{
    EntryPoint:   api.EntryPointCLI,
    ModelFactory: provider,
})
```

### ⚠️ 仅在必要时覆盖

```go
rt, err := api.New(ctx, api.Options{
    EntryPoint:  api.EntryPointCLI,
    ProjectRoot: "/custom/path",           // 自定义项目路径
    Sandbox: api.SandboxOptions{
        NetworkAllow: []string{"api.com"}, // 自定义网络
    },
})
```

### ❌ 避免：重复设置默认值

```go
// 不推荐：手动设置已有默认值
rt, err := api.New(ctx, api.Options{
    EntryPoint:  api.EntryPointCLI,
    ProjectRoot: ".",  // 冗余，会自动解析
    Sandbox: api.SandboxOptions{
        NetworkAllow: []string{"localhost"}, // 冗余，CLI 自动包含
    },
})
```

## 文件变更总结

### 新增文件
- `pkg/api/helpers.go` - 项目根目录解析工具函数

### 修改文件
- `pkg/api/options.go` - 添加智能默认值逻辑
- `pkg/security/validator.go` - 添加 shell 元字符开关
- `pkg/security/sandbox.go` - 暴露配置方法
- `pkg/tool/builtin/bash.go` - 添加配置接口
- `pkg/api/agent.go` - CLI 模式自动配置
- `examples/02-cli/main.go` - 简化示例代码

## 向后兼容性

✅ **完全向后兼容** - 所有现有代码无需修改继续工作。

新的默认值仅在配置项为空时生效，显式设置的值始终优先。
