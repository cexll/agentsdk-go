package toolbuiltin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestWebFetchExecuteAndCache(t *testing.T) {
	t.Parallel()

	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("<html><body><h1>Hello</h1></body></html>"))
	}))
	defer server.Close()

	tool := NewWebFetchTool(&WebFetchOptions{
		HTTPClient:        server.Client(),
		AllowPrivateHosts: true,
	})

	params := map[string]interface{}{
		"url":    server.URL,
		"prompt": "summarise",
	}
	res, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}
	data, ok := res.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", res.Data)
	}
	if fromCache, ok := data["from_cache"].(bool); !ok || fromCache {
		t.Fatalf("expected cache miss, got %v", data["from_cache"])
	}

	res2, err := tool.Execute(context.Background(), params)
	if err != nil {
		t.Fatalf("execute 2 failed: %v", err)
	}
	data2, ok := res2.Data.(map[string]interface{})
	if !ok {
		t.Fatalf("unexpected data type %T", res2.Data)
	}
	if fromCache, ok := data2["from_cache"].(bool); !ok || !fromCache {
		t.Fatalf("expected cache hit, got %v", data2["from_cache"])
	}
}

func TestWebFetchValidationHelpers(t *testing.T) {
	t.Parallel()

	tool := NewWebFetchTool(&WebFetchOptions{AllowPrivateHosts: true})
	if _, err := tool.extractURL(map[string]interface{}{}); err == nil {
		t.Fatalf("expected missing url error")
	}
	if _, err := tool.normaliseURL("ftp://example.com"); err == nil {
		t.Fatalf("expected unsupported scheme error")
	}
	if _, err := tool.normaliseURL("https://"); err == nil {
		t.Fatalf("expected missing host error")
	}

	policy := tool.redirectPolicy()
	orig, _ := url.Parse("https://a.example.com")
	next, _ := url.Parse("https://b.example.com")
	err := policy(&http.Request{URL: next}, []*http.Request{{URL: orig}})
	if err == nil {
		t.Fatalf("expected redirect error")
	}
	if notice := detectRedirectNotice(err); notice == nil || !strings.Contains(notice.URL, "b.example.com") {
		t.Fatalf("expected redirect notice, got %v", notice)
	}

	if notice := detectRedirectNotice(&url.Error{Err: &hostRedirectError{target: "https://x"}}); notice == nil {
		t.Fatalf("expected url error notice")
	}
	if notice := detectRedirectNotice(errors.New("plain")); notice != nil {
		t.Fatalf("expected no notice")
	}

	if _, err := readBounded(strings.NewReader("data"), 1); err == nil {
		t.Fatalf("expected bounded read error")
	}
}

func TestWebFetchMetadataAndMarkdown(t *testing.T) {
	t.Parallel()

	tool := NewWebFetchTool(&WebFetchOptions{AllowPrivateHosts: true})
	if tool.Name() == "" || tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("expected metadata")
	}

	validator := newHostValidator([]string{"example.com"}, []string{"blocked.com"}, false)
	if err := validator.Validate("blocked.com"); err == nil {
		t.Fatalf("expected blocked host")
	}
	if err := validator.Validate("example.com"); err != nil {
		t.Fatalf("expected allowed host, got %v", err)
	}

	md := htmlToMarkdown("<h1>Title</h1><p><strong>Hi</strong> <em>there</em> <a href=\"https://example.com\">link</a><br/>line</p><pre><code>code</code></pre><img src=\"x\" alt=\"img\"/><ul><li>Item</li></ul><ol><li>One</li></ol>")
	if !strings.Contains(md, "#") || !strings.Contains(md, "- ") || !strings.Contains(md, "1. ") {
		t.Fatalf("unexpected markdown %q", md)
	}

	if v, err := stringValue([]byte("ok")); err != nil || v != "ok" {
		t.Fatalf("unexpected stringValue %q err=%v", v, err)
	}

	if host := hostWithoutPort("example.com:443"); host != "example.com" {
		t.Fatalf("unexpected host %q", host)
	}
	if level := nameToHeadingLevel("h4"); level != 4 {
		t.Fatalf("unexpected heading level %d", level)
	}
	if set := sliceToSet([]string{"A", "a", " "}); len(set) != 1 {
		t.Fatalf("unexpected set size %d", len(set))
	}
}

func TestWebFetchExecuteErrors(t *testing.T) {
	if _, err := (*WebFetchTool)(nil).Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatalf("expected nil tool error")
	}
	tool := NewWebFetchTool(&WebFetchOptions{AllowPrivateHosts: true})
	if _, err := tool.Execute(nil, map[string]interface{}{}); err == nil {
		t.Fatalf("expected nil context error")
	}
	if _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"url": "https://example.com"}); err == nil {
		t.Fatalf("expected missing prompt error")
	}

	blocked := NewWebFetchTool(&WebFetchOptions{AllowedHosts: []string{"allowed.com"}})
	if _, err := blocked.Execute(context.Background(), map[string]interface{}{
		"url":    "https://blocked.com",
		"prompt": "x",
	}); err == nil {
		t.Fatalf("expected blocked host error")
	}
}

func TestWebFetchRedirectPolicyBranches(t *testing.T) {
	tool := NewWebFetchTool(&WebFetchOptions{AllowPrivateHosts: true})
	policy := tool.redirectPolicy()

	orig, _ := url.Parse("https://example.com")
	next, _ := url.Parse("https://example.com/path")
	if err := policy(&http.Request{URL: next}, []*http.Request{{URL: orig}}); err != nil {
		t.Fatalf("expected same-host redirect to pass, got %v", err)
	}

	var chain []*http.Request
	for i := 0; i < maxFetchRedirects; i++ {
		chain = append(chain, &http.Request{URL: orig})
	}
	if err := policy(&http.Request{URL: orig}, chain); err == nil {
		t.Fatalf("expected too many redirects error")
	}
}

func TestWebFetchHostValidatorPrivateIP(t *testing.T) {
	validator := newHostValidator(nil, nil, false)
	if err := validator.Validate("127.0.0.1"); err == nil {
		t.Fatalf("expected private ip to be blocked")
	}
}

func TestWebFetchReadBoundedDefaultLimit(t *testing.T) {
	if _, err := readBounded(strings.NewReader("ok"), 0); err != nil {
		t.Fatalf("expected bounded read to succeed, got %v", err)
	}
}

func TestWebFetchHelperBranches(t *testing.T) {
	if level := nameToHeadingLevel("unknown"); level != 6 {
		t.Fatalf("expected default heading level, got %d", level)
	}
	if got := collapseSpaces("  a   b  "); got != "a b" {
		t.Fatalf("unexpected collapsed spaces %q", got)
	}
	lines := make([]string, 0, markdownSnippetMaxLines+1)
	for i := 0; i < markdownSnippetMaxLines+1; i++ {
		lines = append(lines, "line")
	}
	long := strings.Join(lines, "\n")
	if out := summariseMarkdown(long); !strings.Contains(out, "...") {
		t.Fatalf("expected ellipsis in summary")
	}
	if _, err := stringValue(123); err == nil {
		t.Fatalf("expected stringValue error")
	}
}
