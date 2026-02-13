package middleware

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"html/template"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/runtime/skills"
)

// TraceMiddleware records middleware activity per session and renders a
// lightweight HTML viewer alongside JSONL logs.
type TraceMiddleware struct {
	outputDir   string
	sessions    map[string]*traceSession
	tmpl        *template.Template
	mu          sync.Mutex
	clock       func() time.Time
	traceSkills bool
}

type traceSession struct {
	id        string
	createdAt time.Time
	updatedAt time.Time
	timestamp string
	jsonPath  string
	htmlPath  string
	jsonFile  *os.File
	events    []TraceEvent
	mu        sync.Mutex
}

// TraceContextKey identifies values stored in a context for trace middleware consumers.
type TraceContextKey string

const (
	// TraceSessionIDContextKey stores the trace-specific session identifier.
	TraceSessionIDContextKey TraceContextKey = "trace.session_id"
	// SessionIDContextKey stores the generic session identifier fallback.
	SessionIDContextKey TraceContextKey = "session_id"

	traceSkillBeforeKey = "trace.skills.before"
	traceSkillNamesKey  = "trace.skills.names"
	skillsRegistryValue = "skills.registry"
	forceSkillsValue    = "request.force_skills"
)

// TraceOption customizes optional TraceMiddleware behavior.
type TraceOption func(*TraceMiddleware)

// WithSkillTracing enables ForceSkills body-size logging.
func WithSkillTracing(enabled bool) TraceOption {
	return func(tm *TraceMiddleware) {
		tm.traceSkills = enabled
	}
}

// NewTraceMiddleware builds a TraceMiddleware that writes to outputDir
// (defaults to .trace when empty).
func NewTraceMiddleware(outputDir string, opts ...TraceOption) *TraceMiddleware {
	dir := strings.TrimSpace(outputDir)
	if dir == "" {
		dir = ".trace"
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		log.Printf("trace middleware: mkdir %s: %v", dir, err)
	}

	tmpl, err := template.New("trace-viewer").Parse(traceHTMLTemplate)
	if err != nil {
		log.Printf("trace middleware: template parse: %v", err)
	}

	mw := &TraceMiddleware{
		outputDir: dir,
		sessions:  map[string]*traceSession{},
		tmpl:      tmpl,
		clock:     time.Now,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(mw)
		}
	}
	return mw
}

func (m *TraceMiddleware) Name() string { return "trace" }

func (m *TraceMiddleware) BeforeAgent(ctx context.Context, st *State) error {
	m.traceSkillsSnapshot(ctx, st, true)
	m.record(ctx, StageBeforeAgent, st)
	return nil
}

func (m *TraceMiddleware) BeforeModel(ctx context.Context, st *State) error {
	m.record(ctx, StageBeforeModel, st)
	return nil
}

func (m *TraceMiddleware) AfterModel(ctx context.Context, st *State) error {
	m.record(ctx, StageAfterModel, st)
	return nil
}

func (m *TraceMiddleware) BeforeTool(ctx context.Context, st *State) error {
	m.record(ctx, StageBeforeTool, st)
	return nil
}

func (m *TraceMiddleware) AfterTool(ctx context.Context, st *State) error {
	m.record(ctx, StageAfterTool, st)
	return nil
}

func (m *TraceMiddleware) AfterAgent(ctx context.Context, st *State) error {
	m.traceSkillsSnapshot(ctx, st, false)
	m.record(ctx, StageAfterAgent, st)
	return nil
}

