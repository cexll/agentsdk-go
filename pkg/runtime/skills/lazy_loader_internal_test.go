package skills

import (
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// These tests live in the skills package to get coverage on lazy-loading internals.

func TestHandlerLazyLoadsOnFirstExecute(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "lazy")

	writeSkill(t, filepath.Join(dir, "SKILL.md"), "lazy", "lazy body")
	mustWrite(t, filepath.Join(dir, "scripts", "setup.sh"), "echo hi")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}
	if len(regs) != 1 {
		t.Fatalf("expected 1 reg, got %d", len(regs))
	}

	lazy := requireLazyHandler(t, regs[0].Handler)
	if lazy.loaded {
		t.Fatalf("expected not loaded before execute")
	}

	res, err := regs[0].Handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("execute: %v", err)
	}
	output, ok := res.Output.(map[string]any)
	require.True(t, ok)
	if output["body"] != "lazy body" {
		t.Fatalf("unexpected body: %#v", output["body"])
	}
	support, ok := output["support_files"].(map[string][]string)
	require.True(t, ok)
	require.Equal(t, []string{"setup.sh"}, support["scripts"])

	if !lazy.loaded {
		t.Fatalf("expected loaded after execute")
	}
}

func TestHandlerCachesLoadResult(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "cache")
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "cache", "cache body")

	regs, _ := LoadFromFS(LoaderOptions{ProjectRoot: root})
	lazy := requireLazyHandler(t, regs[0].Handler)

	res1, err := lazy.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("first execute failed: %v", err)
	}
	res2, err := lazy.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("second execute failed: %v", err)
	}

	// Results should be the same cached instance
	out1, ok := res1.Output.(map[string]any)
	require.True(t, ok)
	out2, ok := res2.Output.(map[string]any)
	require.True(t, ok)
	if out1["body"] != out2["body"] {
		t.Fatalf("expected same cached body")
	}
}

func TestHandlerConcurrentExecuteSingleLoad(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "concurrent")
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "concurrent", "body")

	regs, _ := LoadFromFS(LoaderOptions{ProjectRoot: root})
	handler := regs[0].Handler

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			if _, err := handler.Execute(context.Background(), ActivationContext{}); err != nil {
				t.Errorf("execute error: %v", err)
			}
		}()
	}
	wg.Wait()

	// All goroutines should complete without error
	lazy := requireLazyHandler(t, handler)
	if !lazy.loaded {
		t.Fatalf("expected loaded after concurrent executes")
	}
}

func TestHandlerHotReloadOnFileChange(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "hotreload")
	skillPath := filepath.Join(dir, "SKILL.md")
	writeSkill(t, skillPath, "hotreload", "original body")

	regs, errs := LoadFromFS(LoaderOptions{ProjectRoot: root})
	if len(errs) != 0 {
		t.Fatalf("unexpected errs: %v", errs)
	}

	handler := regs[0].Handler
	res1, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}
	out1, ok := res1.Output.(map[string]any)
	require.True(t, ok)
	if out1["body"] != "original body" {
		t.Fatalf("unexpected first body: %v", out1["body"])
	}

	// Wait a bit and modify the file
	time.Sleep(10 * time.Millisecond)
	writeSkill(t, skillPath, "hotreload", "updated body")

	// Execute again - should reload
	res2, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("second execute: %v", err)
	}
	out2, ok := res2.Output.(map[string]any)
	require.True(t, ok)
	if out2["body"] != "updated body" {
		t.Fatalf("expected updated body after file change, got: %v", out2["body"])
	}
}

func TestHandlerBodyLengthProbe(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "probe")
	body := "probe body"
	writeSkill(t, filepath.Join(dir, "SKILL.md"), "probe", body)

	regs, _ := LoadFromFS(LoaderOptions{ProjectRoot: root})
	handler := regs[0].Handler

	sizer, ok := handler.(interface {
		BodyLength() (int, bool)
	})
	if !ok {
		t.Fatalf("handler does not expose BodyLength")
	}
	if size, loaded := sizer.BodyLength(); loaded || size != 0 {
		t.Fatalf("expected unloaded body length probe to be zero, got size=%d loaded=%t", size, loaded)
	}

	if _, err := handler.Execute(context.Background(), ActivationContext{}); err != nil {
		t.Fatalf("execute: %v", err)
	}

	if size, loaded := sizer.BodyLength(); !loaded || size != len(body) {
		t.Fatalf("expected loaded body length=%d loaded=%t, got %d %t", len(body), true, size, loaded)
	}
}

func TestHandlerStatError(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, ".claude", "skills", "staterr")
	skillPath := filepath.Join(dir, "SKILL.md")
	writeSkill(t, skillPath, "staterr", "body")

	regs, _ := LoadFromFS(LoaderOptions{ProjectRoot: root})
	handler := regs[0].Handler

	// First execute should work
	_, err := handler.Execute(context.Background(), ActivationContext{})
	if err != nil {
		t.Fatalf("first execute: %v", err)
	}

	// Delete the file
	if err := os.Remove(skillPath); err != nil {
		t.Fatalf("remove: %v", err)
	}

	// Second execute should fail with stat error
	_, err = handler.Execute(context.Background(), ActivationContext{})
	if err == nil {
		t.Fatalf("expected stat error after file deletion")
	}
}

func requireLazyHandler(t *testing.T, handler Handler) *lazySkillHandler {
	t.Helper()
	lazy, ok := handler.(*lazySkillHandler)
	if !ok {
		t.Fatalf("expected *lazySkillHandler, got %T", handler)
	}
	return lazy
}
