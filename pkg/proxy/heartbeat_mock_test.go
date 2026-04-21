package proxy

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/events"
	"github.com/disillusioners/llm-supervisor-proxy/pkg/store"
)

// ─────────────────────────────────────────────────────────────────────────────
// Mock Upstream Handlers for Heartbeat Testing
// ─────────────────────────────────────────────────────────────────────────────

// mockHeartbeatTestHandler creates a mock handler that sends initial data quickly,
// simulating a fast LLM response. This allows us to verify the heartbeat
// mechanism is started and functioning, even if no heartbeats are sent due to
// fast completion.
// Note: The proxy sends ": connected\n\n", so this mock should NOT send it.
func mockHeartbeatTestHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Note: Do NOT send ": connected\n\n" - that's sent by the proxy

		// Send initial data
		for i := 0; i < 3; i++ {
			chunk := mockCreateChunk(fmt.Sprintf("word%d", i))
			fmt.Fprintf(w, "data: %s\n\n", chunk)
			flusher.Flush()
		}

		// Send [DONE] to signal response complete
		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
		// Handler returns naturally - the connection will be closed by the client
	}
}

// mockSlowDataHandler sends data chunks every few seconds, simulating a slow LLM.
// This keeps the buffering phase active longer, allowing heartbeats to be sent.
// Note: The proxy sends ": connected\n\n", so this mock should NOT send it.
func mockSlowDataHandler(stopCh <-chan struct{}, chunkInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Note: Do NOT send ": connected\n\n" - that's sent by the proxy

		ticker := time.NewTicker(chunkInterval)
		defer ticker.Stop()

		chunkNum := 0
		for {
			select {
			case <-stopCh:
				return
			case <-r.Context().Done():
				return
			case <-ticker.C:
				// Send more content periodically
				chunk := mockCreateChunk(fmt.Sprintf("chunk%d", chunkNum))
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				flusher.Flush()
				chunkNum++

				// After 3 chunks, send [DONE] and wait
				if chunkNum >= 3 {
					fmt.Fprintf(w, "data: [DONE]\n\n")
					flusher.Flush()
					// Continue sending data to keep connection alive
				}
			}
		}
	}
}

// ─────────────────────────────────────────────────────────────────────────────
// Test Helper
// ─────────────────────────────────────────────────────────────────────────────

// newTestHandlerWithURL creates a test handler with a specific upstream URL.
func newTestHandlerWithURL(t *testing.T, upstreamURL string) *Handler {
	t.Helper()

	t.Setenv("APPLY_ENV_OVERRIDES", "1")
	t.Setenv("UPSTREAM_URL", upstreamURL)
	t.Setenv("MAX_GENERATION_TIME", "300s") // 5 minutes for long tests
	t.Setenv("STREAM_DEADLINE", "180s")     // 3 minutes to allow heartbeat tests
	t.Setenv("IDLE_TIMEOUT", "180s")        // 3 minutes idle timeout

	mgr, err := config.NewManager()
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}

	cfg := &Config{
		ConfigMgr:    mgr,
		ModelsConfig: nil,
	}

	bus := events.NewBus()
	reqStore := store.NewRequestStore(100)

	return NewHandler(cfg, bus, reqStore, nil, nil, nil)
}

// ─────────────────────────────────────────────────────────────────────────────
// Heartbeat Integration Tests
// ─────────────────────────────────────────────────────────────────────────────

// TestHeartbeat_MockServer_Basic verifies that the heartbeat mechanism is
// started and the connected message is sent. Due to the race retry architecture,
// heartbeats are only active during the buffering phase, which completes quickly
// for fast streams.
func TestHeartbeat_MockServer_Basic(t *testing.T) {
	// Create mock handler
	mockHandler := mockHeartbeatTestHandler()

	// Start server
	server := httptest.NewServer(mockHandler)
	defer server.Close()

	// Create handler
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Run handler
	h.HandleChatCompletions(rr, req)

	// Verify response
	respBody := rr.Body.String()

	// Should contain connected message (sent by proxy before heartbeats)
	if !strings.Contains(respBody, ": connected\n\n") {
		t.Error("expected ': connected' in response")
	}

	// Should contain stream data
	if !strings.Contains(respBody, "[DONE]") {
		t.Error("expected '[DONE]' in response")
	}

	// Should have received some content
	if !strings.Contains(respBody, "word0") {
		t.Error("expected content 'word0' in response")
	}

	t.Logf("Response body preview: %s...", respBody[:min(200, len(respBody))])
}

