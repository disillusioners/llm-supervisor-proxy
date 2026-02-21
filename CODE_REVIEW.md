# Code Review: LLM Supervisor Proxy

**Date**: 2026-02-21  
**Reviewer**: AI Code Review  
**Scope**: Core proxy implementation, monitoring, event bus, storage, and UI server

---

## Summary

This review identified **11 issues** across the codebase:
- 🔴 **2 Critical** - Data loss and race conditions
- 🟠 **3 High** - Resource leaks and memory safety
- 🟡 **3 Medium** - Buffer limits and deadlocks
- 🟢 **3 Minor** - Validation and UX improvements

---

## 🔴 Critical Bugs

### 1. Store.Add() Silently Drops Updates

**File**: `pkg/store/memory.go:69-79`

**Problem**: When updating an existing request (e.g., status change to "retrying"), the method returns early and discards the update.

```go
if existing, exists := s.ByID[req.ID]; exists {
    _ = existing
    return  // ← BUG: Silently ignores all updates!
}
```

**Impact**:
- Retry status updates are lost
- `reqLog.Retries` count never updates in store
- Dashboard shows stale/incorrect status

**Fix**:
```go
if existing, exists := s.ByID[req.ID]; exists {
    // Update the existing entry in place
    *existing = *req
    return
}
```

---

### 2. Race Condition on Config Access

**File**: `pkg/ui/server.go:92-96`

**Problem**: Config struct is read concurrently in the proxy handler while being written via the API endpoint without synchronization.

```go
// Note: concurrency safety for Config access in Handler needs to be ensured.
// For now, we assume simple atomic-like struct copy or acceptable race for demo.
*s.config = newConfig  // ← RACE: No mutex protection
```

**Impact**:
- Torn reads (partial struct updates visible)
- Inconsistent configuration during request processing
- Potential crashes with pointer fields

**Fix**:
```go
// In Server struct, add mutex or use atomic.Value
type Server struct {
    config   atomic.Value  // Store *proxy.Config
    // ...
}

// On write:
s.config.Store(&newConfig)

// On read:
config := s.config.Load().(*proxy.Config)
```

---

## 🟠 High Priority Bugs

### 3. Goroutine Leak in MonitoredReader

**File**: `pkg/supervisor/monitor.go:30-45`

**Problem**: When timeout occurs, the spawned goroutine may remain blocked even after `Close()` is called.

```go
func (m *MonitoredReader) Read(p []byte) (n int, err error) {
    ch := make(chan readResult, 1)

    go func() {
        n, err := m.reader.Read(p)  // ← May block indefinitely
        ch <- readResult{n, err}    // ← Never read if timeout occurs
    }()

    select {
    case <-timer.C:
        m.reader.Close()           // ← Close doesn't guarantee immediate unblock
        return 0, ErrIdleTimeout   // ← ch is never drained, goroutine leaks
    }
}
```

**Impact**:
- Memory leak under high load with frequent timeouts
- Goroutine count grows unbounded

**Fix**:
```go
select {
case res := <-ch:
    return res.n, res.err
case <-timer.C:
    m.reader.Close()
    // Drain the channel to unblock the goroutine
    go func() { <-ch }()
    return 0, ErrIdleTimeout
}
```

---

### 4. Missing Response Body Close Before Retry

**File**: `pkg/proxy/handler.go:265-266, 388`

**Problem**: When idle timeout triggers a retry, the response body may not be properly closed.

```go
if errors.Is(err, supervisor.ErrIdleTimeout) {
    log.Println("Stream idle timeout detected!")
    // monitor closed the body? ← Assumption may be incorrect
    attempt++
    continue  // ← Body might leak if monitor didn't close it
}
```

**Impact**:
- File descriptor leak
- Connection pool exhaustion over time

**Fix**:
```go
if errors.Is(err, supervisor.ErrIdleTimeout) {
    log.Println("Stream idle timeout detected!")
    monitor.Close()  // Explicitly close
    attempt++
    continue
}
```

---

### 5. No Request Body Size Limit

**File**: `pkg/proxy/handler.go:65`

**Problem**: Request body is read without any size limit.

```go
bodyBytes, err := io.ReadAll(r.Body)  // ← Unlimited memory allocation
```

**Impact**:
- OOM from malicious or malformed requests
- DoS vulnerability

**Fix**:
```go
bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 10*1024*1024)) // 10MB limit
if err != nil {
    http.Error(w, "Failed to read body", http.StatusInternalServerError)
    return
}
```

---

## 🟡 Medium Priority Issues

### 6. Scanner Buffer May Overflow

**File**: `pkg/proxy/handler.go:311`

