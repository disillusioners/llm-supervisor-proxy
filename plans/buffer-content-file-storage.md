# Buffer Content File Storage Plan

## Overview
Refactor the buffer content logging to save large buffer content to files instead of sending via events. The event will contain a link/ID to retrieve the file content via a new API endpoint.

## Current Behavior
- When `stream_error_after_headers` event is published, the full `buffer_preview` (potentially large) is included in the event payload
- This can cause performance issues with large buffers

## Proposed Changes

### 1. Create Buffer Storage Package
**File:** `pkg/bufferstore/store.go`

Create a new package to manage buffer content storage:
- Save buffer content to files in a configurable directory
- Generate unique IDs for each saved buffer
- Retrieve buffer content by ID
- Optional: cleanup old files based on age/size

```go
type BufferStore struct {
    baseDir string
    maxSize int64  // Maximum total storage size
}

func New(baseDir string, maxSize int64) *BufferStore
func (s *BufferStore) Save(bufferID string, content []byte) error
func (s *BufferStore) Get(bufferID string) ([]byte, error)
func (s *BufferStore) Delete(bufferID string) error
func (s *BufferStore) Cleanup() error  // Remove old files
```

### 2. Modify Event Publication
**File:** `pkg/proxy/handler_functions.go`

Change the `stream_error_after_headers` event to:
- Save buffer content to file using BufferStore
- Include only `buffer_id` in the event instead of `buffer_preview`

```go
// Before:
h.publishEvent("stream_error_after_headers", map[string]interface{}{
    "error":          err.Error(),
    "id":             rc.reqID,
    "buffer_size":    rc.streamBuffer.Len(),
    "buffer_preview": bufferPreview,  // REMOVE
})

// After:
bufferID := fmt.Sprintf("%s_buffer", rc.reqID)
h.bufferStore.Save(bufferID, rc.streamBuffer.Bytes())
h.publishEvent("stream_error_after_headers", map[string]interface{}{
    "error":       err.Error(),
    "id":          rc.reqID,
    "buffer_size": rc.streamBuffer.Len(),
    "buffer_id":   bufferID,  // Link to file
})
```

### 3. Add New API Endpoint
**File:** `pkg/ui/server.go`

Add endpoint to retrieve buffer content:
- `GET /fe/api/buffers/{id}` - Returns buffer content as plain text

```go
func (s *Server) handleBufferContent(w http.ResponseWriter, r *http.Request) {
    id := r.URL.Path[len("/fe/api/buffers/"):]
    content, err := s.bufferStore.Get(id)
    if err != nil {
        http.Error(w, "Buffer not found", http.StatusNotFound)
        return
    }
    w.Header().Set("Content-Type", "text/plain")
    w.Write(content)
}
```

### 4. Update Handler Initialization
**File:** `pkg/proxy/handler.go`

Add BufferStore to Handler struct and initialize in NewHandler.

### 5. Update Main Application
**File:** `cmd/main.go`

- Initialize BufferStore with config
- Pass to Handler and UI Server

## Configuration
Add to config:
```json
{
  "buffer_storage_dir": "./data/buffers",
  "buffer_max_storage_mb": 100
}
```

## Files to Create/Modify

### Create:
- `pkg/bufferstore/store.go` - Buffer storage implementation

### Modify (Backend):
- `pkg/proxy/handler.go` - Add BufferStore to Handler
- `pkg/proxy/handler_functions.go` - Save to file instead of event
- `pkg/ui/server.go` - Add buffer retrieval endpoint
- `pkg/config/config.go` - Add buffer storage config
- `cmd/main.go` - Initialize BufferStore

### Modify (Frontend):
- `pkg/ui/frontend/src/types.ts` - Add `buffer_id` field to event data types
- `pkg/ui/frontend/src/components/EventLog.tsx` - Render clickable link for buffer content

## Frontend Changes

### Update `types.ts`:
```typescript
// Change buffer_preview to buffer_id
interface EventData {
  // ...
  buffer_size?: number;
  buffer_id?: string;  // Link to buffer file instead of preview
}
```

### Update `EventLog.tsx`:
```tsx
case 'stream_error_after_headers': {
  const bufInfo = event.data?.buffer_size ? ` (buffer: ${event.data.buffer_size} bytes)` : '';
  const bufLink = event.data?.buffer_id 
    ? ` | Content: ` 
    : '';
  return `Stream error after headers: ${event.data?.error || 'Unknown error'}${bufInfo}${bufLink}`;
}

// In the render section, make buffer_id clickable:
<span class="text-gray-300">
  {getEventMessage(event)}
  {event.data?.buffer_id && (
    <a 
      href={`/fe/api/buffers/${event.data.buffer_id}`}
      target="_blank"
      class="text-blue-400 hover:underline ml-1"
    >
      [View Buffer]
    </a>
  )}
</span>
```

## Todo List

### Backend:
- [ ] Create `pkg/bufferstore/store.go` with Save/Get/Delete/Cleanup methods
- [ ] Add buffer storage config to `pkg/config/config.go`
- [ ] Update `pkg/proxy/handler.go` to include BufferStore
- [ ] Modify `pkg/proxy/handler_functions.go` to save buffer to file and publish buffer_id
- [ ] Add `GET /fe/api/buffers/{id}` endpoint to `pkg/ui/server.go`
- [ ] Update `cmd/main.go` to initialize and pass BufferStore
- [ ] Add cleanup mechanism for old buffer files (optional)

### Frontend:
- [ ] Update `pkg/ui/frontend/src/types.ts` - Replace `buffer_preview` with `buffer_id`
- [ ] Update `pkg/ui/frontend/src/components/EventLog.tsx` - Add clickable link to view buffer content
- [ ] Rebuild frontend assets
