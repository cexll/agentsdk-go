package middleware

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

func TestTraceHelpersAndRender(t *testing.T) {
	st := &State{}
	ensureStateValues(st)
	if st.Values == nil {
		t.Fatalf("expected values map initialized")
	}

	ctx := context.WithValue(context.Background(), SessionIDContextKey, "ctx-session")
	st.Values["trace.session_id"] = "state-session"
	tm := NewTraceMiddleware(t.TempDir())
	if id := tm.resolveSessionID(ctx, st); id != "state-session" {
		t.Fatalf("expected state session id, got %q", id)
	}

	st.Values = map[string]any{}
	if id := tm.resolveSessionID(ctx, st); id != "ctx-session" {
		t.Fatalf("expected context session id, got %q", id)
	}

	raw := json.RawMessage(`{"a":1}`)
	copied := sanitizePayload(raw).(json.RawMessage)
	raw[0] = 'x'
	if copied[0] == 'x' {
		t.Fatalf("expected raw message copy")
	}
	if got := sanitizePayload(errors.New("boom")).(string); got != "boom" {
		t.Fatalf("unexpected error payload %q", got)
	}
	if _, ok := sanitizePayload([]byte(`{"ok":true}`)).(json.RawMessage); !ok {
		t.Fatalf("expected json raw message")
	}
	if got := sanitizePayload([]byte("plain")).(string); got != "plain" {
		t.Fatalf("unexpected byte payload %q", got)
	}
	if got := sanitizePayload(func() {}).(string); !strings.HasPrefix(got, "<non-serializable") {
		t.Fatalf("unexpected non-serializable payload %q", got)
	}

	type errStruct struct{ Err error }
	if val := valueErrorString(errStruct{Err: errors.New("oops")}); val != "oops" {
		t.Fatalf("unexpected value error %q", val)
	}
	if val := valueErrorString(errors.New("fail")); val != "fail" {
		t.Fatalf("unexpected error string %q", val)
	}

	dir := t.TempDir()
	outPath := filepath.Join(dir, "trace.html")
	if err := writeAtomic(outPath, []byte("ok")); err != nil {
		t.Fatalf("writeAtomic: %v", err)
	}
	if data, err := os.ReadFile(outPath); err != nil || string(data) != "ok" {
		t.Fatalf("unexpected written data %q err=%v", data, err)
	}

	jsonPath := filepath.Join(dir, "trace.jsonl")
	f, err := os.OpenFile(jsonPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	evt := TraceEvent{Timestamp: time.Now(), Stage: "stage", SessionID: "sess"}
	if err := writeJSONLine(f, evt); err != nil {
		t.Fatalf("write json line: %v", err)
	}
	_ = f.Close()

	tm.tmpl = nil
	sess := &traceSession{
		id:        "sess",
		createdAt: time.Now(),
		updatedAt: time.Now(),
		jsonPath:  jsonPath,
		htmlPath:  outPath,
		events: []TraceEvent{
			{Timestamp: time.Now(), Stage: "stage", SessionID: "sess", Input: map[string]any{"bad": func() {}}},
		},
	}
	if err := tm.renderHTML(sess); err != nil {
		t.Fatalf("render html: %v", err)
	}
	if _, err := os.Stat(outPath); err != nil {
		t.Fatalf("expected html output: %v", err)
	}
}

func TestResolveSessionIDFallbacks(t *testing.T) {
	tm := NewTraceMiddleware(t.TempDir())
	st := &State{Values: map[string]any{"sessionID": "sid"}}
	if id := tm.resolveSessionID(context.Background(), st); id != "sid" {
		t.Fatalf("expected sessionID value, got %q", id)
	}
	if id := tm.resolveSessionID(nil, nil); !strings.HasPrefix(id, "session-") {
		t.Fatalf("expected generated session id, got %q", id)
	}
}

func TestTraceDurationAndUsageHelpers(t *testing.T) {
	tm := &TraceMiddleware{clock: time.Now}
	st := &State{Values: map[string]any{}}
	now := time.Now()

	tm.trackDuration(StageBeforeAgent, st, now)
	if st.Values[traceAgentStartKey] == nil {
		t.Fatalf("expected agent start key set")
	}
	if got := tm.trackDuration(StageAfterAgent, st, now.Add(10*time.Millisecond)); got == 0 {
		t.Fatalf("expected duration recorded")
	}
	if got := tm.trackDuration(StageAfterAgent, st, now.Add(20*time.Millisecond)); got != 0 {
		t.Fatalf("expected start key cleared")
	}

	if durationSince(nil, "k", now) != 0 {
		t.Fatalf("expected zero duration for nil map")
	}

	if got := usageTotal(map[string]any{"usage": model.Usage{TotalTokens: 5}}); got != 5 {
		t.Fatalf("unexpected usage total %d", got)
	}
	if got := usageTotal(map[string]any{"usage": map[string]any{"total_tokens": json.Number("7")}}); got != 7 {
		t.Fatalf("unexpected usage total %d", got)
	}
	if got := usageTotal(map[string]any{"usage": "9"}); got != 9 {
		t.Fatalf("unexpected usage total %d", got)
	}

	if toInt(json.Number("bad")) != 0 {
		t.Fatalf("expected bad json number to return 0")
	}
	if toInt(" 12 ") != 12 {
		t.Fatalf("expected string int parse")
	}
}

func TestTraceStringHelpersAndSkillSnapshot(t *testing.T) {
	if got := stringList([]any{"a", "A", " ", 2}); len(got) != 1 || got[0] != "a" {
		t.Fatalf("unexpected string list %v", got)
	}
	if got := dedupeStrings([]string{"A", "a", "b"}); len(got) != 2 {
		t.Fatalf("unexpected dedupe %v", got)
	}
	if got := orderedSkillNames([]string{"alpha"}, map[string]int{"beta": 1}, map[string]int{}); len(got) != 2 {
		t.Fatalf("unexpected ordered names %v", got)
	}

	reg := skills.NewRegistry()
	handler := sizedHandler{size: 5}
	if err := reg.Register(skills.Definition{Name: "demo"}, handler); err != nil {
		t.Fatalf("register skill: %v", err)
	}

	st := &State{Values: map[string]any{
		forceSkillsValue:    []string{"demo"},
		skillsRegistryValue: reg,
	}}
	tm := NewTraceMiddleware(t.TempDir(), WithSkillTracing(true))
	tm.traceSkillsSnapshot(context.Background(), st, true)
	if st.Values[traceSkillNamesKey] == nil || st.Values[traceSkillBeforeKey] == nil {
		t.Fatalf("expected snapshot values set")
	}
	tm.traceSkillsSnapshot(context.Background(), st, false)
}

func TestWriteAtomicAndJSONLineErrors(t *testing.T) {
	dir := t.TempDir()
	filePath := filepath.Join(dir, "file")
	if err := os.WriteFile(filePath, []byte("x"), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if err := writeAtomic(filepath.Join(filePath, "out.html"), []byte("x")); err == nil {
		t.Fatalf("expected writeAtomic error for file dir")
	}

	jsonPath := filepath.Join(dir, "trace.jsonl")
	f, err := os.OpenFile(jsonPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	defer f.Close()
	evt := TraceEvent{Input: func() {}}
	if err := writeJSONLine(f, evt); err == nil {
		t.Fatalf("expected json marshal error")
	}
}

type sizedHandler struct {
	size int
}

func (s sizedHandler) Execute(context.Context, skills.ActivationContext) (skills.Result, error) {
	return skills.Result{}, nil
}

func (s sizedHandler) BodyLength() (int, bool) {
	return s.size, true
}
