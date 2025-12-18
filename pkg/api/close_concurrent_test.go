package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestRuntimeCloseWaitsForInFlightRun(t *testing.T) {
	mdl := newBlockingModel()
	rt := newConcurrentRuntime(t, mdl)

	runDone := make(chan error, 1)
	go func() {
		_, err := rt.Run(context.Background(), Request{Prompt: "first", SessionID: "sess"})
		runDone <- err
	}()
	waitSignals(t, mdl.started, 1)

	closeDone := make(chan error, 1)
	go func() {
		closeDone <- rt.Close()
	}()

	select {
	case err := <-closeDone:
		t.Fatalf("Close returned while Run in-flight: %v", err)
	case <-time.After(100 * time.Millisecond):
	}

	mdl.Unblock()

	if err := <-runDone; err != nil {
		t.Fatalf("Run failed: %v", err)
	}
	if err := <-closeDone; err != nil {
		t.Fatalf("Close failed: %v", err)
	}

	if _, err := rt.Run(context.Background(), Request{Prompt: "after-close"}); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("expected ErrRuntimeClosed, got %v", err)
	}
	if _, err := rt.RunStream(context.Background(), Request{Prompt: "after-close"}); !errors.Is(err, ErrRuntimeClosed) {
		t.Fatalf("expected ErrRuntimeClosed from RunStream, got %v", err)
	}
}
