//go:build ignore
// +build ignore

package main

import (
	"embed"
	"fmt"
	"io/fs"
	"log"
)

//go:embed .claude
var claudeFS embed.FS

func main() {
	fmt.Println("=== 验证嵌入文件系统 ===")
	fmt.Println()

	// 验证 settings.json
	data, err := fs.ReadFile(claudeFS, ".claude/settings.json")
	if err != nil {
		log.Fatalf("读取 settings.json 失败: %v", err)
	}
	fmt.Printf("✓ settings.json 已嵌入 (%d 字节)\n", len(data))
	fmt.Printf("  内容预览: %s\n", string(data[:min(100, len(data))]))
	fmt.Println()

	// 验证 skill
	data, err = fs.ReadFile(claudeFS, ".claude/skills/demo/SKILL.md")
	if err != nil {
		log.Fatalf("读取 SKILL.md 失败: %v", err)
	}
	fmt.Printf("✓ skills/demo/SKILL.md 已嵌入 (%d 字节)\n", len(data))
	fmt.Printf("  内容预览: %s\n", string(data[:min(100, len(data))]))
	fmt.Println()

	// 列出所有嵌入的文件
	fmt.Println("嵌入的文件列表:")
	err = fs.WalkDir(claudeFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			fmt.Printf("  - %s\n", path)
		}
		return nil
	})
	if err != nil {
		log.Fatalf("遍历嵌入文件失败: %v", err)
	}

	fmt.Println()
	fmt.Println("✓ 所有嵌入文件验证成功！")
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
