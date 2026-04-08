# Phase 2: Race Cancel Cleanup

## Objective
Fix response body and buffer leaks when winner is selected in the race pattern. Currently, `cancelAllExcept()` only cancels the context, and `defer resp.Body.Close()` only fires when the goroutine naturally returns. This leaves losing requests holding memory until goroutine cleanup.

## Coupling
- **Depends on**: None
- **Coupling type**: independent
- **Shared files with other phases**: None (Phase 1 touches different code paths in these files)
- **Shared APIs/interfaces**: `upstreamRequest.Cancel()` — used by race_coordinator
- **Why this coupling**: Different logical path (Cancel) than buffer streaming (Phase 1)

## Context
- Race pattern: 3 parallel upstream requests, first winner cancels the other 2
- `cancelAllExcept()` calls `req.Cancel()` which only calls `context.CancelFunc`
- Losers' goroutines may be blocked in `ReadBytes()` — `defer resp.Body.Close()` doesn't fire
- `upstreamRequest.resp` (HTTP response) is NOT cleared
- Loser buffers are NOT released (Prune never called)
- Memory held: 2 losing requests × ~15MB each = ~30MB per client request

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | **Add explicit response body drain/close in Cancel()** | Modify `upstreamRequest.Cancel()` to also drain and close `resp.Body`. Use `io.Copy(ioutil.Discard, resp.Body)` to drain, then `resp.Body.Close()`. Set `req.resp = nil` after close. Thread-safe with existing mutex. | `pkg/proxy/race_request.go` |
| 2 | **Add buffer release in Cancel()** | Modify `upstreamRequest.Cancel()` to also release the buffer if it has one. Call `buffer.Close()` or similar cleanup. | `pkg/proxy/race_request.go` |
| 3 | **Add goroutine exit flag** | Add `cancelled bool` field to signal the execute goroutine to exit immediately. Check this flag in the streaming loop to break out instead of waiting for next ReadBytes. | `pkg/proxy/race_request.go`, `pkg/proxy/race_executor.go` |
| 4 | **Update cancelAllExcept() documentation** | Document that Cancel() now performs full cleanup. | `pkg/proxy/race_coordinator.go` |
| 5 | **Write cancel cleanup tests** | Test that Cancel() properly drains/closes body. Test that goroutine exits promptly. Test concurrent cancels. | `pkg/proxy/race_request_test.go`, `pkg/proxy/race_executor_test.go` |

## Key Files
- `pkg/proxy/race_request.go` — The `Cancel()` method to enhance
- `pkg/proxy/race_executor.go` — The execute goroutine that needs to check cancellation flag
- `pkg/proxy/race_coordinator.go` — Where `cancelAllExcept()` is called
- `pkg/proxy/race_request_test.go` — Existing 541-line test file

## Implementation Details

### Task 1-3: Enhanced Cancel() Implementation

```go
// In race_request.go:

type upstreamRequest struct {
    // ... existing fields ...
    cancelled     bool
    cancelOnce    sync.Once
}

// New Cancel() implementation:
func (r *upstreamRequest) Cancel() {
    r.mu.Lock()
    if r.cancelled {
        r.mu.Unlock()
        return
    }
    r.cancelled = true
    
    // Cancel context first
    cancel := r.cancel
    r.mu.Unlock()
    
    if cancel != nil {
        cancel()
    }
    
    // Then cleanup resources (outside the lock to avoid deadlock)
    r.cleanup()
}

func (r *upstreamRequest) cleanup() {
    r.mu.Lock()
    defer r.mu.Unlock()
    
    // Drain and close response body if present
    if r.resp != nil {
        if r.resp.Body != nil {
            io.Copy(ioutil.Discard, r.resp.Body)  // Drain
            r.resp.Body.Close()
        }
        r.resp = nil
    }
    
    // Release buffer if present
    if r.buffer != nil {
        r.buffer.Close()
        r.buffer = nil
    }
}
```

### In race_executor.go executeRequest goroutine:

```go
// Add cancellation check in the streaming loop
for {
    select {
    case <-readCtx.Done():
        return readCtx.Err()
    default:
    }
    
    // Check if cancelled
    if req.IsCancelled() {  // New method
        return context.Canceled
    }
    
    // Read next chunk
    chunk, err := reader.ReadBytes('\n')
    // ... process chunk ...
}
```

### Task 5: Test Scenarios

```go
// Test: Cancel releases response body
func TestCancelReleasesResponseBody(t *testing.T) {
    // Setup mock server that sends data slowly
    // Create request with slow response
    // Cancel the request mid-stream
    // Verify resp.Body is closed within short timeout
    // Use goroutine leak detection
}

// Test: Cancel exits goroutine promptly
func TestCancelExitsGoroutinePromptly(t *testing.T) {
    // Create slow mock server
    // Start request in goroutine
    // Cancel request
    // Verify goroutine exits within 100ms (not 30s timeout)
    // Use goroutine leak detection pattern
}

// Test: Concurrent cancels are safe
func TestConcurrentCancels(t *testing.T) {
    // Start multiple requests
    // Cancel them from multiple goroutines concurrently
    // Verify no race conditions or panics
}
```

## Constraints
- Must be thread-safe (multiple goroutines may call Cancel)
- Must handle nil `resp.Body` gracefully
- `io.Copy(ioutil.Discard, ...)` may block if body is large — use timeout or short drain
- Existing tests in `race_request_test.go`, `race_executor_test.go` must pass

## Risks & Mitigations

| Risk | Mitigation |
|------|------------|
| `io.Copy` blocks on large body | Add timeout (e.g., 100ms drain window), skip remainder |
| Double-close panic | Use `cancelOnce` pattern or nil-check before Close |
| Deadlock if cleanup holds lock during drain | Release lock before drain, re-acquire to set nil |

## Deliverables
- [ ] `Cancel()` drains and closes `resp.Body`
- [ ] `Cancel()` clears `req.resp` reference
- [ ] `Cancel()` releases buffer
- [ ] Goroutine exits promptly on cancel (cancellation flag check)
- [ ] New tests for cancel cleanup
- [ ] All existing race_executor_test.go and race_request_test.go pass