**Problem**: Default scanner buffer (64KB) may be insufficient for large SSE responses.

```go
scanner := bufio.NewScanner(monitor)
// Default 64KB buffer
```

**Impact**:
- Scanning stops with "token too long" error on large chunks
- Request fails unexpectedly

**Fix**:
```go
scanner := bufio.NewScanner(monitor)
scanner.Buffer(make([]byte, 1024*1024), 1024*1024) // 1MB buffer
```

---

### 7. Event Bus Subscribe Potential Deadlock

**File**: `pkg/events/bus.go:32-34`

**Problem**: Blocking send during history replay could deadlock if conditions align.

```go
for _, evt := range b.history {
    ch <- evt  // ← Blocking send
}
```

**Impact**:
- Potential deadlock if subscriber doesn't drain fast enough
- Currently safe (100 history, 100 buffer), but fragile

**Fix**:
```go
for _, evt := range b.history {
    select {
    case ch <- evt:
    default:
        // Skip if full, or use larger buffer
    }
}
```

---

### 8. HTTP Client Without Timeout

**File**: `pkg/proxy/handler.go:42`

**Problem**: HTTP client has no explicit timeout, relies only on context deadline.

```go
client: &http.Client{},  // ← No timeout
```

**Impact**:
- Connection establishment can hang indefinitely
- Defense-in-depth issue

**Fix**:
```go
client: &http.Client{
    Timeout: 30 * time.Second,  // Connection timeout
},
```

---

### 9. Non-Streaming Retry Loses Progress

**File**: `pkg/proxy/handler.go:259-307`

**Problem**: Smart resume logic only exists for streaming requests. Non-streaming retries start from scratch.

**Impact**:
- Non-streaming requests with idle timeout waste previous work
- Inconsistent behavior between streaming/non-streaming modes

**Recommendation**: Either implement similar smart resume for non-streaming, or document the limitation.

---

## 🟢 Minor Issues

### 10. Missing UpstreamURL Validation

**File**: `pkg/proxy/handler.go:62`

```go
targetURL, _ := url.JoinPath(h.config.UpstreamURL, "/v1/chat/completions")
// ← Error ignored
```

**Fix**: Validate URL at startup, return error on invalid config.

---

### 11. Static File 404 Returns Generic Error

**File**: `pkg/ui/server.go:39`

**Problem**: SPA routes (e.g., `/requests/123`) return 404 instead of serving index.html.

**Fix**: Implement catch-all route for SPA:
```go
mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
    // Try static file first
    // If not found, serve index.html
})
```

---

## Priority Summary

| Priority | Issue | File | Effort |
|----------|-------|------|--------|
| 🔴 P0 | Store.Add() drops updates | `pkg/store/memory.go:69-79` | Low |
| 🔴 P0 | Config race condition | `pkg/ui/server.go:96` | Medium |
| 🟠 P1 | Goroutine leak | `pkg/supervisor/monitor.go:30-45` | Low |
| 🟠 P1 | Missing body close | `pkg/proxy/handler.go:265,388` | Low |
| 🟠 P1 | No body size limit | `pkg/proxy/handler.go:65` | Low |
| 🟡 P2 | Scanner buffer limit | `pkg/proxy/handler.go:311` | Low |
| 🟡 P2 | Subscribe deadlock risk | `pkg/events/bus.go:32-34` | Low |
| 🟡 P2 | HTTP client timeout | `pkg/proxy/handler.go:42` | Low |
| 🟢 P3 | Non-streaming retry | `pkg/proxy/handler.go:259-307` | Medium |
| 🟢 P3 | URL validation | `pkg/proxy/handler.go:62` | Low |
| 🟢 P3 | SPA routing | `pkg/ui/server.go:39` | Medium |

---

## Recommendations

1. **Immediate**: Fix P0 issues (Store.Add, Config race) before next deployment
2. **Short-term**: Address P1 issues to prevent resource leaks
3. **Medium-term**: Add comprehensive test coverage for retry logic
4. **Consider**: Adding structured logging and metrics for production observability

---

## Developer Response & Resolutions

### ✅ Fixed Issues

The following real bugs were acknowledged and fixed:

