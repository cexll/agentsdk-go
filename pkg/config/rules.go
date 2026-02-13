package config

import (
	"errors"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/fsnotify/fsnotify"
)

// Rule represents a single project rule loaded from .claude/rules.
type Rule struct {
	Name     string // 文件名（不含扩展名）
	Content  string // 规则内容
	Priority int    // 优先级（从文件名前缀解析，如 01-xxx.md -> 1）
}

// RulesLoader loads markdown rules under .claude/rules and watches for changes.
type RulesLoader struct {
	projectRoot string
	rules       []Rule
	mu          sync.RWMutex
	watcher     *fsnotify.Watcher
}

const maxPriority = int(^uint(0) >> 1)

func NewRulesLoader(projectRoot string) *RulesLoader {
	return &RulesLoader{projectRoot: projectRoot}
}

func (l *RulesLoader) rulesDir() string {
	root := strings.TrimSpace(l.projectRoot)
	if root == "" {
		root = "."
	}
	return filepath.Join(root, ".claude", "rules")
}

func (l *RulesLoader) LoadRules() ([]Rule, error) {
	dir := l.rulesDir()
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.mu.Lock()
			l.rules = nil
			l.mu.Unlock()
			return nil, nil
		}
		return nil, err
	}
	if !info.IsDir() {
		return nil, fs.ErrInvalid
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			l.mu.Lock()
			l.rules = nil
			l.mu.Unlock()
			return nil, nil
		}
		return nil, err
	}

	rules := make([]Rule, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		filename := entry.Name()
		if strings.ToLower(filepath.Ext(filename)) != ".md" {
			continue
		}
		path := filepath.Join(dir, filename)
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		base := strings.TrimSuffix(filename, filepath.Ext(filename))
		rules = append(rules, Rule{
			Name:     base,
			Content:  strings.TrimSpace(string(data)),
			Priority: priorityFromBase(base),
		})
	}

	sort.SliceStable(rules, func(i, j int) bool {
		if rules[i].Priority != rules[j].Priority {
			return rules[i].Priority < rules[j].Priority
		}
		return rules[i].Name < rules[j].Name
	})

	l.mu.Lock()
	l.rules = rules
	l.mu.Unlock()

	return append([]Rule(nil), rules...), nil
}

func priorityFromBase(base string) int {
	dash := strings.Index(base, "-")
	if dash <= 0 {
		return maxPriority
	}
	prefix := base[:dash]
	for _, ch := range prefix {
		if ch < '0' || ch > '9' {
			return maxPriority
		}
	}
	n, err := strconv.Atoi(prefix)
	if err != nil {
		return maxPriority
	}
	return n
}

// WatchChanges starts watching the rules directory and reloads on changes.
// If the rules directory doesn't exist, this is a no-op.
func (l *RulesLoader) WatchChanges(callback func([]Rule)) error {
	dir := l.rulesDir()
	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return err
	}
	if !info.IsDir() {
		return nil
	}

	l.mu.Lock()
	if l.watcher != nil {
		l.mu.Unlock()
		return nil
	}
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		l.mu.Unlock()
		return err
	}
	l.watcher = watcher
	l.mu.Unlock()

	if err := watcher.Add(dir); err != nil {
		_ = watcher.Close()
		l.mu.Lock()
		l.watcher = nil
		l.mu.Unlock()
		return err
	}

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&(fsnotify.Create|fsnotify.Write|fsnotify.Remove|fsnotify.Rename) == 0 {
					continue
				}
				if strings.ToLower(filepath.Ext(event.Name)) != ".md" {
					continue
				}
				rules, err := l.LoadRules()
				if err != nil {
					log.Printf("rules: reload failed: %v", err)
					continue
				}
				if callback != nil {
					callback(rules)
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf("rules: watcher error: %v", err)
			}
		}
	}()

	return nil
}

// GetContent merges all loaded rule contents separated by double newlines.
func (l *RulesLoader) GetContent() string {
	l.mu.RLock()
	defer l.mu.RUnlock()
	if len(l.rules) == 0 {
		return ""
	}
	parts := make([]string, 0, len(l.rules))
	for _, rule := range l.rules {
		if strings.TrimSpace(rule.Content) == "" {
			continue
		}
		parts = append(parts, rule.Content)
	}
	return strings.Join(parts, "\n\n")
}

func (l *RulesLoader) Close() error {
	l.mu.Lock()
	watcher := l.watcher
	l.watcher = nil
	l.mu.Unlock()
	if watcher != nil {
		return watcher.Close()
	}
	return nil
}
