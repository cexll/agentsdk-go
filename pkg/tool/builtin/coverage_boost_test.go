package toolbuiltin

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/runtime/commands"
	xhtml "golang.org/x/net/html"
)

type stringerTest string

func (s stringerTest) String() string { return string(s) }

func TestBashOutputRenderHelpers(t *testing.T) {
	read := ShellRead{
		ShellID: "shell",
		Status:  ShellStatusCompleted,
		Lines: []ShellLine{
			{Stream: ShellStreamStdout, Content: "out"},
			{Stream: ShellStreamStderr, Content: "err"},
		},
	}
	if got := renderShellRead(read); !strings.Contains(got, "shell shell") || !strings.Contains(got, "out") {
		t.Fatalf("unexpected render %q", got)
	}
	if got := renderShellRead(ShellRead{ShellID: "s", Status: ShellStatusRunning, Error: "oops"}); !strings.Contains(got, "oops") {
		t.Fatalf("expected error to be included, got %q", got)
	}
	if got := collectStream(read.Lines, ShellStreamStdout); got != "out" {
		t.Fatalf("unexpected stdout %q", got)
	}
	if got := renderAsyncRead("id", "running", "", errors.New("fail")); !strings.Contains(got, "fail") {
		t.Fatalf("expected async error in output, got %q", got)
	}
	if got := renderAsyncRead("id", "running", "chunk", nil); !strings.Contains(got, "chunk") {
		t.Fatalf("expected async chunk in output, got %q", got)
	}
	if id, isAsync, err := parseOutputID(map[string]interface{}{"task_id": "t1"}); err != nil || !isAsync || id != "t1" {
		t.Fatalf("unexpected task id parse: %v %v %v", id, isAsync, err)
	}
}

func TestSlashCommandDescription(t *testing.T) {
	if desc := buildSlashCommandDescription(nil); !strings.Contains(desc, "no commands") {
		t.Fatalf("expected empty commands description, got %q", desc)
	}
	defs := []commands.Definition{
		{Name: "", Description: ""},
		{Name: "hello", Description: "world"},
	}
	desc := buildSlashCommandDescription(defs)
	if !strings.Contains(desc, "/unnamed: No description provided.") || !strings.Contains(desc, "/hello: world") {
		t.Fatalf("unexpected description %q", desc)
	}
}

func TestAsyncManagerRunningCountAndShutdown(t *testing.T) {
	manager := newAsyncTaskManager()
	done := make(chan struct{})
	closed := make(chan struct{})
	close(closed)

	manager.mu.Lock()
	manager.tasks["running"] = &AsyncTask{ID: "running", Done: done, StartTime: time.Now()}
	manager.tasks["done"] = &AsyncTask{ID: "done", Done: closed, StartTime: time.Now()}
	count := manager.runningCountLocked()
	manager.mu.Unlock()

	if count != 1 {
		t.Fatalf("expected 1 running task, got %d", count)
	}

	close(done)
	if err := manager.Shutdown(nil); err != nil {
		t.Fatalf("shutdown: %v", err)
	}

	var nilMgr *AsyncTaskManager
	if err := nilMgr.Shutdown(context.Background()); err != nil {
		t.Fatalf("nil shutdown should be nil, got %v", err)
	}
}

func TestWebFetchHelpers(t *testing.T) {
	if nameToHeadingLevel("h2") != 2 || nameToHeadingLevel("div") != 6 {
		t.Fatalf("unexpected heading level")
	}

	lines := make([]string, 0, markdownSnippetMaxLines+1)
	for i := 0; i < markdownSnippetMaxLines+1; i++ {
		lines = append(lines, "line")
	}
	snippet := summariseMarkdown(strings.Join(lines, "\n"))
	if !strings.HasSuffix(snippet, "...") {
		t.Fatalf("expected ellipsis, got %q", snippet)
	}

	if _, err := stringValue(123); err == nil {
		t.Fatalf("expected stringValue error")
	}
	if val, _ := stringValue(json.Number("12")); val != "12" {
		t.Fatalf("unexpected json number %q", val)
	}
	if val, _ := stringValue(stringerTest("ok")); val != "ok" {
		t.Fatalf("unexpected stringer %q", val)
	}

	if _, err := extractNonEmptyString(map[string]interface{}{}, "prompt"); err == nil {
		t.Fatalf("expected missing key error")
	}
	if _, err := extractNonEmptyString(map[string]interface{}{"prompt": " "}, "prompt"); err == nil {
		t.Fatalf("expected empty value error")
	}
	if _, err := extractNonEmptyString(map[string]interface{}{"prompt": 1}, "prompt"); err == nil {
		t.Fatalf("expected non-string error")
	}
	if val, err := extractNonEmptyString(map[string]interface{}{"prompt": "ok"}, "prompt"); err != nil || val != "ok" {
		t.Fatalf("unexpected prompt value %q err=%v", val, err)
	}

	tool := NewWebFetchTool(nil)
	if _, err := tool.extractURL(map[string]interface{}{"url": ""}); err == nil {
		t.Fatalf("expected empty url error")
	}
	if normalized, err := tool.normaliseURL("http://example.com"); err != nil || !strings.HasPrefix(normalized, "https://") {
		t.Fatalf("expected https normalization, got %q err=%v", normalized, err)
	}
	if _, err := tool.normaliseURL("ftp://example.com"); err == nil {
		t.Fatalf("expected unsupported scheme error")
	}

	policy := tool.redirectPolicy()
	req1, _ := http.NewRequest("GET", "https://a.com", nil)
	req2, _ := http.NewRequest("GET", "https://b.com", nil)
	if err := policy(req2, []*http.Request{req1}); err == nil {
		t.Fatalf("expected redirect error")
	} else {
		if notice := detectRedirectNotice(err); notice == nil || notice.URL != req2.URL.String() {
			t.Fatalf("expected redirect notice, got %v", notice)
		}
		urlErr := &url.Error{Err: err}
		if notice := detectRedirectNotice(urlErr); notice == nil {
			t.Fatalf("expected notice from url error")
		}
	}

	if _, err := readBounded(strings.NewReader("1234"), 3); err == nil {
		t.Fatalf("expected size limit error")
	}
	if data, err := readBounded(strings.NewReader("12"), 3); err != nil || string(data) != "12" {
		t.Fatalf("unexpected bounded read %q err=%v", data, err)
	}

	if got := htmlToMarkdown("<h1>Title</h1><p>Hi</p>"); !strings.Contains(got, "Title") {
		t.Fatalf("unexpected markdown %q", got)
	}
}

