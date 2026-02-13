package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// FS 提供统一的文件系统访问接口，支持 OS 文件系统和嵌入 FS。
// OS 优先：只有当 OS 操作失败时才会回退到嵌入 FS。
type FS struct {
	projectRoot string
	embedFS     fs.FS
}

// NewFS 创建新的文件系统抽象层实例。
func NewFS(projectRoot string, embedFS fs.FS) *FS {
	root := strings.TrimSpace(projectRoot)
	if root != "" {
		root = filepath.Clean(root)
		if abs, err := filepath.Abs(root); err == nil {
			root = abs
		}
	}
	return &FS{
		projectRoot: root,
		embedFS:     embedFS,
	}
}

// ReadFile 读取文件内容，OS 优先，失败时回退到嵌入 FS。
func (f *FS) ReadFile(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err == nil || f.embedFS == nil {
		return data, err
	}

	embedPath := f.toEmbedPath(path)
	data, embedErr := fs.ReadFile(f.embedFS, embedPath)
	if embedErr != nil {
		return nil, fmt.Errorf("read file %s: %w", path, errors.Join(err, embedErr))
	}
	return data, nil
}

// Open 打开指定路径文件，OS 优先，失败时回退到嵌入 FS。
func (f *FS) Open(path string) (fs.File, error) {
	osFile, err := os.Open(path)
	if err == nil || f.embedFS == nil {
		return osFile, err
	}

	embedPath := f.toEmbedPath(path)
	file, embedErr := f.embedFS.Open(embedPath)
	if embedErr != nil {
		return nil, fmt.Errorf("open file %s: %w", path, errors.Join(err, embedErr))
	}
	return file, nil
}

// Stat 返回文件信息，OS 优先，失败时回退到嵌入 FS。
func (f *FS) Stat(path string) (fs.FileInfo, error) {
	info, err := os.Stat(path)
	if err == nil || f.embedFS == nil {
		return info, err
	}

	embedPath := f.toEmbedPath(path)
	info, embedErr := fs.Stat(f.embedFS, embedPath)
	if embedErr != nil {
		return nil, fmt.Errorf("stat file %s: %w", path, errors.Join(err, embedErr))
	}
	return info, nil
}

// ReadDir 读取目录内容，OS 优先，失败时回退到嵌入 FS。
func (f *FS) ReadDir(path string) ([]fs.DirEntry, error) {
	entries, err := os.ReadDir(path)
	if err == nil || f.embedFS == nil {
		return entries, err
	}

	embedPath := f.toEmbedPath(path)
	entries, embedErr := fs.ReadDir(f.embedFS, embedPath)
	if embedErr != nil {
		return nil, fmt.Errorf("read dir %s: %w", path, errors.Join(err, embedErr))
	}
	return entries, nil
}

// WalkDir 遍历目录树，OS 优先，失败时回退到嵌入 FS。
func (f *FS) WalkDir(root string, fn fs.WalkDirFunc) error {
	_, statErr := os.Stat(root)
	if statErr == nil {
		return filepath.WalkDir(root, fn)
	}
	if f.embedFS == nil {
		return statErr
	}

	embedRoot := f.toEmbedPath(root)
	if embedRoot == "" {
		embedRoot = "."
	}

	adapter := func(path string, d fs.DirEntry, walkErr error) error {
		if f.projectRoot == "" {
			return fn(filepath.FromSlash(path), d, walkErr)
		}
		full := filepath.Join(f.projectRoot, filepath.FromSlash(path))
		return fn(full, d, walkErr)
	}

	return fs.WalkDir(f.embedFS, embedRoot, adapter)
}

// toEmbedPath 将绝对路径转换为嵌入 FS 的相对路径。
func (f *FS) toEmbedPath(path string) string {
	cleaned := filepath.Clean(path)
	if cleaned == "." && path == "" {
		cleaned = ""
	}

	absPath := cleaned
	if !filepath.IsAbs(absPath) && !isWindowsAbs(absPath) {
		if f.projectRoot != "" {
			absPath = filepath.Join(f.projectRoot, absPath)
		} else if resolved, err := filepath.Abs(absPath); err == nil {
			absPath = resolved
		}
	}

	pathSlash := normalizeSlashes(absPath)
	rootSlash := normalizeSlashes(f.projectRoot)
	if rootSlash != "" {
		rootPrefix := strings.TrimRight(rootSlash, "/")
		switch {
		case rootPrefix == "":
			pathSlash = strings.TrimLeft(pathSlash, "/")
		case pathSlash == rootPrefix:
			pathSlash = ""
		case strings.HasPrefix(pathSlash, rootPrefix+"/"):
			pathSlash = strings.TrimPrefix(pathSlash, rootPrefix+"/")
		}
	}

	pathSlash = strings.TrimLeft(pathSlash, "/")
	return pathSlash
}

func normalizeSlashes(path string) string {
	if path == "" {
		return ""
	}
	return strings.ReplaceAll(filepath.ToSlash(path), "\\", "/")
}

func isWindowsAbs(path string) bool {
	if len(path) < 3 {
		return false
	}
	if path[0] == '\\' && path[1] == '\\' {
		return true
	}
	if path[1] != ':' {
		return false
	}
	letter := path[0]
	if (letter < 'A' || letter > 'Z') && (letter < 'a' || letter > 'z') {
		return false
	}
	sep := path[2]
	return sep == '\\' || sep == '/'
}
