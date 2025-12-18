package events

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// Handler processes an incoming event.
type Handler func(context.Context, Event)

// Bus routes events to subscribers while preserving order and isolating
// subscriber failures. A single dispatch loop keeps ordering deterministic,
// while per-subscriber queues prevent one slow consumer from blocking others.
type Bus struct {
	queue      chan Event
	subsMu     sync.RWMutex
	subs       map[EventType]map[int64]*subscription
	closed     atomic.Bool
	nextID     atomic.Int64
	deduper    *deduper
	baseCtx    context.Context
	cancel     context.CancelFunc
	wg         sync.WaitGroup
	bufSize    int
	queueDepth int
}

// BusOption configures a Bus.
type BusOption func(*Bus)

const (
	defaultQueueDepth    = 64
	defaultSubscriberBuf = 16
	defaultDedupLimit    = 256
)

// WithBufferSize adjusts the per-subscriber fan-out buffer. Minimum 1.
func WithBufferSize(size int) BusOption {
	return func(b *Bus) {
		if size < 1 {
			size = 1
		}
		b.bufSize = size
	}
}

// WithQueueDepth customizes the central publish queue capacity.
func WithQueueDepth(depth int) BusOption {
	return func(b *Bus) {
		if depth < 1 {
			depth = 1
		}
		b.queueDepth = depth
		b.queue = make(chan Event, depth)
	}
}

// WithDedupWindow enables event de-duplication using a bounded LRU window.
func WithDedupWindow(limit int) BusOption {
	return func(b *Bus) {
		if limit < 1 {
			limit = 1
		}
		b.deduper = newDeduper(limit)
	}
}

// NewBus constructs an event bus and starts its dispatch loop immediately.
func NewBus(opts ...BusOption) *Bus {
	ctx, cancel := context.WithCancel(context.Background())
	b := &Bus{
		queue:      make(chan Event, defaultQueueDepth),
		subs:       make(map[EventType]map[int64]*subscription),
		deduper:    newDeduper(defaultDedupLimit),
		baseCtx:    ctx,
		cancel:     cancel,
		bufSize:    defaultSubscriberBuf,
		queueDepth: defaultQueueDepth,
	}
	for _, opt := range opts {
		opt(b)
	}
	if b.queue == nil {
		b.queue = make(chan Event, b.queueDepth)
	}
	b.wg.Add(1)
	go b.dispatchLoop()
	return b
}

// Close stops the dispatch loop and waits for all subscriptions to drain.
func (b *Bus) Close() {
	if b == nil {
		return
	}
	if b.closed.Swap(true) {
		return
	}
	b.cancel()
	b.subsMu.RLock()
	for _, bucket := range b.subs {
		for _, sub := range bucket {
			sub.stop()
		}
	}
	b.subsMu.RUnlock()
	b.wg.Wait()
}

// Publish enqueues an event for delivery. Ordering is preserved by the
// dispatch loop. De-duplication is applied if configured.
func (b *Bus) Publish(evt Event) error {
	if b == nil {
		return errors.New("events: nil bus")
	}
	if b.closed.Load() {
		return errors.New("events: bus closed")
	}
	if err := evt.Validate(); err != nil {
		return err
	}
	if evt.ID == "" {
		evt.ID = fmt.Sprintf("evt-%d", b.nextID.Add(1))
	}
	if evt.Timestamp.IsZero() {
		evt.Timestamp = time.Now().UTC()
	}
	if b.deduper != nil && !b.deduper.Allow(evt.ID) {
		return nil
	}
	select {
	case <-b.baseCtx.Done():
		return errors.New("events: bus closed")
	case b.queue <- evt:
		return nil
	}
}

// Subscribe registers a handler for a specific event type. It returns an
// unsubscribe function that is safe to call multiple times.
func (b *Bus) Subscribe(t EventType, handler Handler, opts ...SubscriptionOption) func() {
	if b == nil || b.closed.Load() {
		return func() {}
	}
	cfg := subscriptionConfig{
		timeout: 0,
	}
	for _, opt := range opts {
		opt(&cfg)
	}
	sub := newSubscription(handler, cfg, b.bufSize)

	b.subsMu.Lock()
	defer b.subsMu.Unlock()
	if b.subs[t] == nil {
		b.subs[t] = make(map[int64]*subscription)
	}
	id := b.nextID.Add(1)
	sub.id = id
	b.subs[t][id] = sub
	return func() {
		b.removeSubscription(t, id)
	}
}

