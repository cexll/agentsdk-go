package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/message"
)

type diskHistoryPersister struct {
	dir string
}

type persistedHistory struct {
	Version   int               `json:"version"`
	SessionID string            `json:"session_id,omitempty"`
	UpdatedAt time.Time         `json:"updated_at,omitempty"`
	Messages  []message.Message `json:"messages,omitempty"`
}

func newDiskHistoryPersister(projectRoot string) *diskHistoryPersister {
	projectRoot = strings.TrimSpace(projectRoot)
	if projectRoot == "" {
		return nil
	}
	return &diskHistoryPersister{
		dir: filepath.Join(projectRoot, ".claude", "history"),
	}
}

func (p *diskHistoryPersister) Load(sessionID string) ([]message.Message, error) {
	path := p.filePath(sessionID)
	if path == "" {
		return nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("read history: %w", err)
	}
	var wrapper persistedHistory
	if err := json.Unmarshal(data, &wrapper); err == nil {
		if wrapper.Version != 0 || wrapper.SessionID != "" || !wrapper.UpdatedAt.IsZero() || wrapper.Messages != nil {
			return message.CloneMessages(wrapper.Messages), nil
		}
	}
	var msgs []message.Message
	if err := json.Unmarshal(data, &msgs); err != nil {
		return nil, fmt.Errorf("decode history: %w", err)
	}
	return message.CloneMessages(msgs), nil
}

func (p *diskHistoryPersister) Save(sessionID string, msgs []message.Message) error {
	path := p.filePath(sessionID)
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(p.dir, 0o700); err != nil {
		return fmt.Errorf("mkdir history dir: %w", err)
	}
	payload := persistedHistory{
		Version:   1,
		SessionID: sessionID,
		UpdatedAt: time.Now().UTC(),
		Messages:  message.CloneMessages(msgs),
	}
	data, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode history: %w", err)
	}

	tmp, err := os.CreateTemp(p.dir, sanitizePathComponent(sessionID)+".*.tmp")
	if err != nil {
		return fmt.Errorf("create temp history: %w", err)
	}
	tmpPath := tmp.Name()
	defer func() {
		_ = os.Remove(tmpPath)
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write history temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close history temp: %w", err)
	}

	if err := os.Rename(tmpPath, path); err != nil {
		// Windows can't rename over an existing file.
		_ = os.Remove(path)
		if retry := os.Rename(tmpPath, path); retry != nil {
			return fmt.Errorf("rename history: %w", retry)
		}
	}
	return nil
}

func (p *diskHistoryPersister) Cleanup(retainDays int) error {
	if p == nil {
		return nil
	}
	if retainDays <= 0 {
		return nil
	}
	dir := strings.TrimSpace(p.dir)
	if dir == "" {
		return nil
	}

	info, err := os.Stat(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read history dir: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("read history dir: %w", os.ErrInvalid)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil
		}
		return fmt.Errorf("read history dir: %w", err)
	}
	cutoff := time.Now().AddDate(0, 0, -retainDays)
	var errs []error
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasSuffix(strings.ToLower(name), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			errs = append(errs, err)
			continue
		}
		if info.ModTime().Before(cutoff) {
			if err := os.Remove(filepath.Join(dir, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
				errs = append(errs, err)
			}
		}
	}
	return errors.Join(errs...)
}

func (p *diskHistoryPersister) filePath(sessionID string) string {
	if p == nil {
		return ""
	}
	dir := strings.TrimSpace(p.dir)
	if dir == "" {
		return ""
	}
	name := sanitizePathComponent(sessionID)
	if name == "" {
		return ""
	}
	return filepath.Join(dir, name+".json")
}

func (rt *Runtime) persistHistory(sessionID string, history *message.History) {
	if rt == nil || rt.historyPersister == nil || history == nil {
		return
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return
	}
	snapshot := history.All()
	if len(snapshot) == 0 {
		return
	}
	if err := rt.historyPersister.Save(sessionID, snapshot); err != nil {
		log.Printf("api: persist history %q: %v", sessionID, err)
	}
}
