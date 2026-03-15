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

## Root Cause

A panic occurred in `executeExternalShadowRequest` at `handler_shadow.go:434`, which triggered deferred `Cancel()` and `Close()` calls on the `shadowRequestState`. These methods didn't check for nil receivers, causing a cascading panic.

### Stack Trace

```
panic: runtime error: invalid memory address or nil pointer dereference
[signal SIGSEGV: segmentation violation code=0x1 addr=0x0 pc=0xab8bce]

goroutine 170 [running]:
github.com/disillusioners/llm-supervisor-proxy/pkg/proxy.(*shadowRequestState).Cancel
    /app/pkg/proxy/handler_helpers.go:55 +0xe
panic({0xbb5ca0?, 0x135a660?})
    /usr/local/go/src/runtime/panic.go:860 +0x13a
github.com/disillusioners/llm-supervisor-proxy/pkg/proxy.(*shadowRequestState).Close
    /app/pkg/proxy/handler_helpers.go:43 +0xe
panic({0xbb5ca0?, 0x135a660?})
    /usr/local/go/src/runtime/panic.go:860 +0x13a
github.com/disillusioners/llm-supervisor-proxy/pkg/proxy.(*Handler).executeExternalShadowRequest
    /app/pkg/proxy/handler_shadow.go:434 +0x701
created by github.com/disillusioners/llm-supervisor-proxy/pkg/proxy.(*Handler).startShadowRequest
    /app/pkg/proxy/handler_shadow.go:149 +0x647
```

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

## Fix Applied

Added nil receiver checks to `shadowRequestState.Close()` and `shadowRequestState.Cancel()` in `pkg/proxy/handler_helpers.go`:

```go
func (s *shadowRequestState) Close() {
    if s == nil {
        return
    }
    // ... existing code
}

func (s *shadowRequestState) Cancel() {
    if s == nil {
        return
    }
    // ... existing code
}
```

This prevents the cascading panic when deferred cleanup calls are made on a nil shadow state.

---

## Status

- **Build**: ✅ Passes
- **Fix Applied**: Yes
- **Deployed**: Pending
