package commands

import (
	"io/fs"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/config"
)

func TestParseFrontMatterAndApplyArguments(t *testing.T) {
	meta, body, err := parseFrontMatter("hello")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if meta != (CommandMetadata{}) {
		t.Fatalf("expected empty metadata, got %v", meta)
	}
	if body != "hello" {
		t.Fatalf("unexpected body %q", body)
	}

	withMeta := "\uFEFF---\nname: demo\ndescription: test\n---\nbody\nline"
	meta, body, err = parseFrontMatter(withMeta)
	if err != nil {
		t.Fatalf("parse with meta: %v", err)
	}
	if meta.Name != "demo" || meta.Description != "test" {
		t.Fatalf("unexpected meta: %+v", meta)
	}
	if body != "body\nline" {
		t.Fatalf("unexpected body %q", body)
	}

	if _, _, err := parseFrontMatter("---\nname: bad\n"); err == nil {
		t.Fatalf("expected missing closing frontmatter error")
	}

	if got := applyArguments("plain", nil); got != "plain" {
		t.Fatalf("unexpected plain result %q", got)
	}
	got := applyArguments("a $ARGUMENTS b $1 c $2 d $3", []string{"x", "y"})
	if got != "a x y b x c y d " {
		t.Fatalf("unexpected args result %q", got)
	}
	got = applyArguments("x $1 y $2", nil)
	if got != "x  y " {
		t.Fatalf("unexpected missing args result %q", got)
	}
}

func TestResolveFileOpsAndWalkDir(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "cmd.md")
	mustWrite(t, path, "hello")

	fsLayer := config.NewFS(dir, nil)
	ops := resolveFileOps(LoaderOptions{FS: fsLayer})
	data, err := ops.readFile(path)
	if err != nil || string(data) != "hello" {
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

	walk := resolveWalkDirFunc(LoaderOptions{FS: fsLayer})
	seen := false
	if err := walk(dir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && strings.HasSuffix(p, "cmd.md") {
			seen = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}
	if !seen {
		t.Fatalf("expected walk to visit file")
	}

	restore := SetCommandFileOpsForTest(func(string) ([]byte, error) { return []byte("ok"), nil }, nil, nil)
	defer restore()
	ops2 := resolveFileOps(LoaderOptions{})
	data, err = ops2.readFile("ignored")
	if err != nil || string(data) != "ok" {
		t.Fatalf("expected override read, got %q err=%v", data, err)
	}
}

func TestBuildMetadataMapAndReadFrontMatter(t *testing.T) {
	meta := CommandMetadata{
		AllowedTools:           "Bash",
		ArgumentHint:           "hint",
		Model:                  "model",
		DisableModelInvocation: true,
	}
	out := buildMetadataMap(meta, "/tmp/cmd.md")
	if out["allowed-tools"] != "Bash" || out["argument-hint"] != "hint" || out["model"] != "model" || out["disable-model-invocation"] != true || out["source"] != "/tmp/cmd.md" {
		t.Fatalf("unexpected metadata map %v", out)
	}
	if buildMetadataMap(CommandMetadata{}, "") != nil {
		t.Fatalf("expected nil metadata map")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, "cmd.md")
	mustWrite(t, path, "---\nname: demo\ndescription: desc\nallowed-tools: Bash\n---\nbody")
	got, err := readFrontMatterMetadata(path, resolveFileOps(LoaderOptions{}))
	if err != nil || got.Name != "demo" || got.Description != "desc" || got.AllowedTools != "Bash" {
		t.Fatalf("unexpected frontmatter %v err=%v", got, err)
	}
}

func TestLoadedMetaOrFallback(t *testing.T) {
	loader := &lazyCommandBody{metadata: CommandMetadata{Name: "base"}}
	if got := loader.loadedMetaOrFallback(); got.Name != "base" {
		t.Fatalf("expected fallback metadata, got %v", got)
	}
	loader.loadedMeta = CommandMetadata{Name: "loaded"}
	if got := loader.loadedMetaOrFallback(); got.Name != "loaded" {
		t.Fatalf("expected loaded metadata, got %v", got)
	}
}
