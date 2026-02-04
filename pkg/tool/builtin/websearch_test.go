package toolbuiltin

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xhtml "golang.org/x/net/html"
)

func TestWebSearchExecute(t *testing.T) {
	t.Parallel()

	html := `<html><body>
<div class="result">
  <a class="result__a" href="https://example.com">Example</a>
  <a class="result__snippet">Snippet</a>
</div>
</body></html>`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(html))
	}))
	defer server.Close()

	orig := duckDuckGoEndpoint
	duckDuckGoEndpoint = server.URL
	t.Cleanup(func() { duckDuckGoEndpoint = orig })

	tool := NewWebSearchTool(&WebSearchOptions{HTTPClient: server.Client()})
	res, err := tool.Execute(context.Background(), map[string]interface{}{
		"query":           "example",
		"allowed_domains": []string{"example.com"},
	})
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success")
	}
	if !strings.Contains(res.Output, "Search results") {
		t.Fatalf("unexpected output %q", res.Output)
	}
}

func TestWebSearchHelpers(t *testing.T) {
	t.Parallel()

	if _, err := parseDomainList(map[string]interface{}{"domains": "bad"}, "domains"); err == nil {
		t.Fatalf("expected domain list error")
	}
	domains, err := parseDomainList(map[string]interface{}{"domains": []interface{}{"Example.com", "example.com"}}, "domains")
	if err != nil || len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("unexpected domains %v err=%v", domains, err)
	}

	results := []SearchResult{
		{Title: "A", URL: "https://a.example.com", Snippet: "s1"},
		{Title: "B", URL: "https://b.example.com", Snippet: "s2"},
	}
	filtered := filterResultsByDomain(results, []string{"a.example.com"}, []string{"b.example.com"}, 10)
	if len(filtered) != 1 || !strings.Contains(filtered[0].URL, "a.example.com") {
		t.Fatalf("unexpected filtered results %v", filtered)
	}

	if out := formatSearchOutput("q", nil); !strings.Contains(out, "No results") {
		t.Fatalf("expected no results output, got %q", out)
	}

	tool := NewWebSearchTool(nil)
	if tool.Name() == "" || tool.Description() == "" || tool.Schema() == nil {
		t.Fatalf("expected metadata")
	}

	if got := cleanResultURL("ftp://example.com"); got != "" {
		t.Fatalf("expected invalid url cleaned to empty, got %q", got)
	}
	if host := extractHost("://bad"); host != "" {
		t.Fatalf("expected empty host, got %q", host)
	}
	dedup := deduplicateResults([]SearchResult{{URL: "https://a"}, {URL: "https://a"}})
	if len(dedup) != 1 {
		t.Fatalf("expected deduped results, got %v", dedup)
	}
}

func TestWebSearchExecuteErrors(t *testing.T) {
	if _, err := (*WebSearchTool)(nil).Execute(context.Background(), map[string]interface{}{}); err == nil {
		t.Fatalf("expected nil tool error")
	}
	tool := NewWebSearchTool(nil)
	if _, err := tool.Execute(nil, map[string]interface{}{}); err == nil {
		t.Fatalf("expected nil context error")
	}
	if _, err := tool.Execute(context.Background(), nil); err == nil {
		t.Fatalf("expected nil params error")
	}
	if _, err := tool.Execute(context.Background(), map[string]interface{}{"query": 1}); err == nil {
		t.Fatalf("expected query type error")
	}
}

func TestWebSearchSearchErrors(t *testing.T) {
	orig := duckDuckGoEndpoint
	t.Cleanup(func() { duckDuckGoEndpoint = orig })

	duckDuckGoEndpoint = ""
	tool := NewWebSearchTool(nil)
	if _, err := tool.search(context.Background(), "q"); err == nil {
		t.Fatalf("expected empty endpoint error")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	}))
	defer server.Close()

	duckDuckGoEndpoint = server.URL
	tool = NewWebSearchTool(&WebSearchOptions{HTTPClient: server.Client()})
	if _, err := tool.search(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "status") {
		t.Fatalf("expected status error, got %v", err)
	}

	// response too large
	large := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(make([]byte, maxSearchResponseBytes+1))
	}))
	defer large.Close()
	duckDuckGoEndpoint = large.URL
	tool = NewWebSearchTool(&WebSearchOptions{HTTPClient: large.Client()})
	if _, err := tool.search(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "exceeded") {
		t.Fatalf("expected size error, got %v", err)
	}

	// request error
	duckDuckGoEndpoint = "http://example.com"
	client := &http.Client{Transport: roundTripFunc(func(*http.Request) (*http.Response, error) {
		return nil, errors.New("network down")
	})}
	tool = NewWebSearchTool(&WebSearchOptions{HTTPClient: client})
	if _, err := tool.search(context.Background(), "q"); err == nil || !strings.Contains(err.Error(), "search request") {
		t.Fatalf("expected request error, got %v", err)
	}
}

func TestWebSearchHTMLHelpers(t *testing.T) {
	root := &xhtml.Node{
		Type: xhtml.ElementNode,
		Data: "div",
		Attr: []xhtml.Attribute{{Key: "class", Val: "a b"}},
	}
	child := &xhtml.Node{Type: xhtml.ElementNode, Data: "a", Attr: []xhtml.Attribute{{Key: "href", Val: "https://example.com"}}}
	text := &xhtml.Node{Type: xhtml.TextNode, Data: " hello \n world "}
	root.AppendChild(child)
	root.AppendChild(text)

	if !nodeHasClass(root, "a") {
		t.Fatalf("expected class match")
	}
	if nodeHasClass(root, "missing") {
		t.Fatalf("expected missing class")
	}
	if got := getAttr(child, "href"); got == "" {
		t.Fatalf("expected href attr")
	}
	if got := getAttr(child, "missing"); got != "" {
		t.Fatalf("expected empty missing attr")
	}
	if got := collapseWhitespace("  a \n b\t"); got != "a b" {
		t.Fatalf("unexpected collapsed whitespace %q", got)
	}
	if got := nodeText(root); got != "hello world" {
		t.Fatalf("unexpected node text %q", got)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(r *http.Request) (*http.Response, error) {
	return f(r)
}
