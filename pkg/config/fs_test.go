package config

import (
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"testing/fstest"
)

func TestFS_ReadFile_OSOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	want := `{"source":"os"}`
	if err := os.WriteFile(path, []byte(want), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fsys := NewFS(dir, nil)
	data, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != want {
		t.Fatalf("unexpected data: %s", data)
	}
}

func TestFS_Open_OSOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "file.txt")
	if err := os.WriteFile(path, []byte("open-os"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fsys := NewFS(dir, nil)
	file, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() {
		file.Close()
	})

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read opened file: %v", err)
	}
	if got := string(data); got != "open-os" {
		t.Fatalf("unexpected content: %s", got)
	}
}

func TestFS_Stat_OSOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "stat.txt")
	content := []byte("stat-os")
	if err := os.WriteFile(path, content, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fsys := NewFS(dir, nil)
	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Size() != int64(len(content)) {
		t.Fatalf("unexpected size: %d", info.Size())
	}
}

func TestFS_ReadDir_OSOnly(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "dir")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("b"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	fsys := NewFS(dir, nil)
	entries, err := fsys.ReadDir(dir)
	if err != nil {
		t.Fatalf("readdir: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"a.txt", "b.txt", "dir"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected entries: %v", names)
	}
}

func TestFS_WalkDir_OSOnly(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "walk")
	if err := os.MkdirAll(filepath.Join(root, "nested"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "root.txt"), []byte("root"), 0o600); err != nil {
		t.Fatalf("write root: %v", err)
	}
	if err := os.WriteFile(filepath.Join(root, "nested", "child.txt"), []byte("child"), 0o600); err != nil {
		t.Fatalf("write child: %v", err)
	}

	fsys := NewFS(dir, nil)
	visited := map[string]struct{}{}
	if err := fsys.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visited[filepath.ToSlash(rel)] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk: %v", err)
	}

	for _, want := range []string{".", "nested", "nested/child.txt", "root.txt"} {
		if _, ok := visited[want]; !ok {
			t.Fatalf("missing path %s", want)
		}
	}
}

func TestFS_ReadFile_EmbedOnly(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	path := filepath.Join(dir, ".claude", "settings.json")
	data, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("read embed: %v", err)
	}
	if string(data) != `{"source":"embed"}` {
		t.Fatalf("unexpected data: %s", data)
	}
}

func TestFS_Open_EmbedOnly(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	path := filepath.Join(dir, ".claude", "settings.json")
	file, err := fsys.Open(path)
	if err != nil {
		t.Fatalf("open embed: %v", err)
	}
	t.Cleanup(func() {
		file.Close()
	})

	data, err := io.ReadAll(file)
	if err != nil {
		t.Fatalf("read embed: %v", err)
	}
	if string(data) != `{"source":"embed"}` {
		t.Fatalf("unexpected embed data: %s", data)
	}
}

