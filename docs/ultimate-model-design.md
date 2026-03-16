# UltimateModel Feature Design

## Overview

The **UltimateModel** feature provides a "last resort" mechanism for handling the hardest requests that normal models cannot solve. When a duplicate request is detected (based on message content hash), the proxy bypasses all normal logic (fallback, retry, buffering, loop detection) and acts as a raw proxy to a configured "ultimate" model.

## Problem Statement

Some LLM requests are too complex for the primary model or even the fallback chain. Users may need to:
- Retry the same exact request multiple times
- Manually switch to a more powerful model
- Bypass all the proxy's intelligent routing logic

UltimateModel automates this by detecting repeated requests and automatically escalating to the strongest model.

## Architecture

```
┌─────────────────────────────────────────────────────────────────────┐
│                         REQUEST FLOW                                 │
├─────────────────────────────────────────────────────────────────────┤
│                                                                      │
│  Incoming Request                                                    │
│       │                                                              │
│       ▼                                                              │
│  ┌─────────────────────┐                                            │
│  │ initRequestContext  │  (parse body, create request context)     │
│  └─────────┬───────────┘                                            │
│            │                                                         │
│            ▼                                                         │
│  ┌─────────────────────┐     NO      ┌──────────────────────────┐   │
│  │ Ultimate Model      ├────────────►│ Normal Flow              │   │
│  │ Configured?         │             │ (auth → fallback → retry)│   │
│  └─────────┬───────────┘             └──────────────────────────┘   │
│            │ YES                                                     │
│            ▼                                                         │
│  ┌─────────────────────┐     NO      ┌──────────────────────────┐   │
│  │ StoreAndCheck hash  │────────────►│ Continue normal flow     │   │
│  │ (ATOMIC, store 1st) │             │ (hash now stored)        │   │
│  └─────────┬───────────┘             └──────────────────────────┘   │
│            │ YES (was duplicate)                                   │
│            ▼                                                         │
│  ┌─────────────────────┐     FAIL    ┌──────────────────────────┐   │
│  │ Get model from DB   ├────────────►│ Remove hash from cache   │   │
│  │                     │             │ → Continue normal flow   │   │
│  └─────────┬───────────┘             └──────────────────────────┘   │
│            │ OK                                                     │
│            ▼                                                         │
│  ┌─────────────────────────────────────────────────────────────┐    │
│  │ ULTIMATE MODE                                                │    │
│  │ • Check auth if model is internal                            │    │
│  │ • Raw proxy (no retry/fallback/loop-detect/buffer)          │    │
│  │ • Enforce MaxRequestTime timeout                             │    │
│  │ • Add X-LLMProxy-Ultimate-Model header                       │    │
│  │ • Log to request history with flag                          │    │
│  │ • On failure: Remove hash + return error                     │    │
│  └─────────────────────────────────────────────────────────────┘    │
│                                                                      │
└─────────────────────────────────────────────────────────────────────┘
```

## 1. Configuration

### 1.1 Backend Config (`pkg/config/config.go`)

```go
type UltimateModelConfig struct {
    ModelID string `json:"model_id"` // e.g., "claude-3-opus", "gpt-4-turbo"
    MaxHash int    `json:"max_hash"` // Max hashes in circular buffer (default: 100)
}

type Config struct {
    // ... existing fields
    UltimateModel UltimateModelConfig `json:"ultimate_model"`
}
```

**Defaults:**
```go
UltimateModel: UltimateModelConfig{
    MaxHash: 100,
}
```

### 1.2 Environment Variables

| Variable | Default | Description |
|----------|---------|-------------|
| `ULTIMATE_MODEL_ID` | (empty) | Model ID to use for duplicate requests |
| `ULTIMATE_MODEL_MAX_HASH` | 100 | Max hashes in circular buffer |

### 1.3 Frontend Types (`pkg/ui/frontend/src/types.ts`)

```typescript
export interface AppConfig {
  // ... existing
  ultimate_model?: {
    model_id: string;
    max_hash: number;
  };
}
```

## 2. Hash Cache - Circular Buffer

### 2.1 Design

