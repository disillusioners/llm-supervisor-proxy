package proxy

import (
	"sync"
	"sync/atomic"
)

// chunkPool provides pooling for byte slices to reduce allocations and GC pressure.
// Using a pool of pre-sized slices avoids repeated allocations for common chunk sizes.
// Pool is keyed by size class (rounded up to nearest 1KB) for better memory efficiency.
type chunkPool struct {
	pool sync.Pool
}

// newChunkPool creates a new chunk pool with the given size class
func newChunkPool() *chunkPool {
	cp := &chunkPool{}
	cp.pool.New = func() interface{} {
		// Default: 4KB chunk (covers most SSE lines)
		return make([]byte, 4*1024)
	}
	return cp
}

// Get retrieves a chunk from the pool, ensuring it's at least the given size
func (cp *chunkPool) Get(minSize int) []byte {
	// Round up to nearest 1KB for pool efficiency
	roundedSize := ((minSize + 1023) / 1024) * 1024

	// For very large chunks, don't pool (avoid memory bloat)
	if roundedSize > 64*1024 {
		return make([]byte, minSize)
	}

	// For small chunks, try to get from pool
	if roundedSize <= 4*1024 {
		if chunk := cp.pool.Get().([]byte); cap(chunk) >= minSize {
			return chunk[:minSize]
		}
		// Pool chunk too small, allocate new
		return make([]byte, minSize)
	}

	// Medium chunks (4KB-64KB): create temporary pool for this size class
	// Use a simpler approach: just allocate
	return make([]byte, minSize)
}

// Put returns a chunk to the pool if it's a reasonable size
func (cp *chunkPool) Put(chunk []byte) {
	if cap(chunk) >= 4*1024 && cap(chunk) <= 64*1024 {
		cp.pool.Put(chunk[:0])
	}
}

// global chunk pool shared across all stream buffers
// This significantly reduces allocation overhead during streaming
var globalChunkPool = newChunkPool()

// streamBuffer is a thread-safe, bounded buffer for SSE chunks
// Uses notification pattern to avoid blocking writer or corrupting stream
type streamBuffer struct {
	mu        sync.RWMutex
	chunks    [][]byte      // All chunks (protected by mu)
	done      chan struct{} // Closed when stream completes
	notifyCh  chan struct{} // Capacity 1 - signals new data available
	err       error         // Final error (if any)
	totalLen  int64         // Total bytes buffered (atomic access)
	maxBytes  int64         // Maximum bytes to buffer (memory protection)
	overflow  uint32        // atomic: 1 if maxBytes exceeded
	completed int32         // Atomic: 1 when stream done

	// Caching for GetAllRawBytesOnce
	cachedRawBytes []byte // Cached result of GetAllRawBytesOnce
	cacheValid     bool   // Whether cache is valid
}

const (
	// defaultMaxBufferBytes is the default maximum bytes to buffer per stream
	// This protects against unbounded memory growth for very long streams
	defaultMaxBufferBytes = 5242880 // 5MB default limit
)

func newStreamBuffer(maxBytes int64) *streamBuffer {
	if maxBytes <= 0 {
		maxBytes = defaultMaxBufferBytes
	}
	return &streamBuffer{
		chunks:   make([][]byte, 0, 32), // Reduced from 100 to 32 - typical streams don't need 100 chunks
		done:     make(chan struct{}),
		notifyCh: make(chan struct{}, 1), // Capacity 1 - non-blocking signal
		maxBytes: maxBytes,
	}
}

// Add appends a chunk to the buffer. Thread-safe. Never blocks.
// Uses sync.Pool for byte slice allocation to reduce GC pressure.
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
		atomic.StoreUint32(&sb.overflow, 1)
		return false
	}

	// Use pooled allocation for better memory efficiency
	// The pool returns slices that are reused across requests
	chunkData := globalChunkPool.Get(int(chunkSize))
	copy(chunkData, line)
	chunkData[len(line)] = '\n' // Add newline (scanner strips it)

	// Store in slice under lock
	sb.mu.Lock()
	sb.chunks = append(sb.chunks, chunkData)
	sb.InvalidateCache() // Cache is invalidated when new data is added
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
	sb.InvalidateCache() // W1: Invalidate cache when closing to prevent stale data
	sb.mu.Unlock()

	// Send final notification and close done channel
	select {
	case sb.notifyCh <- struct{}{}:
	default:
	}
	close(sb.done)
}

