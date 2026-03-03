# Memory Consumption Analysis

**Date**: 2026-03-03  
**Scope**: LLM Supervisor Proxy - Memory optimization review  
**Status**: Analysis complete, recommendations identified

---

## Executive Summary

This document analyzes memory consumption patterns in the LLM Supervisor Proxy, distinguishing between **intentional design** (necessary for features) and **actual inefficiencies** (candidates for optimization).

### Key Findings

| Category | Memory Impact | Action Required |
|----------|---------------|-----------------|
| Stream Response Buffering | 10-100 MB/request | ✅ **Intentional** - Required for retry capability |
| Request History Storage | ~50 MB (100 requests) | ✅ **Acceptable** - Required for Web UI |
| **HTTP Client Timeout** | **Connection pool** | 🔴 **CRITICAL** - Accumulates over months |
| **SSE Heartbeat Goroutine** | **6 goroutines/hr/stream** | 🔴 **HIGH** - Goroutine leak |
| **MonitoredReader Block** | **1 goroutine/interrupt** | 🔴 **HIGH** - Goroutine leak |
| JSON Re-marshaling in Fallback | 10-50 KB/fallback | 🟡 **Optimize** - Wasteful duplication |
| JSON Unmarshal Redundancy | 5-20 KB/request | 🟡 **Optimize** - Double conversion |
| Response Body Not Closed | 1 connection/error | 🟡 **MEDIUM** - Resource leak |
| Debug Pretty-Printing | 1-5 KB/error | 🟢 **Low Priority** - Minor waste |
| Debug String Conversion | 100 B/chunk | 🟢 **Low Priority** - Minor waste |

**Per-request optimization**: ~15-75 KB  
**Long-term leak fixes**: Prevent system degradation over months

---

## Design Intent: Memory-Intensive Features

### 1. Stream Response Buffering (10-100 MB/request)

**Location**: `pkg/proxy/handler_helpers.go:44-50`

```go
type requestContext struct {
    accumulatedResponse strings.Builder  // Full response text
    accumulatedThinking strings.Builder   // Reasoning content
    streamBuffer         bytes.Buffer      // Raw SSE chunks
}
```

**Purpose**: Enable retry/fallback mid-stream without client awareness

**How it works**:
1. Stream chunks are buffered internally (not sent to client yet)
2. If upstream fails, retry with next fallback model
3. Only send to client after stream completes successfully
4. Client never sees partial failed responses

**Configuration**:
- Default: 10 MB (`MaxStreamBufferSize` in `config.go:130`)
- Hard cap: 100 MB (when unlimited, `handler_functions.go:712`)

**Why this is correct**:
- Sacrifices memory for **reliability**
- Enables transparent fallback (model A fails → model B succeeds)
- Client doesn't need to implement retry logic
- Essential for production LLM applications

**Trade-off accepted**: Higher memory usage for guaranteed retry capability

---

### 2. Request History Storage (~50 MB for 100 requests)

**Location**: `pkg/store/memory.go:54-58`

```go
type RequestStore struct {
    requests []*RequestLog  // Last 100 requests
    maxSize  int            // Default: 100
    ByID     map[string]*RequestLog
}

type RequestLog struct {
    Messages  []Message  // Full conversation history
    Response  string     // Full response text
    Thinking  string     // Reasoning content
    ToolCalls []ToolCall // Tool calls in response
}
```

**Purpose**: Display request history in Web UI for debugging/monitoring

**Memory breakdown** (per request):
- Messages (50 msgs × 1 KB): ~50 KB
- Response: ~10-50 KB
- Thinking: ~5-20 KB
- **Total**: ~65-120 KB/request

**100 requests**: ~6.5-12 MB (typical) | ~50 MB (worst case with long conversations)

**Why this is acceptable**:
- Required for Web UI functionality
- 50 MB is negligible on modern servers (GBs of RAM)
- Capped at 100 requests (configurable in `cmd/main.go:35`)
- Older requests automatically evicted (FIFO)

**Trade-off accepted**: Memory for observability

---

## Actual Memory Issues (Optimization Candidates)

### 🔴 Issue 1: Redundant JSON Re-marshaling in Fallback Loop