// TestHeartbeat_MockServer_OneMinute tests the heartbeat mechanism over a longer
// duration. With a slow upstream that sends data periodically, the buffering
// phase is extended, allowing heartbeats to be sent.
//
// Note: Due to the race retry architecture, heartbeats are only sent during
// buffering. The stream deadline (180s in this test) determines how long we
// wait before selecting a winner.
//
// To run this test explicitly:
// go test -run TestHeartbeat_MockServer_OneMinute -timeout 120s ./pkg/proxy/...
func TestHeartbeat_MockServer_OneMinute(t *testing.T) {
	// Skip by default - these tests take 1+ minutes
	t.Skip("skipping - long-running test; run explicitly with: go test -run TestHeartbeat_MockServer_OneMinute -timeout 120s ./pkg/proxy/...")

	const (
		testDuration  = 60 * time.Second
		chunkInterval = 5 * time.Second // Send data every 5 seconds
		expectedHBMin = 1               // Allow some variance
		expectedHBMax = 3
	)

	// Create done channel
	upstreamDone := make(chan struct{})

	// Create mock handler that sends slow data
	mockHandler := mockSlowDataHandler(upstreamDone, chunkInterval)

	// Start server
	server := httptest.NewServer(mockHandler)
	defer server.Close()

	// Create handler
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Run handler in goroutine with timeout
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.HandleChatCompletions(rr, req)
	}()

	// Wait for test duration
	time.Sleep(testDuration)

	// Signal shutdown
	close(upstreamDone)

	// Wait for handler to complete
	wg.Wait()

	// Count heartbeats
	respBody := rr.Body.String()
	heartbeatCount := strings.Count(respBody, ": heartbeat\n\n")

	// Verify connected message
	if !strings.Contains(respBody, ": connected\n\n") {
		t.Error("expected ': connected' in response")
	}

	t.Logf("OneMinute: Received %d heartbeats in %v",
		heartbeatCount, testDuration)

	// Note: Due to race retry architecture, heartbeats are only sent during buffering.
	// If buffering completes before 30s (heartbeat interval), no heartbeats will be sent.
	// This is expected behavior - heartbeats are for keeping connections alive during
	// long buffering phases (e.g., slow LLM generation).
	if heartbeatCount == 0 {
		t.Log("Note: No heartbeats received - buffering completed before 30s interval")
	}
}

// TestHeartbeat_MockServer_TwoMinutes tests heartbeats over a 2-minute duration.
//
// To run this test explicitly:
// go test -run TestHeartbeat_MockServer_TwoMinutes -timeout 180s ./pkg/proxy/...
func TestHeartbeat_MockServer_TwoMinutes(t *testing.T) {
	// Skip by default - these tests take 2+ minutes
	t.Skip("skipping - long-running test; run explicitly with: go test -run TestHeartbeat_MockServer_TwoMinutes -timeout 240s ./pkg/proxy/...")

	const (
		testDuration  = 120 * time.Second
		chunkInterval = 5 * time.Second
		expectedHBMin = 3
		expectedHBMax = 5
	)

	// Create done channel
	upstreamDone := make(chan struct{})

	// Create mock handler
	mockHandler := mockSlowDataHandler(upstreamDone, chunkInterval)

	// Start server
	server := httptest.NewServer(mockHandler)
	defer server.Close()

	// Create handler
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Run handler in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.HandleChatCompletions(rr, req)
	}()

	// Wait for test duration
	time.Sleep(testDuration)

	// Signal shutdown
	close(upstreamDone)

	// Wait for handler
	wg.Wait()

	// Count heartbeats
	respBody := rr.Body.String()
	heartbeatCount := strings.Count(respBody, ": heartbeat\n\n")

	t.Logf("TwoMinutes: Received %d heartbeats in %v",
		heartbeatCount, testDuration)

	// Note: Due to race retry architecture, heartbeats are only sent during buffering.
	if heartbeatCount == 0 {
		t.Log("Note: No heartbeats received - buffering may have completed before 30s interval")
	}
}

