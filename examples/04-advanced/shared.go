package main

import (
	"crypto/rand"
	"encoding/hex"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/model"
)

const (
	requestIDKey     = "request_id"
	startedAtKey     = "started_at"
	promptKey        = "prompt"
	securityFlagsKey = "security.flags"
)

func genRequestID() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err == nil {
		return "req-" + hex.EncodeToString(buf)
	}
	return "req-unknown"
}

func clampPreview(s string, max int) string {
	if max <= 0 {
		return ""
	}
	s = strings.TrimSpace(s)
	if len(s) <= max {
		return s
	}
	return s[:max] + "…"
}

func readString(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	if val, ok := m[key]; ok {
		if s, ok := val.(string); ok {
			return s
		}
	}
	return ""
}

func nowOr(stored any, fallback time.Time) time.Time {
	if t, ok := stored.(time.Time); ok {
		return t
	}
	return fallback
}

func lastUserPrompt(msgs []model.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if strings.EqualFold(msgs[i].Role, "user") {
			return strings.TrimSpace(msgs[i].Content)
		}
	}
	return ""
}

func lastToolResult(msgs []model.Message, names ...string) string {
	if len(names) == 0 {
		return ""
	}
	lower := make(map[string]struct{}, len(names))
	for _, name := range names {
		if name == "" {
			continue
		}
		lower[strings.ToLower(name)] = struct{}{}
	}
	for i := len(msgs) - 1; i >= 0; i-- {
		msg := msgs[i]
		if !strings.EqualFold(msg.Role, "tool") || strings.TrimSpace(msg.Content) == "" {
			continue
		}
		for _, call := range msg.ToolCalls {
			if _, ok := lower[strings.ToLower(call.Name)]; ok {
				return strings.TrimSpace(msg.Content)
			}
		}
	}
	return ""
}

func readStringParam(params map[string]any, key string) string {
	if params == nil {
		return ""
	}
	if v, ok := params[key]; ok {
		if s, ok := v.(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}
