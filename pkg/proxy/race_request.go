package proxy

import (
	"context"
	"net/http"
	"sync"
	"time"
)

type upstreamModelType string

const (
	modelTypeMain     upstreamModelType = "main"
	modelTypeSecond   upstreamModelType = "second"
	modelTypeFallback upstreamModelType = "fallback"
)

type upstreamStatus string

const (
	statusPending   upstreamStatus = "pending"
	statusRunning   upstreamStatus = "running"
	statusStreaming upstreamStatus = "streaming"
	statusCompleted upstreamStatus = "completed"
	statusFailed    upstreamStatus = "failed"
)

// TokenUsage represents token usage from an upstream response
type TokenUsage struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}

// upstreamRequest represents a single attempt to an upstream provider
type upstreamRequest struct {
	mu sync.RWMutex

	id        int               // Sequence ID (0=main, 1=second, 2=fallback)
	modelType upstreamModelType // main, second, fallback
	modelID   string            // Specific model ID used

	ctx      context.Context    // Request-specific context
	cancel   context.CancelFunc // To cancel this request specifically
	buffer   *streamBuffer      // Buffer for response chunks
	resp     *http.Response     // Set when response received
	status   upstreamStatus     // Current status
	err      error              // Final error (if any)
	attempts int                // Number of HTTP retries (for connectivity)

	// Timing
	startTime        time.Time
	firstByteTime    time.Time
	completionTime   time.Time
	lastActivityTime time.Time // Last time we received data (for idle detection)

	// Stats
	totalChunks int
	totalBytes  int64

	// Token usage (extracted from non-streaming responses)
	usage *TokenUsage
}

func newUpstreamRequest(id int, mType upstreamModelType, modelID string, maxBuffer int) *upstreamRequest {
	return &upstreamRequest{
		id:        id,
		modelType: mType,
		modelID:   modelID,
		status:    statusPending,
		buffer:    newStreamBuffer(int64(maxBuffer)),
	}
}

// MarkStarted transitions status to running
func (r *upstreamRequest) MarkStarted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = statusRunning
	r.startTime = time.Now()
}

func (r *upstreamRequest) MarkStreaming() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = statusStreaming
	r.firstByteTime = time.Now()
	r.lastActivityTime = time.Now() // Initialize activity time when streaming starts
}

func (r *upstreamRequest) MarkCompleted() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = statusCompleted
	r.completionTime = time.Now()
	// Signal buffer completion
	r.buffer.Close(nil)
}

func (r *upstreamRequest) MarkFailed(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.status = statusFailed
	r.err = err
	r.completionTime = time.Now()
	// Signal buffer completion with error
	r.buffer.Close(err)
}

func (r *upstreamRequest) SetContext(ctx context.Context, cancel context.CancelFunc) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ctx = ctx
	r.cancel = cancel
}

func (r *upstreamRequest) GetStatus() upstreamStatus {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status
}

func (r *upstreamRequest) GetID() int {
	return r.id
}

func (r *upstreamRequest) GetModelType() upstreamModelType {
	return r.modelType
}

func (r *upstreamRequest) GetModelID() string {
	return r.modelID
}

func (r *upstreamRequest) GetBuffer() *streamBuffer {
	return r.buffer
}

func (r *upstreamRequest) GetError() error {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.err
}

func (r *upstreamRequest) IsDone() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status == statusCompleted || r.status == statusFailed
}

// IsCompleted returns true only if the request completed successfully (received [DONE])
func (r *upstreamRequest) IsCompleted() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status == statusCompleted
}

func (r *upstreamRequest) IsStreaming() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status == statusStreaming
}

// Cancel safely cancels the request's context if cancel function is set
func (r *upstreamRequest) Cancel() {
	r.mu.RLock()
	cancel := r.cancel
	r.mu.RUnlock()
	if cancel != nil {
		cancel()
	}
}

// TrackActivity updates the last activity time to now
// This is used by the executor to signal that data is still being received
func (r *upstreamRequest) TrackActivity() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastActivityTime = time.Now()
}

// GetLastActivity returns the time of last activity
func (r *upstreamRequest) GetLastActivity() time.Time {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.lastActivityTime
}

// IsIdle returns true if the request has been idle for longer than the given duration
func (r *upstreamRequest) IsIdle(idleTimeout time.Duration) bool {
	r.mu.RLock()
	lastActivity := r.lastActivityTime
	status := r.status
	r.mu.RUnlock()

	// Only consider idle if we're streaming (have received at least some data)
	// and haven't received data for idleTimeout duration
	if status != statusStreaming {
		return false
	}
	return time.Since(lastActivity) > idleTimeout
}

// SetUsage sets the token usage for this request
func (r *upstreamRequest) SetUsage(usage *TokenUsage) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.usage = usage
}

// GetUsage returns the token usage for this request
func (r *upstreamRequest) GetUsage() *TokenUsage {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.usage
}