- **Circular buffer**: Fixed-size array with head pointer
- **Overflow**: When full, oldest hash is overwritten (FIFO)
- **Max size**: Configurable, default 100
- **Hash content**: Only `messages` array (role + content), ignores timestamps, metadata, tool_call_ids
- **Ephemeral**: In-memory only, resets on server restart (by design)
- **Hash format**: Full SHA256 (64 hex chars). Truncation NOT permitted.

### 2.2 Implementation (`pkg/ultimatemodel/hash_cache.go`)

```go
package ultimatemodel

import (
    "crypto/sha256"
    "encoding/hex"
    "sync"
)

// HashCache is a circular buffer of request hashes
type HashCache struct {
    mu     sync.RWMutex
    hashes []string  // circular buffer
    size   int       // max capacity
    head   int       // next write position
    count  int       // current count
}

func NewHashCache(maxSize int) *HashCache

// HashMessages generates a consistent hash from messages (role + content only)
func HashMessages(messages []openai.ChatCompletionMessage) string

// StoreAndCheck stores the hash and returns whether it was ALREADY present.
// ATOMIC: Stores first, then checks - prevents race condition with concurrent requests.
// Returns true if hash was already in cache (duplicate detected).
func (c *HashCache) StoreAndCheck(hash string) bool

// Remove removes a hash from the cache (used when ultimate model fails).
// This prevents infinite retry loop on broken config.
func (c *HashCache) Remove(hash string)

// Reset clears all hashes (called when ultimate_model_id config changes)
func (c *HashCache) Reset()
```

### 2.3 Hash Algorithm

```go
// HashMessages generates a consistent hash from messages.
// FULL SHA256 required (64 hex characters). Truncation NOT permitted.
// Birthday paradox: 16 chars = 2^64 space = collision at ~2^32 hashes.
func HashMessages(messages []openai.ChatCompletionMessage) string {
    h := sha256.New()
    for _, msg := range messages {
        h.Write([]byte(msg.Role))
        h.Write([]byte{'|'})
        if msg.Content != nil {
            h.Write([]byte(*msg.Content))
        }
        h.Write([]byte{'\n'})
    }
    return hex.EncodeToString(h.Sum(nil))
}
```

**Rationale**: Hashing only `role` + `content` ensures:
- Same question → same hash (regardless of timestamps)
- Different context/windowing → different hash
- Simple and deterministic

### 2.4 StoreAndCheck vs CheckAndStore

**Problem with CheckAndStore (check first, then store):**
- Two identical concurrent requests both check → both see "not in cache"
- Both proceed to normal flow
- Neither triggers ultimate mode

**Solution with StoreAndCheck (store first, return if duplicate):**
- First request stores hash → returns false (not duplicate) → normal flow
- Second request stores hash → returns true (was duplicate) → ultimate mode
- Thread-safe with mutex lock around entire operation

## 3. Ultimate Model Handler

### 3.1 Handler Structure (`pkg/ultimatemodel/handler.go`)

```go
package ultimatemodel

import (
    "context"
    "net/http"
    
    "github.com/disillusioners/llm-supervisor-proxy/pkg/config"
    "github.com/disillusioners/llm-supervisor-proxy/pkg/models"
    "github.com/disillusioners/llm-supervisor-proxy/pkg/events"
)

type Handler struct {
    config    *config.Manager
    modelsMgr models.ModelsConfigInterface  // Database-backed
    hashCache *HashCache
    eventBus  *events.Bus  // For config change notifications
}

func NewHandler(cfg *config.Manager, modelsMgr models.ModelsConfigInterface, eventBus *events.Bus) *Handler
```

### 3.2 Core Methods

```go
// ShouldTrigger stores hash and returns true if:
// 1. Ultimate model is configured (non-empty ModelID)
// 2. This request hash was already in cache (duplicate)
// Uses atomic StoreAndCheck to prevent race conditions.
func (h *Handler) ShouldTrigger(messages []openai.ChatCompletionMessage) (bool, string)

// Execute handles request with ultimate model - RAW PROXY
// No retry, no fallback, no loop detection, no buffering.
// On failure, removes hash from cache to prevent infinite retry loop.
func (h *Handler) Execute(
    ctx context.Context,
    w http.ResponseWriter,
    r *http.Request,
    rc *requestContext,  // Pass full context for logging continuity
) error

// GetModelID returns configured ultimate model ID (for logging)
func (h *Handler) GetModelID() string

// onConfigChange handles config update events (resets cache if model_id changed)
func (h *Handler) onConfigChange(event events.Event)
```