func TestWebSearchHelperFunctions(t *testing.T) {
	node := &xhtml.Node{
		Type: xhtml.ElementNode,
		Data: "div",
		Attr: []xhtml.Attribute{{Key: "class", Val: "result test"}},
	}
	if !nodeHasClass(node, "result") {
		t.Fatalf("expected class match")
	}
	if getAttr(node, "class") == "" {
		t.Fatalf("expected class attr")
	}
	text := &xhtml.Node{Type: xhtml.TextNode, Data: "Hello"}
	br := &xhtml.Node{Type: xhtml.ElementNode, Data: "br"}
	text2 := &xhtml.Node{Type: xhtml.TextNode, Data: "World"}
	node.FirstChild = text
	text.NextSibling = br
	br.NextSibling = text2
	if got := nodeText(node); got != "Hello World" {
		t.Fatalf("unexpected node text %q", got)
	}
	if collapseWhitespace("  a  b  ") != "a b" {
		t.Fatalf("unexpected collapse")
	}
	if urlStr := cleanResultURL("https%3A%2F%2Fexample.com%2Fpath#frag"); !strings.HasPrefix(urlStr, "https://example.com/path") {
		t.Fatalf("unexpected cleaned url %q", urlStr)
	}
	results := []SearchResult{{URL: "https://example.com"}, {URL: "https://example.com"}, {URL: ""}}
	if got := deduplicateResults(results); len(got) != 1 {
		t.Fatalf("expected deduped results, got %d", len(got))
	}
	domains := normaliseDomains([]string{"Example.com", " example.com ", ""})
	if len(domains) != 1 || domains[0] != "example.com" {
		t.Fatalf("unexpected domains %v", domains)
	}
	filtered := filterResultsByDomain([]SearchResult{
		{URL: "https://allowed.com/x"},
		{URL: "https://blocked.com/y"},
	}, []string{"allowed.com"}, []string{"blocked.com"}, 0)
	if len(filtered) != 1 || !strings.Contains(filtered[0].URL, "allowed.com") {
		t.Fatalf("unexpected filter results %v", filtered)
	}
}

func TestEscapeXMLAndGitignoreHelpers(t *testing.T) {
	if escapeXML(`a&b<c>"d'`) != "a&amp;b&lt;c&gt;&quot;d&apos;" {
		t.Fatalf("unexpected escape")
	}
	if escapeXML("") != "" {
		t.Fatalf("expected empty escape")
	}

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("*.tmp"), 0o600)
	glob := NewGlobToolWithRoot(dir)
	glob.SetRespectGitignore(false)
	if glob.respectGitignore {
		t.Fatalf("expected gitignore disabled")
	}
	glob.SetRespectGitignore(true)
	if glob.gitignoreMatcher == nil {
		t.Fatalf("expected matcher when enabled")
	}

	grep := NewGrepToolWithRoot(dir)
	grep.SetRespectGitignore(false)
	if grep.respectGitignore {
		t.Fatalf("expected gitignore disabled")
	}
	grep.SetRespectGitignore(true)
	if grep.gitignoreMatcher == nil {
		t.Fatalf("expected grep matcher when enabled")
	}
}

func TestIntFromInt64(t *testing.T) {
	if v, err := intFromInt64(5); err != nil || v != 5 {
		t.Fatalf("unexpected int conversion %d err=%v", v, err)
	}
	if strconv.IntSize == 32 {
		val := maxIntValue
		val++
		if _, err := intFromInt64(val); err == nil {
			t.Fatalf("expected out of range error")
		}
	}
}
