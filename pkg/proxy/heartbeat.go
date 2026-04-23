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
	HeartbeatInterval = 5 * time.Second
	// HeartbeatWriteTimeout is the timeout for writing heartbeat data
	HeartbeatWriteTimeout = 3 * time.Second
)

// startSSEHeartbeat starts a goroutine that sends SSE comments every 5 seconds
// to keep the client connection alive while buffering upstream data.
// When a client disconnection is detected, onClientDropped is called.
// Uses writeMu to synchronize writes with streamResult to prevent concurrent writes.
// Returns a cancel function to stop the heartbeat.
func (h *Handler) startSSEHeartbeat(w http.ResponseWriter, writeMu *sync.Mutex, onClientDropped func()) context.CancelFunc {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		ticker := time.NewTicker(HeartbeatInterval)
		defer ticker.Stop()

		// Reusable timer for write timeouts to avoid memory leaks from time.After in loop
		writeTimeout := time.NewTimer(HeartbeatWriteTimeout)
		defer writeTimeout.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				// sendHeartbeat returns false if client disconnected
				if !h.sendHeartbeat(w, writeMu, ctx, writeTimeout) {
					log.Printf("[HEARTBEAT] Client disconnected, triggering cancellation")
					// Trigger the callback to cancel the request
					if onClientDropped != nil {
						onClientDropped()
					}
					return
				}
			}
		}
	}()

	return cancel
}

// sendHeartbeat sends a single SSE heartbeat comment to the client.
// Uses writeMu to synchronize with streamResult writes.
// Returns true if heartbeat was sent successfully, false if client disconnected.
func (h *Handler) sendHeartbeat(w http.ResponseWriter, writeMu *sync.Mutex, heartbeatCtx context.Context, writeTimeout *time.Timer) bool {
	heartbeatData := []byte(": heartbeat\n\n")
	clientDisconnected := make(chan bool, 1)
	writeDone := make(chan struct{}, 1)

	// Use WaitGroup to ensure goroutine completes before exiting
	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		// Acquire mutex to synchronize with streamResult writes
		writeMu.Lock()
		defer writeMu.Unlock()

		_, err := w.Write(heartbeatData)
		if err != nil {
			log.Printf("[HEARTBEAT] Write error: %v", err)
			// Non-blocking send to signal client disconnected
			select {
			case clientDisconnected <- true:
			default:
			}
		}
		// Signal write completed
		select {
		case writeDone <- struct{}{}:
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
	case <-writeDone:
		wg.Wait() // Wait for goroutine to complete
		// Flush outside the goroutine to avoid data race
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		log.Printf("Sent heartbeat at %s\n", time.Now().Format(time.RFC3339))
		return true
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
