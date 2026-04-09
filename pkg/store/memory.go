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

// Usage tracks token usage statistics for a request
type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// UpstreamRequestStatus tracks the status of parallel upstream requests
type UpstreamRequestStatus struct {
	Main     string `json:"main"`     // "success", "failed", "not_started"
	Second   string `json:"second"`   // "success", "failed", "not_started"
	Fallback string `json:"fallback"` // "success", "failed", "not_started"
}

type RequestLog struct {
	ID        string    `json:"id"`
	Status    string    `json:"status"` // "pending", "running", "completed", "failed", "retrying"
	Model     string    `json:"model"`
	StartTime time.Time `json:"startTime"`
	EndTime   time.Time `json:"endTime"`
	Duration  string    `json:"duration"` // Store as string for easy JSON
	Messages  []Message `json:"messages"` // Full conversation including assistant response
	Retries   int       `json:"retries"`
	Error     string    `json:"error,omitempty"`

	// Token usage tracking
	Usage *Usage `json:"usage,omitempty"` // Final usage from the winning response

	// Token identity for usage tracking
	TokenID   string `json:"token_id,omitempty"`
	TokenName string `json:"token_name,omitempty"`

	// Fallback tracking
	OriginalModel   string   `json:"original_model,omitempty"`   // First requested model
	FallbackUsed    []string `json:"fallback_used,omitempty"`    // List of fallback models that were attempted
	CurrentFallback string   `json:"current_fallback,omitempty"` // Currently active fallback model (if any)

	// Ultimate model tracking
	UltimateModelUsed bool   `json:"ultimate_model_used"`         // Whether ultimate model was triggered for this request
	UltimateModelID   string `json:"ultimate_model_id,omitempty"` // The ultimate model ID used (if triggered)

	// Request metadata
	IsStream   bool                   `json:"is_stream"`            // Whether this was a streaming request
	Parameters map[string]interface{} `json:"parameters,omitempty"` // Request parameters (temperature, max_tokens, etc.)

	// Application tag for grouping requests
	AppTag string `json:"app_tag,omitempty"` // Value from x-proxy-app header

	// Upstream request status tracking (for race retry)
	UpstreamRequests UpstreamRequestStatus `json:"upstream_requests,omitempty"`
}

type RequestStore struct {
	mu       sync.RWMutex
	requests []*RequestLog
	maxSize  int
	ByID     map[string]*RequestLog

	// Cache for GetUniqueAppTags() results
	appTagsCache      []string
	appTagsCacheDirty bool
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

	// Invalidate app tags cache on any modification
	s.appTagsCacheDirty = true

	// If we have an ID collision (shouldn't happen with UUIDs, but safety first), overwrite?
	// or assume Add is for new requests.
	// actually, we might update existing ones.
	// Let's assume Add is for NEW or UPDATE.

	if existing, exists := s.ByID[req.ID]; exists {
		*existing = *req
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

// ListFiltered returns requests filtered by app tag.
// If appTag is empty string, returns requests with no app tag (null/empty).
// If appTag is "*", returns all requests (same as List()).
func (s *RequestStore) ListFiltered(appTag string) []*RequestLog {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// "*" means all requests
	if appTag == "*" {
		n := len(s.requests)
		list := make([]*RequestLog, n)
		for i, req := range s.requests {
			list[n-1-i] = req
		}
		return list
	}

	// Filter by app tag
	var filtered []*RequestLog
	for i := len(s.requests) - 1; i >= 0; i-- {
		req := s.requests[i]
		if appTag == "" {
			// Empty string means requests with no app tag
			if req.AppTag == "" {
				filtered = append(filtered, req)
			}
		} else {
			// Match specific app tag
			if req.AppTag == appTag {
				filtered = append(filtered, req)
			}
		}
	}
	return filtered
}

// GetUniqueAppTags returns a sorted list of unique app tags from all requests.
// Includes an empty string entry if there are requests without an app tag.
// Results are cached and only recomputed when the underlying data changes.
func (s *RequestStore) GetUniqueAppTags() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()

	// Return cached result if valid
	if !s.appTagsCacheDirty {
		// Return a copy to prevent external mutation
		result := make([]string, len(s.appTagsCache))
		copy(result, s.appTagsCache)
		return result
	}

	tagSet := make(map[string]bool)
	hasEmptyTag := false

	for _, req := range s.requests {
		if req.AppTag == "" {
			hasEmptyTag = true
		} else {
			tagSet[req.AppTag] = true
		}
	}

	// Convert to sorted slice
	tags := make([]string, 0, len(tagSet))
	for tag := range tagSet {
		tags = append(tags, tag)
	}

	// Sort tags
	for i := 0; i < len(tags); i++ {
		for j := i + 1; j < len(tags); j++ {
			if tags[i] > tags[j] {
				tags[i], tags[j] = tags[j], tags[i]
			}
		}
	}

	// Add "default" at the beginning if there are requests without app tag
	var result []string
	if hasEmptyTag {
		result = make([]string, 0, len(tags)+1)
		result = append(result, "") // Empty string represents "default"
		result = append(result, tags...)
	} else {
		result = tags
	}

	// Update cache (must hold lock for this)
	s.appTagsCache = result
	s.appTagsCacheDirty = false

	// Return a copy to prevent external mutation
	cached := make([]string, len(result))
	copy(cached, result)
	return cached
}
