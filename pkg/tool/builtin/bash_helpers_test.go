package toolbuiltin

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cexll/agentsdk-go/pkg/middleware"
	"github.com/cexll/agentsdk-go/pkg/model"
)

func TestBashSessionIDFromContext(t *testing.T) {
	t.Parallel()

	st := &middleware.State{Values: map[string]any{"session_id": "sess"}}
	ctx := context.WithValue(context.Background(), model.MiddlewareStateKey, st)
	if got := bashSessionID(ctx); got != "sess" {
		t.Fatalf("unexpected session id %q", got)
	}

	ctx = context.WithValue(context.Background(), middleware.TraceSessionIDContextKey, "trace")
	if got := bashSessionID(ctx); got != "trace" {
		t.Fatalf("unexpected trace session id %q", got)
	}
}

func TestBashOutputFileHelpers(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := ensureBashOutputDir(dir); err != nil {
		t.Fatalf("ensure dir failed: %v", err)
	}
	path := filepath.Join(dir, "out.txt")
	f, _, err := openBashOutputFile(path)
	if err != nil {
		t.Fatalf("open output file: %v", err)
	}
	if _, err := f.WriteString("hello\n"); err != nil {
		t.Fatalf("write: %v", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	f2, err := os.OpenFile(path, os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := trimRightNewlinesInFile(f2); err != nil {
		t.Fatalf("trim newlines: %v", err)
	}
	_ = f2.Close()

	combined := filepath.Join(dir, "combined.txt")
	out, err := os.OpenFile(combined, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open combined: %v", err)
	}
	if err := writeCombinedOutput(out, "stdout", "", "stderr"); err != nil {
		t.Fatalf("write combined: %v", err)
	}
	_ = out.Close()

	data, _ := os.ReadFile(combined)
	if !strings.Contains(string(data), "stderr") {
		t.Fatalf("expected stderr in combined output")
	}
}

func TestBashOutputSpoolFinalize(t *testing.T) {
	t.Parallel()

	spool := newBashOutputSpool(context.Background(), 1)
	if err := spool.Append("hello", false); err != nil {
		t.Fatalf("append stdout: %v", err)
	}
	if err := spool.Append("oops", true); err != nil {
		t.Fatalf("append stderr: %v", err)
	}
	output, path, err := spool.Finalize()
	if err != nil {
		t.Fatalf("finalize failed: %v", err)
	}
	if output == "" {
		t.Fatalf("expected output")
	}
	if path != "" {
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("expected output file to exist: %v", err)
		}
	}
}

func TestBashOutputSpoolFinalizeBranches(t *testing.T) {
	if out, path, err := (*bashOutputSpool)(nil).Finalize(); err != nil || out != "" || path != "" {
		t.Fatalf("expected nil spool to return empty outputs")
	}

	ctx := context.WithValue(context.Background(), middleware.SessionIDContextKey, "branch-test")

	spool := newBashOutputSpool(ctx, 5)
	_ = spool.Append("aaaa", false)
	_ = spool.Append("bbbb", true)
	out, path, err := spool.Finalize()
	if err != nil || path == "" || !strings.Contains(out, "Output saved") {
		t.Fatalf("expected combined output file, got out=%q path=%q err=%v", out, path, err)
	}

	spool2 := newBashOutputSpool(ctx, 3)
	_ = spool2.Append("a", false)
	_ = spool2.Append("bbbb", true)
	out2, path2, err := spool2.Finalize()
	if err != nil || path2 == "" || out2 == "" {
		t.Fatalf("expected stderr output file, got out=%q path=%q err=%v", out2, path2, err)
	}

	spool3 := newBashOutputSpool(ctx, 3)
	_ = spool3.Append("bbbb", false)
	_ = spool3.Append("e", true)
	out3, path3, err := spool3.Finalize()
	if err != nil || path3 == "" || out3 == "" {
		t.Fatalf("expected stdout output file, got out=%q path=%q err=%v", out3, path3, err)
	}

	if _, err := trimRightNewlinesInFile(nil); err != nil {
		t.Fatalf("expected nil file to return nil error, got %v", err)
	}
}
