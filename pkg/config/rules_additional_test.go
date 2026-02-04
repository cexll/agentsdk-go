package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRulesLoaderWatchChanges(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".claude", "rules")
	if err := os.MkdirAll(rulesDir, 0o700); err != nil {
		t.Fatalf("mkdir rules: %v", err)
	}
	loader := NewRulesLoader(root)

	updates := make(chan []Rule, 1)
	if err := loader.WatchChanges(func(r []Rule) {
		select {
		case updates <- r:
		default:
		}
	}); err != nil {
		t.Fatalf("watch changes: %v", err)
	}

	path := filepath.Join(rulesDir, "01-demo.md")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write rule: %v", err)
	}

	select {
	case got := <-updates:
		if len(got) == 0 || got[0].Name != "01-demo" {
			t.Fatalf("unexpected rules %+v", got)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for rules update")
	}

	if err := loader.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
}

func TestRulesLoaderWatchChangesNonDir(t *testing.T) {
	root := t.TempDir()
	rulesDir := filepath.Join(root, ".claude", "rules")
	if err := os.MkdirAll(filepath.Dir(rulesDir), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(rulesDir, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	loader := NewRulesLoader(root)
	if err := loader.WatchChanges(nil); err != nil {
		t.Fatalf("expected no error for non-dir rules path, got %v", err)
	}
}
