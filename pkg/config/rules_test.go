package config

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func makeRulesRoot(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".claude", "rules")
	require.NoError(t, os.MkdirAll(rulesDir, 0o755))
	return root, rulesDir
}

func writeRuleFile(t *testing.T, dir, name, content string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(path, []byte(content), 0o600))
	return path
}

func TestRulesLoader_LoadRulesBasic(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "01-security.md", "rule1")
	writeRuleFile(t, dir, "custom.md", "rule2")

	loader := NewRulesLoader(root)
	rules, err := loader.LoadRules()
	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, "01-security", rules[0].Name)
	require.Equal(t, 1, rules[0].Priority)
	require.Equal(t, "custom", rules[1].Name)
	require.Equal(t, maxPriority, rules[1].Priority)
	require.Equal(t, "rule1\n\nrule2", loader.GetContent())
}

func TestRulesLoader_Sorting(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "02-style.md", "style")
	writeRuleFile(t, dir, "01-security.md", "security")
	writeRuleFile(t, dir, "10-advanced.md", "advanced")
	writeRuleFile(t, dir, "custom.md", "custom")
	writeRuleFile(t, dir, "guide.md", "guide")

	loader := NewRulesLoader(root)
	rules, err := loader.LoadRules()
	require.NoError(t, err)
	require.Len(t, rules, 5)

	names := []string{rules[0].Name, rules[1].Name, rules[2].Name, rules[3].Name, rules[4].Name}
	require.Equal(t, []string{"01-security", "02-style", "10-advanced", "custom", "guide"}, names)
}

func TestRulesLoader_EmptyDir(t *testing.T) {
	root, _ := makeRulesRoot(t)
	loader := NewRulesLoader(root)
	rules, err := loader.LoadRules()
	require.NoError(t, err)
	require.Empty(t, rules)
	require.Equal(t, "", loader.GetContent())
}

func TestRulesLoader_MissingDir(t *testing.T) {
	root := t.TempDir()
	loader := NewRulesLoader(root)
	rules, err := loader.LoadRules()
	require.NoError(t, err)
	require.Empty(t, rules)
}

func TestRulesLoader_InvalidFilesSkipped(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "01-a.md", "a")
	require.NoError(t, os.WriteFile(filepath.Join(dir, "note.txt"), []byte("ignored"), 0o600))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "subdir"), 0o755))
	writeRuleFile(t, dir, "ZZ-b.MD", "b")

	loader := NewRulesLoader(root)
	rules, err := loader.LoadRules()
	require.NoError(t, err)
	require.Len(t, rules, 2)
	require.Equal(t, "01-a", rules[0].Name)
	require.Equal(t, "ZZ-b", rules[1].Name)
	require.Equal(t, maxPriority, rules[1].Priority)
}

func TestRulesLoader_WatchChanges(t *testing.T) {
	root, dir := makeRulesRoot(t)
	path := writeRuleFile(t, dir, "01-one.md", "one")

	loader := NewRulesLoader(root)
	_, err := loader.LoadRules()
	require.NoError(t, err)

	updates := make(chan []Rule, 4)
	require.NoError(t, loader.WatchChanges(func(r []Rule) {
		updates <- r
	}))

	time.Sleep(50 * time.Millisecond)
	require.NoError(t, os.WriteFile(path, []byte("one updated"), 0o600))

	select {
	case r := <-updates:
		require.Len(t, r, 1)
		require.Equal(t, "one updated", r[0].Content)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for rules reload")
	}

	require.Equal(t, "one updated", loader.GetContent())
	require.NoError(t, loader.Close())
}

func TestRulesLoader_ConcurrentAccess(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "01-a.md", "a")
	writeRuleFile(t, dir, "02-b.md", "b")

	loader := NewRulesLoader(root)
	_, err := loader.LoadRules()
	require.NoError(t, err)

	var wg sync.WaitGroup
	done := make(chan struct{})

	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
					_ = loader.GetContent()
					runtime.Gosched()
				}
			}
		}()
	}

	for i := 0; i < 20; i++ {
		require.NoError(t, os.WriteFile(filepath.Join(dir, "02-b.md"), []byte(fmt.Sprintf("b%d", i)), 0o600))
		_, err := loader.LoadRules()
		require.NoError(t, err)
	}

	close(done)
	wg.Wait()
}

func TestRulesLoader_RulesDirFallback(t *testing.T) {
	loader := NewRulesLoader("  ")
	require.Equal(t, filepath.Join(".", ".claude", "rules"), loader.rulesDir())
}

func TestRulesLoader_WatchChangesMissingDirNoop(t *testing.T) {
	root := t.TempDir()
	loader := NewRulesLoader(root)
	require.NoError(t, loader.WatchChanges(nil))
	require.NoError(t, loader.Close())
}

func TestRulesLoader_WatchChangesIdempotent(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "01-a.md", "a")
	loader := NewRulesLoader(root)
	_, err := loader.LoadRules()
	require.NoError(t, err)

	require.NoError(t, loader.WatchChanges(nil))
	first := loader.watcher
	require.NotNil(t, first)
	require.NoError(t, loader.WatchChanges(nil))
	require.Equal(t, first, loader.watcher)
	require.NoError(t, loader.Close())
}

func TestRulesLoader_WatchChangesNonDirNoop(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "rules"), []byte("not a dir"), 0o600))

	loader := NewRulesLoader(root)
	require.NoError(t, loader.WatchChanges(nil))
	require.Nil(t, loader.watcher)
}

func TestRulesLoader_GetContentSkipsEmptyRules(t *testing.T) {
	root, dir := makeRulesRoot(t)
	writeRuleFile(t, dir, "01-a.md", "a")
	writeRuleFile(t, dir, "02-empty.md", "   \n\t")

	loader := NewRulesLoader(root)
	_, err := loader.LoadRules()
	require.NoError(t, err)
	require.Equal(t, "a", loader.GetContent())
}

func TestPriorityFromBase_OverflowReturnsMax(t *testing.T) {
	base := strings.Repeat("9", 100) + "-x"
	require.Equal(t, maxPriority, priorityFromBase(base))
}

func TestRulesLoader_LoadRulesReadDirError(t *testing.T) {
	root := t.TempDir()
	claudeDir := filepath.Join(root, ".claude")
	require.NoError(t, os.MkdirAll(claudeDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(claudeDir, "rules"), []byte("not a dir"), 0o600))

	loader := NewRulesLoader(root)
	_, err := loader.LoadRules()
	require.Error(t, err)
}
