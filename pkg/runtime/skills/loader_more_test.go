package skills

import (
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestParseSkillFileMismatchAndInvalid(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	path := filepath.Join(dir, "SKILL.md")
	writeSkill(t, path, "wrong", "body")

	if _, err := parseSkillFile(path, "demo", config.NewFS(root, nil)); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("expected name mismatch error, got %v", err)
	}

	badPath := filepath.Join(dir, "SKILL.md")
	mustWrite(t, badPath, "---\nname: demo\n---\nbody")
	if _, err := parseSkillFile(badPath, "demo", config.NewFS(root, nil)); err == nil || !strings.Contains(err.Error(), "validate") {
		t.Fatalf("expected validate error, got %v", err)
	}
}

func TestBuildDefinitionMetadata(t *testing.T) {
	file := SkillFile{
		Path: "/tmp/skill",
		Metadata: SkillMetadata{
			Metadata:      map[string]string{"a": "1"},
			AllowedTools:  ToolList{"ToolA", "ToolB"},
			License:       "MIT",
			Compatibility: "v1",
		},
	}
	meta := buildDefinitionMetadata(file)
	if meta["a"] != "1" || meta["allowed-tools"] != "ToolA,ToolB" || meta["license"] != "MIT" || meta["compatibility"] != "v1" || meta["source"] != "/tmp/skill" {
		t.Fatalf("unexpected metadata map: %v", meta)
	}
	if got := buildDefinitionMetadata(SkillFile{}); got != nil {
		t.Fatalf("expected nil metadata, got %v", got)
	}
}

func TestResolveFileOps(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "SKILL.md")
	mustWrite(t, path, "body")

	fsLayer := config.NewFS(root, nil)
	ops := resolveFileOps(fsLayer)
	data, err := ops.readFile(path)
	if err != nil || string(data) != "body" {
		t.Fatalf("unexpected read %q err=%v", data, err)
	}
	file, err := ops.openFile(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	_ = file.Close()
	if info, err := ops.statFile(path); err != nil || info == nil {
		t.Fatalf("stat: %v", err)
	}

	restore := SetSkillFileOpsForTest(
		func(string) ([]byte, error) { return []byte("ok"), nil },
		func(string) (fs.FileInfo, error) { return &mockFileInfo{modTime: time.Now()}, nil },
	)
	defer restore()
	ops2 := resolveFileOps(nil)
	data, err = ops2.readFile("ignored")
	if err != nil || string(data) != "ok" {
		t.Fatalf("expected override read, got %q err=%v", data, err)
	}
}
