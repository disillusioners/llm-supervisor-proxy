# Phase 1: Stream Buffer Optimizations

## Objective
Eliminate redundant buffer copies in `stream_buffer.go` — the biggest memory multiplier. `GetAllRawBytes()` allocates the entire response on every call (3-5× per request), and `GetChunksFrom(0)` does double allocation. These are multiplied by 3× for each race request.

## Coupling
- **Depends on**: None
- **Coupling type**: independent
- **Shared files with other phases**: `stream_buffer.go` (also read by Phase 3 via handler.go)
- **Shared APIs/interfaces**: `GetAllRawBytes()` — Phase 3 calls this
- **Why this coupling**: Phase 3 will consume the optimized API from this phase

## Context
- `GetAllRawBytes()` is called at 5 points in `handler.go` (lines 595, 697, 731, 887, 990)
- Each call allocates a new slice sized to the full response
- For a 5MB response × 5 calls × 3 race requests = 75MB of transient allocations per client request
- `GetChunksFrom(0)` does two `make()` calls + filtering, triggered on every buffer notification event

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Add `GetAllRawBytesOnce()` with caching** | New method that caches the result after first call. Returns cached bytes on subsequent calls. Resets cache when new data is appended. Alternative: return `[]byte` view of internal buffer without copy (requires API design decision). | `pkg/proxy/stream_buffer.go` |
| 2 | **Optimize `GetChunksFrom()` for readIndex=0** | When `fromIndex=0` and no nil chunks, return a slice view without copy. Add fast-path for the common case. Avoid the second `make()` + filter allocation. | `pkg/proxy/stream_buffer.go` |
| 3 | **Add threshold-based pruning** | Add `PruneIfAbove(threshold int)` that prunes when buffer exceeds threshold. Called after chunk append when buffer is significantly larger than read position. Reduce reliance on 10ms ticker. | `pkg/proxy/stream_buffer.go`, `pkg/proxy/handler.go` |
| 4 | **Add `InvalidateCache()` method** | Method to explicitly invalidate the cached `GetAllRawBytesOnce()` result. Called when new chunks are appended. Thread-safe with existing RWMutex. | `pkg/proxy/stream_buffer.go` |
| 5 | **Write buffer optimization tests** | Test `GetAllRawBytesOnce()` caching behavior. Test `GetChunksFrom(0)` no-copy path. Test threshold pruning. Add benchmark comparing before/after allocations. | `pkg/proxy/stream_buffer_test.go` |

## Key Files
- `pkg/proxy/stream_buffer.go` — Main optimization target
- `pkg/proxy/stream_buffer_test.go` — Existing 600-line test file, add new tests here

## Implementation Details

### Task 1: `GetAllRawBytesOnce()` Design

```go
// Add to streamBuffer struct:
type streamBuffer struct {
    // ... existing fields ...
    cachedRawBytes []byte    // Cached result
    cacheValid     bool      // Whether cache is valid
}

// New method:
func (sb *streamBuffer) GetAllRawBytesOnce() []byte {
    sb.mu.RLock()
    if sb.cacheValid && sb.cachedRawBytes != nil {
        result := sb.cachedRawBytes
        sb.mu.RUnlock()
        return result
    }
    sb.mu.RUnlock()
    
    // Build and cache
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

// Invalidate cache when new data arrives:
// Call in Append() or wherever chunks are added
func (sb *streamBuffer) invalidateCache() {
    sb.cacheValid = false
    sb.cachedRawBytes = nil
}
```

**Important**: The returned `[]byte` is shared — callers must NOT modify it. Document this clearly.

### Task 2: `GetChunksFrom()` Fast Path

```go
func (sb *streamBuffer) GetChunksFrom(fromIndex int) ([][]byte, int) {
    sb.mu.RLock()
    defer sb.mu.RUnlock()
    
    if fromIndex < 0 {
        fromIndex = 0
    }
    if fromIndex >= len(sb.chunks) {
        return nil, 0
    }
    
    chunks := sb.chunks[fromIndex:]
    
    // Fast path: if fromIndex=0 and no nil chunks, return view
    hasNil := false
    for _, c := range chunks {
        if c == nil {
            hasNil = true
            break
        }
    }
    if !hasNil {
        // Return slice view — no copy needed
        // Caller must NOT modify the returned slices
        return chunks, len(chunks)
    }
    
    // Slow path: filter out nil chunks (existing logic)
    // ... existing filtering code ...
}
```

### Task 3: Threshold-Based Pruning

```go
// In streamBuffer.Append() or a new method:
func (sb *streamBuffer) ShouldPrune(readIndex int) bool {
    return readIndex > 0 && readIndex > len(sb.chunks)/2
}

// In handler.go, replace/augment the 10ms ticker:
// After each GetChunksFrom() call, check if pruning needed
// if buffer.ShouldPrune(readIndex) {
//     buffer.Prune(readIndex)
// }
```

## Constraints
- `streamBuffer` is accessed from multiple goroutines (RWMutex required)
- Pruning sets chunks to `nil` — any cached references to chunk slices become stale
- The buffer is used for both streaming and non-streaming paths
- Existing tests (`stream_buffer_test.go` — 600 lines) must all pass

## Deliverables
- [ ] `GetAllRawBytesOnce()` method with caching
- [ ] `GetChunksFrom()` fast path for no-nil case
- [ ] Threshold-based pruning helper
- [ ] Cache invalidation in `Append()` / write methods
- [ ] New tests for all optimizations
- [ ] All existing `stream_buffer_test.go` tests pass
- [ ] Benchmark showing reduced allocations
