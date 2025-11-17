package agent

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/cexll/agentsdk-go/pkg/event"
)

const (
	defaultStreamBuffer    = 4
	defaultToolTimeout     = 5 * time.Minute
	defaultWatchdogTimeout = 5 * time.Minute
)

// Config stores the coarse grained runtime settings for an Agent instance.
type Config struct {
	Name            string           `json:"name" yaml:"name"`
	Description     string           `json:"description" yaml:"description"`
	DefaultContext  RunContext       `json:"default_context" yaml:"default_context"`
	StreamBuffer    int              `json:"stream_buffer" yaml:"stream_buffer"`
	EnableRecovery  bool             `json:"enable_recovery" yaml:"enable_recovery"`
	ToolTimeout     time.Duration    `json:"tool_timeout" yaml:"tool_timeout"`
	WatchdogTimeout time.Duration    `json:"watchdog_timeout" yaml:"watchdog_timeout"`
	EventStore      event.EventStore `json:"-" yaml:"-"`
}

// LoadConfig loads and validates configuration from disk.
func LoadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}
	return DecodeConfig(data)
}

// DecodeConfig parses a raw JSON payload into a Config instance.
func DecodeConfig(data []byte) (*Config, error) {
	if len(data) == 0 {
		return nil, errors.New("config payload is empty")
	}
	var cfg Config
	if err := json.Unmarshal(data, &cfg); err != nil {
		return nil, fmt.Errorf("decode config: %w", err)
	}
	cfg.DefaultContext = cfg.DefaultContext.Normalize()
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return &cfg, nil
}

// Validate enforces minimal structural guarantees.
func (c *Config) Validate() error {
	if c == nil {
		return errors.New("config is nil")
	}
	if strings.TrimSpace(c.Name) == "" {
		return errors.New("config name is required")
	}
	if c.StreamBuffer < 0 {
		return fmt.Errorf("stream_buffer cannot be negative: %d", c.StreamBuffer)
	}
	if c.ToolTimeout < 0 {
		return fmt.Errorf("tool_timeout cannot be negative: %s", c.ToolTimeout)
	}
	if c.WatchdogTimeout < 0 {
		return fmt.Errorf("watchdog_timeout cannot be negative: %s", c.WatchdogTimeout)
	}
	c.DefaultContext = c.DefaultContext.Normalize()
	return nil
}

// ResolveContext merges the configuration defaults with a caller override.
func (c Config) ResolveContext(override RunContext) RunContext {
	return c.DefaultContext.Merge(override)
}

func (c Config) streamBuffer() int {
	if c.StreamBuffer <= 0 {
		return defaultStreamBuffer
	}
	return c.StreamBuffer
}

func (c Config) toolTimeout() time.Duration {
	if c.ToolTimeout <= 0 {
		return defaultToolTimeout
	}
	return c.ToolTimeout
}

func (c Config) watchdogTimeout() time.Duration {
	if !c.EnableRecovery {
		return 0
	}
	if c.WatchdogTimeout <= 0 {
		return defaultWatchdogTimeout
	}
	if c.WatchdogTimeout < time.Second {
		return time.Second
	}
	return c.WatchdogTimeout
}