**Severity**: MEDIUM  
**Location**: `pkg/proxy/handler_anthropic.go:169-171`  
**Impact**: 10-50 KB per fallback model (3 fallbacks = 30-150 KB)

**Problem**:
```go
// Inside fallback loop
for _, currentModel := range arc.modelList {
    openaiReq["model"] = currentModel
    arc.openaiBody, _ = json.Marshal(openaiReq)  // ❌ Re-marshals ENTIRE request
    // ... make request
}
```

**Why wasteful**:
- Only `model` field changes on each fallback
- Entire request is re-encoded (messages, parameters, system prompts)
- JSON marshaling allocates new bytes every iteration

**Fix** (safe, doesn't break retry):
```go
// Pre-compute base JSON once
baseJSON, _ := json.Marshal(openaiReq)

// In fallback loop, use string replacement for model field
modelJSON := bytes.Replace(baseJSON, 
    []byte(`"model":"` + originalModel + `"`),
    []byte(`"model":"` + currentModel + `"`),
    1)
arc.openaiBody = modelJSON
```

**Savings**: 10-50 KB per fallback iteration

---

### 🔴 Issue 2: Redundant JSON Unmarshal in Internal Request

**Severity**: MEDIUM  
**Location**: `pkg/proxy/handler_anthropic.go:288-293`  
**Impact**: 5-20 KB per internal request attempt

**Problem**:
```go
// Line 150: Struct → Map → Bytes
arc.openaiBody, _ = json.Marshal(openaiReq)

// Line 288-290: Bytes → Map (again)
var openaiReq map[string]interface{}
if err := json.Unmarshal(arc.openaiBody, &openaiReq); err != nil {
    // ...
}
```

**Why wasteful**:
- Double conversion: `struct → map → bytes → map`
- `arc.openaiBody` is already available as `[]byte`
- Unmarshaling allocates new map and all nested structures

**Fix** (safe, requires refactoring):
```go
// Option A: Keep as map throughout, marshal only when needed
type anthropicRequestContext struct {
    openaiReq map[string]interface{}  // Keep as map
    // ...
}

// Option B: Pass bytes directly if InternalHandler supports it
h.internalHandler.Handle(r.Context(), arc.openaiBody, rc)
```

**Savings**: 5-20 KB per internal request

---

### 🟡 Issue 3: Debug JSON Pretty-Printing

**Severity**: LOW  
**Location**: `pkg/proxy/handler_functions.go:357`  
**Impact**: 1-5 KB per error

**Problem**:
```go
if requestJSON, err := json.MarshalIndent(rc.requestBody, "", "  "); err == nil {
    bufferID := fmt.Sprintf("%s_request", rc.reqID)
    if saveErr := h.bufferStore.Save(bufferID, requestJSON); saveErr != nil {
        // ...
    }
}
```

**Why wasteful**:
- `json.MarshalIndent` adds whitespace for human readability
- Indentation increases size by 20-30%
- Buffer store doesn't need pretty-printing

**Fix**:
```go
if requestJSON, err := json.Marshal(rc.requestBody); err == nil {
    // ...
}
```

**Savings**: 1-5 KB per error (reduces size by 20-30%)

---

### 🟡 Issue 4: Debug String Conversion in Stream Loop

**Severity**: LOW  
**Location**: `pkg/proxy/handler_anthropic.go:467`  
**Impact**: ~100 bytes per chunk (only when `DEBUG_ANTHROPIC=1`)

**Problem**:
```go
lineStr := string(line)  // ❌ Converts every chunk
if len(lineStr) > 200 {
    debugLog("Chunk #%d: %s...", chunkCount, lineStr[:200])
} else {
    debugLog("Chunk #%d: %s", chunkCount, lineStr)
}
```

**Why wasteful**:
- `[]byte → string` conversion allocates new string
- Happens on every chunk, even when debug logging is disabled

**Fix**:
```go
if debugAnthropic {  // Only convert when debug enabled
    lineStr := string(line)
    if len(lineStr) > 200 {
        debugLog("Chunk #%d: %s...", chunkCount, lineStr[:200])
    } else {
        debugLog("Chunk #%d: %s", chunkCount, lineStr)
    }
}
```

**Savings**: ~100 bytes per chunk (only in debug mode)

---

## Long-Term Memory & Goroutine Leaks

These issues accumulate over **months of runtime** and can cause system degradation or crashes. Unlike per-request memory issues, these leaks grow over time even with steady traffic.

### 🔴 CRITICAL: HTTP Client Without Timeout

**Location**: `pkg/proxy/handler.go:68`

```go
func NewHandler(...) *Handler {
    return &Handler{
        client: &http.Client{},  // ❌ No timeout configured
        // ...
    }
}
```

**Why it leaks**:
- HTTP client created without timeout or custom transport
- Hung/idle connections accumulate in default connection pool
- Default `MaxIdleConns = 100`, connections never cleaned up
- Failed requests consume connection slots indefinitely

**Impact over months**:
- Moderate traffic (100 req/day) → hundreds of stale connections
- Connection pool exhaustion → new requests fail
- `TIME_WAIT` state connections accumulate

**Fix**:
```go
client: &http.Client{
    Timeout: 5 * time.Minute,
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 100,
        IdleConnTimeout:     90 * time.Second,
    },
}
```

**Severity**: CRITICAL - Guaranteed accumulation

---

### 🔴 HIGH: SSE Heartbeat Nested Goroutine Leak

**Location**: `pkg/proxy/handler_functions.go:489-534`

```go
func (h *Handler) startSSEHeartbeat(w http.ResponseWriter, ctx context.Context) context.CancelFunc {
    go func() {
        ticker := time.NewTicker(10 * time.Second)
        defer ticker.Stop()

        for {
            select {
            case <-ticker.C:
                go func() {  // ❌ Nested goroutine
                    _, err := w.Write(heartbeatData)
                    // ...
                }()

                select {
                case <-time.After(3 * time.Second):
                    // ❌ Timeout - exits without waiting for nested goroutine
                    return
                }
            }
        }
    }()
}
```

**Why it leaks**:
- Nested goroutine spawned every 10 seconds
- If timeout occurs, outer goroutine returns
- Inner goroutine may still be blocked on `w.Write()`
- No synchronization to wait for completion

**Impact over months**:
- Long-running stream (1 hour) = ~360 heartbeats
- If 1% timeout → 3-4 orphaned goroutines per stream
- 1000 streams/month → 3000-4000 goroutines leaked

**Fix**:
```go
var wg sync.WaitGroup
wg.Add(1)
go func() {
    defer wg.Done()
    _, err := w.Write(heartbeatData)
    // ...
}()

select {
case <-time.After(3 * time.Second):
    wg.Wait()  // ✅ Wait for goroutine to complete
    return
}
```

**Severity**: HIGH - Accumulates per long-running stream

---

### 🔴 HIGH: MonitoredReader Goroutine Blocks Forever

**Location**: `pkg/supervisor/monitor.go:23-47`

```go
func (m *MonitoredReader) Read(p []byte) (n int, err error) {
    ch := make(chan readResult, 1)

    go func() {
        n, err := m.reader.Read(p)
        ch <- readResult{n, err}  // ❌ Blocks if caller abandons
    }()

    select {
    case <-timer.C:
        m.reader.Close()
        return 0, ErrIdleTimeout  // ❌ Goroutine still blocked on send
    }
}
```

**Why it leaks**:
- If caller abandons `MonitoredReader` (client disconnect)
- Outer `Read()` returns due to timeout or cancellation
- Inner goroutine blocked on `m.reader.Read()` forever
- Channel send never completes, goroutine never exits

**Impact over months**:
- Every interrupted streaming request = 1 leaked goroutine
- Client network flakiness (5% disconnect rate)
- 10,000 requests/month → 500 leaked goroutines
- Blocked goroutines hold references to `http.Response.Body`

**Fix**:
```go
go func() {
    n, err := m.reader.Read(p)
    select {
    case ch <- readResult{n, err}:
        // Sent successfully
    case <-time.After(100 * time.Millisecond):
        // Caller abandoned, exit gracefully
        return
    }
}()
```

**Alternative**: Use context cancellation:
```go
go func() {
    select {
    case <-ctx.Done():
        return
    default:
        n, err := m.reader.Read(p)
        ch <- readResult{n, err}
    }
}()
```

**Severity**: HIGH - Accumulates per interrupted request

---

### 🟡 MEDIUM: Response Body Not Closed on Error

**Location**: `pkg/proxy/handler_anthropic.go:255-266`

```go
func (h *Handler) doAnthropicRequest(...) bool {
    resp, err := h.client.Do(req)
    if err != nil {
        return false
    }

    if resp.StatusCode != http.StatusOK {
        bodyBytes, _ := io.ReadAll(resp.Body)
        // ❌ resp.Body not closed
        log.Printf("Upstream error: %s", string(bodyBytes))
        return false
    }
}
```

**Why it leaks**:
- Non-200 responses don't close the response body
- Prevents connection reuse until GC runs
- Under high error rates, connection pool exhausted

**Impact over months**:
- 10% error rate, 1000 requests/day → 100 connections leaked/day
- Connection pool (100 max) exhausted in 1 day
- New requests fail with "connection refused"

**Fix**:
```go
if resp.StatusCode != http.StatusOK {
    defer resp.Body.Close()  // ✅ Always close
    bodyBytes, _ := io.ReadAll(resp.Body)
    // ...
}
```

**Also check**: `handler_anthropic.go:403`, `handler_functions.go:346`

**Severity**: MEDIUM - Accumulates under high error rates

---

### 🟡 MEDIUM: SSE Events Handler Cleanup Edge Case

**Location**: `pkg/ui/server.go:248-284`

```go
func (s *Server) handleEvents(w http.ResponseWriter, r *http.Request) {
    sub := s.bus.Subscribe()
    ticker := time.NewTicker(30 * time.Second)
    defer ticker.Stop()

    for {
        select {
        case <-ctx.Done():
            s.bus.Unsubscribe(sub)  // ❌ Not in defer
            return
        }
    }
}
```

**Why it could leak**:
- Cleanup only happens in `ctx.Done()` case
- Panic or unexpected exit skips cleanup
- Subscriber channel remains in event bus
- Event bus subscribers slice grows unbounded

**Impact over months**:
- Unexpected client disconnects (network failure)
- 1000 client connections → 1000 subscriber channels
- Each channel has 100 buffer size
- Memory: 1000 × 100 × ~1KB = ~100 MB wasted

**Fix**:
```go
sub := s.bus.Subscribe()
defer s.bus.Unsubscribe(sub)  // ✅ Guaranteed cleanup

ticker := time.NewTicker(30 * time.Second)
defer ticker.Stop()

for {
    select {
    // ...
    }
}
```

**Severity**: MEDIUM - Accumulates per abandoned connection

---

### Leak Severity Summary

| Issue | Severity | Type | Accumulates Per | Fix Complexity |
|-------|----------|------|-----------------|----------------|
| HTTP Client Timeout | 🔴 CRITICAL | Resource leak | Failed requests | Trivial (1 line) |
| SSE Heartbeat Goroutine | 🔴 HIGH | Goroutine leak | Long streams (6/hr) | Medium (refactor) |
| MonitoredReader Block | 🔴 HIGH | Goroutine leak | Interrupted requests | Medium (add context) |
| Response Body Close | 🟡 MEDIUM | Resource leak | Error responses | Trivial (add defer) |
| SSE Cleanup Defer | 🟡 MEDIUM | Memory leak | Client disconnects | Trivial (move defer) |

---

## Memory Optimization Roadmap

### Phase 1: Critical Leaks (Immediate)
**Estimated impact**: Prevent system crashes after months of runtime

- [ ] **Add HTTP client timeout** (`handler.go:68`) - 1 line fix
- [ ] **Add `defer resp.Body.Close()`** in error paths (`handler_anthropic.go:255,403`)
- [ ] **Move SSE cleanup to `defer`** (`server.go:279`)

**Implementation time**: 30 minutes  
**Risk**: None (trivial fixes)  
**Priority**: CRITICAL - Deploy immediately

---

### Phase 2: Goroutine Leak Fixes (High Priority)
**Estimated impact**: Prevent goroutine accumulation over weeks/months

- [ ] **Fix SSE heartbeat nested goroutine** (`handler_functions.go:502`)
  - Add `sync.WaitGroup` to wait for completion
  - Implementation: 2-3 hours
  - Testing: Verify with long-running streams

- [ ] **Fix MonitoredReader goroutine block** (`monitor.go:32`)
  - Add context cancellation or non-blocking channel send
  - Implementation: 2-3 hours
  - Testing: Verify with interrupted requests

**Implementation time**: 4-6 hours  
**Risk**: Low (doesn't affect normal operation)  
**Priority**: HIGH - Deploy within 1 week

---

### Phase 3: Per-Request Optimizations (Medium Priority)
**Estimated savings**: 15-70 KB per request

- [ ] Fix JSON re-marshaling in fallback loop (Issue 1)
- [ ] Fix JSON unmarshal redundancy (Issue 2)

**Implementation time**: 2-4 hours  
**Risk**: Low (doesn't affect retry/streaming)

---

### Phase 4: Low Priority Optimizations
**Estimated savings**: 1-5 KB per error

- [ ] Replace `json.MarshalIndent` with `json.Marshal` (Issue 3)
- [ ] Guard debug string conversion with `if debugAnthropic` (Issue 4)

**Implementation time**: 30 minutes  
**Risk**: None

### Phase 5: Future Considerations (Not Recommended)

These optimizations were considered but **rejected** due to breaking design intent:

#### ❌ Stream Buffering Optimization
- **Proposal**: Implement true streaming (passthrough mode)
- **Rejected because**: Breaks retry capability mid-stream
- **Trade-off**: Reliability > Memory efficiency

#### ❌ Request History Compression
- **Proposal**: Compress old request logs
- **Rejected because**: CPU overhead, minimal benefit (50 MB is acceptable)
- **Trade-off**: Simplicity > Marginal memory savings

#### ❌ Request History Disk Spillover
- **Proposal**: Move old requests to disk
- **Rejected because**: Adds latency to Web UI, complexity
- **Trade-off**: Performance > Memory savings

---

## Configuration Reference

### Memory-Related Configuration

```json
{
  "max_stream_buffer_size": 5242880,  // 5 MB (default)
  "buffer_storage_dir": "",            // Disk storage for error buffers
  "buffer_max_storage_mb": 100         // Max disk usage for buffers
}
```

**Recommendations**:
- **Default (5 MB)**: Good balance for most use cases
- **Increase to 10-20 MB**: For long responses (code generation, documents)
- **Decrease to 1-2 MB**: For memory-constrained environments (accept shorter retry window)

### Environment Variables

```bash
# Debug mode (increases memory usage slightly)
DEBUG_ANTHROPIC=1  # Enables verbose logging

# Request history size (in cmd/main.go:35)
# Default: 100 requests
# Modify: reqStore := store.NewRequestStore(100)
```

---

## Memory Profiling Guide

### Enable pprof

```go
// Add to cmd/main.go
import _ "net/http/pprof"

go func() {
    log.Println(http.ListenAndServe("localhost:6060", nil))
}()
```

### Profile Memory Usage

```bash
# Heap profile
curl http://localhost:6060/debug/pprof/heap > heap.prof
go tool pprof -http=:8080 heap.prof

# Allocation profile (live)
curl http://localhost:6060/debug/pprof/allocs > allocs.prof
go tool pprof -http=:8080 allocs.prof

# In-use memory
curl http://localhost:6060/debug/pprof/heap?gc=1 > heap_gc.prof
```

### Expected Memory Usage

| Scenario | Requests | Memory Usage |
|----------|----------|--------------|
| Idle | 0 | ~10-20 MB (base) |
| Light load | 10 concurrent | ~100-200 MB |
| Medium load | 50 concurrent | ~300-600 MB |
| Heavy load | 100 concurrent | ~600 MB - 1.2 GB |
| Request history | 100 stored | ~50 MB |

---

## Conclusion

### What We Learned

1. **Stream buffering is intentional** - It's a feature, not a bug (starts small, grows dynamically)
2. **Request history is acceptable** - 50 MB is negligible for observability
3. **Per-request optimizations exist** - JSON re-marshaling wastes 15-70 KB/request
4. **Long-term leaks are CRITICAL** - 5 leaks found that accumulate over months
5. **Trade-offs are explicit** - Reliability > Memory efficiency

### Recommended Actions

**CRITICAL - Deploy Immediately (Phase 1)**:
- ✅ Add HTTP client timeout (`handler.go:68`) - Prevents connection pool exhaustion
- ✅ Add `defer resp.Body.Close()` in error paths - Prevents connection leaks
- ✅ Move SSE cleanup to `defer` - Prevents subscriber accumulation

**HIGH - Deploy Within 1 Week (Phase 2)**:
- 🔴 Fix SSE heartbeat nested goroutine - Prevents goroutine accumulation
- 🔴 Fix MonitoredReader goroutine block - Prevents blocked goroutines

**MEDIUM - When Convenient (Phase 3)**:
- Fix JSON re-marshaling in fallback loop (15-50 KB savings)
- Fix JSON unmarshal redundancy (5-20 KB savings)

**OPTIONAL (Phase 4)**:
- Remove debug pretty-printing (1-5 KB savings)
- Guard debug string conversions (100 B savings)

**Do NOT implement**:
- ❌ True streaming (breaks retry capability)
- ❌ Request history compression (not worth complexity)
- ❌ Disk spillover (adds latency)

### Expected Impact

**After Phase 1 & 2 (Leak Fixes)**:
- ✅ System can run for months without degradation
- ✅ No connection pool exhaustion
- ✅ Goroutine count stays bounded
- ✅ Memory usage stable over time

**After Phase 3 & 4 (Optimizations)**:
- ✅ 15-75 KB saved per request
- ✅ Better memory efficiency for high-throughput scenarios

### Monitoring Recommendations

Deploy these fixes along with monitoring:

```bash
# Track goroutine count (should stay < 100 in idle)
curl http://localhost:6060/debug/pprof/goroutine?debug=1

# Monitor heap allocation (should not grow over time)
curl http://localhost:6060/debug/pprof/heap?debug=1

# Check connection pool usage
# Look for: "Transport connection pool" metrics
```

**Alert thresholds**:
- Goroutines > 200: Warning (potential leak)
- Goroutines > 500: Critical (investigate immediately)
- Heap > 1 GB: Review request patterns
- Heap > 2 GB: Potential leak

### Final Thoughts

The LLM Supervisor Proxy makes **intentional trade-offs** between memory usage and reliability. The current design prioritizes:
1. **Retry capability** (buffered streaming)
2. **Observability** (request history)
3. **Simplicity** (in-memory storage)

However, **long-term resource leaks must be fixed** to ensure production stability. The identified leaks (HTTP timeout, goroutine leaks, unclosed bodies) are genuine bugs that will cause system degradation over months of runtime.

**Priority order**:
1. Fix CRITICAL leaks (Phase 1) - Deploy today
2. Fix HIGH goroutine leaks (Phase 2) - Deploy this week
3. Optimize per-request memory (Phase 3-4) - When convenient

---

## References

### Per-Request Memory
- Stream buffering: `pkg/proxy/handler_helpers.go:44-50`
- Retry logic: `pkg/proxy/handler_functions.go:700-800`
- Request store: `pkg/store/memory.go`
- Configuration: `pkg/config/config.go`
- Anthropic handler: `pkg/proxy/handler_anthropic.go`

### Long-Term Leaks
- HTTP client: `pkg/proxy/handler.go:68`
- SSE heartbeat: `pkg/proxy/handler_functions.go:489-534`
- MonitoredReader: `pkg/supervisor/monitor.go:23-47`
- SSE events handler: `pkg/ui/server.go:248-284`
- Event bus: `pkg/events/bus.go`

### Monitoring
- pprof endpoint: `net/http/pprof`
- Goroutine debugging: `http://localhost:6060/debug/pprof/goroutine`
- Heap profiling: `http://localhost:6060/debug/pprof/heap`
