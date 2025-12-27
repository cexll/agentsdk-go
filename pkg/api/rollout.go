package api

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type RolloutWriter struct {
	dir string
}

type CompactEvent struct {
	SessionID             string    `json:"session_id"`
	Timestamp             time.Time `json:"timestamp"`
	Summary               string    `json:"summary"`
	OriginalMessages      int       `json:"original_messages"`
	PreservedMessages     int       `json:"preserved_messages"`
	EstimatedTokensBefore int       `json:"estimated_tokens_before"`
	EstimatedTokensAfter  int       `json:"estimated_tokens_after"`
}

func newRolloutWriter(projectRoot, dir string) *RolloutWriter {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return nil
	}
	if !filepath.IsAbs(dir) && strings.TrimSpace(projectRoot) != "" {
		dir = filepath.Join(projectRoot, dir)
	}
	return &RolloutWriter{dir: filepath.Clean(dir)}
}

func (w *RolloutWriter) WriteCompactEvent(sessionID string, res compactResult) error {
	if w == nil {
		return nil
	}
	dir := strings.TrimSpace(w.dir)
	if dir == "" {
		return nil
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("api: create rollout dir: %w", err)
	}

	ts := time.Now().UTC()
	event := CompactEvent{
		SessionID:             sessionID,
		Timestamp:             ts,
		Summary:               res.summary,
		OriginalMessages:      res.originalMsgs,
		PreservedMessages:     res.preservedMsgs,
		EstimatedTokensBefore: res.tokensBefore,
		EstimatedTokensAfter:  res.tokensAfter,
	}
	data, err := json.MarshalIndent(event, "", "  ")
	if err != nil {
		return fmt.Errorf("api: marshal compact event: %w", err)
	}
	data = append(data, '\n')

	filename := fmt.Sprintf("%s_%s_compact.json", safeRolloutName(sessionID), ts.Format("20060102T150405.000000000Z"))
	path := filepath.Join(dir, filename)
	if err := atomicWriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("api: write compact event: %w", err)
	}
	return nil
}

func safeRolloutName(name string) string {
	name = strings.TrimSpace(name)
	if name == "" {
		return "session"
	}
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range name {
		if r >= 'a' && r <= 'z' {
			b.WriteRune(r)
			continue
		}
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r)
			continue
		}
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
			continue
		}
		switch r {
		case '-', '_', '.':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	out := b.String()
	if out == "" {
		return "session"
	}
	return out
}

func atomicWriteFile(path string, data []byte, perm os.FileMode) error {
	dir := filepath.Dir(path)
	base := filepath.Base(path)
	tmp, err := os.CreateTemp(dir, base+".*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	writeErr := func() error {
		if err := tmp.Chmod(perm); err != nil {
			return err
		}
		if _, err := tmp.Write(data); err != nil {
			return err
		}
		if err := tmp.Close(); err != nil {
			return err
		}
		return os.Rename(tmpName, path)
	}()
	if writeErr != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		return writeErr
	}
	return nil
}
