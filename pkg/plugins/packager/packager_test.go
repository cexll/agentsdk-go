package packager

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/plugins"
	"gopkg.in/yaml.v3"
)

func TestPackagerExportImport(t *testing.T) {
	root := t.TempDir()
	pluginDir := filepath.Join(root, "demo")
	if err := os.Mkdir(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	entry := []byte("#!/bin/sh\necho ok\n")
	entryPath := filepath.Join(pluginDir, "main.sh")
	if err := os.WriteFile(entryPath, entry, 0o755); err != nil { //nolint:gosec // executable test script
		t.Fatalf("write entry: %v", err)
	}
	assetsDir := filepath.Join(pluginDir, "assets")
	if err := os.MkdirAll(assetsDir, 0o755); err != nil {
		t.Fatalf("mkdir assets: %v", err)
	}
	if err := os.WriteFile(filepath.Join(assetsDir, "extra.txt"), []byte("extra"), 0o600); err != nil {
		t.Fatalf("write asset: %v", err)
	}
	digest := sha256Bytes(entry)
	mf := plugins.Manifest{
		Name:       "demo",
		Version:    "1.0.0",
		Entrypoint: "main.sh",
		Digest:     digest,
	}
	data, err := yaml.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.yaml"), data, 0o600); err != nil {
		t.Fatalf("write manifest: %v", err)
	}

	packager, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	manifest, err := packager.Export("demo", &buf)
	if err != nil {
		t.Fatalf("export failed: %v", err)
	}
	if manifest.Name != "demo" || manifest.Digest != digest {
		t.Fatalf("unexpected manifest: %+v", manifest)
	}

	installRoot := filepath.Join(t.TempDir(), "plugins")
	installer, err := NewPackager(installRoot, nil)
	if err != nil {
		t.Fatalf("installer: %v", err)
	}
	imported, err := installer.Import(bytes.NewReader(buf.Bytes()), "demo")
	if err != nil {
		t.Fatalf("import failed: %v", err)
	}
	if imported.Name != "demo" || imported.Digest != digest {
		t.Fatalf("imported manifest mismatch: %+v", imported)
	}
	entryData, err := os.ReadFile(filepath.Join(installRoot, "demo", "main.sh"))
	if err != nil {
		t.Fatalf("read extracted: %v", err)
	}
	if string(entryData) != string(entry) {
		t.Fatalf("entrypoint content mismatch")
	}
}

func TestPackagerImportGuards(t *testing.T) {
	root := t.TempDir()
	packager, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}

	// path traversal
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "../evil", Mode: 0o644, Size: int64(len("data"))}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("data")); err != nil {
		t.Fatalf("write data: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if _, err := packager.Import(bytes.NewReader(buf.Bytes()), "evil"); !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("expected unsafe archive error, got %v", err)
	}

	// missing manifest
	buf.Reset()
	gz = gzip.NewWriter(&buf)
	tw = tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "file.txt", Mode: 0o644, Size: int64(len("x"))}); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write([]byte("x")); err != nil {
		t.Fatalf("write x: %v", err)
	}
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if _, err := packager.Import(bytes.NewReader(buf.Bytes()), "missing"); err == nil {
		t.Fatalf("expected missing manifest error")
	}
}

func sha256Bytes(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func TestPackagerValidationHelpers(t *testing.T) {
	if _, err := NewPackager("", nil); err == nil {
		t.Fatalf("expected error for empty root")
	}
	p, err := NewPackager(t.TempDir(), nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	if p.Root() == "" {
		t.Fatalf("expected non-empty root")
	}
	var nilPackager *Packager
	if _, err := nilPackager.Export("demo", io.Discard); err == nil {
		t.Fatalf("nil packager export should error")
	}
	if nilPackager.Root() != "" {
		t.Fatalf("nil packager root should be empty")
	}

	if err := ensureEmptyDir(p.Root()); err != nil {
		t.Fatalf("ensure empty dir on existing empty dir: %v", err)
	}
	filePath := filepath.Join(p.Root(), "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := ensureEmptyDir(p.Root()); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists, got %v", err)
	}
	if err := ensureEmptyDir(filePath); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists for file path")
	}
}

func TestPackagerPackageDirGuards(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(t.TempDir(), &buf); err == nil {
		t.Fatalf("expected error when packaging dir outside root")
	}
	var nilPackager *Packager
	if _, err := nilPackager.PackageDir(root, &buf); err == nil {
		t.Fatalf("expected nil packager package error")
	}
}

