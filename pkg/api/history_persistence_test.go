package api

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/message"
)

func TestDiskHistoryPersisterSaveLoadAndCleanup(t *testing.T) {
	root := t.TempDir()
	p := newDiskHistoryPersister(root)
	if p == nil {
		t.Fatalf("expected persister")
	}

	msgs := []message.Message{{Role: "user", Content: "hi"}}
	if err := p.Save("sess", msgs); err != nil {
		t.Fatalf("save: %v", err)
	}
	loaded, err := p.Load("sess")
	if err != nil || len(loaded) != 1 || loaded[0].Content != "hi" {
		t.Fatalf("load mismatch %v err=%v", loaded, err)
	}

	legacy := []message.Message{{Role: "assistant", Content: "ok"}}
	data, _ := json.Marshal(legacy)
	legacyPath := p.filePath("legacy")
	if err := os.WriteFile(legacyPath, data, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}
	loadedLegacy, err := p.Load("legacy")
	if err != nil || len(loadedLegacy) != 1 || loadedLegacy[0].Content != "ok" {
		t.Fatalf("legacy load mismatch %v err=%v", loadedLegacy, err)
	}

	oldPath := p.filePath("old")
	if err := os.WriteFile(oldPath, data, 0o600); err != nil {
		t.Fatalf("write old: %v", err)
	}
	oldTime := time.Now().AddDate(0, 0, -2)
	if err := os.Chtimes(oldPath, oldTime, oldTime); err != nil {
		t.Fatalf("chtime: %v", err)
	}
	if err := p.Cleanup(1); err != nil {
		t.Fatalf("cleanup: %v", err)
	}
	if _, err := os.Stat(oldPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected old file removed, got %v", err)
	}
}

func TestPersistHistoryWritesSnapshot(t *testing.T) {
	root := t.TempDir()
	p := newDiskHistoryPersister(root)
	if p == nil {
		t.Fatalf("expected persister")
	}
	rt := &Runtime{historyPersister: p}
	h := message.NewHistory()
	h.Append(message.Message{Role: "user", Content: "hello"})

	rt.persistHistory("sess", h)
	if _, err := os.Stat(filepath.Join(root, ".claude", "history", "sess.json")); err != nil {
		t.Fatalf("expected history file, got %v", err)
	}
}

func TestDiskHistoryPersisterFilePathAndLoadErrors(t *testing.T) {
	var nilPersister *diskHistoryPersister
	if got := nilPersister.filePath("sess"); got != "" {
		t.Fatalf("expected empty path for nil persister, got %q", got)
	}

	p := &diskHistoryPersister{dir: ""}
	if got := p.filePath("sess"); got != "" {
		t.Fatalf("expected empty path for blank dir, got %q", got)
	}
	p = &diskHistoryPersister{dir: t.TempDir()}
	if got := p.filePath("   "); !strings.HasSuffix(got, "default.json") {
		t.Fatalf("expected default path for blank session, got %q", got)
	}

	badPath := filepath.Join(p.dir, "bad.json")
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(badPath, []byte("{not-json"), 0o600); err != nil {
		t.Fatalf("write bad json: %v", err)
	}
	if _, err := p.Load("bad"); err == nil {
		t.Fatalf("expected decode error")
	}
}

func TestDiskHistoryPersisterSaveAndCleanupErrors(t *testing.T) {
	root := t.TempDir()
	filePath := filepath.Join(root, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	p := &diskHistoryPersister{dir: filePath}
	if err := p.Save("sess", []message.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatalf("expected save error for file dir")
	}

	if err := p.Cleanup(-1); err != nil {
		t.Fatalf("expected no error on negative retain days")
	}
	if err := p.Cleanup(1); err == nil {
		t.Fatalf("expected cleanup error for file dir")
	}
}

func TestDiskHistoryPersisterSaveRenameFallback(t *testing.T) {
	root := t.TempDir()
	p := newDiskHistoryPersister(root)
	if p == nil {
		t.Fatalf("expected persister")
	}
	dest := p.filePath("sess")
	if err := os.MkdirAll(dest, 0o700); err != nil {
		t.Fatalf("mkdir dest dir: %v", err)
	}
	if err := p.Save("sess", []message.Message{{Role: "user", Content: "hi"}}); err != nil {
		t.Fatalf("save failed: %v", err)
	}
	info, err := os.Stat(dest)
	if err != nil {
		t.Fatalf("stat dest: %v", err)
	}
	if info.IsDir() {
		t.Fatalf("expected dest to be file after save")
	}
}

func TestPersistHistorySkipsEmptyCases(t *testing.T) {
	var rt *Runtime
	rt.persistHistory("sess", message.NewHistory())

	rt = &Runtime{historyPersister: newDiskHistoryPersister(t.TempDir())}
	rt.persistHistory(" ", message.NewHistory())
	h := message.NewHistory()
	rt.persistHistory("sess", h)
}

func TestDiskHistoryPersisterSaveMarshalError(t *testing.T) {
	root := t.TempDir()
	p := newDiskHistoryPersister(root)
	if p == nil {
		t.Fatalf("expected persister")
	}
	msgs := []message.Message{{
		Role: "user",
		ToolCalls: []message.ToolCall{{
			ID:        "1",
			Name:      "tool",
			Arguments: map[string]any{"bad": func() {}},
		}},
	}}
	if err := p.Save("sess", msgs); err == nil {
		t.Fatalf("expected marshal error")
	}
}

func TestDiskHistoryPersisterSaveCreateTempError(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("chmod permissions behave differently on windows")
	}
	root := t.TempDir()
	p := newDiskHistoryPersister(root)
	if p == nil {
		t.Fatalf("expected persister")
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.Chmod(p.dir, 0o500); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	t.Cleanup(func() { _ = os.Chmod(p.dir, 0o700) })

	if err := p.Save("sess", []message.Message{{Role: "user", Content: "hi"}}); err == nil {
		t.Fatalf("expected create temp error")
	}
}

func TestNewDiskHistoryPersisterEmptyRoot(t *testing.T) {
	if p := newDiskHistoryPersister(" "); p != nil {
		t.Fatalf("expected nil persister for empty root")
	}
}
