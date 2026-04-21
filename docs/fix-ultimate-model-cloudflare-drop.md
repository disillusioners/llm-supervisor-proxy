# Fix: Ultimate Model Not Triggered When Cloudflare Drops Connection

## Problem

When Cloudflare (or any middle proxy) drops the connection between the client and our proxy, the **ultimate model was not being triggered** on subsequent retries.

```
Client > Cloudflare (drops) > OurProxy > Upstream (succeeded)
```

## Root Cause

The `streamResult()` function had multiple issues:

### Issue 1: Silent Return on Client Write Failure

When Cloudflare dropped the connection during streaming:
1. Upstream succeeded and returned data
2. `streamResult()` tried to write chunks to client
3. `w.Write(chunk)` failed with `broken pipe` / `connection reset`
4. **Function silently returned** (`return` with no value)
5. Back in `HandleChatCompletions`, function returned normally at line 613
6. **No `MarkFailed` was called**

```go
// BEFORE (buggy)
for _, chunk := range chunks {
    if _, err := w.Write(chunk); err != nil {
        return  // Silent return - MarkFailed never called!
    }
}
```

### Issue 2: Idle Termination Also Silent

When upstream went idle and connection was already dropped:
1. Idle termination code sent SSE error (useless since connection broken)
2. **No `MarkFailed` was called**
3. Also caused potential resource leak by not calling `winner.Cancel()`

### Issue 3: Duplicate SSE Errors

Idle termination called `winner.Cancel()` which closed the buffer with error. This triggered the `buffer.Done()` case which would send **another** SSE error to the already-broken connection.

## Bug Impact

1. Hash was **never stored** in the cache
2. Next retry → `ShouldTrigger` returns `false` (hash not in cache)
3. **Ultimate model NOT triggered** - user gets repeated failures

## Fix

### Change 1: `streamResult` Returns Error

```go
// AFTER (fixed)
func (h *Handler) streamResult(...) error {
    // ...
    for _, chunk := range chunks {
        if _, err := w.Write(chunk); err != nil {
            return fmt.Errorf("client write failed: %w", err)
        }
    }
    // ...
    return nil
}
```

### Change 2: Caller Handles Error

```go
// In HandleChatCompletions
if rc.isStream {
    if err := h.streamResult(w, rc, winner); err != nil {
        // Client write failed - mark hash for ultimate model retry
        log.Printf("[STREAM] Client write failed: %v", err)
        h.ultimateHandler.MarkFailed(msgMaps)
        return
    }
}
```

### Change 3: Idle Termination Fixed

```go
// In streamResult idle termination case
if winner.IsIdle(rc.conf.IdleTerminationTimeout) {
    // Mark for ultimate model retry
    h.ultimateHandler.MarkFailed(msgMaps)
    
    // Cancel winner to properly close buffer
    winner.Cancel()
    return nil  // Don't send SSE error - connection already broken
}
```

### Change 4: Duplicate Error Guard

```go
// In buffer.Done() case
if err := buffer.Err(); err != nil {
    if idleTerminated {
        // Skip duplicate error
        return nil
    }
    // ... handle error
}
```

## Cases Covered

| Scenario | Upstream State | Behavior |
|----------|----------------|----------|
| Cloudflare drops | Succeeded | Returns error → `MarkFailed` called ✅ |
| Cloudflare drops | Idle/pending | `MarkFailed` called, `winner.Cancel()` proper cleanup ✅ |
| Client disconnects | Any | Already handled via `handleRaceFailure` ✅ |
| Normal completion | Complete | Returns nil, success path ✅ |

## Files Changed

- `pkg/proxy/handler.go`

## Testing

All existing tests pass:
```bash
go build ./... && go test ./pkg/proxy/... -count=1
```
