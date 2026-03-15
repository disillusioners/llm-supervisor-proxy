# Kubernetes Pod Crash Report

**Pod**: `llm-supervisor-proxy-7d8dc5fd98-jkjql`  
**Namespace**: `llmproxy`  
**Date**: 2026-03-15  
**Build**: 109

---

## Summary

The pod crashed due to a **nil pointer dereference panic** in the shadow request handling code when processing a stream idle timeout retry.

---

## Timeline

| Time | Event |
|------|-------|
| 13:09:27 | Proxy started successfully on port 4321 |
| 13:10:58 | Stream idle timeout detected on upstream `glm-5` model (144KB buffered) |
| 13:10:58 | Shadow request started to fallback model `MiniMax-M2.5` |
| 13:10:58 | **PANIC**: Nil pointer dereference in shadow request handling |

---

## Root Cause Analysis

### The Architecture Problem

The shadow retry had a **race condition** between the main request goroutine and the shadow goroutine:

```
┌─────────────────────────────────────────────────────────────────┐
│ Main Request Goroutine                                          │
│                                                                  │
│ startShadowRequest()                                            │
│   rc.shadow = &shadowRequestState{...}  ──────┐                │
│   go executeExternalShadowRequest()         │ creates          │
│                                            ▼                    │
└───────────────────────────────────────────────┼─────────────────┘
                                             │ 
                              rc.shadow can be SET TO NIL here
                                             │ by cancelShadow()
                                             ▼
┌─────────────────────────────────────────────────────────────────┐
│ Shadow Goroutine (concurrent)                                    │
│                                                                  │
│ executeExternalShadowRequest()                                  │
│   defer rc.shadow.Cancel()  ◄── CRASH! rc.shadow is nil        │
│   defer rc.shadow.Close()                                        │
│   ...                                                            │
│   rc.shadow.done  ◄── CRASH! nil pointer dereference            │
└─────────────────────────────────────────────────────────────────┘
```

### Where the bug was created

1. **[`handler_shadow.go:112`](pkg/proxy/handler_shadow.go:112)**: Creates `rc.shadow`
2. **[`handler_shadow.go:149`](pkg/proxy/handler_shadow.go:149)**: Starts goroutine with `rc.shadow`
3. **Any time**: Main request calls [`cancelShadow(rc)`](pkg/proxy/handler_helpers.go:170) which sets `rc.shadow = nil`
4. **Shadow goroutine**: Tries to access `rc.shadow.done` → **CRASH**

---

## Fix Applied

### Solution: Local State Ownership

Instead of sharing mutable state (`rc.shadow`) between concurrent goroutines, each shadow goroutine now creates and owns its own local state:

**Before (buggy)**:
```go
func (h *Handler) executeExternalShadowRequest(rc *requestContext, ...) {
    defer rc.shadow.Cancel()    // ← Uses rc.shadow (shared, can be nil)
    defer rc.shadow.Close()
    sendShadowResult(rc.shadow.done, result)
}
```

**After (fixed)**:
```go
func (h *Handler) executeExternalShadowRequest(rc *requestContext, ...) {
    // Create LOCAL state - shadow goroutine owns its own state
    shadowState := &shadowRequestState{
        done:       make(chan shadowResult, 1),
        cancelFunc: shadowCancel,
        // ...
    }
    defer shadowState.Cancel()    // ← Uses local state
    defer shadowState.Close()
    sendShadowResult(shadowState.done, result)  // ← Uses local state
}
```

Now `rc.shadow` is only used as a **flag** (nil vs non-nil) to indicate "shadow is running", not as the state owner.

### Files Modified

1. **[`pkg/proxy/handler_shadow.go`](pkg/proxy/handler_shadow.go)**: 
   - `executeInternalShadowRequest`: Uses local `shadowState` instead of `rc.shadow`
   - `executeExternalShadowRequest`: Uses local `shadowState` instead of `rc.shadow`

2. **[`pkg/proxy/handler_helpers.go`](pkg/proxy/handler_helpers.go)**:
   - Removed nil receiver checks (no longer needed with proper architecture)

---

## Configuration

```json
{
  "version": "1.0",
  "upstream_url": "http://litellm-service.litellm.svc.cluster.local:4000",
  "port": 4321,
  "idle_timeout": "45s",
  "max_generation_time": "8m0s",
  "max_request_time": "10m0s",
  "shadow_retry_enabled": true,
  "loop_detection": {
    "enabled": false
  }
}
```

### Models

| Model | Enabled | Fallback Chain |
|-------|---------|----------------|
| glm-4.5-air | true | [MiniMax-M2.5] |
| glm-5 | true | [MiniMax-M2.5] |
| MiniMax-M2.5 | true | [glm-5] |

---

## Status

- **Build**: ✅ Passes
- **Tests**: ✅ All pass
- **Fix Applied**: Yes (architectural fix)
- **Deployed**: Pending