### 3.3 Execution Flow

```
Execute(ctx, w, r, rc)
    │
    ├─► Get model config from DATABASE by ModelID
    │       │
    │       └─► FAIL: Remove hash from cache
    │                 Log error
    │                 Return error (caller continues normal flow)
    │
    ├─► Set response header: X-LLMProxy-Ultimate-Model: <modelID>
    │
    ├─► Check modelCfg.Internal:
    │       │
    │       ├─► TRUE: executeInternal() - direct provider API call
    │       │       (API key from credential_id → provider → base URL)
    │       │       Auth required for internal providers
    │       │
    │       └─► FALSE: executeExternal() - proxy to upstream URL
    │
    ├─► Stream response directly to client
    │       (no buffering, no transformation)
    │
    └─► On failure: Remove hash from cache
                     Return error to client (502 Bad Gateway)
```

## 4. Integration Points

### 4.1 Proxy Handler (`pkg/proxy/handler.go`)

Add to `Handler` struct:
```go
type Handler struct {
    // ... existing fields
    ultimateHandler *ultimatemodel.Handler
}
```

Modify `HandleChatCompletions`:
```go
func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
    // Step 1: Initialize request context FIRST
    rc, err := h.initRequestContext(r)
    if err != nil {
        h.handleError(w, r, err)
        return
    }
    
    // Step 2: ULTIMATE MODEL CHECK (EARLY EXIT)
    // Must happen AFTER initRequestContext for logging continuity
    if triggered, hash := h.ultimateHandler.ShouldTrigger(rc.reqBody.Messages); triggered {
        ultimateModelID := h.ultimateHandler.GetModelID()
        log.Printf("[UltimateModel] Triggered for duplicate request, using %s, hash=%s", 
            ultimateModelID, hash[:8]+"...")
        
        rc.reqLog.UltimateModelUsed = true
        rc.reqLog.UltimateModelID = ultimateModelID
        
        err := h.ultimateHandler.Execute(r.Context(), w, r, rc)
        if err != nil {
            log.Printf("[UltimateModel] Error: %v", err)
            // Error already written to response, or use:
            // http.Error(w, "ultimate model unavailable", http.StatusBadGateway)
        }
        return // DONE - no fallback, no retry
    }
    // === END ULTIMATE MODEL CHECK ===
    
    // Step 3: Continue normal flow (auth, fallback, retry, etc.)
    if h.requiresInternalAuth(rc) {
        // ... existing auth logic
    }
    // ... rest of existing flow
}
```

### 4.2 Initialization

In proxy handler initialization (e.g., `NewHandler` or `Initialize`):
```go
h.ultimateHandler = ultimatemodel.NewHandler(configMgr, modelsMgr, eventBus)

// Subscribe to config changes for cache reset
eventBus.Subscribe("config_changed", h.ultimateHandler.onConfigChange)
```

### 4.3 Config Change Handler

When `ultimate_model.model_id` changes, reset the hash cache:
```go
func (h *Handler) onConfigChange(event events.Event) {
    // Check if ultimate_model_id changed
    if event.Data["field"] == "ultimate_model.model_id" {
        log.Printf("[UltimateModel] Model ID changed, resetting hash cache")
        h.hashCache.Reset()
    }
}
```

## 5. Request Logging

### 5.1 Database Schema

Add new columns to `requests` table:

**SQLite (`pkg/store/database/migrations/sqlite/008_add_ultimate_model.up.sql`):**
```sql
ALTER TABLE requests ADD COLUMN ultimate_model_used INTEGER NOT NULL DEFAULT 0;
ALTER TABLE requests ADD COLUMN ultimate_model_id TEXT NOT NULL DEFAULT '';
```

**PostgreSQL (`pkg/store/database/migrations/postgres/008_add_ultimate_model.up.sql`):**
```sql
ALTER TABLE requests ADD COLUMN ultimate_model_used BOOLEAN NOT NULL DEFAULT false;
ALTER TABLE requests ADD COLUMN ultimate_model_id TEXT NOT NULL DEFAULT '';
```

### 5.2 Update Generated Code