func TestFS_Open_ErrorFallback(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, fstest.MapFS{})
	path := filepath.Join(dir, ".claude", "missing.txt")
	if _, err := fsys.Open(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_Open_NilEmbedFS(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, nil)
	path := filepath.Join(dir, "missing.txt")
	if _, err := fsys.Open(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_Stat_EmbedOnly(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	path := filepath.Join(dir, ".claude", "settings.json")
	info, err := fsys.Stat(path)
	if err != nil {
		t.Fatalf("stat embed: %v", err)
	}
	if info.Size() == 0 {
		t.Fatalf("expected non-zero size")
	}
}

func TestFS_Stat_FallbackError(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, fstest.MapFS{})
	path := filepath.Join(dir, ".claude", "missing.txt")
	if _, err := fsys.Stat(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_ReadDir_EmbedOnly(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	path := filepath.Join(dir, ".claude", "skills")
	entries, err := fsys.ReadDir(path)
	if err != nil {
		t.Fatalf("readdir embed: %v", err)
	}
	var names []string
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	sort.Strings(names)
	want := []string{"alpha", "beta"}
	if strings.Join(names, ",") != strings.Join(want, ",") {
		t.Fatalf("unexpected embed entries: %v", names)
	}
}

func TestFS_ReadDir_FallbackError(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, fstest.MapFS{})
	path := filepath.Join(dir, ".claude", "missing")
	if _, err := fsys.ReadDir(path); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_WalkDir_EmbedOnly(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	root := filepath.Join(dir, ".claude", "walk")
	visited := map[string]struct{}{}
	if err := fsys.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			t.Fatalf("unexpected walk error: %v", walkErr)
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		visited[filepath.ToSlash(rel)] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk embed: %v", err)
	}
	for _, want := range []string{".", "inner", "inner/file.txt"} {
		if _, ok := visited[want]; !ok {
			t.Fatalf("missing embed path %s", want)
		}
	}
}

func TestFS_WalkDir_FallbackError(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, fstest.MapFS{})
	root := filepath.Join(dir, ".claude", "missing")
	if err := fsys.WalkDir(root, func(_ string, _ fs.DirEntry, walkErr error) error {
		return walkErr
	}); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_WalkDir_NoProjectRoot(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, ".claude", "walk")
	key := strings.TrimLeft(filepath.ToSlash(root), "/")
	embed := fstest.MapFS{
		key:               &fstest.MapFile{Mode: fs.ModeDir},
		key + "/leaf.txt": &fstest.MapFile{Data: []byte("leaf")},
	}
	fsys := &FS{embedFS: embed}
	visited := map[string]struct{}{}
	if err := fsys.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		visited[path] = struct{}{}
		return nil
	}); err != nil {
		t.Fatalf("walk no root: %v", err)
	}
	if len(visited) == 0 {
		t.Fatalf("expected visits when project root empty")
	}
}

func TestFS_WalkDir_NilEmbedFS(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, nil)
	root := filepath.Join(dir, "missing")
	if err := fsys.WalkDir(root, func(_ string, _ fs.DirEntry, walkErr error) error {
		return walkErr
	}); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_WalkDir_EmbedRootFallback(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	if err := os.RemoveAll(dir); err != nil {
		t.Fatalf("remove dir: %v", err)
	}
	var visited bool
	if err := fsys.WalkDir(dir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.Contains(filepath.ToSlash(path), ".claude") {
			visited = true
		}
		return nil
	}); err != nil {
		t.Fatalf("walk embed root fallback: %v", err)
	}
	if !visited {
		t.Fatalf("expected embed traversal when root missing")
	}
}
func TestFS_ReadFile_OSOverridesEmbed(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".claude", "settings.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(path, []byte(`{"source":"os"}`), 0o600); err != nil {
		t.Fatalf("write os file: %v", err)
	}

	fsys := NewFS(dir, buildEmbedFS())
	data, err := fsys.ReadFile(path)
	if err != nil {
		t.Fatalf("read with override: %v", err)
	}
	if string(data) != `{"source":"os"}` {
		t.Fatalf("os data not preferred: %s", data)
	}
}

