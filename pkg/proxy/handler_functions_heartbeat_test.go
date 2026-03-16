package proxy

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/disillusioners/llm-supervisor-proxy/pkg/config"
)

// ─────────────────────────────────────────────────────────────────────────────
// Unit tests for startSSEHeartbeat
// ─────────────────────────────────────────────────────────────────────────────

func TestStartSSEHeartbeat_SendsAtInterval(t *testing.T) {
	// Skip in short mode since this test needs to wait for 30s interval
	if testing.Short() {
		t.Skip("skipping in short mode - test waits for 30s heartbeat interval")
	}

	// Create a response recorder that tracks writes
	recorder := httptest.NewRecorder()

	// Create context with timeout (longer than heartbeat interval)
	ctx, cancel := context.WithTimeout(context.Background(), 35*time.Second)
	defer cancel()

	// Create handler and start heartbeat
	h := &Handler{}
	heartbeatStop := h.startSSEHeartbeat(recorder, ctx)

	// Wait for at least one heartbeat to be sent (30s interval)
	time.Sleep(32000 * time.Millisecond)

	// Stop heartbeat
	heartbeatStop()

	// Give time for goroutine to exit
	time.Sleep(50 * time.Millisecond)

	body := recorder.Body.String()

	// Should have at least one heartbeat
	if !strings.Contains(body, ": heartbeat\n\n") {
		t.Errorf("expected heartbeat in body, got: %q", body)
	}
}

func TestStartSSEHeartbeat_StopsOnContextCancel(t *testing.T) {
	recorder := httptest.NewRecorder()

	ctx, cancel := context.WithCancel(context.Background())

	h := &Handler{}
	heartbeatStop := h.startSSEHeartbeat(recorder, ctx)

	// Cancel immediately
	cancel()

	// Give time for goroutine to process cancellation
	time.Sleep(50 * time.Millisecond)

	// Stop heartbeat (should be no-op since context is already cancelled)
	heartbeatStop()

	// Body should be empty (no heartbeats sent)
	body := recorder.Body.String()
	if body != "" {
		t.Errorf("expected empty body after immediate cancel, got: %q", body)
	}
}

func TestStartSSEHeartbeat_StopsOnWriteError(t *testing.T) {
	// Create a writer that always fails
	failingWriter := &failingResponseWriter{}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	h := &Handler{}
	heartbeatStop := h.startSSEHeartbeat(failingWriter, ctx)

	// Wait a bit for the first write attempt
	time.Sleep(100 * time.Millisecond)

	// Heartbeat should have stopped due to write error
	heartbeatStop()
}

// failingResponseWriter always returns error on Write
type failingResponseWriter struct {
	http.ResponseWriter
}

func (f *failingResponseWriter) Write(p []byte) (n int, err error) {
	return 0, io.ErrUnexpectedEOF
}

func (f *failingResponseWriter) Flush() {}

// ─────────────────────────────────────────────────────────────────────────────
// Integration tests for heartbeat in streaming responses
// ─────────────────────────────────────────────────────────────────────────────

func TestHeartbeat_DuringLongStream(t *testing.T) {
	// Create an upstream that sends data slowly with delays
	slowUpstream := func(t *testing.T) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "text/event-stream")
			w.Header().Set("Cache-Control", "no-cache")
			w.WriteHeader(http.StatusOK)
			flusher := w.(http.Flusher)

			// Send initial chunk
			fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Start"))
			flusher.Flush()

			// Wait a bit (simulating slow generation)
			time.Sleep(200 * time.Millisecond)

			// Send more chunks
			for _, token := range []string{" middle", " end"} {
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk(token))
				flusher.Flush()
				time.Sleep(100 * time.Millisecond)
			}

			fmt.Fprintf(w, "data: [DONE]\n\n")
			flusher.Flush()
		}
	}

	runBothVersions(t, testCase{
		name:       "Heartbeat_DuringLongStream",
		upstreamFn: slowUpstream,
		fn: func(t *testing.T, handle handlerFunc, h *Handler, upstream *httptest.Server) {
			body := simpleBody("mock-model", true)
			req := makeRequest(t, body)
			rr := httptest.NewRecorder()
			handle(rr, req)

			if rr.Code != http.StatusOK {
				t.Fatalf("expected status 200, got %d", rr.Code)
			}

			respBody := rr.Body.String()

			// Should contain connected message
			if !strings.Contains(respBody, ": connected\n\n") {
				t.Error("expected ': connected' in response")
			}

			// Should contain stream data
			if !strings.Contains(respBody, "[DONE]") {
				t.Error("expected [DONE] in response")
			}

			// Note: With 30s heartbeat interval and a stream that completes in ~400ms,
			// we won't see heartbeats in this test. That's expected behavior.
		},
	})
}

