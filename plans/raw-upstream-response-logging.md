# Raw Upstream Response Logging Plan

## Overview
Add optional logging of raw upstream responses to files, with clickable links in the event log UI. Default disabled. This reuses the existing BufferStore infrastructure.

## Architecture

```
Request Flow with Logging:
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Client    │────►│   Proxy     │────►│  Upstream   │
└─────────────┘     └─────────────┘     └─────────────┘
                          │
                          ▼
                   ┌─────────────────┐
                   │  StreamBuffer   │ (already captures chunks)
                   └─────────────────┘
                          │
                          ▼ (if LogRawUpstreamResponse=true)
                   ┌─────────────────┐
                   │  BufferStore    │ → saves to file
                   └─────────────────┘
                          │
                          ▼
                   ┌─────────────────┐
                   │   Event Bus     │ → emits response_logged event
                   └─────────────────┘
                          │
                          ▼
                   ┌─────────────────┐
                   │   Event Log UI  │ → shows clickable link
                   └─────────────────┘
```

## Configuration

### New Config Fields (`pkg/config/config.go`)

```go
type Config struct {
    // ...existing...
    
    // Raw response logging
    LogRawUpstreamResponse bool `json:"log_raw_upstream_response"` // Log successful responses (default: false)
    LogRawUpstreamOnError  bool `json:"log_raw_upstream_on_error"` // Log failed responses (default: false)
    LogRawUpstreamMaxKB    int  `json:"log_raw_upstream_max_kb"`   // Max KB per response (default: 1024)
}
```

### Validation
```go
// In Validate():
if (c.LogRawUpstreamResponse || c.LogRawUpstreamOnError) && c.BufferStorageDir == "" {
    return errors.New("buffer_storage_dir is required when raw response logging is enabled")
}
```

## Backend Changes

### 1. Add `GetAllRawBytes()` to StreamBuffer (`pkg/proxy/stream_buffer.go`)

```go
// GetAllRawBytes returns all buffered chunks as a single byte slice.
// Thread-safe for concurrent access.
func (sb *streamBuffer) GetAllRawBytes() []byte {
    sb.mu.RLock()
    defer sb.mu.RUnlock()
    
    result := make([]byte, 0, sb.totalLen)
    for _, chunk := range sb.chunks {
        if chunk != nil {
            result = append(result, chunk...)
        }
    }
    return result
}
```

### 2. Add `saveRawResponse()` to Handler (`pkg/proxy/handler.go`)

```go
// saveRawResponse saves the raw upstream response to disk and emits an event.
// This is a best-effort operation - errors are logged but don't fail the request.
// Should be called in a goroutine to avoid blocking the response.
func (h *Handler) saveRawResponse(requestID string, buffer *streamBuffer, rawRequestBody []byte) {
    rawBytes := buffer.GetAllRawBytes()
    maxBytes := int64(h.config.ConfigMgr.Get().LogRawUpstreamMaxKB) * 1024
    
    // Skip if too large
    if int64(len(rawBytes)) > maxBytes {
        log.Printf("[RAW-LOG] Response too large: %d > %d limit (request=%s)", 
            len(rawBytes), maxBytes, requestID)
        return
    }
    
    // Save response
    bufferID := fmt.Sprintf("%s-response", requestID)
    if err := h.bufferStore.Save(bufferID, rawBytes); err != nil {
        log.Printf("[RAW-LOG] Failed to save response: %v (request=%s)", err, requestID)
        return
    }
    
    // Save request body (for correlation) - optional but useful for debugging
    var requestBodyID string
    if len(rawRequestBody) > 0 && int64(len(rawRequestBody)) <= maxBytes {
        requestBodyID = fmt.Sprintf("%s-request", requestID)
        if err := h.bufferStore.Save(requestBodyID, rawRequestBody); err != nil {
            log.Printf("[RAW-LOG] Failed to save request body: %v (request=%s)", err, requestID)
            requestBodyID = "" // Clear on error
        }
    }
    
    // Emit event
    h.publishEvent("response_logged", map[string]interface{}{
        "id":              requestID,
        "buffer_id":       bufferID,
        "request_body_id": requestBodyID,
        "size_bytes":      len(rawBytes),
    })
}
```

### 3. Call Sites in Handler (`pkg/proxy/handler.go`)

After streaming completes (success path):
```go
// In streamResult(), after request_completed event (~line 662):
if rc.conf.LogRawUpstreamResponse {
    go h.saveRawResponse(rc.reqID, winner.GetBuffer(), rc.rawBody)
}
```

After streaming error:
```go
// In streamResult(), in error handling (~line 599):
if rc.conf.LogRawUpstreamOnError {
    go h.saveRawResponse(rc.reqID, winner.GetBuffer(), rc.rawBody)
}
```

Same pattern for `handleNonStreamResult()` (non-streaming responses).

### 4. Handler Struct Update

Ensure `handler.go` has access to the raw request body. May need to add to `requestContext`:
```go
type requestContext struct {
    // ...existing...
    rawBody []byte  // Raw request body for logging
}
```

## Frontend Changes

### 1. Event Types (`pkg/ui/frontend/src/types.ts`)

```typescript
// Add to EventType union
type EventType = 
  | 'request_started'
  | 'response_logged'  // NEW
  | 'request_completed'
  | 'request_failed'
  // ... rest
```

### 2. Event Log Component (`pkg/ui/frontend/src/components/EventLog.tsx`)

Add case for `response_logged` in `getEventMessage()`:
```typescript
case 'response_logged': {
  const d = event.data;
  const size = d?.size_bytes ? ` (${(d.size_bytes / 1024).toFixed(1)} KB)` : '';
  return `Raw response logged${size}`;
}
```