*   **🔴 1. `Store.Add()` Silently Drops Updates**: Fixed. When the request already exists in `pkg/store/memory.go`, it now updates the existing entry `*existing = *req` instead of returning early.
*   **🔴 2. Race Condition on Config Access**: Fixed. Introduced a `sync.RWMutex` to the `proxy.Config` and safely duplicate it on request start. Updates via API use `CopyFrom(newConfig)` to safely modify it.
*   **🟠 5. No Request Body Size Limit**: Fixed. Implemented an `io.LimitReader(r.Body, 10*1024*1024)` check to cap bodies at 10MB during the initial ingestion phase in `pkg/proxy/handler.go`.
*   **🟡 6. Scanner Buffer May Overflow**: Fixed. Replaced the default 64KB bufio scanner buffer limit with a 1MB limit `scanner.Buffer(buffer, 1024*1024)` to safely handle large SSE reasoning chunks.
*   **🟢 10. Missing UpstreamURL Validation**: Fixed. Now validating the URL immediately with `url.JoinPath(conf.UpstreamURL, ...)` after cloning the safe config instead of ignoring returning errors.
*   **🟢 11. Static File 404 Returns Generic Error**: Fixed. Wrapped `fileServer.ServeHTTP` in `pkg/ui/server.go` to safely fallback missing frontend URL routes to the layout root `/`.

### ❌ False Positives

The code review AI misunderstood Go concurrency and falsely flagged the following:

*  **🟠 3. Goroutine Leak in MonitoredReader (False)**: Claimed `ch <- readResult{n, err}` leaks because it blocks forever if the timeout triggered first. **Reality:** `ch` is initialized as a buffered channel `make(chan readResult, 1)`, so a single write will never block even if no one is reading it. No leak is possible.
*  **🟠 4. Missing Response Body Close Before Retry (False)**: Claimed body closure was missed during `ErrIdleTimeout`. **Reality:** `monitor.Close()` is explicitly called by the `MonitoredReader` internally when the timeout occurs. (A sibling non-timeout defect *did* exist and was fixed though).
*  **🟡 7. Event Bus Subscribe Potential Deadlock (False)**: Claimed history replay blocks subscriber. **Reality:** `ch` is buffered at exactly 100 loops `make(chan Event, 100)` and history is restricted to length 100. Impossible to deadlock on initialization.
*  **🟡 8. HTTP Client Without Timeout (Mitigated)**: Claimed a missing `&http.Client{}` connection timeout allows hangs. **Reality**: The overall `http.NewRequestWithContext` natively binds the stream and socket connection time to `h.config.MaxGenerationTime`.

### 📋 Acknowledged / Won't Fix

*   **🟢 9. Non-Streaming Retry Loses Progress**: Acknowledged as intentional design limitation. Non-streaming requests don't have smart resume. Document if needed.

---

## ✅ Second Review Confirmation (2026-02-21)

**Reviewer**: AI Code Review (Follow-up)

### Verified Fixes

| Issue | Status | Verification |
|-------|--------|--------------|
| Store.Add() updates | ✅ Fixed | `pkg/store/memory.go:70` - Now does `*existing = *req` |
| Config race condition | ✅ Fixed | `pkg/proxy/handler.go:24-50` - Added `sync.RWMutex` with `Clone()` and `CopyFrom()` |
| Request body limit | ✅ Fixed | `pkg/proxy/handler.go:92` - Uses `io.LimitReader(r.Body, 10*1024*1024)` |
| Scanner buffer | ✅ Fixed | `pkg/proxy/handler.go:342-343` - Added 1MB buffer |
| URL validation | ✅ Fixed | `pkg/proxy/handler.go:85-89` - Checks error from `url.JoinPath` |
| SPA routing | ✅ Fixed | `pkg/ui/server.go:40-49` - Fallback to index.html for missing routes |

### Confirmed False Positives

| Issue | Verdict | Reason |
|-------|---------|--------|
| Goroutine leak | ❌ False Positive | Buffered channel `make(chan readResult, 1)` - write never blocks |
| Missing body close | ❌ False Positive | `MonitoredReader.Read()` calls `m.reader.Close()` on timeout (line 44) |
| Event bus deadlock | ❌ False Positive | Channel buffer (100) ≥ history size (100) - impossible to block |
| HTTP client timeout | ❌ False Positive | `http.NewRequestWithContext` binds connection to context deadline |

### New Observations

1. **Monitor.Close() added in non-timeout paths** - Team correctly added explicit `monitor.Close()` calls in error paths (lines 297, 335, 426, 441, 449). Good defensive coding even though timeout path already closes internally.

2. **Config.Clone() pattern is solid** - Using `conf := h.config.Clone()` at handler start ensures consistent config throughout request lifecycle.

---

## Final Status

| Priority | Total | Fixed | False Positive | Remaining |
|----------|-------|-------|----------------|-----------|
| 🔴 P0 | 2 | 2 | 0 | 0 |
| 🟠 P1 | 3 | 1 | 2 | 0 |
| 🟡 P2 | 3 | 1 | 2 | 0 |
| 🟢 P3 | 3 | 2 | 0 | 1 (acknowledged) |

**All critical and high-priority issues resolved.** The codebase is in good shape for production use.
