package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func mustWrite(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
}

func writeSkill(t *testing.T, path, name, body string) {
	t.Helper()
	frontmatter := strings.Join([]string{
		"---",
		"name: " + name,
		"description: test",
		"---",
		body,
	}, "\n")
	mustWrite(t, path, frontmatter)
}
