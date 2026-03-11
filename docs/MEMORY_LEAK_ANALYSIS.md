# Memory Leak Analysis Report

**Date**: 2026-03-11  
**Issue**: Memory growth in production proxy server, suspected resource/goroutine leaks

## Executive Summary

Analysis of logs showing heartbeats running for 4+ hours (11:51 → 15:47) identified **4 resource leak issues**. The root cause is missing total stream duration enforcement, allowing slow upstreams to keep connections alive indefinitely while accumulating goroutines and buffered data.

---

## Log Evidence

```
2026/03/10 11:51:25 Stream error after headers sent (will retry silently): stream idle timeout
2026/03/10 11:51:25 Stream idle timeout detected!
2026/03/10 11:51:25 Retrying request (attempt 1)...
2026/03/10 11:51:43 Sent heartbeat at 2026-03-10T11:51:43Z
2026/03/10 11:51:53 Sent heartbeat at 2026-03-10T11:51:53Z
...
2026/03/10 12:55:40 Sent heartbeat at 2026-03-10T12:55:40Z
2026/03/10 12:56:07 Sent heartbeat at 2026-03-10T12:56:07Z
...
2026/03/10 15:43:40 Client disconnected
2026/03/10 15:47:26 Client disconnected
```

**Key observation**: Heartbeats ran for ~4 hours, indicating streams weren't properly terminated despite retry at 11:51:25.

---

## Issues Found

### 🔴 Issue 1: No Total Stream Duration Limit (HIGH)

**Location**: `pkg/proxy/handler_functions.go:handleStreamResponse()`

**Problem**: Only `IdleTimeout` is enforced (time between tokens), not total stream duration. A slow upstream sending tokens every 50 seconds (under 60s idle timeout) keeps the stream alive indefinitely.

**Resource Accumulation**:
- Heartbeat goroutine (runs every 10s)
- MonitoredReader Read goroutines (piles up on slow upstream)
- Buffered stream data in `rc.streamBuffer`

**Impact**: Each stuck stream consumes memory indefinitely. With multiple concurrent streams, memory grows unbounded.

---

### 🔴 Issue 2: MonitoredReader.Read() Goroutine Accumulation (HIGH)

**Location**: `pkg/supervisor/monitor.go:30-39`

```go
func (m *MonitoredReader) Read(p []byte) (n int, err error) {
    ch := make(chan readResult, 1)

    go func() {
        n, err := m.reader.Read(p)  // Blocks until data arrives
        select {
        case ch <- readResult{n, err}:
        default:
            // Caller abandoned, exit gracefully
        }
    }()
    // ...
}
```

**Problem**: Each `Read()` call spawns a new goroutine. If upstream is slow:
1. Goroutine blocks on `m.reader.Read(p)`
2. Timer fires → returns `ErrIdleTimeout`
3. Retry starts new attempt → new MonitoredReader
4. Old goroutine still blocked until underlying connection closes
5. Pattern repeats → goroutine accumulation

**Impact**: Under load with slow upstreams, goroutine count can spike, consuming stack memory (8KB+ per goroutine).

---

### 🟡 Issue 3: Config LoadWithContext/SaveWithContext Goroutine Leak (MEDIUM)

**Location**: `pkg/models/config.go:524-549`

```go
func (mc *ModelsConfig) LoadWithContext(ctx context.Context, filePath string) error {
    errCh := make(chan error, 1)

    go func() {
        errCh <- mc.Load(filePath)
    }()

    select {
    case <-ctx.Done():
        return ctx.Err()  // ⚠️ Returns but goroutine continues running!
    case err := <-errCh:
        return err
    }
}
```

**Problem**: When context is canceled, function returns immediately but the goroutine continues executing `mc.Load()`. The buffered channel (size 1) prevents deadlock, but the goroutine leaks until Load() completes.

**Impact**: Low severity (file I/O is usually fast), but improper cleanup.

---

### 🟡 Issue 4: No Panic Recovery in Stream Handlers (MEDIUM)

**Location**: `pkg/proxy/handler_functions.go:handleStreamResponse()`

**Problem**: If a panic occurs (nil pointer, index out of bounds, etc.), the `defer stopHeartbeat()` and `monitor.Close()` won't execute because there's no `recover()` at a higher level.

**Impact**: Panic leaves:
- Heartbeat goroutine running
- Response body unclosed
- Connection resources leaked

---

## Root Cause Analysis

The primary issue is **missing total stream duration enforcement**:

