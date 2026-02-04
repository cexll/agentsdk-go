package toolbuiltin

import (
	"errors"
	"testing"
)

func TestBashOutputToolMetadataAndStore(t *testing.T) {
	tool := NewBashOutputTool(nil)
	if tool.Name() == "" {
		t.Fatalf("expected name")
	}
	if tool.Description() == "" {
		t.Fatalf("expected description")
	}
	if tool.Schema() == nil {
		t.Fatalf("expected schema")
	}
	if DefaultShellStore() == nil {
		t.Fatalf("expected default shell store")
	}
}

func TestShellStoreCloseAndFail(t *testing.T) {
	store := newShellStore()
	handle, err := store.Register("shell")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := handle.Close(0); err != nil {
		t.Fatalf("close: %v", err)
	}
	if err := handle.Fail(errors.New("boom")); err != nil {
		t.Fatalf("fail: %v", err)
	}
	if err := store.Fail("shell", nil); err != nil {
		t.Fatalf("fail nil: %v", err)
	}
}

func TestShellHandleLifecycle(t *testing.T) {
	store := newShellStore()
	handle, err := store.Register("shell-handle")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := handle.Append(ShellStreamStdout, "line"); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := handle.Close(0); err != nil {
		t.Fatalf("close: %v", err)
	}

	handle2, err := store.Register("shell-fail")
	if err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := handle2.Fail(errors.New("boom")); err != nil {
		t.Fatalf("fail: %v", err)
	}
}
