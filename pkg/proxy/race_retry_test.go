package proxy

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
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
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		if callCount == 1 {
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
		ModelID:            "test-model",
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	
	req, _ := http.NewRequest("POST", "/v1/chat/completions", strings.NewReader(`{"model":"test-model"}`))
	rawBody := []byte(`{"model":"test-model"}`)
	models := []string{"test-model"}

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
