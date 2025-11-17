package agent

import (
	"context"
	"sync"
	"time"
)

const defaultWatchdogTick = 30 * time.Second

// Watchdog monitors agent progress and triggers recovery when heartbeats stop.
type Watchdog struct {
	timeout       time.Duration
	interval      time.Duration
	onTimeout     func()
	lastHeartbeat time.Time
	mu            sync.RWMutex
	cancel        context.CancelFunc
}

// NewWatchdog constructs a watchdog with the provided timeout.
func NewWatchdog(timeout time.Duration, onTimeout func()) *Watchdog {
	if timeout <= 0 {
		return nil
	}
	interval := defaultWatchdogTick
	if timeout/2 < interval {
		interval = timeout / 2
		if interval <= 0 {
			interval = timeout
		}
	}
	return &Watchdog{
		timeout:       timeout,
		interval:      interval,
		onTimeout:     onTimeout,
		lastHeartbeat: time.Now(),
	}
}

// Start begins monitoring until the provided context is canceled.
func (w *Watchdog) Start(ctx context.Context) {
	if w == nil {
		return
	}
	if ctx == nil {
		ctx = context.Background()
	}
	w.mu.Lock()
	w.lastHeartbeat = time.Now()
	monCtx, cancel := context.WithCancel(ctx)
	w.cancel = cancel
	interval := w.interval
	if interval <= 0 {
		interval = defaultWatchdogTick
	}
	w.mu.Unlock()

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				w.check()
			case <-monCtx.Done():
				return
			}
		}
	}()
}

// Stop halts monitoring.
func (w *Watchdog) Stop() {
	if w == nil {
		return
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.cancel != nil {
		w.cancel()
		w.cancel = nil
	}
}

// Heartbeat records forward progress.
func (w *Watchdog) Heartbeat() {
	if w == nil {
		return
	}
	w.mu.Lock()
	w.lastHeartbeat = time.Now()
	w.mu.Unlock()
}

func (w *Watchdog) check() {
	w.mu.RLock()
	elapsed := time.Since(w.lastHeartbeat)
	timeout := w.timeout
	onTimeout := w.onTimeout
	w.mu.RUnlock()
	if elapsed < timeout || timeout <= 0 {
		return
	}
	if onTimeout != nil {
		onTimeout()
	}
	w.Heartbeat()
}
