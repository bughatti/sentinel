package events

import (
	"log/slog"
	"sync"
)

// EventBus is an in-process publish/subscribe hub for Event values.
// Subscribers receive events via buffered channels. If a subscriber's channel
// is full the event is dropped for that subscriber (non-blocking publish) and
// a warning is logged. This ensures a slow consumer never blocks producers.
type EventBus struct {
	mu          sync.RWMutex
	subscribers map[chan<- Event]struct{}
}

// NewEventBus creates an empty EventBus.
func NewEventBus() *EventBus {
	return &EventBus{
		subscribers: make(map[chan<- Event]struct{}),
	}
}

// Subscribe registers ch as a recipient of future events. ch must be a
// buffered channel; the caller owns the channel and is responsible for
// draining and closing it after calling Unsubscribe.
func (b *EventBus) Subscribe(ch chan<- Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.subscribers[ch] = struct{}{}
}

// Unsubscribe removes ch from the subscriber set. After this call returns, no
// further events will be sent to ch.
func (b *EventBus) Unsubscribe(ch chan<- Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	delete(b.subscribers, ch)
}

// Publish sends e to all current subscribers. Delivery is non-blocking: if a
// subscriber's channel buffer is full the event is silently dropped for that
// subscriber.
func (b *EventBus) Publish(e Event) {
	b.mu.RLock()
	defer b.mu.RUnlock()
	for ch := range b.subscribers {
		select {
		case ch <- e:
		default:
			slog.Warn("event bus: subscriber channel full, dropping event",
				"event_id", e.ID,
				"camera", e.Camera,
				"label", e.Label,
			)
		}
	}
}

// Len returns the current number of subscribers. Useful for metrics.
func (b *EventBus) Len() int {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return len(b.subscribers)
}