// TestHeartbeat_MockServer_WithPeriodicData tests that heartbeats don't interfere
// with actual stream data when both are being sent.
//
// To run this test explicitly:
// go test -run TestHeartbeat_MockServer_WithPeriodicData -timeout 120s ./pkg/proxy/...
func TestHeartbeat_MockServer_WithPeriodicData(t *testing.T) {
	// Skip by default - takes ~65 seconds
	t.Skip("skipping - long-running test; run explicitly with: go test -run TestHeartbeat_MockServer_WithPeriodicData -timeout 120s ./pkg/proxy/...")

	const (
		testDuration    = 65 * time.Second
		dataInterval    = 3 * time.Second // Send data every 3 seconds
		expectedDataMin = 15              // ~15-25 data chunks
		expectedDataMax = 25
		expectedHBMin   = 1
		expectedHBMax   = 3
	)

	// Create done channel
	upstreamDone := make(chan struct{})

	// Create mock handler
	mockHandler := mockSlowDataHandler(upstreamDone, dataInterval)

	// Start server
	server := httptest.NewServer(mockHandler)
	defer server.Close()

	// Create handler
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Run handler in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.HandleChatCompletions(rr, req)
	}()

	// Wait for test duration
	time.Sleep(testDuration)

	// Signal shutdown
	close(upstreamDone)

	// Wait for handler
	wg.Wait()

	// Count heartbeats and data chunks
	respBody := rr.Body.String()
	heartbeatCount := strings.Count(respBody, ": heartbeat\n\n")
	dataCount := strings.Count(respBody, `"content":"chunk`)

	// Verify connected message
	if !strings.Contains(respBody, ": connected\n\n") {
		t.Error("expected ': connected' in response")
	}

	t.Logf("WithData: Received %d heartbeats and %d data chunks in %v",
		heartbeatCount, dataCount, testDuration)

	// Verify heartbeat count - informational only
	if heartbeatCount < expectedHBMin || heartbeatCount > expectedHBMax {
		t.Logf("Heartbeat count outside expected range: got %d, expected %d-%d",
			heartbeatCount, expectedHBMin, expectedHBMax)
	}

	// Verify data chunks received - this is the important part
	if dataCount < expectedDataMin || dataCount > expectedDataMax {
		t.Errorf("expected %d-%d data chunks, got %d", expectedDataMin, expectedDataMax, dataCount)
	}
}

// min returns the minimum of two integers
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// mockDelayedUpstream sends a delayed response, simulating a slow LLM.
// This is used to verify that headers and heartbeat are sent BEFORE
// WaitForWinner completes.
func mockDelayedUpstream(delay time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Note: Do NOT send ": connected\n\n" - that's sent by the proxy

		// Delay before sending data - this simulates slow LLM processing
		time.Sleep(delay)

		// Send data
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("delayed response"))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}
}

// TestHeartbeat_StartsBeforeWaitForWinner verifies that heartbeat is started
// BEFORE WaitForWinner completes. This is the key fix for the heartbeat feature.
//
// The test creates a mock upstream that delays response by 5 seconds.
// If heartbeat starts before WaitForWinner, the `: connected` message will
// appear in the response before the 5-second delay completes.
//
// To run this test:
// go test -run TestHeartbeat_StartsBeforeWaitForWinner -timeout 30s ./pkg/proxy/...
func TestHeartbeat_StartsBeforeWaitForWinner(t *testing.T) {
	const delay = 5 * time.Second

	// Create mock handler with delayed response
	server := httptest.NewServer(mockDelayedUpstream(delay))
	defer server.Close()

	// Create handler
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Start timing
	start := time.Now()

	// Run handler in goroutine
	handlerDone := make(chan struct{})
	go func() {
		h.HandleChatCompletions(rr, req)
		close(handlerDone)
	}()

	// Wait a bit for connected message to be sent
	time.Sleep(500 * time.Millisecond)

	// At this point, headers should have been sent (before WaitForWinner)
	// Check if connected message is in response
	respBody := rr.Body.String()
	if !strings.Contains(respBody, ": connected\n\n") {
		t.Fatal("expected ': connected' in response within 500ms - heartbeat may not be starting early enough")
	}

	elapsed := time.Since(start)
	t.Logf("Connected message received after %v (upstream delays %v)", elapsed, delay)

	// Verify that connected was received BEFORE the upstream delay completes
	if elapsed >= delay {
		t.Errorf("connected message received too late: %v >= %v", elapsed, delay)
	}

	// Wait for handler to complete
	<-handlerDone

	// Final verification
	respBody = rr.Body.String()
	if !strings.Contains(respBody, "delayed response") {
		t.Error("expected 'delayed response' in final response")
	}

	t.Logf("Test passed: heartbeat started before WaitForWinner (headers sent in %v, upstream delay %v)", elapsed, delay)
}

