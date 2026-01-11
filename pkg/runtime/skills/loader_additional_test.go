package skills

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReadFrontMatterMissingClosing(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: test\ndescription: desc\n")

	if _, err := readFrontMatter(path, nil); err == nil || !strings.Contains(err.Error(), "closing frontmatter") {
		t.Fatalf("expected closing frontmatter error, got %v", err)
	}
}

func TestValidateMetadataErrors(t *testing.T) {
	cases := []SkillMetadata{
		{Name: "", Description: "d"},
		{Name: "Bad!", Description: "d"},
		{Name: "ok", Description: ""},
		{Name: "ok", Description: strings.Repeat("x", 1025)},
	}

	for i, meta := range cases {
		if err := validateMetadata(meta); err == nil {
			t.Fatalf("case %d: expected error", i)
		}
	}
}

func TestLoadSupportFilesErrors(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, "scripts"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("prep: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "references"), []byte("not a dir"), 0o600); err != nil {
		t.Fatalf("prep: %v", err)
	}

	support, errs := loadSupportFiles(dir)
	if len(errs) != 2 {
		t.Fatalf("expected two errors, got %v", errs)
	}
	if support != nil {
		t.Fatalf("expected no support files, got %v", support)
	}
}

func TestLoadSupportFilesNoFiles(t *testing.T) {
	dir := t.TempDir()

	support, errs := loadSupportFiles(dir)
	if support != nil {
		t.Fatalf("expected nil support map, got %v", support)
	}
	if len(errs) != 0 {
		t.Fatalf("expected no errors, got %v", errs)
	}
}

func TestLoadSkillBodyReadError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: fail\ndescription: desc\n---\nbody")

	restore := SetReadFileForTest(func(string) ([]byte, error) {
		return nil, fs.ErrPermission
	})
	defer restore()

	if _, err := loadSkillBody(path); err == nil {
		t.Fatalf("expected read error")
	}
}

func TestLoadSkillBodyParseError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "---\nname: fail\ndescription: desc\n")

	if _, err := loadSkillBody(path); err == nil {
		t.Fatalf("expected parse error")
	}
}

func TestParseFrontMatterInvalidYAML(t *testing.T) {
	if _, _, err := parseFrontMatter("---\nname: [\n---\nbody"); err == nil {
		t.Fatalf("expected YAML decode error")
	}
}

func TestReadFrontMatterWithBOM(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "SKILL.md")
	mustWrite(t, path, "\uFEFF---\nname: bom\ndescription: desc\n---\n")

	meta, err := readFrontMatter(path, nil)
	if err != nil {
		t.Fatalf("unexpected err: %v", err)
	}
	if meta.Name != "bom" {
		t.Fatalf("unexpected name %q", meta.Name)
	}
}

func TestLoadSkillContentSupportError(t *testing.T) {
	dir := t.TempDir()
	skillPath := filepath.Join(dir, "SKILL.md")
	writeSkill(t, skillPath, "lazy", "body")
	mustWrite(t, filepath.Join(dir, "scripts"), "not a directory")

	if _, err := loadSkillContent(SkillFile{Path: skillPath, Metadata: SkillMetadata{Name: "lazy"}}); err == nil || !strings.Contains(err.Error(), "not a directory") {
		t.Fatalf("expected support dir error, got %v", err)
	}
}

func TestLoadSkillDirNotDirectory(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notdir")
	if err := os.WriteFile(file, []byte("data"), 0o600); err != nil {
		t.Fatalf("prep: %v", err)
	}

	_, errs := loadSkillDir(file, nil)
	if len(errs) != 1 || !strings.Contains(errs[0].Error(), "not a directory") {
		t.Fatalf("expected not a directory error, got %v", errs)
	}
}

func TestLoadSkillDirMissing(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "not-there")
	if result, errs := loadSkillDir(missing, nil); result != nil || len(errs) != 0 {
		t.Fatalf("expected nil results and no errors, got %v %v", result, errs)
	}
}

func TestSkillBodyLengthVariants(t *testing.T) {
	if size := skillBodyLength(Result{}); size != 0 {
		t.Fatalf("expected zero for empty result, got %d", size)
	}
	if size := skillBodyLength(Result{Output: map[string]any{"body": []byte("abc")}}); size != 3 {
		t.Fatalf("expected byte slice length, got %d", size)
	}
	if size := skillBodyLength(Result{Output: map[string]any{"body": 123}}); size != 0 {
		t.Fatalf("expected unsupported body types to return zero, got %d", size)
	}
}

func TestSetSkillFileOpsForTest(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "testops")
	skillPath := filepath.Join(dir, "SKILL.md")
	writeSkill(t, skillPath, "testops", "original body")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}

	handler := regs[0].Handler

	// First execute
	res1, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	out1, ok := res1.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output")
	}
	if out1["body"] != "original body" {
		t.Fatalf("unexpected body: %v", out1["body"])
	}

	// Override stat to return future time, triggering reload
	futureTime := time.Now().Add(time.Hour)
	restore := SetSkillFileOpsForTest(
		nil, // don't override read
		func(path string) (fs.FileInfo, error) {
			return &mockFileInfo{modTime: futureTime}, nil
		},
	)
	defer restore()

	// Modify the actual file
	writeSkill(t, skillPath, "testops", "updated body")

	// Execute again - should reload due to mocked future modTime
	res2, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	out2, ok := res2.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output")
	}
	if out2["body"] != "updated body" {
		t.Fatalf("expected updated body, got: %v", out2["body"])
	}
}

func TestNilHandlerExecute(t *testing.T) {
	var h *lazySkillHandler
	_, err := h.Execute(context.Background(), ActivationContext{})
	if err == nil || !strings.Contains(err.Error(), "handler is nil") {
		t.Fatalf("expected nil handler error, got %v", err)
	}
}

func TestNilHandlerBodyLength(t *testing.T) {
	var h *lazySkillHandler
	size, loaded := h.BodyLength()
	if size != 0 || loaded {
		t.Fatalf("expected zero size and not loaded for nil handler, got %d %v", size, loaded)
	}
}

func TestHandlerReloadAfterError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "reloaderr")
	skillPath := filepath.Join(dir, "SKILL.md")
	writeSkill(t, skillPath, "reloaderr", "body")

	regs, _ := LoadFromFS(LoaderOptions{ProjectRoot: root})
	handler := regs[0].Handler

	// First execute should work
	_, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// Corrupt the file
	time.Sleep(10 * time.Millisecond)
	mustWrite(t, skillPath, "no frontmatter")

	// Second execute should fail
	_, err = handler.Execute(context.Background(), ActivationContext{})
	if err == nil {
		t.Fatalf("expected error after file corruption")
	}

	// Fix the file
	time.Sleep(10 * time.Millisecond)
	writeSkill(t, skillPath, "reloaderr", "fixed body")

	// Third execute should work again
	res, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("third execute: %v", err)
	}
	out, ok := res.Output.(map[string]any)
	if !ok {
		t.Fatalf("expected map output")
	}
	if out["body"] != "fixed body" {
		t.Fatalf("expected fixed body, got: %v", out["body"])
	}
}

// mockFileInfo implements fs.FileInfo for testing
type mockFileInfo struct {
	modTime time.Time
}

func (m *mockFileInfo) Name() string       { return "SKILL.md" }
func (m *mockFileInfo) Size() int64        { return 0 }
func (m *mockFileInfo) Mode() fs.FileMode  { return 0o644 }
func (m *mockFileInfo) ModTime() time.Time { return m.modTime }
func (m *mockFileInfo) IsDir() bool        { return false }
func (m *mockFileInfo) Sys() any           { return nil }
