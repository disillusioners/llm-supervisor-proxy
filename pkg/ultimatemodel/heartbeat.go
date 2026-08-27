package ultimatemodel

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"
)

var (
	// HeartbeatInterval is the interval between SSE heartbeat comments.
	// Var (not const) so TestUltimate_HeartbeatRaceVsLiveRelay can shorten
	// it to ~50ms (with t.Cleanup restore) to make the heartbeat-vs-relay
	// concurrency window observable under `go test -race`; the production
	// value is 15 seconds.
	HeartbeatInterval = 15 * time.Second
	// HeartbeatWriteTimeout is the timeout for writing heartbeat data
	HeartbeatWriteTimeout = 3 * time.Second
)

// startSSEHeartbeat starts a goroutine that sends SSE comments every
// HeartbeatInterval to keep the client connection alive while streaming.
// Uses parentCtx so it exits when request completes.
//
// writeMu (Phase 4 / approver iteration 001, Blocking 2) is the
// request-scoped mutex owned by Execute; sendHeartbeat takes it around
// its write+flush so heartbeat comments cannot interleave mid-write
// with the live relay loops' per-chunk write+flush (mirrors the
// proxy-side pattern at pkg/proxy/heartbeat.go).
func startSSEHeartbeat(w http.ResponseWriter, writeMu *sync.Mutex, ctx context.Context) {
	heartbeatCtx, cancel := context.WithCancel(ctx)
	defer cancel()

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
			if !sendHeartbeat(w, writeMu, heartbeatCtx, writeTimeout) {
				log.Printf("[HEARTBEAT][Ultimate] Client disconnected, stopping heartbeat goroutine")
				return
			}
		}
	}
}

// sendHeartbeat sends a single SSE heartbeat comment to the client.
// Returns true if heartbeat was sent successfully, false if client disconnected.
//
// writeMu serializes the heartbeat write AND flush against the live-mode
// per-chunk relay writes (approver iteration 001, Blocking 2). The lock
// covers only the write+flush pair — it is never held across a blocking
// operation other than the client write itself.
func sendHeartbeat(w http.ResponseWriter, writeMu *sync.Mutex, heartbeatCtx context.Context, writeTimeout *time.Timer) bool {
	heartbeatData := []byte(": heartbeat\n\n")
	written := make(chan bool, 1)
	clientDisconnected := make(chan bool, 1)

	var wg sync.WaitGroup
	wg.Add(1)

	go func() {
		defer wg.Done()
		// Acquire the per-request write mutex: the write AND the flush
		// both touch the ResponseWriter, so both go under the lock.
		writeMu.Lock()
		defer writeMu.Unlock()

		_, err := w.Write(heartbeatData)
		if err != nil {
			log.Printf("[HEARTBEAT][Ultimate] Write error: %v", err)
			select {
			case clientDisconnected <- true:
			default:
			}
		}
		if err == nil {
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
		}
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
		wg.Wait()
		return false
	case <-clientDisconnected:
		wg.Wait()
		return false
	case ok := <-written:
		if ok {
			log.Printf("[HEARTBEAT][Ultimate] Sent heartbeat at %s", time.Now().Format(time.RFC3339))
		}
		wg.Wait()
		return ok
	case <-writeTimeout.C:
		log.Printf("[HEARTBEAT][Ultimate] Write timeout, client may be slow or disconnected")
		done := make(chan struct{})
		go func() { wg.Wait(); close(done) }()
		select {
		case <-done:
		case <-time.After(100 * time.Millisecond):
			log.Printf("[HEARTBEAT][Ultimate] Write still blocked after timeout, abandoning goroutine")
		}
		return false
	}
}
