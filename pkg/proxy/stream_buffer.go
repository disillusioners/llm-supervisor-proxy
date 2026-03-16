package proxy

import (
	"sync"
	"sync/atomic"
)

// streamBuffer is a thread-safe, bounded buffer for SSE chunks
// Uses notification pattern to avoid blocking writer or corrupting stream
type streamBuffer struct {
	mu         sync.RWMutex
	chunks     [][]byte      // All chunks (protected by mu)
	done       chan struct{} // Closed when stream completes
	notifyCh   chan struct{} // Capacity 1 - signals new data available
	err        error         // Final error (if any)
	totalLen   int64         // Total bytes buffered (atomic access)
	maxBytes   int64         // Maximum bytes to buffer (memory protection)
	overflow   bool          // True if maxBytes exceeded
	completed  int32         // Atomic: 1 when stream done
}

const (
	// MEMORY TRAP FIX: Reduced from 50MB to 5MB per buffer
	// With 3 parallel requests, max ~15MB per client request
	// LLM responses rarely exceed a few megabytes
	defaultMaxBufferBytes = 5 * 1024 * 1024 // 5MB default limit
)

func newStreamBuffer(maxBytes int64) *streamBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBufferBytes
	}
	return &streamBuffer{
		chunks:   make([][]byte, 0, 100),
		done:     make(chan struct{}),
		notifyCh: make(chan struct{}, 1), // Capacity 1 - non-blocking signal
		maxBytes: maxBytes,
	}
}

// Add appends a chunk to the buffer. Thread-safe. Never blocks.
// MEMORY TRAP FIX: Single allocation - allocates once with newline included
// Returns false if buffer overflow (caller should stop).
func (sb *streamBuffer) Add(line []byte) bool {
	// Check if already completed
	if atomic.LoadInt32(&sb.completed) == 1 {
		return false
	}

	// Calculate size with newline
	chunkSize := int64(len(line) + 1) // +1 for newline

	// Check overflow atomically
	newLen := atomic.AddInt64(&sb.totalLen, chunkSize)
	if newLen > sb.maxBytes {
		sb.overflow = true
		return false
	}

	// SINGLE ALLOCATION: Allocate once with newline included
	// This avoids double allocation (once in caller, once here)
	chunkData := make([]byte, chunkSize)
	copy(chunkData, line)
	chunkData[len(line)] = '\n' // Add newline (scanner strips it)

	// Store in slice under lock
	sb.mu.Lock()
	sb.chunks = append(sb.chunks, chunkData)
	sb.mu.Unlock()

	// Send non-blocking notification (signal that new data is available)
	select {
	case sb.notifyCh <- struct{}{}:
	default:
		// Notification already pending - that's fine
	}

	return true
}

// Close marks the buffer as complete. Thread-safe.
func (sb *streamBuffer) Close(err error) {
	if !atomic.CompareAndSwapInt32(&sb.completed, 0, 1) {
		return // Already closed
	}

	sb.mu.Lock()
	sb.err = err
	sb.mu.Unlock()

	// Send final notification and close done channel
	select {
	case sb.notifyCh <- struct{}{}:
	default:
	}
	close(sb.done)
}

// GetChunksFrom returns chunks starting from index. Thread-safe.
// Returns the chunks and the new index to use for next call.
func (sb *streamBuffer) GetChunksFrom(fromIndex int) ([][]byte, int) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	if fromIndex >= len(sb.chunks) {
		return nil, fromIndex
	}

	// Return copy of chunks from index
	chunks := sb.chunks[fromIndex:]
	result := make([][]byte, len(chunks))
	copy(result, chunks)

	// Filter out nil chunks (pruned)
	validCount := 0
	for _, c := range result {
		if c != nil {
			validCount++
		}
	}

	if validCount == 0 {
		return nil, len(sb.chunks)
	}

	finalResult := make([][]byte, 0, validCount)
	for _, c := range result {
		if c != nil {
			finalResult = append(finalResult, c)
		}
	}

	return finalResult, len(sb.chunks)
}

// Prune releases already-read chunks to GC. Thread-safe.
// MEMORY OPTIMIZATION (Phase 1): Call this after successfully sending chunks
// to allow GC to reclaim memory during long streams instead of holding all
// chunks until stream completes. This is critical for long-running streams.
func (sb *streamBuffer) Prune(readIndex int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Set already-read chunks to nil to allow GC to reclaim
	for i := 0; i < readIndex && i < len(sb.chunks); i++ {
		sb.chunks[i] = nil
	}
}

// TotalLen returns total buffered bytes. Thread-safe.
func (sb *streamBuffer) TotalLen() int64 {
	return atomic.LoadInt64(&sb.totalLen)
}

// IsComplete returns true if stream has completed.
func (sb *streamBuffer) IsComplete() bool {
	return atomic.LoadInt32(&sb.completed) == 1
}

// NotifyCh returns the notification channel (signals when new data available).
func (sb *streamBuffer) NotifyCh() <-chan struct{} {
	return sb.notifyCh
}

// Done returns a channel that's closed when the stream completes.
func (sb *streamBuffer) Done() <-chan struct{} {
	return sb.done
}

// Err returns the final error (if any). Thread-safe.
func (sb *streamBuffer) Err() error {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	return sb.err
}