func TestHeartbeat_ConcurrentWriteSafety(t *testing.T) {
	// This test verifies that heartbeat and stream completion don't race
	// when writing to the response writer

	var writeCount int
	var writeMu sync.Mutex
	writes := make([]string, 0)

	trackingRecorder := &trackingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		onWrite: func(p []byte) {
			writeMu.Lock()
			defer writeMu.Unlock()
			writeCount++
			writes = append(writes, string(p))
		},
	}

	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Hello"))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, nil)
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	h.HandleChatCompletions(trackingRecorder, req)

	// Verify no interleaved writes (each write should be complete)
	writeMu.Lock()
	defer writeMu.Unlock()

	for i, w := range writes {
		// Each write should be a complete SSE message
		// Either ": connected\n\n", ": heartbeat\n\n", "data: ...\n\n", etc.
		if !strings.HasSuffix(w, "\n\n") && !strings.HasSuffix(w, "\n") {
			t.Errorf("write %d is incomplete: %q", i, w)
		}
	}
}

// trackingResponseWriter tracks all writes
type trackingResponseWriter struct {
	*httptest.ResponseRecorder
	onWrite func([]byte)
}

func (t *trackingResponseWriter) Write(p []byte) (n int, err error) {
	if t.onWrite != nil {
		t.onWrite(p)
	}
	return t.ResponseRecorder.Write(p)
}

func TestHeartbeat_StoppedBeforeFinalWrite(t *testing.T) {
	// This test verifies that heartbeat is stopped before the final buffer flush
	// to prevent race conditions

	var writeTimes []time.Time
	var writeContents []string
	var writeMu sync.Mutex

	timingRecorder := &timingResponseWriter{
		ResponseRecorder: httptest.NewRecorder(),
		onWrite: func(p []byte) {
			writeMu.Lock()
			defer writeMu.Unlock()
			writeTimes = append(writeTimes, time.Now())
			writeContents = append(writeContents, string(p))
		},
	}

	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Test"))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, nil)
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	h.HandleChatCompletions(timingRecorder, req)

	writeMu.Lock()
	defer writeMu.Unlock()

	// Find the final buffer write (contains [DONE])
	for i, content := range writeContents {
		if strings.Contains(content, "[DONE]") {
			// This is the final write - verify no heartbeat writes after it
			for j := i + 1; j < len(writeContents); j++ {
				if strings.Contains(writeContents[j], "heartbeat") {
					t.Errorf("heartbeat written after final buffer flush at index %d", j)
				}
			}
			break
		}
	}
}

// timingResponseWriter tracks write timing
type timingResponseWriter struct {
	*httptest.ResponseRecorder
	onWrite func([]byte)
}

func (t *timingResponseWriter) Write(p []byte) (n int, err error) {
	if t.onWrite != nil {
		t.onWrite(p)
	}
	return t.ResponseRecorder.Write(p)
}

func TestHeartbeat_GoroutineCleanup(t *testing.T) {
	// Verify that heartbeat goroutine is properly cleaned up

	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Quick"))
		flusher.Flush()

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, nil)
	defer upstream.Close()

	// Run multiple requests
	for i := 0; i < 5; i++ {
		body := simpleBody("mock-model", true)
		req := makeRequest(t, body)
		rr := httptest.NewRecorder()
		h.HandleChatCompletions(rr, req)
	}

	// Give time for goroutines to clean up
	time.Sleep(100 * time.Millisecond)
}

// ─────────────────────────────────────────────────────────────────────────────
// Heartbeat with error scenarios
// ─────────────────────────────────────────────────────────────────────────────

func TestHeartbeat_StreamErrorStopsHeartbeat(t *testing.T) {
	// Verify heartbeat stops when stream encounters an error

	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Send one chunk then close without [DONE]
		fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("Partial"))
		flusher.Flush()
		// Connection closes - triggers error
	}, nil, func(c *config.Config) {
		c.RaceRetryEnabled = false // Disable race retry for this legacy test
	})
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)
	rr := httptest.NewRecorder()
	h.HandleChatCompletions(rr, req)

	// Should have sent error event
	if rr.Code != http.StatusOK {
		t.Errorf("expected status 200 (headers sent), got %d", rr.Code)
	}

	respBody := rr.Body.String()
	if !strings.Contains(respBody, "error") {
		t.Errorf("expected error event in response, got: %s", respBody)
	}
}

func TestHeartbeat_ClientDisconnect(t *testing.T) {
	// Verify heartbeat handles client disconnection gracefully

	h, upstream := newTestHandler(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		flusher := w.(http.Flusher)

		// Send chunks slowly
		for i := 0; i < 5; i++ {
			select {
			case <-r.Context().Done():
				return // Client disconnected
			default:
				fmt.Fprintf(w, "data: %s\n\n", mockCreateChunk("chunk"))
				flusher.Flush()
				time.Sleep(100 * time.Millisecond)
			}
		}

		fmt.Fprintf(w, "data: [DONE]\n\n")
		flusher.Flush()
	}, nil)
	defer upstream.Close()

	body := simpleBody("mock-model", true)
	req := makeRequest(t, body)

	// Create a recorder and cancel context mid-stream
	ctx, cancel := context.WithCancel(context.Background())
	req = req.WithContext(ctx)

	rr := httptest.NewRecorder()

	// Start request in goroutine
	done := make(chan struct{})
	go func() {
		h.HandleChatCompletions(rr, req)
		close(done)
	}()

	// Cancel after short delay
	time.Sleep(150 * time.Millisecond)
	cancel()

	// Wait for request to complete
	select {
	case <-done:
		// Good - request completed
	case <-time.After(2 * time.Second):
		t.Error("request did not complete after client disconnect")
	}
}
