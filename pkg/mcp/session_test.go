package mcp

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"
)

func TestSessionCacheReuseAndExpiry(t *testing.T) {
	cache := NewSessionCache(50 * time.Millisecond)
	now := time.Now()
	cache.now = func() time.Time { return now }

	var (
		builds     atomic.Int32
		transports []*stubTransport
	)
	build := func() (*Client, error) {
		builds.Add(1)
		st := &stubTransport{}
		transports = append(transports, st)
		return NewClient(st), nil
	}

	client1, reused, err := cache.Get("server", build)
	if err != nil {
		t.Fatalf("get session: %v", err)
	}
	if reused {
		t.Fatal("first fetch should not reuse session")
	}

	client2, reused, err := cache.Get("server", build)
	if err != nil {
		t.Fatalf("reuse session: %v", err)
	}
	if !reused || client1 != client2 {
		t.Fatal("expected cached client reuse")
	}

	now = now.Add(100 * time.Millisecond)
	if err := cache.CloseIdle(); err != nil {
		t.Fatalf("close idle: %v", err)
	}
	if builds.Load() != 1 {
		t.Fatalf("expected single builder invocation, got %d", builds.Load())
	}
	if len(transports) == 0 || !transports[0].closed {
		t.Fatalf("expected cached client to be closed on expiry")
	}

	_, reused, err = cache.Get("server", build)
	if err != nil {
		t.Fatalf("get after expiry: %v", err)
	}
	if reused {
		t.Fatal("expected rebuild after expiry")
	}
	if builds.Load() != 2 {
		t.Fatalf("expected rebuild count 2, got %d", builds.Load())
	}

	if err := cache.CloseAll(); err != nil {
		t.Fatalf("close all: %v", err)
	}
}

func TestSessionCacheBuilderError(t *testing.T) {
	cache := NewSessionCache(time.Second)
	_, _, err := cache.Get("server", func() (*Client, error) {
		return nil, errors.New("boom")
	})
	if err == nil || err.Error() != "boom" {
		t.Fatalf("expected builder error, got %v", err)
	}
}

func TestSessionCacheNoExpiryWhenTTLZero(t *testing.T) {
	cache := NewSessionCache(0)
	cache.now = func() time.Time { return time.Unix(0, 0) }

	var builds atomic.Int32
	build := func() (*Client, error) {
		builds.Add(1)
		return NewClient(&stubTransport{}), nil
	}

	if _, reused, err := cache.Get("server", build); err != nil || reused {
		t.Fatalf("first fetch failed: %v reused=%v", err, reused)
	}
	if _, reused, err := cache.Get("server", build); err != nil || !reused {
		t.Fatalf("expected reuse when ttl zero, err=%v reused=%v", err, reused)
	}
	if builds.Load() != 1 {
		t.Fatalf("should not rebuild when ttl zero, builds=%d", builds.Load())
	}
	if err := cache.CloseIdle(); err != nil {
		t.Fatalf("close idle: %v", err)
	}
}
