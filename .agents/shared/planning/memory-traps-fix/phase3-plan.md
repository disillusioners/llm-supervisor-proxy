# Phase 3: Handler Logging Dedup

## Objective
Replace multiple `GetAllRawBytes()` calls in `handler.go` with a single call-and-reuse pattern. Currently called at 5 locations (lines 595, 697, 731, 887, 990) for logging purposes. After Phase 1's `GetAllRawBytesOnce()`, this phase consumes the optimized API.

## Coupling
- **Depends on**: Phase 1 (uses `GetAllRawBytesOnce()` API)
- **Coupling type**: loose (only depends on method signature, not implementation)
- **Shared files with other phases**: `handler.go` (main file), Phase 1's `stream_buffer.go` (API provider)
- **Shared APIs/interfaces**: `GetAllRawBytesOnce()` — replaces `GetAllRawBytes()` usage for logging
- **Why this coupling**: Phase 1 defines the new API, Phase 3 consumes it

## Context
Current flow (per request, per logging call):
1. Handler calls `GetAllRawBytes()` 
2. Allocates full response buffer (~5MB)
3. Passes to logging function
4. Logging function may also copy it for formatting
5. Original buffer eligible for GC

With Phase 1's caching:
1. First call to `GetAllRawBytesOnce()` caches result
2. Subsequent calls return cached result (no allocation)
3. Memory shared across all logging calls

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Audit all GetAllRawBytes() call sites** | Find every call to `GetAllRawBytes()` in handler.go. Document the purpose (logging, error handling, etc.). Identify which can use the cached version. | `pkg/proxy/handler.go` |
| 2 | **Replace with GetAllRawBytesOnce()** | Replace all 5 logging call sites with the new cached method. Ensure the buffer is available at each call site. | `pkg/proxy/handler.go` |
| 3 | **Add logging-specific helper (optional)** | If logging needs formatting/transformation, create a helper that works with the cached bytes without extra allocation. | `pkg/proxy/handler.go`, `pkg/proxy/handler_helpers.go` |
| 4 | **Update logging calls to accept []byte** | Change logging function signatures from `string` to `[]byte` where possible to avoid string conversion. | `pkg/proxy/handler.go`, `pkg/proxy/handler_helpers.go` |
| 5 | **Verify no duplicate allocations remain** | Add logging/telemetry to confirm single allocation per request. Run benchmark. | `pkg/proxy/handler.go` |

## Key Files
- `pkg/proxy/handler.go` — Main file with 5 call sites (lines 595, 697, 731, 887, 990)
- `pkg/proxy/handler_helpers.go` — Logging helper functions
- `pkg/proxy/stream_buffer.go` — Phase 1's `GetAllRawBytesOnce()` provider

## Call Site Analysis

| Line | Condition | Current | Can Use Cached? |
|------|-----------|---------|----------------|
| 595 | `LogRawUpstreamResponse` (streaming, success) | Full copy | ✅ Yes |
| 697 | `LogRawUpstreamOnError` (streaming, error) | Full copy | ✅ Yes |
| 731 | `LogRawUpstreamResponse` (streaming, success) | Full copy | ✅ Yes |
| 887 | `LogRawUpstreamOnError` (non-stream, error) | Full copy | ✅ Yes |
| 990 | `LogRawUpstreamResponse` (non-stream, success) | Full copy | ✅ Yes |

**All 5 can use `GetAllRawBytesOnce()`** — they all occur after the streaming is complete.

## Implementation Approach

### Pattern: Pass buffer through context or store in request state

```go
// Option 1: Store in request context (if available)
rawBytes := buffer.GetAllRawBytesOnce()
// Pass to all logging calls

// Option 2: Pass buffer reference to helper
func logRawUpstream(buffer *streamBuffer, logFunc func([]byte)) {
    rawBytes := buffer.GetAllRawBytesOnce()
    logFunc(rawBytes)
}
```

### Logging Function Signatures

```go
// Before:
func LogRawUpstreamResponse(response []byte, modelID string, reqID int64) { ... }

// After (accepts []byte directly):
func LogRawUpstreamResponse(rawBytes []byte, modelID string, reqID int64) { ... }
```

## Constraints
- Logging must still work correctly (log content unchanged)
- Cannot hold the cached reference longer than request lifecycle
- `GetAllRawBytesOnce()` returns shared memory — logging must not modify it
- Existing logging tests must pass

## Deliverables
- [ ] All 5 call sites use `GetAllRawBytesOnce()`
- [ ] Logging functions accept `[]byte` directly
- [ ] No extra string conversions in logging path
- [ ] Benchmark shows reduced allocations
- [ ] All existing handler tests pass
