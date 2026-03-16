# Review of Unified Race Retry Design against Go Memory Traps

## Review Status: ✅ ALL MEMORY TRAPS ADDRESSED

All critical memory traps identified have been fixed in the design document.

---

## Original Issues and Resolutions

### 1. `time.After()` in Loops (Trap 11) - ✅ FIXED

**Original Issue:**  
Using `time.After` inside a `for/select` loop creates a new timer on the heap every iteration. If the upstream provider generates tokens slowly over 5 minutes (300 seconds), this loop will iterate 3000 times, creating 3000 un-garbage-collected timers per request.

**Fix Applied:**  
Replaced `time.After()` with `time.Ticker` created outside the loop:
```go
checkFailedTicker := time.NewTicker(100 * time.Millisecond)
defer checkFailedTicker.Stop()

for {
    select {
    case <-checkFailedTicker.C:
        if rc.checkAllFailed() { ... }
    }
}
```

### 2. Double Allocation of Byte Slices - ✅ FIXED

**Original Issue:**  
When chunks are read via the scanner, a new byte slice is allocated to add the newline character. Then, that new slice is passed to `req.buffer.Add()`, which allocates *another* byte slice and copies the data again.

**Fix Applied:**  
Modified `streamBuffer.Add()` to handle newline internally with single allocation:
```go
// Add now accepts line without newline
func (sb *streamBuffer) Add(line []byte) bool {
    // Single allocation with newline included
    chunkData := make([]byte, len(line) + 1)
    copy(chunkData, line)
    chunkData[len(line)] = '\n'
    // ...
}
```

And updated `executeRequest` to pass line directly:
```go
// No longer creates lineWithNewline here
if !req.buffer.Add(line) { ... }
```

### 3. High Memory Consumption Limits (Trap 18) - ✅ FIXED

**Original Issue:**  
The default buffer size of 50MB per request × 3 parallel requests = 150MB per client. With 100 concurrent streams, this could require up to 15GB of RAM, causing OOM crashes.

**Fix Applied:**  
Reduced `defaultMaxBufferBytes` from 50MB to **5MB**:
```go
const (
    // With 3 parallel requests, max ~15MB per client request
    // LLM responses rarely exceed a few megabytes
    defaultMaxBufferBytes = 5 * 1024 * 1024  // 5MB default limit
)
```

### 4. `bufio.Scanner` with Very Large Tokens - ✅ ALREADY HANDLED

**Observation:** The plan correctly increases the scanner buffer to 4MB max and implements proper slicing/copying. The design handles this well.

### 5. Goroutine / Resource Leaks - ✅ ALREADY HANDLED

**Observation:** The plan successfully includes `defer winner.cancel()` blocks and `defer resp.Body.Close()`. Context tree correctly stops background processing.

---

## Summary

| Issue | Status | Resolution |
|-------|--------|------------|
| `time.After()` in loop | ✅ Fixed | Use `time.Ticker` outside loop |
| Double allocation | ✅ Fixed | Single allocation in `Add()` with newline |
| High memory (50MB) | ✅ Fixed | Reduced to 5MB per buffer |
| bufio.Scanner large tokens | ✅ OK | 4MB max buffer, proper copying |
| Goroutine/resource leaks | ✅ OK | defer cancel/close patterns |

**The design is now safe from memory traps and ready for implementation.**
