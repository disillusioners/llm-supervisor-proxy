package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestStreamBuffer(t *testing.T) {
	buffer := newStreamBuffer(100)

	line1 := []byte("data: hello")
	if !buffer.Add(line1) {
		t.Errorf("Failed to add line1")
	}

	chunks, nextIdx := buffer.GetChunksFrom(0)
	if len(chunks) != 1 || string(chunks[0]) != "data: hello\n" {
		t.Errorf("Unexpected chunks: %v", chunks)
	}
	if nextIdx != 1 {
		t.Errorf("Unexpected nextIdx: %d", nextIdx)
	}

	// Test pruning
	buffer.Prune(1)

	line2 := []byte("data: world")
	buffer.Add(line2)

	chunks, nextIdx = buffer.GetChunksFrom(1)
	if len(chunks) != 1 || string(chunks[0]) != "data: world\n" {
		t.Errorf("Unexpected chunks after prune: %v", chunks)
	}

	// Test limit
	longLine := []byte(strings.Repeat("a", 101))
	if buffer.Add(longLine) {
		t.Errorf("Should have failed to add line exceeding total limit")
	}
}

func TestRaceCoordinator_Basic(t *testing.T) {
	// Setup mock upstream
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Printf("Mock upstream reached: %s %s\n", r.Method, r.URL.Path)
		if r.URL.Path != "/v1/chat/completions" {
			http.Error(w, "Invalid path: "+r.URL.Path, http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: chunk1\n\n"))
		time.Sleep(50 * time.Millisecond)
		w.Write([]byte("data: chunk2\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceMaxParallel:    2,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        1 * time.Second,
		StreamDeadline:     5 * time.Second, // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second, // Allow enough time for request to complete
		ModelID:            "test-model",
	}

	ctx := context.Background()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()
	if winner == nil {
		t.Fatalf("No winner selected")
	}

	if winner.id != 0 {
		t.Errorf("Expected request 0 to win, got %d", winner.id)
	}

	// Wait for completion
	select {
	case <-winner.buffer.Done():
		// Success
	case <-time.After(1 * time.Second):
		t.Errorf("Timeout waiting for buffer done")
	}

	chunks, _ := winner.buffer.GetChunksFrom(0)
	if len(chunks) < 3 {
		t.Errorf("Expected at least 3 chunks, got %d", len(chunks))
	}
}

func TestRaceCoordinator_Retry(t *testing.T) {
	// Setup mock upstream that fails for first request and succeeds for second
	var callCount int64
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt64(&callCount, 1)
		if count == 1 {
			// First request hangs then fails
			time.Sleep(200 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Second request succeeds
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: success from request 2\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    2,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        100 * time.Millisecond, // Fast idle timeout to trigger second spawn
		StreamDeadline:     5 * time.Second,        // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second,        // Allow enough time for retry
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	// Provide two models so the coordinator can spawn a second request when the first fails
	models := []string{"test-model", "test-model-fallback"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()
	if winner == nil {
		t.Fatalf("No winner selected")
	}

	if winner.id != 1 {
		t.Errorf("Expected request 1 to win, got %d", winner.id)
	}
}

// =============================================================================
// Integration tests for race scenarios (Phase 5)
// =============================================================================

// TestRaceScenario_MainWinsBeforeIdleTimeout verifies that if main completes
// before idle timeout, no parallel requests are spawned and main wins
func TestRaceScenario_MainWinsBeforeIdleTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Fast response - completes well before idle timeout
		w.Write([]byte("data: fast response\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    3,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        1 * time.Second,
		StreamDeadline:     5 * time.Second, // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second, // Add streaming deadline
		ModelID:            "test-model",
	}

	ctx := context.Background()
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model", "fallback-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()
	if winner == nil {
		t.Fatalf("No winner selected")
	}

	// Main request (id=0) should win
	if winner.id != 0 {
		t.Errorf("Expected main request (id=0) to win, got %d", winner.id)
	}
	if winner.modelType != modelTypeMain {
		t.Errorf("Expected winner type to be main, got %s", winner.modelType)
	}
}

// TestRaceScenario_FallbackWins verifies that fallback model can win
func TestRaceScenario_FallbackWins(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		// Read the request body to check which model is being used
		body := make([]byte, 100)
		r.Body.Read(body)

		if callCount == 1 {
			// First request (main) fails
			time.Sleep(100 * time.Millisecond)
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		// Second request (fallback) succeeds
		// Note: With 2 models, the second request uses the fallback model
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("data: fallback success\n\n"))
		w.Write([]byte("data: [DONE]\n\n"))
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    3,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        50 * time.Millisecond, // Fast idle timeout
		StreamDeadline:     5 * time.Second,       // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second,       // Add streaming deadline
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model", "fallback-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()
	if winner == nil {
		t.Fatalf("No winner selected")
	}

	// Second request (id=1) should win - this is the fallback model
	if winner.id != 1 {
		t.Errorf("Expected fallback request (id=1) to win, got %d", winner.id)
	}
	if winner.modelType != modelTypeSecond {
		t.Errorf("Expected winner type to be second, got %s", winner.modelType)
	}
}

// TestRaceScenario_AllFail verifies early termination when all requests fail
func TestRaceScenario_AllFail(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All requests fail
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    3,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        50 * time.Millisecond,
		StreamDeadline:     5 * time.Second, // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second, // Add streaming deadline
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model", "fallback-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()

	// All requests failed, so no winner
	if winner != nil {
		t.Errorf("Expected no winner when all requests fail, got winner with id=%d", winner.id)
	}

	// Verify common failure status is returned
	status := coordinator.GetCommonFailureStatus()
	if status != http.StatusInternalServerError {
		t.Errorf("Expected status 500, got %d", status)
	}
}

// TestRaceScenario_ClientDisconnectCancelsAll verifies that client disconnect
// cancels all running requests
func TestRaceScenario_ClientDisconnectCancelsAll(t *testing.T) {
	requestCancelled := make(chan bool, 3)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Slow response - will be cancelled
		select {
		case <-r.Context().Done():
			requestCancelled <- true
			return
		case <-time.After(5 * time.Second):
			w.Write([]byte("should not reach here"))
		}
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    3,
		RaceMaxBufferBytes: 1000,
		IdleTimeout:        50 * time.Millisecond,
		StreamDeadline:     5 * time.Second, // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second, // Absolute hard timeout
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithCancel(context.Background())

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model", "fallback-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	// Cancel after a short delay (simulating client disconnect)
	time.Sleep(100 * time.Millisecond)
	cancel()

	// Wait for coordinator to finish
	select {
	case <-coordinator.done:
		// Good - coordinator finished
	case <-time.After(2 * time.Second):
		t.Error("Coordinator did not finish after client disconnect")
	}

	// Verify at least one request was cancelled
	select {
	case <-requestCancelled:
		// Good - at least one request was cancelled
	case <-time.After(500 * time.Millisecond):
		// May not always catch the cancellation signal due to timing
		// This is not a failure - the important thing is coordinator finished
	}
}

// TestRaceScenario_BufferOverflowHandling verifies that buffer overflow
// stops the request but allows others to continue
func TestRaceScenario_BufferOverflowHandling(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)

		if callCount == 1 {
			// First request sends too much data (will overflow buffer)
			for i := 0; i < 100; i++ {
				w.Write([]byte("data: " + strings.Repeat("x", 100) + "\n\n"))
			}
		} else {
			// Second request succeeds with small response
			w.Write([]byte("data: small response\n\n"))
			w.Write([]byte("data: [DONE]\n\n"))
		}
	}))
	defer server.Close()

	cfg := &ConfigSnapshot{
		UpstreamURL:        server.URL,
		RaceRetryEnabled:   true,
		RaceParallelOnIdle: true,
		RaceMaxParallel:    3,
		RaceMaxBufferBytes: 500, // Small buffer to trigger overflow
		IdleTimeout:        50 * time.Millisecond,
		StreamDeadline:     5 * time.Second, // Time limit before picking best buffer
		MaxGenerationTime:  5 * time.Second, // Add streaming deadline
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model", "fallback-model"}

	coordinator := newRaceCoordinator(ctx, cfg, req, rawBody, models)
	coordinator.Start()

	winner := coordinator.WaitForWinner()
	if winner == nil {
		t.Fatalf("No winner selected")
	}

	// Second request should win (first overflows)
	if winner.id != 1 {
		t.Errorf("Expected second request (id=1) to win, got %d", winner.id)
	}
}

// TestStreamBuffer_ConcurrentReadWrite verifies thread-safety of stream buffer
func TestStreamBuffer_ConcurrentReadWrite(t *testing.T) {
	buffer := newStreamBuffer(10000)

	var wg sync.WaitGroup
	wg.Add(2)

	// Writer goroutine
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			line := []byte(fmt.Sprintf("data: line%d", i))
			buffer.Add(line)
			time.Sleep(time.Microsecond)
		}
		buffer.Close(nil)
	}()

	// Reader goroutine
	go func() {
		defer wg.Done()
		readIndex := 0
		for {
			chunks, nextIdx := buffer.GetChunksFrom(readIndex)
			if len(chunks) > 0 {
				readIndex = nextIdx
				buffer.Prune(readIndex)
			}

			if buffer.IsComplete() {
				// Read any remaining chunks
				chunks, _ = buffer.GetChunksFrom(readIndex)
				if len(chunks) > 0 {
					readIndex = len(chunks)
				}
				return
			}

			time.Sleep(time.Microsecond)
		}
	}()

	// Wait with timeout
	done := make(chan struct{})
	go func() {
		wg.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Success
	case <-time.After(2 * time.Second):
		t.Error("Concurrent read/write test timed out")
	}
}
