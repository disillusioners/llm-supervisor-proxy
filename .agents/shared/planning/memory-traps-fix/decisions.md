# Architectural Decisions

## Decision 1: Buffer Copy Strategy for GetAllRawBytes()

**Status**: Accepted (Phase 1)

### Context
`GetAllRawBytes()` currently allocates a new slice on every call. This is called 5 times per request for logging purposes.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Cache result after first call** | Simple, backward compatible | Caller must not modify returned slice |
| B: Return slice view of internal buffer | Zero copy | Caller must not modify; requires more careful API design |
| C: Return copy always (current) | Safe for caller | Memory waste |

### Decision
**Option A: Cache result after first call**

Rationale:
- Simpler than slice view (no special lifetime semantics)
- `GetAllRawBytesOnce()` method name makes caching explicit
- Caller discipline is reasonable (logging functions don't modify data)
- Can add `Clone()` method if caller needs their own copy

### Implementation
```go
func (sb *streamBuffer) GetAllRawBytesOnce() []byte {
    // Thread-safe check-and-set with RWMutex
    // Returns cached result on subsequent calls
}
```

---

## Decision 2: Cancel() Cleanup Approach

**Status**: Accepted (Phase 2)

### Context
When race winner is selected, losers' goroutines need to exit promptly and release resources.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Add cleanup to Cancel()** | Centralized, works with existing callers | Cancel() becomes heavier |
| B: Add new Cleanup() method | Separation of concerns | Callers must remember to call both |
| C: Use sync.Once for cleanup | Idempotent cleanup | Still need to call it |

### Decision
**Option A: Add cleanup to Cancel() + new IsCancelled() method**

Rationale:
- Cancel() is already the public API for aborting requests
- Adding cleanup there ensures resources are always released
- New `IsCancelled()` method for goroutines to check without modifying state
- sync.Once ensures cleanup runs exactly once

### Implementation
```go
func (r *upstreamRequest) Cancel() {
    // Idempotent cancel with cleanup
    r.mu.Lock()
    if r.cancelled { r.mu.Unlock(); return }
    r.cancelled = true
    cancel := r.cancel
    r.mu.Unlock()
    cancel()
    r.cleanup()  // Drain body, close, release buffer
}

func (r *upstreamRequest) IsCancelled() bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.cancelled
}
```

---

## Decision 3: UltimateModel HTTP Client Scope

**Status**: Accepted (Phase 4)

### Context
Per-request HTTP client creation causes GC pressure under load.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Package-level shared client** | Connection reuse, simple | Potential state leakage if client mutates |
| B: Injected client (DI) | Testable, flexible | Larger refactor, more files to change |
| C: `http.DefaultClient` | Zero changes | Not configurable, shared global state |

### Decision
**Option A: Package-level shared client with configurable Transport**

Rationale:
- `http.Client` is safe for concurrent use by design
- Transport is the stateful part but safe for connection pooling
- Minimal code change (1 variable definition)
- Can add package-level configuration for timeouts, etc.

### Implementation
```go
var sharedClient = &http.Client{
    Transport: &http.Transport{
        MaxIdleConns:        100,
        MaxIdleConnsPerHost: 100,
        IdleConnTimeout:     300 * time.Second,
    },
}
```

---

## Decision 4: SSE Data Line Handling

**Status**: Accepted (Phase 4)

### Context
Current implementation accumulates ALL SSE data lines just to extract usage from the last one.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Track last usage-bearing chunk** | Minimal memory, O(1) | Only captures last usage |
| B: Use ring buffer (last N chunks) | Catches usage even if in middle | More complex |
| C: Stream usage extraction | Best memory | Requires parse-as-you-go approach |

### Decision
**Option A: Track last usage-bearing chunk only**

Rationale:
- SSE usage data appears in the last chunk by OpenAI/Anthropic spec
- Simple boolean check: does chunk contain "usage"?
- O(1) memory overhead
- Consistent with internal handler pattern

### Implementation
```go
var lastUsageChunk []byte
// In streaming loop:
if bytes.Contains(data, []byte("usage")) {
    lastUsageChunk = data
}
// At end:
if lastUsageChunk != nil {
    extractUsageFromChunk(lastUsageChunk)
}
```

---

## Decision 5: JSON Unmarshal Strategy

**Status**: Accepted (Phase 5)

### Context
`extractUsageFromSSEChunk()` unmarshals every SSE chunk, but usage appears in only one chunk.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Quick contains check before unmarshal** | Simple, effective | Slight latency on non-usage chunks |
| B: Regex pre-check | More specific | Overkill for simple string check |
| C: Parse only last chunk | Guaranteed | Requires buffering or lookahead |

### Decision
**Option A: Quick `bytes.Contains` check**

Rationale:
- `bytes.Contains(line, []byte("\"usage\":"))` is O(n) with tiny n (one line)
- Avoids map allocation for non-usage chunks
- Simple and readable
- Most SSE chunks don't contain usage anyway

---

## Decision 6: Builder Capacity Hints

**Status**: Accepted (Phase 5)

### Context
`strings.Builder` used for response accumulation grows without capacity hints.

### Options Considered

| Option | Pros | Cons |
|--------|------|------|
| **A: Add Grow(n) with estimate** | Pre-allocates, reduces reallocations | Estimate may be wrong |
| B: Use bytes.Buffer instead | More flexible | Different API, more changes |
| C: Leave as-is | No changes | Potential reallocation waste |

### Decision
**Option A: Add `Grow()` calls with conservative estimates**

Rationale:
- Conservative estimate (e.g., expected response size) is better than 0
- `strings.Builder.Grow()` is idempotent if over-estimate
- Low risk: doesn't break behavior if estimate is wrong
- Easy to tune based on benchmark results
