package tool

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type stubWriteCloser struct {
	writes int
	failAt int
	err    error
}

func (s *stubWriteCloser) Write(p []byte) (int, error) {
	s.writes++
	if s.failAt > 0 && s.writes == s.failAt {
		return 0, s.err
	}
	return len(p), nil
}

func (s *stubWriteCloser) Close() error { return nil }

func TestSpoolWriterNilReceiverWrite(t *testing.T) {
	var w *SpoolWriter
	n, err := w.Write([]byte("x"))
	if err != nil || n != 1 {
		t.Fatalf("unexpected write result n=%d err=%v", n, err)
	}
}

func TestSpoolWriterNilReceiverAccessors(t *testing.T) {
	var w *SpoolWriter
	if got := w.Path(); got != "" {
		t.Fatalf("expected empty path, got %q", got)
	}
	if got := w.String(); got != "" {
		t.Fatalf("expected empty string, got %q", got)
	}
	if w.Truncated() {
		t.Fatalf("expected truncated=false")
	}
	if err := w.Close(); err != nil {
		t.Fatalf("expected nil close error, got %v", err)
	}
}

func TestSpoolWriterWriteStringBuffers(t *testing.T) {
	w := NewSpoolWriter(10, nil)
	n, err := w.WriteString("hi")
	if err != nil || n != 2 {
		t.Fatalf("unexpected write result n=%d err=%v", n, err)
	}
	if got := w.String(); got != "hi" {
		t.Fatalf("unexpected buffer %q", got)
	}
}

func TestSpoolWriterTruncatesWhenFactoryNil(t *testing.T) {
	w := NewSpoolWriter(1, nil)
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate when factory is nil")
	}
	if got := w.Path(); got != "" {
		t.Fatalf("expected no path when truncated, got %q", got)
	}
	if w.String() != "a" {
		t.Fatalf("expected buffered output to remain, got %q", w.String())
	}
	_, _ = w.Write([]byte("c"))
	if w.String() != "a" {
		t.Fatalf("expected truncated writer to ignore writes, got %q", w.String())
	}
	if err := w.Close(); err == nil || !strings.Contains(err.Error(), "file factory") {
		t.Fatalf("expected close to surface factory error, got %v", err)
	}
}

func TestSpoolWriterTruncatesAfterFactoryError(t *testing.T) {
	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		return nil, "", errors.New("boom")
	})
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate after spill failure")
	}
	if got := w.Path(); got != "" {
		t.Fatalf("expected no path when truncated, got %q", got)
	}
	if w.String() != "a" {
		t.Fatalf("expected buffered output to remain, got %q", w.String())
	}
	if err := w.Close(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected close to surface factory error, got %v", err)
	}
}

func TestSpoolWriterTruncatesWhenFactoryReturnsInvalidFile(t *testing.T) {
	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		return nil, filepath.Join(t.TempDir(), "spool.txt"), nil
	})
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate when factory returns invalid file")
	}
	if err := w.Close(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Fatalf("expected close to surface invalid file error, got %v", err)
	}
}

func TestSpoolWriterTruncatesWhenFactoryReturnsEmptyPath(t *testing.T) {
	dir := t.TempDir()
	path := ""
	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		f, err := os.CreateTemp(dir, "badpath-*.tmp")
		if err != nil {
			return nil, "", err
		}
		path = f.Name()
		return f, "   ", nil
	})
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate when factory returns empty path")
	}
	if err := w.Close(); err == nil || !strings.Contains(strings.ToLower(err.Error()), "invalid") {
		t.Fatalf("expected close to surface invalid path error, got %v", err)
	}
	if path != "" {
		_ = os.Remove(path)
	}
}

func TestSpoolWriterTruncatesAfterFileWriteFailure(t *testing.T) {
	dir := t.TempDir()
	tmp, err := os.CreateTemp(dir, "spool-*.tmp")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		return &stubWriteCloser{failAt: 1, err: errors.New("boom")}, path, nil
	})
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate after file write failure")
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected temp file to be removed")
	}
}

func TestSpoolWriterTruncatesAfterSecondWriteFailure(t *testing.T) {
	dir := t.TempDir()
	tmp, err := os.CreateTemp(dir, "spool-*.tmp")
	if err != nil {
		t.Fatalf("create temp file: %v", err)
	}
	path := tmp.Name()
	if err := tmp.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}

	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		return &stubWriteCloser{failAt: 2, err: errors.New("boom")}, path, nil
	})
	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate after second write failure")
	}
	if got := w.String(); got != "a" {
		t.Fatalf("expected buffered output to remain, got %q", got)
	}
	if _, err := os.Stat(path); err == nil {
		t.Fatalf("expected temp file to be removed")
	}
	if err := w.Close(); err == nil || !strings.Contains(err.Error(), "boom") {
		t.Fatalf("expected close to surface write error, got %v", err)
	}
}

func TestSpoolWriterWritesToOpenFile(t *testing.T) {
	dir := t.TempDir()
	var file *os.File
	w := NewSpoolWriter(1, func() (io.WriteCloser, string, error) {
		var err error
		file, err = os.CreateTemp(dir, "spool-*.tmp")
		if err != nil {
			return nil, "", err
		}
		return file, file.Name(), nil
	})

	_, _ = w.Write([]byte("a"))
	_, _ = w.Write([]byte("b"))
	_, _ = w.Write([]byte("c"))
	path := w.Path()
	if strings.TrimSpace(path) == "" {
		t.Fatalf("expected writer to spill to disk")
	}
	if w.Truncated() {
		t.Fatalf("unexpected truncation")
	}
	if got := w.String(); got != "" {
		t.Fatalf("expected buffer to be reset after spill, got %q", got)
	}

	if err := w.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read file: %v", err)
	}
	if string(data) != "abc" {
		t.Fatalf("unexpected file contents %q", string(data))
	}
}

func TestSpoolWriterTruncatesAfterFileWriteFailureWhenAlreadySpooling(t *testing.T) {
	dir := t.TempDir()
	var file *os.File
	w := NewSpoolWriter(0, func() (io.WriteCloser, string, error) {
		var err error
		file, err = os.CreateTemp(dir, "spool-*.tmp")
		if err != nil {
			return nil, "", err
		}
		return file, file.Name(), nil
	})

	_, _ = w.Write([]byte("a"))
	if w.Truncated() || w.Path() == "" || file == nil {
		t.Fatalf("expected initial spill to succeed")
	}
	if err := file.Close(); err != nil {
		t.Fatalf("close temp file: %v", err)
	}
	_, _ = w.Write([]byte("b"))
	if !w.Truncated() {
		t.Fatalf("expected writer to truncate after write to closed file")
	}
	if got := w.Path(); got != "" {
		t.Fatalf("expected no path after truncation, got %q", got)
	}
	if err := w.Close(); err == nil {
		t.Fatalf("expected close to surface write error")
	}
}
