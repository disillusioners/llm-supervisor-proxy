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
	startTime      time.Time
	firstByteTime  time.Time
	completionTime time.Time

	// Stats
	totalChunks int
	totalBytes  int64
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

// markStarted transitions status to running
func (r *upstreamRequest) markStarted() {
	r.mu.Lock()
	defer r.mu.RUnlock() // BUG FIX: Unlock! (Wait, I used defer RUnlock, should be Unlock)
}

// Correcting the above:
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

func (r *upstreamRequest) IsStreaming() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.status == statusStreaming
}