func TestPackagerImportDestinationExists(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	pluginDir := filepath.Join(root, "src")
	if err := os.Mkdir(pluginDir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	entry := []byte("echo hi")
	if err := os.WriteFile(filepath.Join(pluginDir, "main.sh"), entry, 0o755); err != nil { //nolint:gosec // executable test script
		t.Fatalf("write entry: %v", err)
	}
	digest := sha256Bytes(entry)
	mf := plugins.Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main.sh", Digest: digest}
	data, err := yaml.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(pluginDir, "manifest.yaml"), data, 0o600); err != nil {
		t.Fatalf("manifest write: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(pluginDir, &buf); err != nil {
		t.Fatalf("package dir: %v", err)
	}
	dest := filepath.Join(root, "demo")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dest, "placeholder"), []byte("x"), 0o600); err != nil {
		t.Fatalf("write placeholder: %v", err)
	}
	if _, err := p.Import(bytes.NewReader(buf.Bytes()), "demo"); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected destination exists error, got %v", err)
	}
}

func TestPackagerRestoreEntry(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}
	dirHeader := &tar.Header{Name: "dir", Typeflag: tar.TypeDir, Mode: 0o755}
	if err := p.restoreEntry(dest, dirHeader, nil); err != nil {
		t.Fatalf("restore dir: %v", err)
	}
	fileHeader := &tar.Header{Name: "dir/file.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len("data"))}
	if err := p.restoreEntry(dest, fileHeader, bytes.NewReader([]byte("data"))); err != nil {
		t.Fatalf("restore file: %v", err)
	}
	content, err := os.ReadFile(filepath.Join(dest, "dir", "file.txt"))
	if err != nil || string(content) != "data" {
		t.Fatalf("unexpected content: %s err=%v", content, err)
	}
	if err := p.restoreEntry(dest, &tar.Header{Name: "/abs", Typeflag: tar.TypeReg}, bytes.NewReader(nil)); !errors.Is(err, ErrUnsafeArchive) {
		t.Fatalf("expected unsafe error, got %v", err)
	}
	if err := p.restoreEntry(dest, &tar.Header{Name: "../escape", Typeflag: tar.TypeDir}, nil); err == nil {
		t.Fatalf("expected unsafe path rejection")
	}
	if err := p.restoreEntry(dest, &tar.Header{Name: ".", Typeflag: tar.TypeDir}, nil); err != nil {
		t.Fatalf("dot entry should be ignored: %v", err)
	}
}

func TestPackagerRestoreEntryErrorPaths(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	dest := filepath.Join(root, "dest")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatalf("mkdir dest: %v", err)
	}

	// mkdir failure (parent already file)
	block := filepath.Join(dest, "file-as-dir")
	if err := os.WriteFile(block, []byte("x"), 0o644); err != nil {
		t.Fatalf("write block: %v", err)
	}
	header := &tar.Header{Name: "file-as-dir/child.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: int64(len("child"))}
	if err := p.restoreEntry(dest, header, bytes.NewReader([]byte("child"))); err == nil {
		t.Fatalf("expected mkdirAll error")
	}

	// open failure due to existing directory
	existing := filepath.Join(dest, "existing")
	if err := os.Mkdir(existing, 0o755); err != nil {
		t.Fatalf("mkdir existing: %v", err)
	}
	header = &tar.Header{Name: "existing", Typeflag: tar.TypeReg, Mode: 0o644, Size: 0}
	if err := p.restoreEntry(dest, header, bytes.NewReader(nil)); err == nil {
		t.Fatalf("expected open file error")
	}

	// copy failure
	header = &tar.Header{Name: "copy.txt", Typeflag: tar.TypeReg, Mode: 0o644, Size: 10}
	if err := p.restoreEntry(dest, header, errReader{}); err == nil {
		t.Fatalf("expected copy error")
	}

	// default branch (symlink) should be ignored
	if err := p.restoreEntry(dest, &tar.Header{Name: "noop", Typeflag: tar.TypeSymlink}, bytes.NewReader(nil)); err != nil {
		t.Fatalf("default branch should succeed: %v", err)
	}
}

func TestPackagerPackageDirMissingManifest(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	dir := filepath.Join(root, "plugin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(dir, &buf); err == nil {
		t.Fatalf("expected error for missing manifest")
	}
}

func TestPackagerPackageDirLockedFile(t *testing.T) {
	root := t.TempDir()
	dir := setupPlugin(t, root, "demo", []byte("#!/bin/sh\necho hi\n"))
	locked := filepath.Join(dir, "locked.bin")
	if err := os.WriteFile(locked, []byte("secret"), 0o000); err != nil {
		t.Fatalf("write locked: %v", err)
	}
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(dir, &buf); err == nil {
		t.Fatalf("expected error due to locked file")
	}
}

func TestPackagerPackageDirWriterFailures(t *testing.T) {
	root := t.TempDir()
	payload := bytes.Repeat([]byte("x"), 4096)
	dir := setupPlugin(t, root, "demo", payload)
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}

	failWriter := failingWriter{err: errors.New("sink failure")}
	if _, err := p.PackageDir(dir, failWriter); err == nil {
		t.Fatalf("expected writer error")
	}
}