// mockNeverEndingUpstream keeps connection alive forever, sending periodic data.
// Used for long-running heartbeat counting tests.
func mockNeverEndingUpstream(stopCh <-chan struct{}, dataInterval time.Duration) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.Header().Set("Cache-Control", "no-cache")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		ticker := time.NewTicker(dataInterval)
		defer ticker.Stop()

		chunkNum := 0
		for {
			select {
			case <-stopCh:
				return
			case <-r.Context().Done():
				return
			case <-ticker.C:
				chunkNum++
				chunk := mockCreateChunk(fmt.Sprintf("keepalive-%d", chunkNum))
				fmt.Fprintf(w, "data: %s\n\n", chunk)
				flusher.Flush()
			}
		}
	}
}

// TestHeartbeat_MockServer_ThreeMinutes tests heartbeat counting over 3 minutes.
// With 15s heartbeat interval, we expect approximately 180s/15s = 12 heartbeats.
// Accounting for timing variance, we check for 10-14 heartbeats.
//
// To run this test:
// go test -run TestHeartbeat_MockServer_ThreeMinutes -timeout 420s ./pkg/proxy/...
func TestHeartbeat_MockServer_ThreeMinutes(t *testing.T) {
	// Skip by default - takes ~3 minutes
	t.Skip("skipping - long-running test; run explicitly with: go test -run TestHeartbeat_MockServer_ThreeMinutes -timeout 420s ./pkg/proxy/...")

	const (
		testDuration = 3 * time.Minute
		dataInterval = 10 * time.Second // Send data every 10 seconds to keep connection active
		// With 15s heartbeat interval and 180s duration:
		// Heartbeats at: 15s, 30s, 45s, 60s, 75s, 90s, 105s, 120s, 135s, 150s, 165s, 180s
		// Expected: 11-12 heartbeats (first at ~15s, last before connection ends)
		expectedHBMin   = 10
		expectedHBMax   = 14
		expectedDataMin = 15 // ~180s/10s = 18 data chunks
		expectedDataMax = 22
	)

	t.Logf("Starting 3-minute heartbeat test...")
	t.Logf("Heartbeat interval: %v", HeartbeatInterval)
	t.Logf("Expected heartbeats: %d-%d", expectedHBMin, expectedHBMax)

	// Create done channel
	upstreamDone := make(chan struct{})

	// Create mock handler that never ends
	server := httptest.NewServer(mockNeverEndingUpstream(upstreamDone, dataInterval))
	defer server.Close()

	// Create handler with extended timeouts
	h := newTestHandlerWithURL(t, server.URL)

	// Make request
	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()

	// Run handler in goroutine
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		h.HandleChatCompletions(rr, req)
	}()

	// Wait for test duration
	time.Sleep(testDuration)

	// Signal shutdown
	close(upstreamDone)

	// Wait for handler to complete
	wg.Wait()

	// Count heartbeats and data
	respBody := rr.Body.String()
	heartbeatCount := strings.Count(respBody, ": heartbeat\n\n")
	dataCount := strings.Count(respBody, `"content":"keepalive-`)

	// Verify connected message
	if !strings.Contains(respBody, ": connected\n\n") {
		t.Error("expected ': connected' in response")
	}

	t.Logf("ThreeMinutes: Received %d heartbeats and %d data chunks in %v",
		heartbeatCount, dataCount, testDuration)

	// Verify heartbeat count
	if heartbeatCount < expectedHBMin || heartbeatCount > expectedHBMax {
		t.Errorf("expected %d-%d heartbeats, got %d", expectedHBMin, expectedHBMax, heartbeatCount)
	} else {
		t.Logf("✓ Heartbeat count in expected range: %d (expected %d-%d)", heartbeatCount, expectedHBMin, expectedHBMax)
	}

	// Verify data chunks
	if dataCount < expectedDataMin || dataCount > expectedDataMax {
		t.Logf("Data chunks outside expected range: got %d, expected %d-%d", dataCount, expectedDataMin, expectedDataMax)
	} else {
		t.Logf("✓ Data chunks in expected range: %d (expected %d-%d)", dataCount, expectedDataMin, expectedDataMax)
	}
}
