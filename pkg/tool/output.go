package tool

import (
	"bytes"
	"errors"
	"io"
	"os"
	"strings"
	"sync"
)

// SpoolFileFactory creates (or opens) the file backing a SpoolWriter once it
// crosses its in-memory threshold. It must return a valid file handle and a
// non-empty path.
type SpoolFileFactory func() (io.WriteCloser, string, error)

// SpoolWriter buffers writes in-memory until the configured threshold is
// exceeded, then spills to a file created via the provided factory. When the
// spill fails, the writer is truncated: it preserves whatever data was already
// buffered, swallows further writes, and surfaces the error on Close.
//
// Write never returns an error. Callers that need to observe failures should
// check Close and Truncated.
type SpoolWriter struct {
	mu          sync.Mutex
	threshold   int
	buf         bytes.Buffer
	file        io.WriteCloser
	path        string
	fileFactory SpoolFileFactory
	truncated   bool
	err         error
}

func NewSpoolWriter(threshold int, fileFactory SpoolFileFactory) *SpoolWriter {
	return &SpoolWriter{threshold: threshold, fileFactory: fileFactory}
}

func (w *SpoolWriter) WriteString(s string) (int, error) {
	return w.Write([]byte(s))
}

func (w *SpoolWriter) Write(p []byte) (int, error) {
	if w == nil {
		return len(p), nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.truncated {
		return len(p), nil
	}
	if w.file != nil {
		if _, err := w.file.Write(p); err != nil {
			if w.err == nil {
				w.err = err
			}
			w.truncated = true
		}
		return len(p), nil
	}
	if w.buf.Len()+len(p) <= w.threshold {
		_, _ = w.buf.Write(p)
		return len(p), nil
	}

	if w.fileFactory == nil {
		if w.err == nil {
			w.err = errors.New("spool: file factory is nil")
		}
		w.truncated = true
		return len(p), nil
	}

	f, path, err := w.fileFactory()
	if err != nil {
		if w.err == nil {
			w.err = err
		}
		w.truncated = true
		return len(p), nil
	}
	if f == nil || strings.TrimSpace(path) == "" {
		if f != nil {
			_ = f.Close()
		}
		if w.err == nil {
			w.err = errors.New("spool: output file is invalid")
		}
		w.truncated = true
		return len(p), nil
	}
	if _, err := f.Write(w.buf.Bytes()); err != nil {
		if w.err == nil {
			w.err = err
		}
		_ = f.Close()
		_ = os.Remove(path)
		w.truncated = true
		return len(p), nil
	}
	if _, err := f.Write(p); err != nil {
		if w.err == nil {
			w.err = err
		}
		_ = f.Close()
		_ = os.Remove(path)
		w.truncated = true
		return len(p), nil
	}
	w.buf.Reset()
	w.file = f
	w.path = path
	return len(p), nil
}

func (w *SpoolWriter) Close() error {
	if w == nil {
		return nil
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.file == nil {
		return w.err
	}
	closeErr := w.file.Close()
	w.file = nil
	return errors.Join(w.err, closeErr)
}

func (w *SpoolWriter) Path() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.truncated {
		return ""
	}
	return w.path
}

func (w *SpoolWriter) String() string {
	if w == nil {
		return ""
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.buf.String()
}

func (w *SpoolWriter) Truncated() bool {
	if w == nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.truncated
}
