# Unified Race Retry Design - Review

Overall, the architectural shift to a parallel race mechanism is excellent. It neatly solves the latency issues of sequential retries and the lost progress problem from idle timeouts. The system state machine is simplified significantly by handling multiple concurrent requests gracefully. 

## Review Status: ✅ ALL CRITICAL ISSUES RESOLVED

All four critical issues identified in the initial review have been addressed in the design document.

---

### 1. Deadlock / Stream Corruption in Chunk Streaming (CRITICAL) - ✅ RESOLVED

**Original Issue:**  
In `streamBuffer.Add()`, chunks are sent to a `chunkCh` with a capacity of 64. However, nothing reads from this channel until the `raceCoordinator` declares a winner and `streamResult()` is invoked. 
- If you use a blocking send, any request over 64 chunks before the race ends will block the writer goroutine indefinitely.
- With non-blocking send (`default:` drop), data is preserved in slice but `streamResult` reads from channel, causing stream corruption.

**Solution Applied:** ✅  
Replaced `chunkCh` with notification pattern:
- `notifyCh chan struct{}` (capacity 1) - non-blocking signal only
- Writer appends to `chunks [][]byte` slice under lock, sends notification
- Reader tracks `readIndex` integer, reads slice elements under lock, waits for notification
- `streamResult` updated to use `GetChunksFrom(readIndex)` pattern

---

### 2. Disastrously Slow Byte-by-Byte Reading (CRITICAL) - ✅ RESOLVED

**Original Issue:**  
Reading 1 byte at a time from unbuffered `resp.Body` would throttle CPU and network performance.

**Solution Applied:** ✅  
Using `bufio.Scanner` with increased buffer:
```go
scanner := bufio.NewScanner(resp.Body)
scanner.Buffer(make([]byte, 64*1024), 4*1024*1024) // 64KB initial, 4MB max
for scanner.Scan() {
    line := scanner.Bytes()
    // ...
}
```

---

### 3. Context Cancellation Resource Leak (CRITICAL) - ✅ RESOLVED

**Original Issue:**  
After winner is chosen, `coordinate()` exits. If client disconnects during `streamResult()`, winner's context is never cancelled - upstream continues downloading.

**Solution Applied:** ✅  
Added `defer winner.cancel()` in `HandleChatCompletions`:
```go
defer winner.cancel()

// Stream winner's buffer to client
h.streamResult(w, winner)
```

---

### 4. Bounded Buffer Edge Case (Minor) - ⚠️ ACKNOWLEDGED

**Original Issue:**  
50MB per request × 3 parallel = ~150MB per client request. Under heavy load could exhaust memory.

**Resolution:**  
- Current design uses 50MB limit per buffer as reasonable default
- Future enhancement: Consider global semaphore for memory pressure
- For now, acceptable given typical LLM response sizes

---

## Summary

The conceptual diagram and sequence are solid. All critical issues have been addressed:

| Issue | Status | Resolution |
|-------|--------|------------|
| Chunk channel deadlock/corruption | ✅ Fixed | Notification pattern (slice + notifyCh) |
| Byte-by-byte reading | ✅ Fixed | bufio.Scanner with 4MB max buffer |
| Winner context leak | ✅ Fixed | defer winner.cancel() in handler |
| Memory pressure | ⚠️ Acknowledged | 50MB limit, future global semaphore |

**The design is ready for implementation.**
