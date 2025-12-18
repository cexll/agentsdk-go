package api

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestSessionGate(t *testing.T) {
	t.Run("same session blocks", func(t *testing.T) {
		gate := newSessionGate()
		sessionID := "session"

		release := make(chan struct{})
		held := make(chan struct{})
		holderDone := make(chan struct{})
		holderErr := make(chan error, 1)

		go func() {
			if err := gate.Acquire(context.Background(), sessionID); err != nil {
				holderErr <- err
				return
			}
			close(held)
			<-release
			gate.Release(sessionID)
			close(holderDone)
		}()

		select {
		case <-held:
		case err := <-holderErr:
			t.Fatalf("holder Acquire: %v", err)
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for holder Acquire")
		}

		go func() {
			time.Sleep(200 * time.Millisecond)
			close(release)
		}()

		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := gate.Acquire(ctx, sessionID); err != nil {
			t.Fatalf("Acquire after release: %v", err)
		}

		select {
		case <-release:
		default:
			t.Fatal("Acquire succeeded before Release")
		}

		gate.Release(sessionID)

		select {
		case <-holderDone:
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for holder Release")
		}
		if _, ok := gate.gates.Load(sessionID); ok {
			t.Fatalf("gate entry leaked for %q", sessionID)
		}
	})

	t.Run("different sessions independent", func(t *testing.T) {
		gate := newSessionGate()
		sessionA := "session-a"
		sessionB := "session-b"

		if err := gate.Acquire(context.Background(), sessionA); err != nil {
			t.Fatalf("Acquire(sessionA): %v", err)
		}

		started := make(chan struct{})
		done := make(chan error, 1)

		go func() {
			close(started)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			if err := gate.Acquire(ctx, sessionB); err != nil {
				done <- err
				return
			}
			gate.Release(sessionB)
			done <- nil
		}()

		<-started
		select {
		case err := <-done:
			if err != nil {
				t.Fatalf("Acquire(sessionB): %v", err)
			}
		case <-time.After(250 * time.Millisecond):
			t.Fatal("Acquire for a different session blocked unexpectedly")
		}

		gate.Release(sessionA)

		if _, ok := gate.gates.Load(sessionA); ok {
			t.Fatalf("gate entry leaked for %q", sessionA)
		}
		if _, ok := gate.gates.Load(sessionB); ok {
			t.Fatalf("gate entry leaked for %q", sessionB)
		}
	})

	t.Run("context cancel while waiting", func(t *testing.T) {
		gate := newSessionGate()
		sessionID := "session"

		if err := gate.Acquire(context.Background(), sessionID); err != nil {
			t.Fatalf("Acquire: %v", err)
		}

		ctx, cancel := context.WithCancel(context.Background())
		started := make(chan struct{})
		done := make(chan error, 1)

		go func() {
			close(started)
			done <- gate.Acquire(ctx, sessionID)
		}()

		<-started
		cancel()

		select {
		case err := <-done:
			if !errors.Is(err, context.Canceled) {
				t.Fatalf("expected context.Canceled, got %v", err)
			}
		case <-time.After(time.Second):
			t.Fatal("timed out waiting for Acquire cancel")
		}

		gate.Release(sessionID)

		if _, ok := gate.gates.Load(sessionID); ok {
			t.Fatalf("gate entry leaked for %q", sessionID)
		}
	})

	t.Run("context timeout while waiting", func(t *testing.T) {
		gate := newSessionGate()
		sessionID := "session"

		if err := gate.Acquire(context.Background(), sessionID); err != nil {
			t.Fatalf("Acquire: %v", err)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()

		if err := gate.Acquire(ctx, sessionID); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("expected context.DeadlineExceeded, got %v", err)
		}

		gate.Release(sessionID)

		if _, ok := gate.gates.Load(sessionID); ok {
			t.Fatalf("gate entry leaked for %q", sessionID)
		}
	})

	t.Run("canceled context does not hold gate", func(t *testing.T) {
		gate := newSessionGate()
		sessionID := "session"

		ctx, cancel := context.WithCancel(context.Background())
		cancel()

		if err := gate.Acquire(ctx, sessionID); !errors.Is(err, context.Canceled) {
			t.Fatalf("expected context.Canceled, got %v", err)
		}
		if _, ok := gate.gates.Load(sessionID); ok {
			t.Fatalf("gate entry leaked for %q", sessionID)
		}
	})

	t.Run("repeat release safe", func(t *testing.T) {
		var gate *sessionGate
		gate.Release("missing")

		gate = newSessionGate()
		sessionID := "session"

		gate.Release(sessionID)
		if err := gate.Acquire(context.Background(), sessionID); err != nil {
			t.Fatalf("Acquire: %v", err)
		}

		gate.Release(sessionID)
		gate.Release(sessionID)
	})
}