func TestFS_toEmbedPath(t *testing.T) {
	dir := t.TempDir()
	defaultFS := NewFS(dir, nil)

	windowsFS := &FS{projectRoot: "C:/project"}
	noRootFS := NewFS("", nil)
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	cwdRelative := strings.TrimLeft(filepath.ToSlash(filepath.Join(cwd, ".claude", "file.json")), "/")
	uncFS := &FS{projectRoot: `\\server\share`}

	tests := []struct {
		name string
		fs   *FS
		path string
		want string
	}{
		{
			name: "absolute path",
			fs:   defaultFS,
			path: filepath.Join(dir, ".claude", "settings.json"),
			want: ".claude/settings.json",
		},
		{
			name: "relative path anchored to root",
			fs:   defaultFS,
			path: filepath.Join(".claude", "settings.local.json"),
			want: ".claude/settings.local.json",
		},
		{
			name: "windows separators normalized",
			fs:   windowsFS,
			path: `C:\project\.claude\settings.json`,
			want: ".claude/settings.json",
		},
		{
			name: ".claude subpath retained",
			fs:   defaultFS,
			path: filepath.Join(dir, ".claude", "skills", "demo"),
			want: ".claude/skills/demo",
		},
		{
			name: "outside project root",
			fs:   defaultFS,
			path: filepath.Join(dir+"-external", "other.txt"),
			want: strings.TrimLeft(filepath.ToSlash(filepath.Join(dir+"-external", "other.txt")), "/"),
		},
		{
			name: "root directory collapses to empty",
			fs:   defaultFS,
			path: dir,
			want: "",
		},
		{
			name: "root slash handling",
			fs:   NewFS(string(filepath.Separator), nil),
			path: filepath.Join(string(filepath.Separator), "tmp", "file.txt"),
			want: "tmp/file.txt",
		},
		{
			name: "empty path stays empty",
			fs:   defaultFS,
			path: "",
			want: "",
		},
		{
			name: "no project root uses cwd",
			fs:   noRootFS,
			path: filepath.Join(".claude", "file.json"),
			want: cwdRelative,
		},
		{
			name: "windows UNC path",
			fs:   uncFS,
			path: `\\server\share\.claude\settings.json`,
			want: ".claude/settings.json",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.fs.toEmbedPath(tc.path)
			if got != tc.want {
				t.Fatalf("toEmbedPath(%q)=%q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestFS_FileNotFound(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, buildEmbedFS())
	path := filepath.Join(dir, ".claude", "missing.json")
	_, err := fsys.ReadFile(path)
	if err == nil {
		t.Fatalf("expected error for missing file")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestFS_NilEmbedFS(t *testing.T) {
	dir := t.TempDir()
	fsys := NewFS(dir, nil)
	path := filepath.Join(dir, "missing.json")
	_, err := fsys.ReadFile(path)
	if err == nil {
		t.Fatalf("expected error")
	}
	if !errors.Is(err, fs.ErrNotExist) {
		t.Fatalf("expected ErrNotExist, got %v", err)
	}
}

func TestNormalizeSlashes(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		if got := normalizeSlashes(""); got != "" {
			t.Fatalf("unexpected result %q", got)
		}
	})

	t.Run("windows path", func(t *testing.T) {
		const input = `C:\foo\bar`
		if got := normalizeSlashes(input); got != "C:/foo/bar" {
			t.Fatalf("unexpected normalized value %q", got)
		}
	})

	t.Run("unix path", func(t *testing.T) {
		if got := normalizeSlashes("/tmp/value"); got != "/tmp/value" {
			t.Fatalf("unexpected normalized value %q", got)
		}
	})
}

func TestIsWindowsAbs(t *testing.T) {
	tCases := []struct {
		path string
		want bool
	}{
		{path: `C:\foo`, want: true},
		{path: `C:/foo`, want: true},
		{path: `\\server\share`, want: true},
		{path: `C:relative`, want: false},
		{path: `C:`, want: false},
		{path: `/unix`, want: false},
		{path: "short", want: false},
		{path: `1:\foo`, want: false},
	}
	for _, tc := range tCases {
		if got := isWindowsAbs(tc.path); got != tc.want {
			t.Fatalf("isWindowsAbs(%q)=%v, want %v", tc.path, got, tc.want)
		}
	}
}

func buildEmbedFS() fstest.MapFS {
	return fstest.MapFS{
		".claude":                       &fstest.MapFile{Mode: fs.ModeDir},
		".claude/settings.json":         &fstest.MapFile{Data: []byte(`{"source":"embed"}`)},
		".claude/skills":                &fstest.MapFile{Mode: fs.ModeDir},
		".claude/skills/alpha":          &fstest.MapFile{Mode: fs.ModeDir},
		".claude/skills/alpha/SKILL.md": &fstest.MapFile{Data: []byte("alpha")},
		".claude/skills/beta":           &fstest.MapFile{Mode: fs.ModeDir},
		".claude/skills/beta/SKILL.md":  &fstest.MapFile{Data: []byte("beta")},
		".claude/walk":                  &fstest.MapFile{Mode: fs.ModeDir},
		".claude/walk/inner":            &fstest.MapFile{Mode: fs.ModeDir},
		".claude/walk/inner/file.txt":   &fstest.MapFile{Data: []byte("walk")},
	}
}
