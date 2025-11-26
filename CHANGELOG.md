# Changelog

All notable changes to agentsdk-go will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [0.3.0] - 2025-01-26

### Added
- **Custom tool registration**: New `api.WithCustomTools()` option allows registering custom tools at runtime without modifying built-in tool registry (`pkg/api/agent.go`)
- Example 05: Custom tools demo showcasing registration and execution (`examples/05-custom-tools/`)

### Changed
- **BREAKING**: Removed global configuration loading (`~/.claude/`). SDK now only loads project-level config from `.claude/` directory (`pkg/config/`)
- Documentation: Translated core docs (API reference, getting started, security) to English

### Fixed
- File permission warnings: Changed default file mode from `0o644` to `0o600` for security compliance
- Removed unused `taskTool` variable in tests

### Documentation
- Updated README files to reflect custom tool registration feature
- Consolidated custom tools guide with reduced redundancy
- Added comprehensive examples for tool registration patterns

---

## [0.3.0] - 2025-01-26

### 新增
- **自定义工具注册**：新增 `api.WithCustomTools()` 选项，支持运行时注册自定义工具，无需修改内置工具注册表（`pkg/api/agent.go`）
- 示例 05：自定义工具演示，展示工具注册与执行（`examples/05-custom-tools/`）

### 变更
- **破坏性变更**：移除全局配置加载（`~/.claude/`），SDK 现仅从 `.claude/` 目录加载项目级配置（`pkg/config/`）
- 文档：核心文档（API 参考、入门指南、安全指南）翻译为英文

### 修复
- 文件权限警告：将默认文件权限从 `0o644` 改为 `0o600` 以符合安全规范
- 移除测试中未使用的 `taskTool` 变量

### 文档
- 更新 README 以反映自定义工具注册功能
- 精简自定义工具指南，减少冗余内容
- 添加工具注册模式的完整示例

---

## [0.2.0] - 2025-01-XX

### Added
- Initial public release
- Core agent loop with 6-point middleware system
- Built-in tools: bash, file_read, file_write, grep, glob
- MCP integration support
- Configuration hot-reload
- Sandbox security layer

### 新增
- 首次公开发布
- 核心 Agent 循环与 6 点中间件系统
- 内置工具：bash、file_read、file_write、grep、glob
- MCP 集成支持
- 配置热重载
- 沙箱安全层

---

## [0.1.0] - 2025-01-XX

### Added
- Project initialization
- Basic agent architecture

### 新增
- 项目初始化
- 基础 Agent 架构