// removeSubscription removes and stops a subscription. Caller must not hold
// subsMu when calling stop to avoid deadlocks in defer chains.
func (b *Bus) removeSubscription(t EventType, id int64) {
	b.subsMu.Lock()
	sub, ok := b.subs[t][id]
	if ok {
		delete(b.subs[t], id)
		if len(b.subs[t]) == 0 {
			delete(b.subs, t)
		}
	}
	b.subsMu.Unlock()
	if ok {
		sub.stop()
	}
}

// SubscriptionOption configures per-subscription behaviour.
type SubscriptionOption func(*subscriptionConfig)

// WithSubscriptionTimeout enforces a per-event timeout for the handler.
func WithSubscriptionTimeout(d time.Duration) SubscriptionOption {
	return func(cfg *subscriptionConfig) {
		cfg.timeout = d
	}
}

type subscriptionConfig struct {
	timeout time.Duration
}

type subscription struct {
	id      int64
	handler Handler
	queue   chan Event
	config  subscriptionConfig
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	closed  atomic.Bool
}

func newSubscription(h Handler, cfg subscriptionConfig, bufSize int) *subscription {
	ctx, cancel := context.WithCancel(context.Background())
	if bufSize < 1 {
		bufSize = 1
	}
	s := &subscription{
		handler: h,
		queue:   make(chan Event, bufSize),
		config:  cfg,
		ctx:     ctx,
		cancel:  cancel,
	}
	s.wg.Add(1)
	go s.loop()
	return s
}

func (s *subscription) loop() {
	defer s.wg.Done()
	for {
		select {
		case <-s.ctx.Done():
			return
		case evt := <-s.queue:
			s.invoke(evt)
		}
	}
}

func (s *subscription) invoke(evt Event) {
	ctx := s.ctx
	cancel := func() {}
	if s.config.timeout > 0 {
		ctx, cancel = context.WithTimeout(ctx, s.config.timeout)
	}
	defer cancel()
	done := make(chan struct{})
	go func() {
		defer close(done)
		defer func() {
			if r := recover(); r != nil {
				_ = r // swallow panics to ensure isolation
			}
		}()
		s.handler(ctx, evt)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		// timeout or context cancellation: we stop waiting to unblock dispatch
	}
}

func (s *subscription) stop() {
	if s.closed.Swap(true) {
		return
	}
	s.cancel()
	s.wg.Wait()
}

func (s *subscription) enqueue(evt Event) {
	if s.closed.Load() {
		return
	}
	select {
	case <-s.ctx.Done():
		return
	case s.queue <- evt:
	}
}

func (b *Bus) dispatchLoop() {
	defer b.wg.Done()
	for {
		select {
		case <-b.baseCtx.Done():
			return
		case evt, ok := <-b.queue:
			if !ok {
				return
			}
			b.dispatch(evt)
		}
	}
}

func (b *Bus) dispatch(evt Event) {
	b.subsMu.RLock()
	subs := b.subs[evt.Type]
	// create a copy of pointers to avoid holding lock during enqueue (which can block)
	copies := make([]*subscription, 0, len(subs))
	for _, sub := range subs {
		copies = append(copies, sub)
	}
	b.subsMu.RUnlock()
	for _, sub := range copies {
		sub.enqueue(evt)
	}
}

type deduper struct {
	limit int
	order []string
	set   map[string]struct{}
	mu    sync.Mutex
}

func newDeduper(limit int) *deduper {
	return &deduper{
		limit: limit,
		order: make([]string, 0, limit),
		set:   make(map[string]struct{}, limit),
	}
}

// Allow returns true if the id has not been seen recently and records it;
// otherwise false.
func (d *deduper) Allow(id string) bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	if _, ok := d.set[id]; ok {
		return false
	}
	d.set[id] = struct{}{}
	d.order = append(d.order, id)
	if len(d.order) > d.limit {
		evict := d.order[0]
		d.order = d.order[1:]
		delete(d.set, evict)
	}
	return true
}
