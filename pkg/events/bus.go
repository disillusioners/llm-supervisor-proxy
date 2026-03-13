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

// LoopDetectionEvent represents a loop detection result published by the proxy
type LoopDetectionEvent struct {
	RequestID   string   `json:"request_id"`
	Strategy    string   `json:"strategy"`     // "exact_match" | "similarity" | "action_pattern"
	Severity    string   `json:"severity"`     // "info" | "warning" | "critical"
	Evidence    string   `json:"evidence"`     // Human-readable description
	Confidence  float64  `json:"confidence"`   // 0.0 - 1.0
	Pattern     []string `json:"pattern"`      // Matched patterns
	RepeatCount int      `json:"repeat_count"` // Number of repeats detected
	ShadowMode  bool     `json:"shadow_mode"`  // Whether detection was in shadow mode
}

// ToolRepairEvent represents a tool call repair operation
type ToolRepairEvent struct {
	RequestID      string         `json:"request_id"`
	TotalToolCalls int            `json:"total_tool_calls"`
	Repaired       int            `json:"repaired"`
	Failed         int            `json:"failed"`
	StrategiesUsed []string       `json:"strategies_used"`
	Duration       string         `json:"duration"` // Human-readable duration
	Details        []RepairDetail `json:"details,omitempty"`
}

// RepairDetail contains details about a single repair operation
type RepairDetail struct {
	ToolName   string `json:"tool_name"`
	Success    bool   `json:"success"`
	Strategies string `json:"strategies,omitempty"`
	Error      string `json:"error,omitempty"`
}

// StreamChunkDeadlineEvent represents a stream chunk deadline reached event
type StreamChunkDeadlineEvent struct {
	RequestID  string `json:"request_id"`
	Deadline   string `json:"deadline"`    // Configured deadline duration
	BufferSize int    `json:"buffer_size"` // Size of buffer flushed
	Elapsed    string `json:"elapsed"`     // Time since request start
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
