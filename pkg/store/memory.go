package store

import (
	"fmt"
	"sync"
	"time"
)

// Size returns an approximate in-memory byte cost for this RequestLog.
// It is intentionally cheap: it sums the JSON-encoded byte lengths of
// the heavy string fields (id/status/model/error/duration/token_*/…),
// the per-message content + thinking + tool-call JSON + arguments,
// the parameter map (best-effort, fmt.Sprint'ed), and a fixed per-field
// overhead constant so the approximation tracks reality closely enough
// for byte-budget eviction.
//
// This is NOT a precise heap-allocator measurement; it is a stable,
// O(n) in message count upper bound that the store uses to enforce its
// cumulative payload byte budget (P2-5b). Exposed publicly so the FE
// API summary projection can surface it as `total_size_bytes` for the
// UI.
//
// IMPORTANT: this heuristic is the SAME whether the request log is in
// its first-Added form or has been mutated since. The byte-budget
// accounting in RequestStore.Add tracks "last-accounted size" per id in
// sizeByID, so any delta (growth via in-place Messages append, etc.)
// is reflected in totalBytes regardless of how many times the same
// *RequestLog pointer is re-Added. Do NOT replace this with a JSON
// encoder (which would be both slow and surprising for tests that check
// deterministic size values).
func (r *RequestLog) Size() int64 {
	// Fixed per-field overhead: each field carries a JSON name + a
	// few bytes of structural overhead (quotes, comma, etc.). Using
	// len()+16 is a conservative estimator that avoids measuring the
// exact JSON encoding on every Add.
	const perFieldOverhead = 16
	// Fields we charge an overhead for (must match the actual struct
	// fields + the per-field `n += len(...)` calls below):
	//   ID, Status, Model, Duration, Error, TokenID, TokenName,
	//   OriginalModel, AppTag, UltimateModelID, CurrentFallback (11 strings;
	//   the slice of strings FallbackUsed is charged per-element below).
	//   Plus the Usage object (1 wrapper overhead) and the UpstreamRequests
	//   struct (1 wrapper overhead).
	// Total charged overheads = 13. Keep this list in sync with the per-
	// field n += len(...) lines below — if you add a field, bump this count.
	const fieldsCharged = 13
	n := int64(perFieldOverhead) * fieldsCharged

	n += int64(len(r.ID))
	n += int64(len(r.Status))
	n += int64(len(r.Model))
	n += int64(len(r.Duration))
 n += int64(len(r.Error))
	n += int64(len(r.TokenID))
	n += int64(len(r.TokenName))
	n += int64(len(r.OriginalModel))
	n += int64(len(r.AppTag))
	n += int64(len(r.UltimateModelID))
	n += int64(len(r.CurrentFallback))

	for _, fb := range r.FallbackUsed {
		n += int64(len(fb)) + perFieldOverhead
	}

	for _, msg := range r.Messages {
		n += int64(perFieldOverhead) * 4 // role, content, tool_calls, thinking
		n += int64(len(msg.Role))
		n += int64(len(msg.Content))
		n += int64(len(msg.Thinking))
		for _, tc := range msg.ToolCalls {
			n += int64(perFieldOverhead) * 4 // id, type, function name, function arguments
			n += int64(len(tc.ID))
			n += int64(len(tc.Type))
			n += int64(len(tc.Function.Name))
			n += int64(len(tc.Function.Arguments))
		}
	}

	for k, v := range r.Parameters {
		n += int64(perFieldOverhead) + int64(len(k))
		n += int64(len(fmt.Sprintf("%v", v)))
	}

	return n
}

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

	// sizeByID is the last-accounted byte cost for each id currently
	// held in `requests`. Used to compute byte-budget deltas on
	// in-place mutation + re-Add (the dominant pattern: pkg/proxy/
	// handler_finalize.go:39 appends Messages to rc.reqLog then
	// re-Adds the SAME pointer; Size() of the same pointer before
	// and after the append returns the NEW size, so naive
	// `totalBytes -= existing.Size(); += req.Size()` cancels to
	// zero and the store drifts). Tracking last-accounted size
	// per-id gives an O(1) correct delta on every Add.
	sizeByID map[string]int64

	// Cache for GetUniqueAppTags() results
	appTagsCache      []string
	appTagsCacheDirty bool

	// maxBytes is the cumulative payload byte budget for the ring buffer.
	// 0 disables byte-based eviction (count-only, original behavior).
	maxBytes int64
	// totalBytes is the running sum of sizeByID entries for every
	// request currently held in `requests`. Maintained atomically
	// alongside the slice mutations so the eviction check is O(1).
	// Invariant: totalBytes == sum(sizeByID[id] for id in ByID).
	totalBytes int64
}

// RequestStoreOption mutates a RequestStore during construction. Used to
// add optional knobs (e.g. byte-budget eviction) without breaking the
// existing single-arg constructor that the rest of the codebase calls.
type RequestStoreOption func(*RequestStore)

// WithMaxBytes enables cumulative payload byte-budget eviction.
//
// When set, the store keeps evicting the OLDEST entry until the running
// total of approximate per-entry sizes fits within maxBytes. The count
// cap (NewRequestStore's first arg) is still enforced — byte eviction
// fires only when count is below cap AND bytes is over budget.
//
// Pass 0 (or omit) to disable byte-based eviction (legacy behavior).
func WithMaxBytes(maxBytes int64) RequestStoreOption {
	return func(s *RequestStore) { s.maxBytes = maxBytes }
}

