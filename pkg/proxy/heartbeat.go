// ─────────────────────────────────────────────────────────────────────────────
// SSE Heartbeat
// ─────────────────────────────────────────────────────────────────────────────

package proxy

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

const (
	// HeartbeatInterval is the interval between SSE heartbeat comments
	HeartbeatInterval = 15 * time.Second
	// HeartbeatWriteTimeout is the timeout for writing heartbeat data
	HeartbeatWriteTimeout = 3 * time.Second
)

// startSSEHeartbeat starts a goroutine that sends SSE comments every 15 seconds
// to keep the client connection alive while buffering upstream data.
// Uses background context so it survives request completion/deadline.
// Returns a cancel function to stop the heartbeat.
func (h *Handler) startSSEHeartbeat(w http.ResponseWriter, ctx context.Context) context.CancelFunc {
	heartbeatCtx, cancel := context.WithCancel(ctx)

	go func() {
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()

		// Reusable timer for write timeouts to avoid memory leaks from time.After in loop
		writeTimeout := time.NewTimer(HeartbeatWriteTimeout)
		defer writeTimeout.Stop()

		for {
			select {
			case <-heartbeatCtx.Done():
				return
			case <-ticker.C:
				// sendHeartbeat returns false if client disconnected
				// In that case, exit the goroutine
				if !h.sendHeartbeat(w, heartbeatCtx, writeTimeout) {
					log.Printf("[HEARTBEAT] Client disconnected, stopping heartbeat goroutine")
					return
				}
			}
		}
	}()

	return cancel
}

// sendHeartbeat sends a single SSE heartbeat comment to the client.
// Returns true if heartbeat was sent successfully, false if client disconnected.
// The goroutine should exit when this returns false.
func (h *Handler) sendHeartbeat(w http.ResponseWriter, heartbeatCtx context.Context, writeTimeout *time.Timer) bool {
	// Send SSE comment as heartbeat - use non-blocking write with timeout
	// to prevent blocking the select loop if the client TCP buffer is full
	heartbeatData := []byte(": heartbeat\n\n")
	written := make(chan bool, 1)
	clientDisconnected := make(chan bool, 1)

	// Use WaitGroup to ensure goroutine completes before exiting
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		_, err := w.Write(heartbeatData)
		if err != nil {
			log.Printf("[HEARTBEAT] Write error: %v", err)
			// Non-blocking send to signal client disconnected
			select {
			case clientDisconnected <- true:
			default:
			}
		}
		// Use non-blocking send to prevent goroutine leak
		select {
		case written <- (err == nil):
		default:
		}
	}()

	// Reset timer for this iteration
	if !writeTimeout.Stop() {
		select {
		case <-writeTimeout.C:
		default:
		}
	}
	writeTimeout.Reset(HeartbeatWriteTimeout)

	// Wait for write to complete or context canceled, with timeout
	select {
	case <-heartbeatCtx.Done():
		wg.Wait() // Wait for goroutine to complete before returning
		return false
	case <-clientDisconnected:
		wg.Wait()    // Wait for goroutine to complete
		return false // Client disconnected
	case ok := <-written:
		if ok {
			log.Printf("Sent heartbeat at %s\n", time.Now().Format(time.RFC3339))
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
		wg.Wait() // Ensure goroutine completes
		return ok
	case <-writeTimeout.C:
		// Timeout - heartbeat write took too long
		log.Printf("[HEARTBEAT] Write timeout, client may be slow or disconnected")
		// Don't wait indefinitely for a blocked write - exit after short delay
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			log.Printf("[HEARTBEAT] Write still blocked after timeout, abandoning goroutine")
		}
		return false // Treat timeout as disconnected
	}
}
