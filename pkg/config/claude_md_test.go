package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadClaudeMDWithIncludes(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("hello\n@extra.md\n```\\n@ignored.md\\n```"), 0o600); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "extra.md"), []byte("extra"), 0o600); err != nil {
		t.Fatalf("write extra: %v", err)
	}
	content, err := LoadClaudeMD(root, nil)
	if err != nil {
		t.Fatalf("load failed: %v", err)
	}
	if !strings.Contains(content, "extra") || !strings.Contains(content, "@ignored.md") {
		t.Fatalf("unexpected content %q", content)
	}
}

func TestLoadClaudeMDErrors(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "CLAUDE.md"), []byte("@../outside.md"), 0o600); err != nil {
		t.Fatalf("write claude: %v", err)
	}
	if _, err := LoadClaudeMD(root, nil); err == nil {
		t.Fatalf("expected include escape error")
	}
	if content, err := LoadClaudeMD(filepath.Join(root, "missing"), nil); err != nil || content != "" {
		t.Fatalf("expected missing claude md empty, got %q err=%v", content, err)
	}
}

func TestReadFileLimited(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "small.md")
	if err := os.WriteFile(path, []byte("hello"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, err := readFileLimited(nil, "", 10); err == nil {
		t.Fatalf("expected empty path error")
	}

	data, err := readFileLimited(nil, path, 10)
	if err != nil || string(data) != "hello" {
		t.Fatalf("unexpected read %q err=%v", data, err)
	}

	if _, err := readFileLimited(nil, path, 1); err == nil {
		t.Fatalf("expected size limit error")
	}

	if _, err := readFileLimited(nil, path, 0); err != nil {
		t.Fatalf("expected default max bytes to allow read: %v", err)
	}

	fsLayer := NewFS(dir, nil)
	data2, err := readFileLimited(fsLayer, path, 10)
	if err != nil || string(data2) != "hello" {
		t.Fatalf("unexpected fs read %q err=%v", data2, err)
	}
}
