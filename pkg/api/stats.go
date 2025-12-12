package api

import (
	"sync"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
)

// TokenStats captures token usage for a single model call.
type TokenStats struct {
	InputTokens   int64     `json:"input_tokens"`
	OutputTokens  int64     `json:"output_tokens"`
	TotalTokens   int64     `json:"total_tokens"`
	CacheCreation int64     `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     int64     `json:"cache_read_input_tokens,omitempty"`
	Model         string    `json:"model"`
	SessionID     string    `json:"session_id"`
	RequestID     string    `json:"request_id"`
	Timestamp     time.Time `json:"timestamp"`
}

// SessionTokenStats aggregates token usage across all requests in a session.
type SessionTokenStats struct {
	SessionID    string                 `json:"session_id"`
	TotalInput   int64                  `json:"total_input"`
	TotalOutput  int64                  `json:"total_output"`
	TotalTokens  int64                  `json:"total_tokens"`
	CacheCreated int64                  `json:"cache_created,omitempty"`
	CacheRead    int64                  `json:"cache_read,omitempty"`
	ByModel      map[string]*ModelStats `json:"by_model,omitempty"`
	RequestCount int                    `json:"request_count"`
	FirstRequest time.Time              `json:"first_request"`
	LastRequest  time.Time              `json:"last_request"`
}

// ModelStats aggregates token usage for a specific model.
type ModelStats struct {
	InputTokens   int64 `json:"input_tokens"`
	OutputTokens  int64 `json:"output_tokens"`
	TotalTokens   int64 `json:"total_tokens"`
	CacheCreation int64 `json:"cache_creation_input_tokens,omitempty"`
	CacheRead     int64 `json:"cache_read_input_tokens,omitempty"`
	RequestCount  int   `json:"request_count"`
}

// TokenCallback is invoked after each model call with the usage stats.
type TokenCallback func(stats TokenStats)

// tokenTracker maintains thread-safe token statistics across sessions.
type tokenTracker struct {
	mu       sync.RWMutex
	sessions map[string]*SessionTokenStats
	total    *SessionTokenStats
	callback TokenCallback
	enabled  bool
}

func newTokenTracker(enabled bool, callback TokenCallback) *tokenTracker {
	return &tokenTracker{
		sessions: make(map[string]*SessionTokenStats),
		total: &SessionTokenStats{
			SessionID: "_total",
			ByModel:   make(map[string]*ModelStats),
		},
		callback: callback,
		enabled:  enabled,
	}
}

// Record adds a token usage record. Thread-safe.
func (t *tokenTracker) Record(stats TokenStats) {
	if t == nil || !t.enabled {
		return
	}

	t.mu.Lock()
	defer t.mu.Unlock()

	// Update session stats
	session, ok := t.sessions[stats.SessionID]
	if !ok {
		session = &SessionTokenStats{
			SessionID:    stats.SessionID,
			ByModel:      make(map[string]*ModelStats),
			FirstRequest: stats.Timestamp,
		}
		t.sessions[stats.SessionID] = session
	}

	session.TotalInput += stats.InputTokens
	session.TotalOutput += stats.OutputTokens
	session.TotalTokens += stats.TotalTokens
	session.CacheCreated += stats.CacheCreation
	session.CacheRead += stats.CacheRead
	session.RequestCount++
	session.LastRequest = stats.Timestamp

	// Update per-model stats for session
	if stats.Model != "" {
		modelStats, ok := session.ByModel[stats.Model]
		if !ok {
			modelStats = &ModelStats{}
			session.ByModel[stats.Model] = modelStats
		}
		modelStats.InputTokens += stats.InputTokens
		modelStats.OutputTokens += stats.OutputTokens
		modelStats.TotalTokens += stats.TotalTokens
		modelStats.CacheCreation += stats.CacheCreation
		modelStats.CacheRead += stats.CacheRead
		modelStats.RequestCount++
	}

	// Update global total
	t.total.TotalInput += stats.InputTokens
	t.total.TotalOutput += stats.OutputTokens
	t.total.TotalTokens += stats.TotalTokens
	t.total.CacheCreated += stats.CacheCreation
	t.total.CacheRead += stats.CacheRead
	t.total.RequestCount++
	if t.total.FirstRequest.IsZero() {
		t.total.FirstRequest = stats.Timestamp
	}
	t.total.LastRequest = stats.Timestamp

	// Update per-model stats for total
	if stats.Model != "" {
		modelStats, ok := t.total.ByModel[stats.Model]
		if !ok {
			modelStats = &ModelStats{}
			t.total.ByModel[stats.Model] = modelStats
		}
		modelStats.InputTokens += stats.InputTokens
		modelStats.OutputTokens += stats.OutputTokens
		modelStats.TotalTokens += stats.TotalTokens
		modelStats.CacheCreation += stats.CacheCreation
		modelStats.CacheRead += stats.CacheRead
		modelStats.RequestCount++
	}

	// Invoke callback outside the lock would be better but we keep it simple
	if t.callback != nil {
		// Copy stats to avoid mutation
		statsCopy := stats
		go t.callback(statsCopy)
	}
}

// GetSessionStats returns stats for a specific session. Thread-safe.
func (t *tokenTracker) GetSessionStats(sessionID string) *SessionTokenStats {
	if t == nil {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	session, ok := t.sessions[sessionID]
	if !ok {
		return nil
	}

	// Return a copy
	return copySessionStats(session)
}

// GetTotalStats returns aggregated stats across all sessions. Thread-safe.
func (t *tokenTracker) GetTotalStats() *SessionTokenStats {
	if t == nil {
		return nil
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	return copySessionStats(t.total)
}

// IsEnabled returns whether tracking is active.
func (t *tokenTracker) IsEnabled() bool {
	if t == nil {
		return false
	}
	return t.enabled
}

func copySessionStats(s *SessionTokenStats) *SessionTokenStats {
	if s == nil {
		return nil
	}
	cp := &SessionTokenStats{
		SessionID:    s.SessionID,
		TotalInput:   s.TotalInput,
		TotalOutput:  s.TotalOutput,
		TotalTokens:  s.TotalTokens,
		CacheCreated: s.CacheCreated,
		CacheRead:    s.CacheRead,
		RequestCount: s.RequestCount,
		FirstRequest: s.FirstRequest,
		LastRequest:  s.LastRequest,
	}
	if len(s.ByModel) > 0 {
		cp.ByModel = make(map[string]*ModelStats, len(s.ByModel))
		for k, v := range s.ByModel {
			cp.ByModel[k] = &ModelStats{
				InputTokens:   v.InputTokens,
				OutputTokens:  v.OutputTokens,
				TotalTokens:   v.TotalTokens,
				CacheCreation: v.CacheCreation,
				CacheRead:     v.CacheRead,
				RequestCount:  v.RequestCount,
			}
		}
	}
	return cp
}

// tokenStatsFromUsage converts model.Usage to TokenStats.
func tokenStatsFromUsage(usage model.Usage, modelName, sessionID, requestID string) TokenStats {
	return TokenStats{
		InputTokens:   int64(usage.InputTokens),
		OutputTokens:  int64(usage.OutputTokens),
		TotalTokens:   int64(usage.TotalTokens),
		CacheCreation: int64(usage.CacheCreationTokens),
		CacheRead:     int64(usage.CacheReadTokens),
		Model:         modelName,
		SessionID:     sessionID,
		RequestID:     requestID,
		Timestamp:     time.Now().UTC(),
	}
}
