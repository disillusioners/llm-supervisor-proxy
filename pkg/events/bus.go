package events

import (
	"sync"
)

type Event struct {
	Type      string      `json:"type"`
	Timestamp int64       `json:"timestamp"`
	Data      interface{} `json:"data"`
}

// FallbackEvent represents a fallback model transition
type FallbackEvent struct {
	FromModel string `json:"from_model"`
	ToModel   string `json:"to_model"`
	Reason    string `json:"reason"` // "max_retries" | "deadline_exceeded" | "upstream_error"
}

type Bus struct {
	mu          sync.RWMutex
	subscribers []chan Event
	history     []Event // Keep a small buffer of recent events
}

func NewBus() *Bus {
	return &Bus{
		subscribers: make([]chan Event, 0),
		history:     make([]Event, 0, 100),
	}
}

func (b *Bus) Subscribe() chan Event {
	b.mu.Lock()
	defer b.mu.Unlock()
	ch := make(chan Event, 100)

	// Replay history
	for _, evt := range b.history {
		ch <- evt
	}

	b.subscribers = append(b.subscribers, ch)
	return ch
}

func (b *Bus) Unsubscribe(ch chan Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	for i, sub := range b.subscribers {
		if sub == ch {
			// Close and remove
			close(sub)
			b.subscribers = append(b.subscribers[:i], b.subscribers[i+1:]...)
			return
		}
	}
}

func (b *Bus) Publish(evt Event) {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Add to history
	if len(b.history) >= 100 {
		b.history = b.history[1:]
	}
	b.history = append(b.history, evt)

	for _, ch := range b.subscribers {
		select {
		case ch <- evt:
		default:
			// Full channel, skip or drop? Drop for now.
		}
	}
}
