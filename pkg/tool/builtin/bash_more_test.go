package toolbuiltin

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"crypto/rand"
)

type errReader struct{}

func (errReader) Read([]byte) (int, error) { return 0, errors.New("boom") }

func TestEnsureBashOutputDirEmpty(t *testing.T) {
	if err := ensureBashOutputDir(" "); err == nil {
		t.Fatalf("expected empty dir error")
	}
}

func TestOpenBashOutputFileExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	if _, _, err := openBashOutputFile(path); err == nil {
		t.Fatalf("expected open error for existing file")
	}
}

func TestTrimRightNewlinesInFileReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "out.txt")
	if err := os.WriteFile(path, []byte("x\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	if _, err := trimRightNewlinesInFile(f); err == nil {
		t.Fatalf("expected truncate error for read-only file")
	}
}

func TestAppendStderrBranches(t *testing.T) {
	dir := t.TempDir()
	out, err := os.CreateTemp(dir, "out-*.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer out.Close()

	if err := appendStderr(out, 1, filepath.Join(dir, "missing.txt"), ""); err == nil {
		t.Fatalf("expected missing stderr path error")
	}

	out2, err := os.CreateTemp(dir, "out2-*.txt")
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	defer out2.Close()

	if err := appendStderr(out2, 1, "", "stderr"); err != nil {
		t.Fatalf("append stderr inline: %v", err)
	}
	if _, err := out2.Seek(0, io.SeekStart); err != nil {
		t.Fatalf("seek: %v", err)
	}
	data, _ := io.ReadAll(out2)
	if !strings.Contains(string(data), "stderr") {
		t.Fatalf("expected stderr content")
	}
}

func TestGenerateAsyncTaskIDFallback(t *testing.T) {
	orig := rand.Reader
	rand.Reader = errReader{}
	t.Cleanup(func() { rand.Reader = orig })

	id := generateAsyncTaskID()
	if !strings.HasPrefix(id, "task-") {
		t.Fatalf("unexpected id %q", id)
	}
}

func TestResolveRootDeletedCwd(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not allow removing current working directory")
	}
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	tmp := t.TempDir()
	if err := os.Chdir(tmp); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	if err := os.RemoveAll(tmp); err != nil {
		t.Fatalf("remove: %v", err)
	}
	defer func() { _ = os.Chdir(cwd) }()

	if got := resolveRoot(""); strings.TrimSpace(got) == "" {
		t.Fatalf("expected fallback root")
	}
}