```
Current behavior:
  Stream starts → idle timeout (60s) → retry → idle timeout → retry → ...
  Each attempt can run indefinitely as long as tokens arrive within 60s

Expected behavior:
  Stream starts → total timeout (300s) → abort regardless of idle state
```

When upstream is slow but not idle:
1. Tokens arrive every ~50s (under 60s idle timeout)
2. No retry triggered
3. Heartbeat goroutine keeps running
4. MonitoredReader goroutines accumulate
5. Stream buffer grows
6. Memory consumption increases

---

## Recommended Fixes

### Fix 1: Add Total Stream Duration Check

In `handleStreamResponse()`, add periodic check against `attemptCtx` deadline:

```go
for scanner.Scan() {
    // Check total stream duration
    if attemptCtx.Err() != nil {
        log.Printf("Stream exceeded max generation time, aborting")
        monitor.Close()
        counters.genRetries++
        return attemptContinueRetry
    }
    // ... rest of loop
}
```

### Fix 2: Refactor MonitoredReader to Use Single Goroutine

Replace per-Read goroutine spawning with a single persistent goroutine:

```go
type MonitoredReader struct {
    reader      io.ReadCloser
    idleTimeout time.Duration
    readCh      chan readResult
    done        chan struct{}
}

func NewMonitoredReader(r io.ReadCloser, timeout time.Duration) *MonitoredReader {
    m := &MonitoredReader{
        reader:      r,
        idleTimeout: timeout,
        readCh:      make(chan readResult, 1),
        done:        make(chan struct{}),
    }
    go m.readLoop()
    return m
}

func (m *MonitoredReader) readLoop() {
    defer close(m.readCh)
    buf := make([]byte, 32*1024)
    for {
        n, err := m.reader.Read(buf)
        select {
        case m.readCh <- readResult{data: buf[:n], err: err}:
            if err != nil {
                return
            }
        case <-m.done:
            return
        }
    }
}

func (m *MonitoredReader) Read(p []byte) (int, error) {
    timer := time.NewTimer(m.idleTimeout)
    defer timer.Stop()
    
    select {
    case res := <-m.readCh:
        copy(p, res.data)
        return len(res.data), res.err
    case <-timer.C:
        close(m.done)
        m.reader.Close()
        return 0, ErrIdleTimeout
    }
}

func (m *MonitoredReader) Close() error {
    close(m.done)
    return m.reader.Close()
}
```

### Fix 3: Fix Config Context Leak

Use a cancellable context for the goroutine:

```go
func (mc *ModelsConfig) LoadWithContext(ctx context.Context, filePath string) error {
    ctx, cancel := context.WithCancel(ctx)
    defer cancel() // Ensures goroutine can terminate
    
    errCh := make(chan error, 1)
    
    go func() {
        select {
        case <-ctx.Done():
            errCh <- ctx.Err()
        default:
            errCh <- mc.Load(filePath)
        }
    }()
    
    select {
    case <-ctx.Done():
        return ctx.Err()
    case err := <-errCh:
        return err
    }
}
```

### Fix 4: Add Panic Recovery

Wrap stream handling with recovery:

```go
func (h *Handler) handleStreamResponse(...) (result attemptResult) {
    defer func() {
        if r := recover(); r != nil {
            log.Printf("PANIC in handleStreamResponse: %v", r)
            stopHeartbeat()
            monitor.Close()
            result = attemptBreakToFallback
        }
    }()
    // ... rest of function
}
```

---

## Monitoring Recommendations

1. **Add pprof endpoints** for runtime diagnostics:
   ```go
   import _ "net/http/pprof"
   ```
   Then check: `curl http://localhost:4321/debug/pprof/goroutine?debug=1`

2. **Add metrics for**:
   - Active streams count
   - Goroutine count
   - Stream buffer sizes
   - Stream duration histogram

3. **Add logging for heartbeat lifecycle**:
   ```go
   log.Printf("[HEARTBEAT] Started for request %s", reqID)
   log.Printf("[HEARTBEAT] Stopped for request %s", reqID)
   ```

---

## Fix Implementation & Review

**Review Date**: 2026-03-11  
**Status**: ✅ Completed

### Fixes Implemented

| Issue | Status | Notes |
|-------|--------|-------|
| Issue 1: No Total Stream Duration Limit | ✅ Fixed | Added `attemptCtx.Err()` check in stream loop |
| Issue 2: MonitoredReader Goroutine Accumulation | ✅ Fixed | Refactored to single persistent goroutine |
| Issue 3: Config Context Leak | ⏸️ Deferred | Low severity, file I/O is fast |
| Issue 4: No Panic Recovery | ✅ Fixed | Added `defer recover()` with named return |