// InvalidateCache clears the raw bytes cache. Must hold write lock.
func (sb *streamBuffer) InvalidateCache() {
	sb.cachedRawBytes = nil
	sb.cacheValid = false
}

// GetAllRawBytesOnce returns cached raw bytes if valid, builds cache if not.
// Thread-safe using double-checked locking.
// The returned []byte is shared - callers must NOT modify it.
func (sb *streamBuffer) GetAllRawBytesOnce() []byte {
	sb.mu.RLock()
	if sb.cacheValid && sb.cachedRawBytes != nil {
		result := sb.cachedRawBytes
		sb.mu.RUnlock()
		return result
	}
	sb.mu.RUnlock()

	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Double-check after acquiring write lock
	if sb.cacheValid && sb.cachedRawBytes != nil {
		return sb.cachedRawBytes
	}

	result := make([]byte, 0, sb.totalLen)
	for _, chunk := range sb.chunks {
		if chunk != nil {
			result = append(result, chunk...)
		}
	}
	sb.cachedRawBytes = result
	sb.cacheValid = true
	return result
}

// GetChunksFrom returns chunks starting from index. Thread-safe.
// Returns the chunks and the new index to use for next call.
func (sb *streamBuffer) GetChunksFrom(fromIndex int) ([][]byte, int) {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	if fromIndex >= len(sb.chunks) {
		return nil, fromIndex
	}

	// Fast path: fromIndex=0 with no nil chunks - return defensive copy of slice header
	// Note: fast path only works before Prune() is called, since Prune() sets
	// chunks to nil. This is intentional — after pruning, the slow path handles
	// the sparse chunk slice correctly.
	if fromIndex == 0 {
		hasNil := false
		for _, c := range sb.chunks {
			if c == nil {
				hasNil = true
				break
			}
		}
		if !hasNil {
			// W2: Defensive copy of slice header (~24 bytes) to prevent callers
			// from getting direct reference to internal state
			return append([][]byte(nil), sb.chunks...), len(sb.chunks)
		}
	}

	// Slow path: need to filter out nil chunks
	chunks := sb.chunks[fromIndex:]
	result := make([][]byte, 0, len(chunks))
	for _, c := range chunks {
		if c != nil {
			result = append(result, c)
		}
	}

	if len(result) == 0 {
		return nil, len(sb.chunks)
	}

	return result, len(sb.chunks)
}

// Prune releases already-read chunks to GC. Thread-safe.
// Uses sync.Pool to return chunks for reuse instead of deallocation.
// This is critical for memory efficiency during long streams.
func (sb *streamBuffer) Prune(readIndex int) {
	sb.mu.Lock()
	defer sb.mu.Unlock()

	// Return chunks to pool and set to nil for GC
	for i := 0; i < readIndex && i < len(sb.chunks); i++ {
		if sb.chunks[i] != nil {
			globalChunkPool.Put(sb.chunks[i])
			sb.chunks[i] = nil
		}
	}
	// Invalidate cache since chunks have been modified
	sb.InvalidateCache()
}

// ShouldPrune returns true if pruning would be beneficial.
// Pruning is beneficial when readIndex is past the halfway point of chunks.
func (sb *streamBuffer) ShouldPrune(readIndex int) bool {
	sb.mu.RLock()
	defer sb.mu.RUnlock()
	// W3: Fix boundary - only prune if readIndex is within bounds (not at end)
	return readIndex > 0 && readIndex < len(sb.chunks) && readIndex > len(sb.chunks)/2
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

// GetAllRawBytes returns all buffered chunks as a single byte slice.
// Thread-safe for concurrent access. Used for raw response logging.
func (sb *streamBuffer) GetAllRawBytes() []byte {
	sb.mu.RLock()
	defer sb.mu.RUnlock()

	// Pre-allocate with known total size
	result := make([]byte, 0, sb.totalLen)
	for _, chunk := range sb.chunks {
		if chunk != nil {
			result = append(result, chunk...)
		}
	}
	return result
}