func NewRequestStore(maxSize int, opts ...RequestStoreOption) *RequestStore {
	s := &RequestStore{
		requests: make([]*RequestLog, 0, maxSize),
		maxSize:  maxSize,
		ByID:     make(map[string]*RequestLog),
		sizeByID: make(map[string]int64),
	}
	for _, opt := range opts {
		opt(s)
	}
	return s
}

// MaxBytes returns the configured cumulative payload byte budget. 0
// means byte-budget eviction is disabled (count-only, original behavior).
func (s *RequestStore) MaxBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.maxBytes
}

// TotalBytes returns the current running sum of last-accounted sizes
// held in the store. Cheap O(1) snapshot for telemetry / tests.
func (s *RequestStore) TotalBytes() int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.totalBytes
}

// SizeOf returns the last-accounted size for the given id, or 0 if
// the id is not present. O(1) accessor for the FE API summary hot
// path so handleRequests / handleRequestSummary do not have to call
// the O(messages) Size() on every list element. Returns the same
// value Size() would return for that entry's CURRENT mutation state,
// because sizeByID is updated to track every growth on re-Add.
func (s *RequestStore) SizeOf(id string) int64 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.sizeByID[id]
}

// enforceBudget drops the OLDEST entries (front of the slice) until
// the running totalBytes fits within maxBytes, OR the store empties.
// Called from Add after BOTH the new-entry and overwrite paths so
// byte-budget eviction fires regardless of how the entry got bigger
// (first Add, in-place Messages append + re-Add, count-cap replacement).
//
// Pre-condition: caller holds s.mu (write lock). enforceBudget also
// updates sizeByID / ByID atomically with the slice mutation so the
// totalBytes invariant holds on every path.
func (s *RequestStore) enforceBudget() {
	if s.maxBytes <= 0 {
		return
	}
	for s.totalBytes > s.maxBytes && len(s.requests) > 0 {
		s.evictOldest()
	}
}

// evictOldest removes the oldest entry (front of the slice) and
// decrements totalBytes by its last-accounted size. Caller holds
// s.mu (write lock).
func (s *RequestStore) evictOldest() {
	if len(s.requests) == 0 {
		return
	}
	oldest := s.requests[0]
	oldestSize := s.sizeByID[oldest.ID]
	s.totalBytes -= oldestSize
	if s.totalBytes < 0 {
		// Defensive: should never happen with correct sizeByID
		// accounting, but guard against bookkeeping drift silently
		// disabling byte-budget eviction (the C1 failure mode).
		s.totalBytes = 0
	}
	delete(s.ByID, oldest.ID)
	delete(s.sizeByID, oldest.ID)
	s.requests = s.requests[1:]
}

func (s *RequestStore) Add(req *RequestLog) {
	s.mu.Lock()
	defer s.mu.Unlock()

	// Invalidate app tags cache on any modification
	s.appTagsCacheDirty = true

	newSize := req.Size()

	if existing, exists := s.ByID[req.ID]; exists {
		// Overwrite path. Compute the delta against the LAST-accounted
		// size in sizeByID, not against Size() of `existing` — those
		// are the SAME pointer after the handler's in-place mutation
		// (e.g. handler_finalize.go:39 appends Messages then re-Adds),
		// so a Size()-based delta would cancel to zero and the store
		// would drift toward negative totalBytes on the next eviction.
		//
		// Total deltas after this block: totalBytes += newSize -
		// sizeByID[req.ID]; sizeByID[req.ID] = newSize; invariant
		// holds regardless of how many messages were appended in
		// place between Adds.
		prevSize := s.sizeByID[req.ID]
		*existing = *req
		s.totalBytes += newSize - prevSize
		s.sizeByID[req.ID] = newSize
		// Enforce the byte budget on the OVERWRITE path too (M4 fix):
		// a single growing conversation that exceeds the budget via
		// repeated in-place re-Add must be evicted promptly, without
		// waiting for the next NEW request to arrive.
		s.enforceBudget()
		return
	}

	// New-entry path: count-cap eviction first, then add, then
	// byte-budget eviction. The byte-budget loop also enforces the
	// cap repeatedly so a single very large entry that exceeds the
	// budget is dropped immediately (otherwise totalBytes would
	// exceed maxBytes until the next Add).
	if len(s.requests) >= s.maxSize {
		s.evictOldest()
	}

	s.requests = append(s.requests, req)
	s.ByID[req.ID] = req
	s.sizeByID[req.ID] = newSize
	s.totalBytes += newSize

	s.enforceBudget()
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
// Uses double-checked locking for thread-safe cache access.
func (s *RequestStore) GetUniqueAppTags() []string {
	// Fast path: check with read lock first
	s.mu.RLock()
	if !s.appTagsCacheDirty {
		// Return a copy to prevent external mutation
		result := make([]string, len(s.appTagsCache))
		copy(result, s.appTagsCache)
		s.mu.RUnlock()
		return result
	}
	s.mu.RUnlock()

	// Slow path: cache is dirty, need exclusive write access
	s.mu.Lock()
	defer s.mu.Unlock()

	// Double-check: another goroutine may have recomputed while we waited
	if !s.appTagsCacheDirty {
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

	// Update cache while holding write lock
	s.appTagsCache = result
	s.appTagsCacheDirty = false

	// Return a copy to prevent external mutation
	cached := make([]string, len(result))
	copy(cached, result)
	return cached
}
