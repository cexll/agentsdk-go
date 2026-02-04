package toolbuiltin

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/tool"
)

func TestBashOutputSpoolFinalizeTruncated(t *testing.T) {
	stdout := tool.NewSpoolWriter(2, nil)
	_, _ = stdout.WriteString("a")
	_, _ = stdout.WriteString("bc")
	stderr := tool.NewSpoolWriter(2, nil)
	_, _ = stderr.WriteString("x")

	spool := &bashOutputSpool{
		threshold:  10,
		outputPath: filepath.Join(t.TempDir(), "out.txt"),
		stdout:     stdout,
		stderr:     stderr,
	}
	out, path, err := spool.Finalize()
	if err == nil {
		t.Fatalf("expected close error for truncated spool")
	}
	if path != "" {
		t.Fatalf("expected empty path for truncated spool, got %q", path)
	}
	if out == "" {
		t.Fatalf("expected combined output")
	}
}

func TestBashOutputSpoolFinalizeCombinedInMemory(t *testing.T) {
	stdout := tool.NewSpoolWriter(100, nil)
	_, _ = stdout.WriteString("hello")
	stderr := tool.NewSpoolWriter(100, nil)
	_, _ = stderr.WriteString("world")

	spool := &bashOutputSpool{
		threshold:  50,
		outputPath: filepath.Join(t.TempDir(), "out.txt"),
		stdout:     stdout,
		stderr:     stderr,
	}
	out, path, err := spool.Finalize()
	if err != nil {
		t.Fatalf("unexpected finalize error: %v", err)
	}
	if path != "" {
		t.Fatalf("expected no output file, got %q", path)
	}
	if !strings.Contains(out, "hello") || !strings.Contains(out, "world") {
		t.Fatalf("unexpected output %q", out)
	}
}

func TestBashOutputSpoolFinalizeWritesFile(t *testing.T) {
	stdout := tool.NewSpoolWriter(100, nil)
	_, _ = stdout.WriteString("hello")

	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "out.txt")
	spool := &bashOutputSpool{
		threshold:  2,
		outputPath: outPath,
		stdout:     stdout,
		stderr:     tool.NewSpoolWriter(100, nil),
	}
	out, path, err := spool.Finalize()
	if err != nil {
		t.Fatalf("finalize error: %v", err)
	}
	if path == "" || path != outPath {
		t.Fatalf("expected output path %q, got %q", outPath, path)
	}
	if !strings.Contains(out, "Output saved") {
		t.Fatalf("expected output reference, got %q", out)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected output file: %v", err)
	}
}

func TestBashOutputSpoolFinalizeStderrFileOnly(t *testing.T) {
	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "combined.txt")

	stdout := tool.NewSpoolWriter(100, nil)
	_, _ = stdout.WriteString("out")

	stderr := tool.NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		f, err := os.CreateTemp(tmp, "stderr-*.tmp")
		if err != nil {
			return nil, "", err
		}
		return f, f.Name(), nil
	})
	_, _ = stderr.WriteString("stderr-line")

	spool := &bashOutputSpool{
		threshold:  10,
		outputPath: outPath,
		stdout:     stdout,
		stderr:     stderr,
	}
	out, path, err := spool.Finalize()
	if err != nil {
		t.Fatalf("finalize error: %v", err)
	}
	if path == "" || path != outPath {
		t.Fatalf("expected output path %q, got %q", outPath, path)
	}
	if !strings.Contains(out, "Output saved") {
		t.Fatalf("expected output reference")
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected output file: %v", err)
	}
}

func TestBashOutputSpoolFinalizeStdoutFile(t *testing.T) {
	tmp := t.TempDir()
	outPath := filepath.Join(tmp, "stdout.txt")

	stdout := tool.NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		f, err := os.OpenFile(outPath, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
		if err != nil {
			return nil, "", err
		}
		return f, outPath, nil
	})
	_, _ = stdout.WriteString("stdout-line")

	stderr := tool.NewSpoolWriter(100, nil)
	_, _ = stderr.WriteString("stderr")

	spool := &bashOutputSpool{
		threshold:  10,
		outputPath: outPath,
		stdout:     stdout,
		stderr:     stderr,
	}
	out, path, err := spool.Finalize()
	if err != nil {
		t.Fatalf("finalize error: %v", err)
	}
	if path == "" || path != outPath {
		t.Fatalf("expected output path %q, got %q", outPath, path)
	}
	if !strings.Contains(out, "Output saved") {
		t.Fatalf("expected output reference")
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read output: %v", err)
	}
	if !strings.Contains(string(data), "stdout-line") {
		t.Fatalf("expected stdout in file")
	}
}