- Update `pkg/store/database/db/models.go` - add fields to `Request` struct
- Update `pkg/store/database/sqlc/queries.sql` - include new columns in SELECT/INSERT
- Run `sqlc generate`

### 5.3 Log Ultimate Mode Usage

In `ultimatemodel/handler.go` `Execute()`:
```go
// After successful upstream call, log request with ultimate model flag
h.logRequest(ctx, modelID, ultimateModelID, responseStatus)
```

## 6. Frontend UI

### 6.1 Settings Page (`pkg/ui/frontend/src/components/config/ProxySettings.tsx`)

Add new field:

```tsx
<div>
  <label class="block text-sm font-medium text-gray-300 mb-1">
    Ultimate Model ID
  </label>
  <input
    type="text"
    value={ultimateModelId}
    onInput={(e) => onUltimateModelIdChange((e.target as HTMLInputElement).value)}
    class="w-full bg-gray-800 border border-gray-700 rounded px-3 py-2 text-white"
    placeholder="e.g., claude-3-opus-20240229"
  />
  <p class="text-xs text-gray-500 mt-1">
    When a duplicate request is detected, this model will be used as a raw proxy.
    Leave empty to disable. Model must exist in database.
  </p>
</div>

<div>
  <label class="block text-sm font-medium text-gray-300 mb-1">
    Max Hash Cache Size
  </label>
  <input
    type="number"
    value={maxHash}
    onInput={(e) => onMaxHashChange(parseInt((e.target as HTMLInputElement).value) || 100)}
    class="w-full bg-gray-800 border border-gray-700 rounded px-3 py-2 text-white"
    min="1"
    max="10000"
  />
  <p class="text-xs text-gray-500 mt-1">
    Maximum number of request hashes to remember for duplicate detection.
    Uses circular buffer (oldest removed when full).
  </p>
</div>
```

### 6.2 Settings Page State (`pkg/ui/frontend/src/components/SettingsPage.tsx`)

Add state:
```typescript
const [ultimateModelId, setUltimateModelId] = useState('');
const [maxHash, setMaxHash] = useState(100);
```

Sync from config:
```typescript
useEffect(() => {
  if (config) {
    setUltimateModelId(config.ultimate_model?.model_id || '');
    setMaxHash(config.ultimate_model?.max_hash || 100);
  }
}, [config]);
```

Apply handler:
```typescript
const handleApplyProxy = async () => {
  await onUpdateConfig({
    // ... existing fields
    ultimate_model: {
      model_id: ultimateModelId,
      max_hash: maxHash,
    },
  });
};
```

Pass to component:
```tsx
<ProxySettings
  ultimateModelId={ultimateModelId}
  onUltimateModelIdChange={setUltimateModelId}
  maxHash={maxHash}
  onMaxHashChange={setMaxHash}
  // ... other props
/>
```

### 6.3 Request History Display

Add indicator column in request list:
- Show "⭐ [modelID]" when `ultimate_model_used = true`
- Show normal model when `ultimate_model_used = false`

## 7. Files to Create/Modify

### New Files

| File | Description |
|------|-------------|
| `pkg/ultimatemodel/hash_cache.go` | Circular buffer hash storage with StoreAndCheck, Remove, Reset |
| `pkg/ultimatemodel/hash_cache_test.go` | Unit tests for hash cache |
| `pkg/ultimatemodel/handler.go` | Main orchestration with config change handling |
| `pkg/ultimatemodel/handler_internal.go` | Direct provider calls |
| `pkg/ultimatemodel/handler_external.go` | External upstream calls |
| `pkg/store/database/migrations/sqlite/008_add_ultimate_model.up.sql` | DB columns |
| `pkg/store/database/migrations/postgres/008_add_ultimate_model.up.sql` | DB columns |

### Modified Files

| File | Modification |
|------|--------------|
| `pkg/config/config.go` | Add `UltimateModelConfig` struct + defaults + env vars |
| `pkg/proxy/handler.go` | Add early check + ultimate handler + event subscription |
| `pkg/ui/frontend/src/types.ts` | Add `ultimate_model` interface |
| `pkg/ui/frontend/src/components/config/ProxySettings.tsx` | Add UI fields |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | Add state management |
| `pkg/ui/frontend/src/components/ConfigModal.tsx` | Add state management (if needed) |
| `pkg/store/database/sqlc/queries.sql` | Update queries with new columns |