The clickable link rendering already handles `buffer_id` via the existing pattern around line 341-350.

### 3. Settings Component (`pkg/ui/frontend/src/components/Settings.tsx`)

Add Debug & Logging section:
```tsx
{/* Debug & Logging Section */}
<div class="space-y-4 border-t border-gray-700 pt-4">
  <h3 class="text-lg font-semibold text-gray-200">Debug & Logging</h3>
  
  <label class="flex items-center gap-3">
    <input 
      type="checkbox" 
      checked={config.log_raw_upstream_response}
      onChange={(e) => updateConfig('log_raw_upstream_response', e.currentTarget.checked)}
    />
    <span class="text-gray-300">Log successful upstream responses</span>
  </label>
  
  <label class="flex items-center gap-3">
    <input 
      type="checkbox" 
      checked={config.log_raw_upstream_on_error}
      onChange={(e) => updateConfig('log_raw_upstream_on_error', e.currentTarget.checked)}
    />
    <span class="text-gray-300">Log failed/error upstream responses</span>
  </label>
  
  <div class="flex items-center gap-3">
    <label class="text-gray-300">Max size per response:</label>
    <input 
      type="number" 
      value={config.log_raw_upstream_max_kb}
      min={1}
      max={102400}
      onChange={(e) => updateConfig('log_raw_upstream_max_kb', parseInt(e.currentTarget.value) || 1024)}
      class="w-24 rounded bg-gray-700 px-2 py-1 text-gray-200"
    />
    <span class="text-gray-400">KB</span>
  </div>
  
  {config.buffer_storage_dir === '' && (config.log_raw_upstream_response || config.log_raw_upstream_on_error) && (
    <p class="text-yellow-400 text-sm">
      ⚠ Buffer storage directory must be configured to enable logging
    </p>
  )}
</div>
```

## Files to Modify

| File | Change |
|------|--------|
| `pkg/config/config.go` | Add 3 config fields + validation |
| `pkg/proxy/stream_buffer.go` | Add `GetAllRawBytes()` method |
| `pkg/proxy/handler.go` | Add `saveRawResponse()` + call sites + rawBody in requestContext |
| `pkg/ui/frontend/src/types.ts` | Add `response_logged` event type |
| `pkg/ui/frontend/src/components/EventLog.tsx` | Handle new event type |
| `pkg/ui/frontend/src/components/Settings.tsx` | Add toggles + max size input |

## Event Payload

```json
{
  "type": "response_logged",
  "timestamp": 1710846725,
  "data": {
    "id": "req-abc123",
    "buffer_id": "req-abc123-response",
    "request_body_id": "req-abc123-request",
    "size_bytes": 45678
  }
}
```

## UI Mockup

### Settings Page
```
┌─────────────────────────────────────────────────┐
│ Debug & Logging                                 │
├─────────────────────────────────────────────────┤
│ ☐ Log successful upstream responses             │
│ ☐ Log failed/error upstream responses           │
│                                                 │
│   Max size per response: [1024] KB              │
│   (larger responses are not logged)             │
│                                                 │
│   ⚠ Buffer storage directory must be           │
│     configured to enable logging               │
└─────────────────────────────────────────────────┘
```

### Event Log
```
● 14:32:05  request_started     POST /v1/chat/completions
● 14:32:08  response_logged     Raw response logged (45.2 KB) [View Response]
● 14:32:08  request_completed   200 OK (3.2s)
```

## Design Decisions (from @oracle review)

1. **Location**: `handler.go` instead of `race_coordinator.go` - better separation of concerns (concurrency manager shouldn't do I/O)

2. **Two config options**: Separate toggles for success vs error responses - different debugging needs

3. **Async save**: Use `go h.saveRawResponse()` - fire-and-forget to avoid blocking responses

4. **Skip, don't truncate**: If response exceeds size limit, skip entirely - truncated raw responses are often useless

5. **Request body correlation**: Save request body too for debugging context

6. **Reuse BufferStore**: Same infrastructure as existing buffer storage - same cleanup, same endpoint

## Security & Performance Considerations

| Concern | Mitigation |
|---------|------------|
| Storage exhaustion | Size limit per response + `BufferMaxStorageMB` global limit |
| Blocking response | Use `go` for async save - fire-and-forget |
| Memory spike | Buffer already exists; `GetAllRawBytes()` creates one copy |
| Sensitive data | Document that raw responses may contain PII |
| Concurrent access | BufferStore already has mutex; streamBuffer has RLock |

## Todo List

### Backend:
- [ ] Add config fields to `pkg/config/config.go` with defaults and validation
- [ ] Add `GetAllRawBytes()` method to `pkg/proxy/stream_buffer.go`
- [ ] Add `rawBody` to `requestContext` in `pkg/proxy/handler.go`
- [ ] Add `saveRawResponse()` method to `pkg/proxy/handler.go`
- [ ] Add call sites in `streamResult()` for success and error paths
- [ ] Add call sites in `handleNonStreamResult()` for success and error paths
- [ ] Run `go build ./...` to verify compilation

### Frontend:
- [ ] Add `response_logged` to event types in `pkg/ui/frontend/src/types.ts`
- [ ] Handle `response_logged` in `pkg/ui/frontend/src/components/EventLog.tsx`
- [ ] Add Debug & Logging section to `pkg/ui/frontend/src/components/Settings.tsx`
- [ ] Run `npm run build` in frontend directory

### Testing:
- [ ] Test with logging disabled (default) - no events, no files
- [ ] Test with success logging enabled - verify file saved and event emitted
- [ ] Test with error logging enabled - trigger error, verify file saved
- [ ] Test size limit - send large response, verify not logged
- [ ] Test UI toggles - verify config saved correctly
- [ ] Test clickable link - verify opens buffer content