func TestPackagerPackageDirManifestMismatch(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "plugin")
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	entry := []byte("echo hi")
	if err := os.WriteFile(filepath.Join(dir, "main.sh"), entry, 0o755); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	mf := plugins.Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main.sh", Digest: "deadbeef"}
	data, err := yaml.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(dir, &buf); err == nil {
		t.Fatalf("expected digest mismatch error")
	}
}

func TestPackagerPackageDirUnreadableFile(t *testing.T) {
	root := t.TempDir()
	dir := setupPlugin(t, root, "demo", []byte("echo hi"))
	restrictedDir := filepath.Join(dir, "secret")
	if err := os.Mkdir(restrictedDir, 0o000); err != nil {
		t.Fatalf("mkdir secret: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(restrictedDir, 0o755); err != nil {
			t.Fatalf("restore restricted dir: %v", err)
		}
	})
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	if _, err := p.PackageDir(dir, &buf); err == nil {
		t.Fatalf("expected walk error due to restricted dir")
	}
}

func TestPackagerImportInvalidArchive(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	if _, err := p.Import(bytes.NewReader([]byte("not gzip data")), "broken"); err == nil {
		t.Fatalf("expected gzip reader error")
	}

	var nilPackager *Packager
	if _, err := nilPackager.Import(bytes.NewReader(nil), "demo"); err == nil {
		t.Fatalf("nil packager import should error")
	}
}

func TestPackagerImportInvalidManifest(t *testing.T) {
	root := t.TempDir()
	p, err := NewPackager(root, nil)
	if err != nil {
		t.Fatalf("packager: %v", err)
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	main := []byte("echo hi\n")
	writeTarFile(t, tw, "main.sh", main, 0o755)
	badManifest := plugins.Manifest{Name: "demo", Version: "1.0.0", Entrypoint: "main.sh", Digest: "deadbeef"}
	data, err := yaml.Marshal(badManifest)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	writeTarFile(t, tw, "manifest.yaml", data, 0o644)
	if err := tw.Close(); err != nil {
		t.Fatalf("close tar writer: %v", err)
	}
	if err := gz.Close(); err != nil {
		t.Fatalf("close gzip writer: %v", err)
	}
	if _, err := p.Import(bytes.NewReader(buf.Bytes()), "demo"); err == nil {
		t.Fatalf("expected manifest load error")
	}
}

func TestEnsureEmptyDirAdditionalGuards(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("x"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := ensureEmptyDir(dir); !errors.Is(err, ErrDestinationExists) {
		t.Fatalf("expected ErrDestinationExists, got %v", err)
	}

	locked := filepath.Join(t.TempDir(), "locked")
	if err := os.Mkdir(locked, 0o000); err != nil {
		t.Fatalf("mkdir locked: %v", err)
	}
	t.Cleanup(func() {
		if err := os.Chmod(locked, 0o755); err != nil {
			t.Fatalf("restore locked dir: %v", err)
		}
	})
	if err := ensureEmptyDir(locked); err == nil {
		t.Fatalf("expected read dir error")
	}
}

func setupPlugin(t *testing.T, root, name string, entry []byte) string {
	t.Helper()
	dir := filepath.Join(root, name)
	if err := os.Mkdir(dir, 0o755); err != nil {
		t.Fatalf("mkdir plugin: %v", err)
	}
	entryPath := filepath.Join(dir, "main.sh")
	if err := os.WriteFile(entryPath, entry, 0o755); err != nil {
		t.Fatalf("write entry: %v", err)
	}
	mf := plugins.Manifest{Name: name, Version: "1.0.0", Entrypoint: "main.sh", Digest: sha256Bytes(entry)}
	data, err := yaml.Marshal(mf)
	if err != nil {
		t.Fatalf("marshal manifest: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.yaml"), data, 0o644); err != nil {
		t.Fatalf("write manifest: %v", err)
	}
	return dir
}

type failingWriter struct {
	err error
}

func (w failingWriter) Write([]byte) (int, error) {
	if w.err == nil {
		return 0, errors.New("writer failure")
	}
	return 0, w.err
}

func writeTarFile(t *testing.T, tw *tar.Writer, name string, data []byte, mode int64) {
	t.Helper()
	header := &tar.Header{Name: name, Mode: mode, Size: int64(len(data))}
	if err := tw.WriteHeader(header); err != nil {
		t.Fatalf("write header: %v", err)
	}
	if _, err := tw.Write(data); err != nil {
		t.Fatalf("write content: %v", err)
	}
}

type errReader struct{}

func (errReader) Read([]byte) (int, error) {
	return 0, errors.New("reader failure")
}
