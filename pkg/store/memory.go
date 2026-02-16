package store

import (
	"sync"
	"time"
)

type Function struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type ToolCall struct {
	ID       string   `json:"id"`
	Type     string   `json:"type"`
	Function Function `json:"function"`
}

type Message struct {
	Role      string     `json:"role"`
	Content   string     `json:"content"`
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
	Thinking  string     `json:"thinking,omitempty"` // For reasoning_content
}

type RequestLog struct {
	ID        string     `json:"id"`
	Status    string     `json:"status"` // "pending", "running", "completed", "failed", "retrying"
	Model     string     `json:"model"`
	StartTime time.Time  `json:"startTime"`
	EndTime   time.Time  `json:"endTime"`
	Duration  string     `json:"duration"` // Store as string for easy JSON
	Messages  []Message  `json:"messages"`
	Response  string     `json:"response"`
	Thinking  string     `json:"thinking,omitempty"`   // Captured thinking content
	ToolCalls []ToolCall `json:"tool_calls,omitempty"` // Captured tool calls in the response?
	// Actually, tool calls are usually part of the response message in OpenAI API.
	// But our "Response" field is just a string buffer.
	// Let's store them separately for easy access,
	// OR we should construct a proper Assistant Message at the end.
	Retries int    `json:"retries"`
	Error   string `json:"error,omitempty"`
}

type RequestStore struct {
	mu       sync.RWMutex
	requests []*RequestLog
	maxSize  int
	ByID     map[string]*RequestLog
}

func NewRequestStore(maxSize int) *RequestStore {
	return &RequestStore{
		requests: make([]*RequestLog, 0, maxSize),
		maxSize:  maxSize,
		ByID:     make(map[string]*RequestLog),
	}
}

func (s *RequestStore) Add(req *RequestLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// If we have an ID collision (shouldn't happen with UUIDs, but safety first), overwrite?
	// or assume Add is for new requests.
	// actually, we might update existing ones.
	// Let's assume Add is for NEW or UPDATE.

	if existing, exists := s.ByID[req.ID]; exists {
		// Update existing
		// Find index? O(N) scan or just update fields in place?
		// Since we store pointers, updating the struct pointed to by 'existing' would work IF req was a pointer to it.
		// But here 'req' is a passed pointer.
		// Simpler: Just update the map and the slice slot.
		// O(N) to find slot is annoying.
		// Let's just assume we don't update via Add, but via Get() -> modify.
		// So Add is only for new.
		_ = existing
		return
	}

	if len(s.requests) >= s.maxSize {
		// Remove oldest
		oldest := s.requests[0]
		delete(s.ByID, oldest.ID)
		s.requests = s.requests[1:]
	}

	s.requests = append(s.requests, req)
	s.ByID[req.ID] = req
}

func (s *RequestStore) Get(id string) *RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.ByID[id]
}

func (s *RequestStore) List() []*RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// meaningful to return in reverse order (newest first)
	n := len(s.requests)
	list := make([]*RequestLog, n)
	for i, req := range s.requests {
		list[n-1-i] = req
	}
	return list
}
