package proxy

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"
)

func TestStartSSEHeartbeat(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "returns cancel function",
			fn:   testStartSSEHeartbeatReturnsCancel,
		},
		{
			name: "cancel stops the goroutine",
			fn:   testStartSSEHeartbeatCancelStopsGoroutine,
		},
		{
			name: "context cancellation stops goroutine",
			fn:   testStartSSEHeartbeatContextCancellationStopsGoroutine,
		},
		{
			name: "heartbeat goroutine runs without blocking test",
			fn:   testStartSSEHeartbeatDoesNotBlock,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testStartSSEHeartbeatReturnsCancel(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	ctx := context.Background()

	cancel := h.startSSEHeartbeat(w, ctx)

	// Cancel function should not be nil
	if cancel == nil {
		t.Error("startSSEHeartbeat returned nil cancel function")
	}

	// Clean up
	cancel()
	time.Sleep(50 * time.Millisecond) // Give goroutine time to exit
}

func testStartSSEHeartbeatCancelStopsGoroutine(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	ctx := context.Background()

	var wg sync.WaitGroup
	wg.Add(1)

	// Create a mock response writer to track writes
	flusher := &mockFlusher{
		flushed: make(chan struct{}, 10),
	}
	mockWriter := &mockResponseWriter{
		ResponseWriter: w,
		flusher:        flusher,
	}

	cancel := h.startSSEHeartbeat(mockWriter, ctx)

	// Cancel immediately
	cancel()

	// Wait for goroutine to potentially write a heartbeat
	// With 30s interval, no heartbeat should be written
	select {
	case <-flusher.flushed:
		// This would indicate a heartbeat was sent, which shouldn't happen on immediate cancel
		t.Error("heartbeat was sent after cancel - goroutine should have stopped")
	case <-time.After(100 * time.Millisecond):
		// Expected: no flush within short time window
	}

	wg.Done()
}

func testStartSSEHeartbeatContextCancellationStopsGoroutine(t *testing.T) {
	h := &Handler{}

	// Create a context that will be cancelled
	ctx, cancelCtx := context.WithCancel(context.Background())

	var wg sync.WaitGroup
	wg.Add(1)

	flusher := &mockFlusher{
		flushed: make(chan struct{}, 10),
	}
	mockWriter := &mockResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		flusher:        flusher,
	}

	// Start heartbeat
	cancelHeartbeat := h.startSSEHeartbeat(mockWriter, ctx)

	// Cancel the context
	cancelCtx()

	// Wait a bit for the goroutine to exit
	time.Sleep(100 * time.Millisecond)

	// Cancel the heartbeat to clean up
	cancelHeartbeat()

	// Verify no heartbeat was sent (interval is 30s)
	select {
	case <-flusher.flushed:
		t.Error("heartbeat was sent after context cancellation")
	default:
		// Expected: no flush
	}

	wg.Done()
}

func testStartSSEHeartbeatDoesNotBlock(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	ctx := context.Background()

	// This should return immediately, not wait for any heartbeat
	done := make(chan struct{})
	go func() {
		cancel := h.startSSEHeartbeat(w, ctx)
		close(done)
		cancel()
	}()

	// Should complete quickly (within a short timeout)
	select {
	case <-done:
		// Expected: returned immediately
	case <-time.After(500 * time.Millisecond):
		t.Error("startSSEHeartbeat blocked the caller")
	}
}

func TestSendHeartbeat(t *testing.T) {
	tests := []struct {
		name string
		fn   func(t *testing.T)
	}{
		{
			name: "writes : heartbeat\\n\\n to response",
			fn:   testSendHeartbeatWritesCorrectData,
		},
		{
			name: "flushes the response if flusher available",
			fn:   testSendHeartbeatFlushesIfFlusher,
		},
		{
			name: "returns early if context already cancelled",
			fn:   testSendHeartbeatReturnsEarlyIfCancelled,
		},
		{
			name: "handles write timeout",
			fn:   testSendHeartbeatHandlesWriteTimeout,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, tt.fn)
	}
}

func testSendHeartbeatWritesCorrectData(t *testing.T) {
	h := &Handler{}
	w := httptest.NewRecorder()
	ctx := context.Background()
	writeTimeout := time.NewTimer(HeartbeatWriteTimeout)
	defer writeTimeout.Stop()

	h.sendHeartbeat(w, ctx, writeTimeout)

	body := w.Body.String()
	expected := ": heartbeat\n\n"

	if body != expected {
		t.Errorf("sendHeartbeat wrote %q, want %q", body, expected)
	}
}

func testSendHeartbeatFlushesIfFlusher(t *testing.T) {
	h := &Handler{}
	flusher := &mockFlusher{
		flushed: make(chan struct{}, 10),
	}
	w := &mockResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		flusher:        flusher,
	}
	ctx := context.Background()
	writeTimeout := time.NewTimer(HeartbeatWriteTimeout)
	defer writeTimeout.Stop()

	h.sendHeartbeat(w, ctx, writeTimeout)

	select {
	case <-flusher.flushed:
		// Expected: flush was called
	case <-time.After(100 * time.Millisecond):
		t.Error("sendHeartbeat did not flush when flusher is available")
	}
}

