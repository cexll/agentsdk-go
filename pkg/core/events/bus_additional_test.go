package events

import (
	"context"
	"testing"
	"time"
)

func TestBusOptionsClampValuesAndRecoverNilQueue(t *testing.T) {
	t.Parallel()

	bus := NewBus(
		WithBufferSize(0),
		WithQueueDepth(0),
		WithDedupWindow(0),
		func(b *Bus) { b.queue = nil },
	)
	defer bus.Close()

	if bus.bufSize != 1 {
		t.Fatalf("bufSize=%d want 1", bus.bufSize)
	}
	if bus.queueDepth != 1 {
		t.Fatalf("queueDepth=%d want 1", bus.queueDepth)
	}
	if bus.queue == nil || cap(bus.queue) != 1 {
		t.Fatalf("queue not initialised as expected: %#v", bus.queue)
	}
	if bus.deduper == nil || bus.deduper.limit != 1 {
		t.Fatalf("deduper not initialised as expected: %#v", bus.deduper)
	}

	got := make(chan struct{}, 1)
	unsub := bus.Subscribe(Notification, func(_ context.Context, _ Event) {
		select {
		case got <- struct{}{}:
		default:
		}
	})
	defer unsub()

	if err := bus.Publish(Event{Type: Notification}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	select {
	case <-got:
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for notification")
	}
}

func TestBusNilReceiverCloseNoPanic(t *testing.T) {
	t.Parallel()

	var bus *Bus
	bus.Close()
}

func TestBusNilReceiverPublishReturnsError(t *testing.T) {
	t.Parallel()

	var bus *Bus
	if err := bus.Publish(Event{Type: Notification}); err == nil {
		t.Fatalf("expected error for nil bus")
	}
}

func TestNewSubscriptionClampAndStopIdempotent(t *testing.T) {
	t.Parallel()

	sub := newSubscription(func(context.Context, Event) {}, subscriptionConfig{}, 0)
	if sub == nil {
		t.Fatalf("newSubscription returned nil")
	}
	if cap(sub.queue) != 1 {
		t.Fatalf("queue cap=%d want 1", cap(sub.queue))
	}
	sub.stop()
	sub.stop()
	sub.enqueue(Event{Type: Notification})
}