## 8. Edge Cases

| Scenario | Behavior |
|----------|----------|
| Ultimate model not in database | Remove hash from cache, log error, return error (don't trigger infinite retry) |
| Ultimate model request fails | Remove hash from cache, return 502 Bad Gateway to client |
| Hash cache full | Overwrite oldest hash (circular buffer behavior) |
| Streaming response fails mid-stream | Use SSE error format: `data: {"error": "..."}` if headers sent, else 502 |
| Request with tools/functions | Hash ignores tools (only messages.content) |
| Multi-turn conversation | Each message array = new hash check |
| Empty messages array | Skip hash check (not a valid request) |
| Ultimate model ID changed | Reset hash cache via config change event |
| Concurrent identical requests | First → normal flow, second → ultimate mode (atomic StoreAndCheck) |
| Server restart | Hash cache cleared (ephemeral by design) |

## 8.1 Feature Interaction (What Ultimate Mode Bypasses)

**Ultimate mode BYPASSES:**
- Fallback chain
- Retry logic (idle, generation, upstream error)
- Loop detection
- Tool repair
- Shadow retry
- Response buffering

**Ultimate mode STILL APPLIES:**
- Authentication (if ultimate model is internal, auth is still required)
- Request logging (with `ultimate_model_used=true` flag)
- Timeout enforcement (`MaxRequestTime` still applies)

## 9. Testing Strategy

### Unit Tests

1. **HashCache**
   - Test `StoreAndCheck` returns false for first insert
   - Test `StoreAndCheck` returns true for duplicate
   - Test circular buffer wraps around
   - Test `Reset` clears all hashes
   - Test `Remove` removes specific hash
   - Test concurrent `StoreAndCheck` calls (race condition test)

2. **HashMessages**
   - Test same messages produce same hash
   - Test different messages produce different hash
   - Test order matters
   - Test full 64-char SHA256 output

### Integration Tests

1. Test ultimate mode triggers on second identical request
2. Test normal flow continues when ultimate model not configured
3. Test ultimate mode bypasses fallback chain
4. Test request logging includes ultimate model info
5. Test hash removed when ultimate model not found in DB
6. Test hash removed when ultimate model request fails
7. Test concurrent identical requests (one normal, one ultimate)
8. Test config change resets hash cache

## 10. Future Considerations (Out of Scope)

- [ ] Add TTL-based hash expiration (for long-running sessions)
- [ ] Add manual trigger via header (`x-llmproxy-force-ultimate`)
- [ ] Add rate limiting (`MaxTriggersPerHour` config) to prevent abuse
- [ ] Add metrics/observability for ultimate mode usage (Prometheus, structured logging)
- [ ] Support multiple ultimate models (escalation path)
- [ ] Add ultimate mode to WebUI dashboard

## 11. Implementation Order

1. Add `UltimateModelConfig` struct to `pkg/config/config.go`
2. Add `GetUltimateModel()` to `ManagerInterface` + implementation
3. Create `pkg/ultimatemodel/hash_cache.go` + unit tests
4. Add DB migration for request logging columns (008_add_ultimate_model.up.sql)
5. Run `sqlc generate` to update generated code
6. Create `pkg/ultimatemodel/handler.go` (core logic)
7. Create `pkg/ultimatemodel/handler_internal.go` + `handler_external.go`
8. Update `pkg/proxy/handler.go` with early exit check
9. Add frontend UI fields (ProxySettings.tsx, SettingsPage.tsx)
10. Integration tests

## 12. Summary of Key Design Decisions

| Decision | Rationale |
|----------|-----------|
| `StoreAndCheck` instead of `CheckAndStore` | Prevents race condition with concurrent identical requests |
| Pass `requestContext` to `Execute()` | Ensures logging continuity and request ID tracking |
| Remove hash on ultimate model failure | Prevents infinite retry loop on broken config |
| Reset cache on `model_id` config change | Different model = different behavior expectations |
| Full SHA256 (64 chars) | Prevents collision risk from truncation |
| In-memory hash cache | Ephemeral by design, no persistence overhead |
| Auth still applies for internal models | Security: bypass fallback/retry, NOT auth |