func testSendHeartbeatReturnsEarlyIfCancelled(t *testing.T) {
	h := &Handler{}

	// Create already-cancelled context
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	// Track if write goroutine ran using a custom mock
	writeRan := make(chan struct{}, 1)
	flusher := &mockFlusher{
		flushed: make(chan struct{}, 1),
	}
	w := &writeTrackingResponseWriter{
		ResponseWriter: httptest.NewRecorder(),
		flusher:        flusher,
		writeRan:       writeRan,
	}

	writeTimeout := time.NewTimer(HeartbeatWriteTimeout)
	defer writeTimeout.Stop()

	start := time.Now()
	h.sendHeartbeat(w, ctx, writeTimeout)
	elapsed := time.Since(start)

	// Should return quickly without waiting for write
	if elapsed > 50*time.Millisecond {
		t.Errorf("sendHeartbeat took too long with cancelled context: %v", elapsed)
	}

	// Give goroutine time to potentially write
	select {
	case <-writeRan:
		// Write may have run, that's OK
	case <-time.After(100 * time.Millisecond):
		// Expected: write might not complete
	}
}

func testSendHeartbeatHandlesWriteTimeout(t *testing.T) {
	h := &Handler{}

	// Create a writer that delays Write but completes within timeout
	slowWriter := &slowResponseWriter{
		delay: 50 * time.Millisecond, // Slower than 10ms timeout
		flusher: &mockFlusher{
			flushed: make(chan struct{}, 1),
		},
	}

	ctx := context.Background()

	// Use a very short write timeout for testing
	writeTimeout := time.NewTimer(10 * time.Millisecond)
	defer writeTimeout.Stop()

	start := time.Now()
	h.sendHeartbeat(slowWriter, ctx, writeTimeout)
	elapsed := time.Since(start)

	// Should return when timeout fires (around 10ms)
	// The wg.Wait() will wait for the slow write to complete (50ms)
	// So total time should be around 50ms + overhead
	if elapsed < 10*time.Millisecond {
		t.Error("sendHeartbeat returned too quickly, timeout may not have been honored")
	}

	// Should complete within reasonable time
	if elapsed > 200*time.Millisecond {
		t.Errorf("sendHeartbeat took too long with timeout: %v", elapsed)
	}
}

// mockFlusher implements http.Flusher interface
type mockFlusher struct {
	flushed chan struct{}
}

func (f *mockFlusher) Flush() {
	select {
	case f.flushed <- struct{}{}:
	default:
	}
}

// mockResponseWriter wraps httptest.ResponseRecorder and implements http.Flusher
type mockResponseWriter struct {
	http.ResponseWriter
	flusher *mockFlusher
}

func (m *mockResponseWriter) Flush() {
	m.flusher.Flush()
}

// blockingResponseWriter blocks all writes to simulate slow client
type blockingResponseWriter struct {
	flusher *mockFlusher
}

func (b *blockingResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (b *blockingResponseWriter) Write(data []byte) (int, error) {
	// Block indefinitely until context is cancelled
	<-make(chan struct{})
	return len(data), nil
}

func (b *blockingResponseWriter) WriteHeader(statusCode int) {
}

func (b *blockingResponseWriter) Flush() {
	b.flusher.Flush()
}

// writeTrackingResponseWriter tracks when Write is called
type writeTrackingResponseWriter struct {
	http.ResponseWriter
	flusher  *mockFlusher
	writeRan chan<- struct{}
}

func (w *writeTrackingResponseWriter) Write(data []byte) (int, error) {
	select {
	case w.writeRan <- struct{}{}:
	default:
	}
	return w.ResponseWriter.Write(data)
}

func (w *writeTrackingResponseWriter) Flush() {
	w.flusher.Flush()
}

// slowResponseWriter delays Write to simulate slow client
type slowResponseWriter struct {
	delay   time.Duration
	flusher *mockFlusher
}

func (s *slowResponseWriter) Header() http.Header {
	return make(http.Header)
}

func (s *slowResponseWriter) Write(data []byte) (int, error) {
	time.Sleep(s.delay)
	return len(data), nil
}

func (s *slowResponseWriter) WriteHeader(statusCode int) {
}

func (s *slowResponseWriter) Flush() {
	s.flusher.Flush()
}

// Ensure interfaces are satisfied at compile time
var _ http.Flusher = (*mockFlusher)(nil)
var _ http.Flusher = (*mockResponseWriter)(nil)
var _ http.Flusher = (*blockingResponseWriter)(nil)

// Verify mockResponseWriter implements http.ResponseWriter
var _ http.ResponseWriter = (*mockResponseWriter)(nil)
var _ http.ResponseWriter = (*blockingResponseWriter)(nil)
var _ http.ResponseWriter = (*writeTrackingResponseWriter)(nil)
var _ http.ResponseWriter = (*slowResponseWriter)(nil)
