package skills

import "testing"

func TestActivationContextCloneIsolation(t *testing.T) {
	ctx := ActivationContext{
		Prompt:   "Hello world",
		Channels: []string{"cli"},
		Tags:     map[string]string{"k": "v"},
		Traits:   []string{"dev"},
		Metadata: map[string]any{"x": 1},
	}
	cloned := ctx.Clone()
	cloned.Prompt = "changed"
	cloned.Channels[0] = "ui"
	cloned.Tags["k"] = "other"
	cloned.Metadata["x"] = 2
	cloned.Traits[0] = "ops"

	meta, ok := ctx.Metadata["x"].(int)
	if !ok {
		t.Fatalf("expected int metadata, got %T", ctx.Metadata["x"])
	}
	if ctx.Prompt != "Hello world" || ctx.Channels[0] != "cli" || ctx.Tags["k"] != "v" || meta != 1 || ctx.Traits[0] != "dev" {
		t.Fatalf("clone mutated original context: %#v", ctx)
	}
}

func TestKeywordMatcher(t *testing.T) {
	matcher := KeywordMatcher{All: []string{"deploy"}, Any: []string{"staging", "prod"}}
	result := matcher.Match(ActivationContext{Prompt: "deploy to staging please"})
	if !result.Matched || result.Score <= 0.5 {
		t.Fatalf("expected match with score>0.5, got %+v", result)
	}

	miss := matcher.Match(ActivationContext{Prompt: "just chat"})
	if miss.Matched {
		t.Fatalf("expected no match, got %+v", miss)
	}
}

func TestTagMatcher(t *testing.T) {
	matcher := TagMatcher{Require: map[string]string{"env": "prod"}, Exclude: map[string]string{"role": "readonly"}}
	result := matcher.Match(ActivationContext{Tags: map[string]string{"Env": "Prod", "Role": "writer"}})
	if !result.Matched {
		t.Fatalf("expected match, got %+v", result)
	}
	miss := matcher.Match(ActivationContext{Tags: map[string]string{"env": "prod", "role": "readonly"}})
	if miss.Matched {
		t.Fatalf("expected exclude match to fail")
	}
}

func TestTraitMatcher(t *testing.T) {
	matcher := TraitMatcher{Traits: []string{"vip", "beta"}}
	result := matcher.Match(ActivationContext{Traits: []string{"VIP"}})
	if !result.Matched || result.Score < 0.55 {
		t.Fatalf("trait match expected, got %+v", result)
	}
	miss := matcher.Match(ActivationContext{Traits: []string{"standard"}})
	if miss.Matched {
		t.Fatalf("expected no match")
	}
}

func TestMatchResultBetterThan(t *testing.T) {
	a := MatchResult{Matched: true, Score: 0.8, Reason: "a"}
	b := MatchResult{Matched: true, Score: 0.7, Reason: "b"}
	if !a.BetterThan(b) {
		t.Fatalf("expected a better than b")
	}
	if !(MatchResult{Matched: true, Score: 0.5, Reason: "a"}).BetterThan(MatchResult{Matched: true, Score: 0.5, Reason: "b"}) {
		t.Fatalf("reason comparison branch not hit")
	}
	if (MatchResult{Matched: false}).BetterThan(MatchResult{Matched: true}) {
		t.Fatalf("unmatched result should never win")
	}
	if (MatchResult{Matched: true, Score: 0.5, Reason: ""}).BetterThan(MatchResult{Matched: true, Score: 0.5, Reason: "z"}) {
		t.Fatalf("empty reason should not outrank non-empty")
	}
	if !(MatchResult{Matched: true, Score: 0.5, Reason: "a"}).BetterThan(MatchResult{Matched: true, Score: 0.5, Reason: ""}) {
		t.Fatalf("non-empty reason should win over empty")
	}
	if a.BetterThan(MatchResult{Matched: false}) == false && a.Matched {
		// unreachable but keeps coverage simple
		return
	}
}

func TestMatcherFunc(t *testing.T) {
	var nilFn MatcherFunc
	if res := nilFn.Match(ActivationContext{}); res.Matched {
		t.Fatalf("nil matcher should not match, got %+v", res)
	}
	called := false
	fn := MatcherFunc(func(ctx ActivationContext) MatchResult {
		called = true
		return MatchResult{Matched: true, Score: 0.9}
	})
	res := fn.Match(ActivationContext{})
	if !res.Matched || !called {
		t.Fatalf("expected matcher func to run, got %+v", res)
	}
}

func TestHelpersAndEdgeCases(t *testing.T) {
	// keyword matcher empty inputs / prompt
	if res := (KeywordMatcher{}).Match(ActivationContext{}); res.Matched {
		t.Fatalf("empty keyword matcher should not match")
	}
	if res := (KeywordMatcher{All: []string{"deploy"}}).Match(ActivationContext{}); res.Matched {
		t.Fatalf("keyword matcher should not match empty prompt")
	}

	// tag matcher coverage: exclude with empty value, normalize case
	tags := map[string]string{"Role": "Admin"}
	if res := (TagMatcher{Exclude: map[string]string{"role": ""}}).Match(ActivationContext{Tags: tags}); res.Matched {
		t.Fatalf("exclude any value should block match")
	}
	req := map[string]string{"Env": "Prod"}
	if !(TagMatcher{Require: req}.Match(ActivationContext{Tags: map[string]string{"env": "prod"}}).Matched) {
		t.Fatalf("require should match ignoring case")
	}

	// trait matcher with empty context
	if res := (TraitMatcher{Traits: []string{"vip"}}).Match(ActivationContext{}); res.Matched {
		t.Fatalf("should not match when ctx has no traits")
	}

	// helper utilities
	if v := clampScore(-1); v != 0 {
		t.Fatalf("clampScore lower bound failed: %v", v)
	}
	if v := clampScore(1.5); v != 0.99 {
		t.Fatalf("clampScore upper bound failed: %v", v)
	}
	if r := ratio(1, 0); r != 0 {
		t.Fatalf("ratio should guard zero total")
	}
	tokens := normalizeTokens([]string{" B ", "a", "a"})
	if len(tokens) != 2 || tokens[0] != "a" || tokens[1] != "b" {
		t.Fatalf("normalizeTokens unexpected: %v", tokens)
	}
	if m := normalizeTagMap(map[string]string{" Key ": " Val "}); m["key"] != "val" {
		t.Fatalf("normalizeTagMap failed: %v", m)
	}
	if len(tokenSet([]string{"", "X", "x"})) != 1 {
		t.Fatalf("tokenSet should dedupe and ignore empty")
	}
}
