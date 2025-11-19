package api

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ResolveProjectRoot 智能解析项目根目录。
// 优先级：环境变量 > 向上查找 go.mod > 当前工作目录
// 自动处理符号链接（macOS /var -> /private/var）
func ResolveProjectRoot() (string, error) {
	// 优先使用环境变量指定的路径
	if root := strings.TrimSpace(os.Getenv("AGENTSDK_PROJECT_ROOT")); root != "" {
		abs, err := filepath.Abs(root)
		if err != nil {
			return "", fmt.Errorf("resolve project root: %w", err)
		}
		// 解析符号链接
		if resolved, err := filepath.EvalSymlinks(abs); err == nil {
			return resolved, nil
		}
		return abs, nil
	}

	// 默认使用实际项目根目录（向上查找包含 go.mod 的目录）
	cwd, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	// 从当前目录向上查找 go.mod
	current := cwd
	for {
		gomod := filepath.Join(current, "go.mod")
		if _, err := os.Stat(gomod); err == nil {
			// 找到 go.mod，解析符号链接
			if resolved, err := filepath.EvalSymlinks(current); err == nil {
				return resolved, nil
			}
			return current, nil
		}

		parent := filepath.Dir(current)
		if parent == current {
			// 已到达根目录，回退到使用当前目录
			break
		}
		current = parent
	}

	// 未找到 go.mod，使用当前工作目录
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		return resolved, nil
	}
	return cwd, nil
}