### Issues Found During Code Review

The initial fix implementation had **2 critical bugs** that were identified and corrected:

#### Bug 1: Double Close Panic (HIGH 🔴)

**Location**: `pkg/supervisor/monitor.go`

**Problem**: When `Read()` times out, it closed `m.done` channel. If `Close()` was called later, it would panic trying to close an already-closed channel.

```go
// BEFORE (buggy):
case <-timer.C:
    close(m.done)      // Closes channel
    m.reader.Close()
    return 0, ErrIdleTimeout

// Later, Close() would panic:
func (m *MonitoredReader) Close() error {
    close(m.done)      // PANIC: close of closed channel
}
```

**Fix**: Use mutex-protected `closed` flag before closing channel:

```go
// AFTER (fixed):
case <-timer.C:
    m.mu.Lock()
    if !m.closed {
        m.closed = true
        close(m.done)
    }
    m.mu.Unlock()
    m.reader.Close()
    return 0, ErrIdleTimeout
```

#### Bug 2: Wrong Byte Count & Data Loss (HIGH 🔴)

**Location**: `pkg/supervisor/monitor.go`

**Problem**: `Read()` returned the full read size even when caller's buffer was smaller, causing:
1. Incorrect byte count returned
2. Data loss (leftover bytes discarded)
3. Panic when caller tried to copy more bytes than buffer capacity

```go
// BEFORE (buggy):
case res := <-m.readCh:
    copy(p, res.data)
    return res.n, nil  // Returns 32KB even if p is only 5 bytes!
```

**Fix**: Added leftover buffer handling:

```go
// AFTER (fixed):
type MonitoredReader struct {
    // ... other fields
    leftover []byte  // Buffer for data that doesn't fit in caller's buffer
}

func (m *MonitoredReader) Read(p []byte) (n int, err error) {
    // Use leftover data first if available
    if len(m.leftover) > 0 {
        copied := copy(p, m.leftover)
        m.leftover = m.leftover[copied:]
        return copied, nil
    }
    
    // ... select from readCh ...
    
    case res := <-m.readCh:
        copied := copy(p, res.data)
        if copied < len(res.data) {
            m.leftover = res.data[copied:]  // Save leftover for next Read()
        }
        return copied, nil
}
```

### Final Implementation

#### pkg/supervisor/monitor.go

Key changes:
1. Single persistent `readLoop()` goroutine spawned in constructor
2. `sync.WaitGroup` ensures clean shutdown
3. Mutex-protected `closed` flag prevents double-close panic
4. `leftover` buffer handles small caller buffers
5. 5-second timeout on `Close()` to prevent indefinite blocking

#### pkg/proxy/handler_functions.go

Key changes:
1. Added panic recovery with named return value
2. Added `attemptCtx.Err()` check to enforce `MaxGenerationTime`
3. Ensured `monitor.Close()` called in defer

### Test Coverage

Created comprehensive test suite in `pkg/supervisor/monitor_test.go`:

| Test | Description |
|------|-------------|
| `TestMonitoredReader_BasicRead` | Normal read operation |
| `TestMonitoredReader_IdleTimeout` | Timeout fires correctly (~100ms) |
| `TestMonitoredReader_ReadAfterClose` | Error on closed reader |
| `TestMonitoredReader_DoubleClose` | No panic on double close |
| `TestMonitoredReader_EOF` | EOF propagated correctly |
| `TestMonitoredReader_ReadError` | Errors propagated correctly |
| `TestMonitoredReader_ConcurrentClose` | Clean shutdown during read |
| `TestMonitoredReader_BytesReader` | Works with real bytes.Reader |
| `TestMonitoredReader_LargeData` | 100KB data handled correctly |
| `TestMonitoredReader_SmallBuffer` | Small caller buffers work |
| `TestMonitoredReader_TimeoutThenClose` | Close after timeout doesn't panic |

All 11 tests pass.

### Verification Commands

```bash
# Build entire project
go build ./...

# Run supervisor tests
go test -v ./pkg/supervisor/...

# Run proxy stream/heartbeat tests
go test -v ./pkg/proxy/... -run "TestStream|TestHeartbeat"
```

---

## Testing Recommendations

1. **Slow upstream test**: Simulate upstream sending tokens every 50s for 10 minutes
2. **Goroutine leak test**: Use `runtime.NumGoroutine()` before/after requests
3. **Memory profile test**: Run load test and check memory growth with `pprof`