func (m *TraceMiddleware) record(ctx context.Context, stage Stage, st *State) {
	if m == nil || st == nil {
		return
	}
	ensureStateValues(st)
	sessionID := m.resolveSessionID(ctx, st)
	now := m.now()
	evt := TraceEvent{
		Timestamp: now,
		Stage:     stageName(stage),
		Iteration: st.Iteration,
		SessionID: sessionID,
	}
	evt.Input, evt.Output = stageIO(stage, st)
	evt.Input = sanitizePayload(evt.Input)
	evt.Output = sanitizePayload(evt.Output)
	evt.ModelRequest = captureModelRequest(stage, st)
	evt.ModelResponse = captureModelResponse(stage, st)
	evt.ToolCall = captureToolCall(stage, st)
	evt.ToolResult = captureToolResult(stage, st, evt.ToolCall)
	evt.Error = captureTraceError(stage, st, evt.ToolResult)
	evt.DurationMS = m.trackDuration(stage, st, now)

	sess := m.sessionFor(sessionID)
	if sess == nil {
		return
	}
	sess.append(evt, m)
}

func (m *TraceMiddleware) sessionFor(id string) *traceSession {
	if id == "" {
		id = "session"
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	if sess, ok := m.sessions[id]; ok {
		return sess
	}

	sess, err := m.newSessionLocked(id)
	if err != nil {
		m.logf("create session %s: %v", id, err)
		return nil
	}
	m.sessions[id] = sess
	return sess
}

func (m *TraceMiddleware) newSessionLocked(id string) (*traceSession, error) {
	if err := os.MkdirAll(m.outputDir, 0o755); err != nil {
		return nil, err
	}
	timestamp := m.now().UTC().Format(time.RFC3339)
	safeID := sanitizeSessionComponent(id)
	base := fmt.Sprintf("log-%s", safeID)
	jsonPath := filepath.Join(m.outputDir, base+".jsonl")
	htmlPath := filepath.Join(m.outputDir, base+".html")
	file, err := os.OpenFile(jsonPath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, err
	}
	now := m.now()
	return &traceSession{
		id:        id,
		timestamp: timestamp,
		jsonPath:  jsonPath,
		htmlPath:  htmlPath,
		jsonFile:  file,
		createdAt: now,
		updatedAt: now,
		events:    []TraceEvent{},
	}, nil
}

func sanitizeSessionComponent(id string) string {
	const fallback = "session"
	if strings.TrimSpace(id) == "" {
		return fallback
	}
	var b strings.Builder
	b.Grow(len(id))
	for _, r := range id {
		switch {
		case r >= 'a' && r <= 'z',
			r >= 'A' && r <= 'Z',
			r >= '0' && r <= '9',
			r == '-', r == '_':
			b.WriteRune(r)
		default:
			b.WriteByte('-')
		}
	}
	sanitized := strings.Trim(b.String(), "-")
	if sanitized == "" {
		return fallback
	}
	return sanitized
}

func (sess *traceSession) append(evt TraceEvent, owner *TraceMiddleware) {
	if sess == nil || owner == nil {
		return
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()

	sess.events = append(sess.events, evt)
	if sess.jsonFile != nil {
		if err := writeJSONLine(sess.jsonFile, evt); err != nil {
			owner.logf("write jsonl %s: %v", sess.jsonPath, err)
		}
	} else {
		owner.logf("json file handle missing for %s", sess.id)
	}

	sess.updatedAt = owner.now()
	if err := owner.renderHTML(sess); err != nil {
		owner.logf("render html %s: %v", sess.htmlPath, err)
	}
}

func writeJSONLine(f *os.File, evt TraceEvent) error {
	if f == nil {
		return nil
	}
	line, err := json.Marshal(evt)
	if err != nil {
		return err
	}
	if _, err := f.Write(append(line, '\n')); err != nil {
		return err
	}
	return nil
}

func (m *TraceMiddleware) renderHTML(sess *traceSession) error {
	if sess == nil {
		return nil
	}
	data := traceTemplateData{
		SessionID:  sess.id,
		CreatedAt:  sess.createdAt.UTC().Format(time.RFC3339),
		UpdatedAt:  sess.updatedAt.UTC().Format(time.RFC3339),
		EventCount: len(sess.events),
		JSONLog:    filepath.Base(sess.jsonPath),
	}
	tokens, duration := aggregateStats(sess.events)
	data.TotalTokens = tokens
	data.TotalDuration = duration
	raw, err := json.Marshal(sess.events)
	if err != nil {
		sanitized := make([]TraceEvent, 0, len(sess.events))
		for _, evt := range sess.events {
			sanitized = append(sanitized, TraceEvent{
				Timestamp: evt.Timestamp,
				Stage:     evt.Stage,
				Iteration: evt.Iteration,
				SessionID: evt.SessionID,
			})
		}
		raw, err = json.Marshal(sanitized)
		if err != nil {
			raw = []byte("[]")
		}
	}
	// EventsJSON is generated by json.Marshal from our TraceEvent structs (or the sanitized fallback above),
	// so it never contains user input that could introduce executable content.
	// #nosec G203 -- Treating this trusted, server-generated JSON as template.JS is safe for the trace viewer.
	data.EventsJSON = template.JS(string(raw))

	var buf bytes.Buffer
	if m.tmpl != nil {
		if err := m.tmpl.Execute(&buf, data); err != nil {
			return err
		}
	} else {
		buf.WriteString("<html><body><pre>")
		template.HTMLEscape(&buf, raw)
		buf.WriteString("</pre></body></html>")
	}

	if err := writeAtomic(sess.htmlPath, buf.Bytes()); err != nil {
		return err
	}
	return nil
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, "trace-*.html")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (m *TraceMiddleware) resolveSessionID(ctx context.Context, st *State) string {
	if st != nil {
		if id := firstString(st.Values, "trace.session_id", "session_id", "sessionID", "session"); id != "" {
			return id
		}
	}
	if id := contextString(ctx, TraceSessionIDContextKey); id != "" {
		return id
	}
	if id := contextString(ctx, SessionIDContextKey); id != "" {
		return id
	}
	if id := contextString(ctx, "trace.session_id"); id != "" {
		return id
	}
	if id := contextString(ctx, "session_id"); id != "" {
		return id
	}
	if st != nil {
		return fmt.Sprintf("session-%p", st)
	}
	return fmt.Sprintf("session-%d", m.now().UnixNano())
}

func contextString(ctx context.Context, key any) string {
	if ctx == nil || key == nil {
		return ""
	}
	return anyToString(ctx.Value(key))
}

func firstString(values map[string]any, keys ...string) string {
	if len(keys) == 0 || len(values) == 0 {
		return ""
	}
	for _, key := range keys {
		if val, ok := values[key]; ok {
			if s := anyToString(val); s != "" {
				return s
			}
		}
	}
	return ""
}

func anyToString(v any) string {
	switch val := v.(type) {
	case string:
		return strings.TrimSpace(val)
	case fmt.Stringer:
		return strings.TrimSpace(val.String())
	case []byte:
		return strings.TrimSpace(string(val))
	}
	return ""
}

func stageIO(stage Stage, st *State) (any, any) {
	if st == nil {
		return nil, nil
	}
	switch stage {
	case StageBeforeAgent:
		return st.Agent, nil
	case StageBeforeModel:
		if st.ModelInput != nil {
			return st.ModelInput, nil
		}
		return st.Agent, nil
	case StageAfterModel:
		return st.ModelInput, st.ModelOutput
	case StageBeforeTool:
		return st.ToolCall, nil
	case StageAfterTool:
		return st.ToolCall, st.ToolResult
	case StageAfterAgent:
		return st.Agent, st.ModelOutput
	default:
		return nil, nil
	}
}

func stageName(stage Stage) string {
	switch stage {
	case StageBeforeAgent:
		return "before_agent"
	case StageBeforeModel:
		return "before_model"
	case StageAfterModel:
		return "after_model"
	case StageBeforeTool:
		return "before_tool"
	case StageAfterTool:
		return "after_tool"
	case StageAfterAgent:
		return "after_agent"
	default:
		return fmt.Sprintf("stage_%d", stage)
	}
}

func (m *TraceMiddleware) now() time.Time {
	if m == nil || m.clock == nil {
		return time.Now()
	}
	return m.clock()
}

func (m *TraceMiddleware) logf(format string, args ...any) {
	log.Printf("trace middleware: "+format, args...)
}

func (m *TraceMiddleware) traceSkillsSnapshot(ctx context.Context, st *State, before bool) {
	if m == nil || !m.traceSkills || st == nil {
		return
	}
	ensureStateValues(st)
	names := forceSkillsFromState(st.Values)
	if len(names) == 0 {
		return
	}
	registry := registryFromState(st.Values)
	if registry == nil {
		return
	}
	snapshot := skillBodies(registry, names)
	if before {
		st.Values[traceSkillNamesKey] = names
		st.Values[traceSkillBeforeKey] = snapshot
		return
	}

	beforeSnapshot, ok := st.Values[traceSkillBeforeKey].(map[string]int)
	if !ok || len(beforeSnapshot) == 0 {
		return
	}

	ordered := orderedSkillNames(names, beforeSnapshot, snapshot)
	for _, name := range ordered {
		m.logf("skill=%s body_before=%d body_after=%d", name, beforeSnapshot[name], snapshot[name])
	}
}

func forceSkillsFromState(values map[string]any) []string {
	if len(values) == 0 {
		return nil
	}
	if names := stringList(values[forceSkillsValue]); len(names) > 0 {
		return names
	}
	return stringList(values[traceSkillNamesKey])
}

func registryFromState(values map[string]any) *skills.Registry {
	if len(values) == 0 {
		return nil
	}
	if reg, ok := values[skillsRegistryValue].(*skills.Registry); ok {
		return reg
	}
	return nil
}

func skillBodies(reg *skills.Registry, names []string) map[string]int {
	if reg == nil || len(names) == 0 {
		return nil
	}
	out := make(map[string]int, len(names))
	seen := map[string]struct{}{}
	for _, name := range names {
		key := strings.ToLower(strings.TrimSpace(name))
		if key == "" {
			continue
		}
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}

		skill, ok := reg.Get(key)
		if !ok {
			continue
		}
		out[skill.Definition().Name] = skillBodySize(skill.Handler())
	}
	return out
}

type bodySizer interface {
	BodyLength() (int, bool)
}

func skillBodySize(handler skills.Handler) int {
	if handler == nil {
		return 0
	}
	if sizer, ok := handler.(bodySizer); ok && sizer != nil {
		if size, loaded := sizer.BodyLength(); loaded {
			return size
		}
		return 0
	}
	return 0
}

func stringList(value any) []string {
	switch v := value.(type) {
	case []string:
		return dedupeStrings(v)
	case []any:
		out := make([]string, 0, len(v))
		for _, val := range v {
			if s := anyToString(val); s != "" {
				out = append(out, s)
			}
		}
		return dedupeStrings(out)
	default:
		if s := anyToString(v); s != "" {
			return []string{s}
		}
	}
	return nil
}

func dedupeStrings(values []string) []string {
	if len(values) == 0 {
		return nil
	}
	seen := map[string]struct{}{}
	result := make([]string, 0, len(values))
	for _, value := range values {
		val := strings.ToLower(strings.TrimSpace(value))
		if val == "" {
			continue
		}
		if _, ok := seen[val]; ok {
			continue
		}
		seen[val] = struct{}{}
		result = append(result, val)
	}
	return result
}

func orderedSkillNames(names []string, before, after map[string]int) []string {
	seen := map[string]struct{}{}
	order := make([]string, 0, len(names))
	for _, name := range names {
		norm := strings.TrimSpace(name)
		if norm == "" {
			continue
		}
		if _, ok := seen[norm]; ok {
			continue
		}
		seen[norm] = struct{}{}
		order = append(order, norm)
	}

	var extras []string
	for key := range before {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extras = append(extras, key)
	}
	for key := range after {
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		extras = append(extras, key)
	}
	if len(extras) > 0 {
		sort.Strings(extras)
		order = append(order, extras...)
	}
	return order
}
