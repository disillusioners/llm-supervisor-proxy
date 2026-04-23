# Stream Result Hang Bug - Cloudflare Silent Drop

## Issue

When Cloudflare drops a client connection silently, `streamResult()` could hang forever if no new data arrived from upstream.

## Root Cause

The original `streamResult()` function had a select loop with these cases:

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

**Problems:**

1. **Race Condition (Fixed):** When client disconnects, Go's HTTP server cancels `r.Context()` (baseCtx), AND heartbeat detected write failure and closed `clientGoneCh`. Both channels fire simultaneously → select picks randomly → ~50% chance of silent success instead of error handling.

2. **Concurrent Writes (Fixed):** Heartbeat goroutine and `streamResult()` both called `w.Write()` on the same `ResponseWriter` - not thread-safe.

3. **No disconnect detection:** None of the select cases detected a dropped client when upstream was still sending slowly.

## Solution

### Fix 1: Mutex-Protected Writes
Both heartbeat and `streamResult()` acquire a mutex before writing to ResponseWriter:

```go
// requestContext has:
writeMu sync.Mutex

// streamResult writes:
rc.writeMu.Lock()
if _, err := w.Write(chunk); err != nil {
    rc.writeMu.Unlock()
    return fmt.Errorf("client write failed: %w", err)
}
rc.writeMu.Unlock()

// Heartbeat writes:
writeMu.Lock()
_, err := w.Write(heartbeatData)
writeMu.Unlock()
```

### Fix 2: Heartbeat as Sole Disconnect Detector
Heartbeat is now the only signal for client disconnection:
- Changed interval from 15s → 5s
- On write failure → closes `clientGoneCh`
- `streamResult()` listens for `clientGoneCh` in select
- `baseCtx.Done()` only used for timeout handling (not disconnect detection)

```go
select {
case <-rc.baseCtx.Done():
    return nil  // Timeout - not a client error
case <-rc.clientGoneCh:
    return fmt.Errorf("client disconnected (heartbeat detected)")
case <-buffer.NotifyCh():
    // ...
}
```

### Fix 3: Immediate Cancellation on Error
When client write fails:
```go
coordinator.cancelAllExcept(winner)  // Cancel other parallel requests
```

## Timeline After Fix

```
t=0s     Cloudflare drops connection
t=5s     Heartbeat tries to send, write fails
         → close(clientGoneCh)
         → streamResult() receives from clientGoneCh
         → Returns error
         → coordinator.cancelAllExcept(winner)
         → Handler marks for ultimate model
         → Frontend receives completion/error event
```

## Changes Made

| File | Change |
|------|--------|
| `heartbeat.go` | 5s interval, takes `*sync.Mutex`, mutex used for all writes |
| `handler_helpers.go` | Added `clientGoneCh` and `writeMu` fields |
| `handler.go` | Pass `writeMu` to heartbeat, wrap all writes with mutex |
