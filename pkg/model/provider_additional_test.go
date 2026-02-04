package model

import (
	"context"
	"errors"
	"testing"
	"time"
)

type stubProvider struct {
	mdl Model
	err error
}

func (s stubProvider) Model(context.Context) (Model, error) {
	return s.mdl, s.err
}

func TestProviderFuncNil(t *testing.T) {
	var fn ProviderFunc
	if _, err := fn.Model(context.Background()); err == nil {
		t.Fatalf("expected error for nil provider func")
	}
}

func TestAnthropicProviderCaching(t *testing.T) {
	p := &AnthropicProvider{APIKey: "key", CacheTTL: time.Minute}
	m1, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	m2, err := p.Model(context.Background())
	if err != nil {
		t.Fatalf("model: %v", err)
	}
	if m1 != m2 {
		t.Fatalf("expected cached model")
	}
}

func TestAnthropicProviderResolveAPIKey(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	if got := p.resolveAPIKey(); got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}
}

func TestAnthropicProviderResolveAPIKeyPriority(t *testing.T) {
	p := &AnthropicProvider{APIKey: "explicit"}
	t.Setenv("ANTHROPIC_API_KEY", "envkey")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "auth")
	if got := p.resolveAPIKey(); got != "explicit" {
		t.Fatalf("expected explicit key, got %q", got)
	}

	p.APIKey = ""
	if got := p.resolveAPIKey(); got != "envkey" {
		t.Fatalf("expected env key, got %q", got)
	}

	t.Setenv("ANTHROPIC_API_KEY", "")
	if got := p.resolveAPIKey(); got != "auth" {
		t.Fatalf("expected auth token, got %q", got)
	}
}

func TestMustProvider(t *testing.T) {
	if _, err := func() (Model, error) {
		defer func() {
			if r := recover(); r == nil {
				t.Fatalf("expected panic for nil provider")
			}
		}()
		_ = MustProvider(nil)
		return nil, nil
	}(); err != nil {
		t.Fatalf("unexpected error %v", err)
	}

	ok := MustProvider(stubProvider{mdl: &anthropicModel{}})
	if ok == nil {
		t.Fatalf("expected model")
	}
}

func TestAnthropicProviderCacheDisabled(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: 0}
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil cached model when cache disabled")
	}
	p.store(&anthropicModel{})
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected nil cached model when cache disabled")
	}
}

func TestAnthropicProviderCacheExpiry(t *testing.T) {
	p := &AnthropicProvider{CacheTTL: time.Millisecond}
	p.store(&anthropicModel{})
	p.expires = time.Now().Add(-time.Minute)
	if got := p.cachedModel(); got != nil {
		t.Fatalf("expected expired cache to return nil")
	}
}

func TestAnthropicProviderModelMissingAPIKey(t *testing.T) {
	p := &AnthropicProvider{}
	t.Setenv("ANTHROPIC_API_KEY", "")
	t.Setenv("ANTHROPIC_AUTH_TOKEN", "")
	if _, err := p.Model(context.Background()); err == nil {
		t.Fatalf("expected error for missing api key")
	}
}

func TestMustProviderError(t *testing.T) {
	defer func() {
		if r := recover(); r == nil {
			t.Fatalf("expected panic on provider error")
		}
	}()
	_ = MustProvider(stubProvider{err: errors.New("boom")})
}
