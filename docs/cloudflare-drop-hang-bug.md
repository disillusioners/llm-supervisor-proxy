# Stream Result Hang Bug - Cloudflare Silent Drop

## Issue

When Cloudflare drops a client connection silently, `streamResult()` can hang forever if no new data arrives from upstream.

## Root Cause

The `streamResult()` function in `pkg/proxy/handler.go` has a select loop with these cases:

```go
select {
case <-rc.baseCtx.Done():
    return nil  // Context cancelled
case <-buffer.NotifyCh():
    // New data available - write to client
case <-buffer.Done():
    // Stream complete
case <-ticker.C:
    // Safety backup + idle termination check
}
```

**Problem:** None of these cases detect a dropped client connection when upstream is still sending (or paused):

1. `rc.baseCtx.Done()` - NOT triggered by Cloudflare drop (Go HTTP server doesn't propagate this)
2. `buffer.NotifyCh()` - Only fires when upstream sends NEW data
3. `buffer.Done()` - Only fires when upstream COMPLETES
4. `ticker.C` (10ms) - Only checks for missed notifications, doesn't test connection

**If Cloudflare drops connection:**
- Upstream continues streaming (or pauses)
- No new data → `buffer.NotifyCh()` never fires
- No context cancellation → `rc.baseCtx.Done()` never fires
- Buffer not complete → `buffer.Done()` never fires
- Ticker checks idle termination every 120s (configurable)

**Result:** `streamResult()` hangs forever. Frontend shows "running" forever.

## Solution

Add a client liveness probe that periodically tests if the connection is still writable:

```go
clientLivenessCheck := time.NewTicker(30 * time.Second)
defer clientLivenessCheck.Stop()

select {
    // ... existing cases ...
case <-clientLivenessCheck.C:
    // Write a space to detect dropped connection
    if _, err := fmt.Fprint(w, " "); err != nil {
        return fmt.Errorf("client liveness probe failed: %w", err)
    }
    flusher.Flush()
}
```

**Why a space character?**
- Invisible in SSE output (no `data: ` prefix)
- Forces a write + flush
- Detects connection drop immediately

## Additional Fix

When client write fails, also cancel other parallel requests:

```go
if err := h.streamResult(w, rc, winner); err != nil {
    coordinator.cancelAllExcept(winner)  // Cancel second/fallback
    // ...
}
```

Previously, only the winner was cancelled via defer, leaving second/fallback requests running until idle timeout.

## Timeline After Fix

```
t=0s     Cloudflare drops connection
t=30s    Liveness probe fails
         → streamResult() returns error
         → coordinator.cancelAllExcept(winner)
         → handler returns
         → Frontend receives completion/error event
```
