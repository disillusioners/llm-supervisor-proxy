# UltimateModel Retry Limit Feature Plan

> **Status**: Updated based on code review (see [ultimate-model-retry-limit-review.md](./ultimate-model-retry-limit-review.md))
> **Last Updated**: 2026-03-17 (Review Round 2)

## Overview

Add a retry limit mechanism for the UltimateModel feature to prevent infinite retry loops when the ultimate model itself fails. Currently, if the ultimate model fails, the hash is removed from the cache, allowing the client to retry indefinitely. This plan introduces a configurable retry counter per hash to cap the number of ultimate model attempts.

## Problem Statement

**Current Flow:**
```
Client Request → Fail → Client Retry → UltimateModel Activated → Still Fails → Hash Removed → Client Can Retry Forever
```

**Desired Flow:**
```
Client Request → Fail → Client Retry → UltimateModel Activated → Still Fails
    → Check Retry Counter for Hash (atomically increment in ShouldTrigger)
    → If Counter <= Max UltimateModel Retries: Allow retry (counter already incremented)
    → If Counter > Max UltimateModel Retries: Return JSON stream error (HTTP 200) to stop client
```

## Architecture

> **⚠️ Review Fix**: Clarified that retry counter is atomically incremented in `ShouldTrigger`.
> See [review issue #1](./ultimate-model-retry-limit-review.md#1-critical-race-condition-in-retry-counter-check).

```
┌─────────────────────────────────────────────────────────────────────────────┐
│                    ULTIMATE MODEL RETRY FLOW                                 │
├─────────────────────────────────────────────────────────────────────────────┤
│                                                                              │
│  Incoming Request (2nd+ attempt, hash exists)                               │
│       │                                                                      │
│       ▼                                                                      │
│  ┌─────────────────────────┐                                                │
│  │ UltimateModel Triggered │                                                │
│  └───────────┬─────────────┘                                                │
│              │                                                               │
│              ▼                                                               │
│  ┌─────────────────────────────────────┐                                    │
│  │ ATOMIC: IncrementAndCheckRetry()    │  ◄─── Prevents race condition    │
│  │ (increments counter and checks      │                                    │
│  │  atomically in ShouldTrigger)       │                                    │
│  └───────────┬─────────────────────────┘                                    │
│              │                                                               │
│              ├─► exhausted = true (count > maxRetries)                      │
│              │       │                                                       │
│              │       ▼                                                       │
│              │   ┌─────────────────────────────────────────┐                │
│              │   │ Return JSON Stream Error (HTTP 200)     │                │
│              │   │ data: {"error": {"message": "...",      │                │
│              │   │        "type": "ultimate_model_retry_exhausted",         │
│              │   │        "code": "exhausted"}}            │                │
│              │   └─────────────────────────────────────────┘                │
│              │                                                               │
│              └─► exhausted = false (count <= maxRetries)                    │
│                      │                                                       │
│                      ▼                                                       │
│                  ┌─────────────────────────┐                                │
│                  │ Execute Ultimate Model  │                                │
│                  └───────────┬─────────────┘                                │
│                              │                                               │
│                              ├─► SUCCESS: Stream response                   │
│                              │         ClearRetryCount()                    │
│                              │         (keep hash in cache)                 │
│                              │                                               │
│                              └─► FAIL: KEEP counter (already incremented)    │
│                                        DON'T remove hash                    │
│                                        Return error to client                │
│                                                                              │
└─────────────────────────────────────────────────────────────────────────────┘
```

## 1. Configuration Changes

### 1.1 Backend Config (`pkg/config/config.go`)

Add `MaxRetries` field to `UltimateModelConfig`:

```go
type UltimateModelConfig struct {
    ModelID    string `json:"model_id"`     // Model ID to use for duplicate requests
    MaxHash    int    `json:"max_hash"`     // Max hashes in circular buffer (default: 100)
    MaxRetries int    `json:"max_retries"`  // Max ultimate model retries per hash (default: 2)
}
```

**Update Defaults:**
```go
UltimateModel: UltimateModelConfig{
    ModelID:    "",
    MaxHash:    100,
    MaxRetries: 2,  // NEW: Allow 2 retries by default
},
```

### 1.2 Add Config Validation

> **⚠️ Review Fix**: Added explicit validation for `MaxRetries`.
> See [review issue #2](./ultimate-model-retry-limit-review.md#2-missing-maxretries-validation).

Add validation in `Config.Validate()` method:

```go
// In Config.Validate()
if c.UltimateModel.MaxRetries < 0 {
    return errors.New("ultimate_model.max_retries cannot be negative")
}
if c.UltimateModel.MaxRetries > 100 {
    return errors.New("ultimate_model.max_retries cannot exceed 100")
}
```

### 1.3 Environment Variable

| Variable | Default | Description |
|----------|---------|-------------|
| `ULTIMATE_MODEL_MAX_RETRIES` | 2 | Max retry attempts for ultimate model per hash |

### 1.4 Add Env Override in `applyEnvOverrides`

```go
if v := os.Getenv("ULTIMATE_MODEL_MAX_RETRIES"); v != "" {
    if r, err := strconv.Atoi(v); err == nil && r >= 0 {
        cfg.UltimateModel.MaxRetries = r
    }
}
```

### 1.5 Update ManagerInterface

> **⚠️ Review Note (Issue #3)**: The new `GetUltimateModelMaxRetries()` method is **optional** since `GetUltimateModel()` already exists and returns the full `UltimateModelConfig`. You can use `cfg.Get().UltimateModel.MaxRetries` directly.
>
> Add this method only for convenience if preferred.

```go
type ManagerInterface interface {
    // ... existing methods ...
    GetUltimateModelMaxRetries() int  // OPTIONAL - can use GetUltimateModel().MaxRetries instead
}
```

Add implementation in `Manager` (optional):

```go
func (m *Manager) GetUltimateModelMaxRetries() int {
    m.mu.RLock()
    defer m.mu.RUnlock()
    return m.config.UltimateModel.MaxRetries
}
```

### 1.6 Frontend Types (`pkg/ui/frontend/src/types.ts`)

```typescript
export interface AppConfig {
  // ... existing
  ultimate_model?: {
    model_id: string;
    max_hash: number;
    max_retries: number;  // NEW
  };
}
```

## 2. Retry Counter Implementation

### 2.1 Hash Cache Enhancement (`pkg/ultimatemodel/hash_cache.go`)

Add a retry counter map alongside the hash cache:

```go
// HashCache is a circular buffer of request hashes with retry tracking.
type HashCache struct {
    mu           sync.RWMutex
    hashes       []string        // circular buffer for hash storage
    size         int             // max capacity
    head         int             // next write position
    count        int             // current count
    retryCounter map[string]int  // hash -> retry count for ultimate model
}
```

### 2.2 New Methods

> **⚠️ Critical Fix for Race Condition**: Use atomic `IncrementAndCheckRetry` instead of separate check + increment.
> See [review issue #1](./ultimate-model-retry-limit-review.md#1-critical-race-condition-in-retry-counter-check).

```go
// IncrementAndCheckRetry atomically increments and checks if limit exceeded.
// This prevents TOCTOU race condition between check and increment.
// Returns (newCount, exhausted) where exhausted=true if newCount > maxRetries.
func (c *HashCache) IncrementAndCheckRetry(hash string, maxRetries int) (newCount int, exhausted bool) {
    c.mu.Lock()
    defer c.mu.Unlock()
    c.retryCounter[hash]++
    newCount = c.retryCounter[hash]
    return newCount, newCount > maxRetries
}

// GetRetryCount returns the current retry count for a hash.
// Returns 0 if hash not found in retry counter.
func (c *HashCache) GetRetryCount(hash string) int {
    c.mu.RLock()
    defer c.mu.RUnlock()
    return c.retryCounter[hash]
}

// ClearRetryCount removes the retry counter for a hash.
// Called when ultimate model succeeds or when hash is removed.
func (c *HashCache) ClearRetryCount(hash string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    delete(c.retryCounter, hash)
}

// Remove (existing) - also clear retry counter
func (c *HashCache) Remove(hash string) {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    // Find and remove the hash from circular buffer
    for i := 0; i < c.count; i++ {
        if c.hashes[i] == hash {
            copy(c.hashes[i:], c.hashes[i+1:c.count])
            c.count--
            c.head = (c.head - 1 + c.size) % c.size
            c.hashes[c.count] = ""
            break
        }
    }
    
    // Also clear retry counter
    delete(c.retryCounter, hash)
}

// Reset (existing) - also clear retry counters
func (c *HashCache) Reset() {
    c.mu.Lock()
    defer c.mu.Unlock()
    
    c.hashes = make([]string, c.size)
    c.head = 0
    c.count = 0
    c.retryCounter = make(map[string]int)  // Clear all retry counters
}
```

## 3. Handler Changes

### 3.1 Update `ShouldTrigger` Method

> **⚠️ Critical Fix**: Use atomic `IncrementAndCheckRetry` to prevent race condition.
> The increment happens atomically with the check, not in `Execute()`.

```go
// ShouldTriggerResult contains the result of ShouldTrigger check
type ShouldTriggerResult struct {
    Triggered       bool   // True if ultimate model should be used
    Hash            string // The computed hash
    RetryExhausted  bool   // True if max retries exceeded (after increment)
    CurrentRetry    int    // NEW retry count (after increment)
    MaxRetries      int    // Configured max retries
}

// ShouldTrigger checks if ultimate model should be triggered.
// IMPORTANT: This method atomically increments the retry counter when triggered.
// This prevents TOCTOU race condition between check and increment.
func (h *Handler) ShouldTrigger(messages []map[string]interface{}) ShouldTriggerResult {
    cfg := h.config.Get()
    if cfg.UltimateModel.ModelID == "" {
        return ShouldTriggerResult{Triggered: false}
    }

    if len(messages) == 0 {
        return ShouldTriggerResult{Triggered: false}
    }

    hash := HashMessages(messages)
    wasDuplicate := h.hashCache.StoreAndCheck(hash)

    if !wasDuplicate {
        return ShouldTriggerResult{Triggered: false, Hash: hash}
    }

    // Get max retries config
    maxRetries := cfg.UltimateModel.MaxRetries
    if maxRetries <= 0 {
        maxRetries = 2 // Default
    }
    
    // ATOMIC increment and check - prevents race condition
    newCount, exhausted := h.hashCache.IncrementAndCheckRetry(hash, maxRetries)
    
    return ShouldTriggerResult{
        Triggered:      true,
        Hash:           hash,
        RetryExhausted: exhausted,
        CurrentRetry:   newCount,
        MaxRetries:     maxRetries,
    }
}
```

### 3.2 New Method: `SendRetryExhaustedError`

Send a proper JSON stream error response when retry limit is exhausted:

> **⚠️ Review Fix (Issue #5)**: Fixed header logic - set `Content-Type` based on `isStream` parameter BEFORE writing response body.

```go
// SendRetryExhaustedError sends a JSON stream error response.
// This uses HTTP 200 with SSE error format to make streaming clients stop gracefully.
func (h *Handler) SendRetryExhaustedError(
    w http.ResponseWriter,
    hash string,
    currentRetry int,
    maxRetries int,
    isStream bool,
) error {
    errorResp := map[string]interface{}{
        "error": map[string]interface{}{
            "message": fmt.Sprintf(
                "Ultimate model retry limit exceeded (attempt %d of %d max). Hash: %s",
                currentRetry, maxRetries, hash[:8]+"...",
            ),
            "type": "ultimate_model_retry_exhausted",
            "code": "exhausted",
            "hash": hash,
        },
    }
    
    errorJSON, _ := json.Marshal(errorResp)
    
    // Set headers based on response type FIRST
    if isStream {
        // SSE format for streaming requests
        w.Header().Set("Content-Type", "text/event-stream")
        w.Header().Set("Cache-Control", "no-cache")
        w.Header().Set("Connection", "keep-alive")
        w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
        fmt.Fprintf(w, "data: %s\n\n", string(errorJSON))
        fmt.Fprintf(w, "data: [DONE]\n\n")
    } else {
        // Regular JSON response for non-streaming
        w.Header().Set("Content-Type", "application/json")
        w.Header().Set("X-LLMProxy-Ultimate-Model", "retry-exhausted")
        w.Write(errorJSON)
    }
    
    // Flush if possible
    if flusher, ok := w.(http.Flusher); ok {
        flusher.Flush()
    }
    
    return nil
}
```

### 3.3 Update `Execute` Method

> **⚠️ Review Fix (Issue #1 - CRITICAL)**: You MUST delete the existing `h.hashCache.Remove(hash)` call from the failure path.
>
> **Current code in [`handler.go:140`](../pkg/ultimatemodel/handler.go:140):**
> ```go
> if err != nil {
>     // Remove hash on failure to prevent infinite retry loop
>     h.hashCache.Remove(hash)  // <-- DELETE THIS LINE
>     log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
>     return err
> }
> ```
>
> **Diff to apply:**
> ```diff
> if err != nil {
> -   // Remove hash on failure to prevent infinite retry loop
> -   h.hashCache.Remove(hash)
> +   // On failure: KEEP retry counter to enforce limit
> +   // DON'T remove hash - client can retry until MaxRetries exhausted
>     log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
>     return err
> }
> ```

> **⚠️ Behavior Clarification**: On failure, we KEEP the retry counter (don't remove hash).
> This enforces the max retry limit across multiple failed attempts.
> Only clear counter on SUCCESS or when model_id config changes.

```go
func (h *Handler) Execute(
    parentCtx context.Context,
    w http.ResponseWriter,
    r *http.Request,
    requestBody map[string]interface{},
    originalModelID string,
    hash string,
    headersSent *bool,
) error {
    cfg := h.config.Get()
    modelID := cfg.UltimateModel.ModelID

    // Get model config from DATABASE
    modelCfg := h.modelsMgr.GetModel(modelID)
    if modelCfg == nil {
        // Model not found - this is a config error, clear everything
        h.hashCache.Remove(hash) // Also clears retry counter
        return &ultimateModelError{
            message:  "ultimate model not found in database",
            internal: false,
        }
    }

    // NOTE: Retry counter is already incremented in ShouldTrigger()
    // No need to increment here

    // Set response header
    w.Header().Set("X-LLMProxy-Ultimate-Model", modelID)
    *headersSent = true

    // ... rest of execution logic ...

    if err != nil {
        // On failure: KEEP retry counter to enforce limit
        // DON'T remove hash - client can retry until MaxRetries exhausted
        log.Printf("[UltimateModel] Error executing with %s: %v", modelID, err)
        return err
    }

    // On success: clear retry counter but keep hash in cache
    // This prevents immediate re-triggering of ultimate model for same content
    h.hashCache.ClearRetryCount(hash)
    
    return nil
}
```

## 4. Proxy Handler Integration

### 4.1 Update `HandleChatCompletions` (`pkg/proxy/handler.go`)

```go
func (h *Handler) HandleChatCompletions(w http.ResponseWriter, r *http.Request) {
    // Step 1: Initialize request context
    rc, err := h.initRequestContext(r)
    if err != nil {
        h.handleError(w, r, err)
        return
    }
    
    // Step 2: ULTIMATE MODEL CHECK
    result := h.ultimateHandler.ShouldTrigger(rc.reqBody.Messages)
    
    if result.Triggered {
        // Check if retry limit exhausted
        if result.RetryExhausted {
            log.Printf("[UltimateModel] Retry limit exhausted for hash=%s (attempt %d/%d)",
                result.Hash[:8]+"...", result.CurrentRetry, result.MaxRetries)
            
            // Determine if streaming
            isStream := false
            if stream, ok := rc.reqBody.Stream.(bool); ok {
                isStream = stream
            }
            
            // Send error response (HTTP 200 with JSON stream error)
            h.ultimateHandler.SendRetryExhaustedError(
                w, result.Hash, result.CurrentRetry, result.MaxRetries, isStream)
            return
        }
        
        // Proceed with ultimate model
        ultimateModelID := h.ultimateHandler.GetModelID()
        log.Printf("[UltimateModel] Triggered for duplicate request, using %s, hash=%s, retry=%d/%d",
            ultimateModelID, result.Hash[:8]+"...", result.CurrentRetry+1, result.MaxRetries)
        
        rc.reqLog.UltimateModelUsed = true
        rc.reqLog.UltimateModelID = ultimateModelID
        
        err := h.ultimateHandler.Execute(r.Context(), w, r, rc.reqBody, rc.originalModel, result.Hash, &rc.headersSent)
        if err != nil {
            log.Printf("[UltimateModel] Error: %v", err)
        }
        return
    }
    
    // Step 3: Continue normal flow
    // ... existing code ...
}
```

## 5. Error Response Format

### 5.1 Streaming Response (SSE)

```
HTTP/1.1 200 OK
Content-Type: text/event-stream
X-LLMProxy-Ultimate-Model: retry-exhausted

data: {"error":{"message":"Ultimate model retry limit exceeded (attempt 2 of 2 max). Hash: abc12345...","type":"ultimate_model_retry_exhausted","code":"exhausted","hash":"abc12345...full hash..."}}

data: [DONE]

```

### 5.2 Non-Streaming Response (JSON)

```
HTTP/1.1 200 OK
Content-Type: application/json
X-LLMProxy-Ultimate-Model: retry-exhausted

{
  "error": {
    "message": "Ultimate model retry limit exceeded (attempt 2 of 2 max). Hash: abc12345...",
    "type": "ultimate_model_retry_exhausted",
    "code": "exhausted",
    "hash": "abc12345...full hash..."
  }
}
```

**Why HTTP 200?**
- Many LLM clients treat non-200 responses as network/transport errors and may retry
- HTTP 200 with error payload signals "request processed, but logical error occurred"
- The `data: [DONE]` in SSE signals the client to stop the stream gracefully

## 6. Frontend UI Changes

### 6.1 ProxySettings Component

> **⚠️ Review Fix (Issue #2)**: Aligned frontend validation with backend (0-100 allowed).
> Shows warning (yellow text) for values > 10 instead of blocking input.

Add new input field for max retries with validation:

```tsx
<div>
  <label class="block text-sm font-medium text-gray-300 mb-1">
    Ultimate Model Max Retries
  </label>
  <input
    type="number"
    value={maxRetries}
    onInput={(e) => {
      const val = parseInt((e.target as HTMLInputElement).value) || 0;
      // Allow 0-100 to match backend validation
      if (val >= 0 && val <= 100) {
        onMaxRetriesChange(val);
      }
    }}
    class={`w-full bg-gray-800 border rounded px-3 py-2 text-white ${
      maxRetries < 0 || maxRetries > 100 ? 'border-red-500' :
      maxRetries > 10 ? 'border-yellow-500' : 'border-gray-700'
    }`}
    min="0"
    max="100"
  />
  {maxRetries < 0 && (
    <p class="text-red-500 text-xs mt-1">Value cannot be negative</p>
  )}
  {maxRetries > 100 && (
    <p class="text-red-500 text-xs mt-1">Value cannot exceed 100</p>
  )}
  {maxRetries > 10 && maxRetries <= 100 && (
    <p class="text-yellow-500 text-xs mt-1">⚠️ High values may cause long retry loops</p>
  )}
  <p class="text-xs text-gray-500 mt-1">
    Maximum number of times the ultimate model can be retried for the same request hash.
    Set to 0 to disable retry limit (not recommended).
  </p>
</div>
```

### 6.2 SettingsPage State

```typescript
const [maxRetries, setMaxRetries] = useState(2);

// Sync from config
useEffect(() => {
  if (config) {
    setMaxRetries(config.ultimate_model?.max_retries ?? 2);
  }
}, [config]);

// Apply handler
const handleApplyProxy = async () => {
  await onUpdateConfig({
    // ... existing fields
    ultimate_model: {
      model_id: ultimateModelId,
      max_hash: maxHash,
      max_retries: maxRetries,
    },
  });
};
```

## 7. Files to Modify

| File | Changes |
|------|---------|
| `pkg/config/config.go` | Add `MaxRetries` field to `UltimateModelConfig`, update defaults, add env override |
| `pkg/ultimatemodel/hash_cache.go` | Add `retryCounter` map, add retry counter methods, update `Remove` and `Reset` |
| `pkg/ultimatemodel/handler.go` | Add `ShouldTriggerResult` struct, update `ShouldTrigger`, add `SendRetryExhaustedError`, update `Execute` |
| `pkg/proxy/handler.go` | Update ultimate model check to handle retry exhaustion |
| `pkg/ui/frontend/src/types.ts` | Add `max_retries` to `ultimate_model` interface |
| `pkg/ui/frontend/src/components/config/ProxySettings.tsx` | Add max retries input field |
| `pkg/ui/frontend/src/components/SettingsPage.tsx` | Add state management for max retries |

## 8. Edge Cases

> **⚠️ Review Fix**: Clarified failure behavior - counter is KEPT on failure (not cleared).
> See [review issue #3](./ultimate-model-retry-limit-review.md#3-inconsistency-remove-clears-counter-on-failure).

| Scenario | Behavior |
|----------|----------|
| `MaxRetries = 0` | No retry limit (infinite retries) - backward compatible |
| `MaxRetries = 1` | Ultimate model can only be attempted once |
| Hash removed from cache | Retry counter also cleared, fresh start |
| Ultimate model succeeds | Retry counter cleared, hash remains in cache |
| Ultimate model FAILS | **Retry counter KEPT (not cleared)** - enforces limit across retries |
| Config change (model_id) | Both hash cache and retry counters reset |
| Server restart | All retry counters lost (ephemeral, in-memory) |
| Concurrent requests with same hash | Atomic increment prevents race condition |
| **Hash evicted from circular buffer** | **Both hash and retry counter lost, treated as new request** |

> **🔑 Key Design Decision**: On ultimate model failure, the retry counter is KEPT (not cleared).
> This enforces the max retry limit across multiple failed attempts. The client receives an error
> only after all retry attempts are exhausted. This prevents clients from bypassing the retry limit
> by simply retrying after each failure.

> **⚠️ Review Addition (Issue #4)**: Hash eviction edge case documented.
> When the circular buffer is full (default 100 hashes), old hashes are overwritten.
> If a hash is evicted, both the hash entry and its retry counter are lost.
> Subsequent retries for that request will be treated as first-time requests.

## 9. Testing Strategy

### Unit Tests

1. **HashCache Retry Counter**
   - Test `GetRetryCount` returns 0 for new hash
   - Test `IncrementAndCheckRetry` increments correctly
   - Test `ClearRetryCount` removes counter
   - Test `Remove` clears both hash and counter
   - Test `Reset` clears all counters

2. **ShouldTriggerResult**
   - Test returns `RetryExhausted=false` when counter < max
   - Test returns `RetryExhausted=true` when counter > max
   - Test handles `MaxRetries=0` (no limit)

3. **Concurrent Request Test**
   > **⚠️ Review Addition**: Added concurrent test to verify mutex correctness.
   > See [review issue #3](./ultimate-model-retry-limit-review.md#3-inconsistency-remove-clears-counter-on-failure).
   
   ```go
   func TestHashCache_ConcurrentRetryCounter(t *testing.T) {
       cache := NewHashCache(100)
       hash := "test-hash-123"
       maxRetries := 2
       
       var wg sync.WaitGroup
       results := make([]bool, 10)
       
       for i := 0; i < 10; i++ {
           wg.Add(1)
           go func(idx int) {
               defer wg.Done()
               newCount, exhausted := cache.IncrementAndCheckRetry(hash, maxRetries)
               results[idx] = exhausted
               t.Logf("Request %d: count=%d, exhausted=%v", idx, newCount, exhausted)
           }(i)
       }
       
       wg.Wait()
       
       // Verify exactly (10 - maxRetries) requests see exhausted=true
       exhaustedCount := 0
       for _, ex := range results {
           if ex {
               exhaustedCount++
           }
       }
       assert.Equal(t, 10-maxRetries, exhaustedCount)
   }
   ```

### Integration Tests

1. Test ultimate model triggers on second request
2. Test ultimate model fails, retry counter increments
3. Test retry exhausted returns proper error response
4. Test retry exhausted error format (streaming and non-streaming)
5. Test successful ultimate model clears retry counter
6. Test hash removal clears retry counter
7. Test config change resets all counters

## 10. Migration Notes

- **No database migration required**: Retry counters are in-memory only
- **Backward compatible**: Default `MaxRetries=2` provides reasonable limit
- **Zero-downtime**: New field has sensible default

## 11. Implementation Order

1. Update `pkg/config/config.go` - add `MaxRetries` field
2. Update `pkg/ultimatemodel/hash_cache.go` - add retry counter map and methods
3. Update `pkg/ultimatemodel/handler.go` - add `ShouldTriggerResult`, update methods
4. Update `pkg/proxy/handler.go` - handle retry exhaustion
5. Update frontend types and components
6. Add unit tests
7. Add integration tests

## 12. Summary

| Aspect | Decision |
|--------|----------|
| Storage | In-memory map (ephemeral, resets on restart) |
| Default Max Retries | 2 (configurable) |
| Error Response | HTTP 200 with JSON error payload |
| Thread Safety | Mutex-protected map operations with atomic increment |
| Backward Compatible | Yes (default provides limit, 0 = unlimited) |

---

## 13. Retry Counter Semantics

> **⚠️ Review Addition (Issue #6)**: Clarified `MaxRetries` semantics.

The `MaxRetries` value determines the **total number of attempts** allowed for the ultimate model:

| MaxRetries | Meaning | Total Attempts |
|------------|---------|----------------|
| 0 | Unlimited (backward compatible) | ∞ |
| 1 | Single attempt only | 1 |
| 2 | First attempt + 1 retry | 2 |
| N | First attempt + (N-1) retries | N |

**Example with `MaxRetries = 2`:**
- Attempt 1: counter becomes 1, allowed (1 ≤ 2)
- Attempt 2: counter becomes 2, allowed (2 ≤ 2)
- Attempt 3: counter becomes 3, **exhausted** (3 > 2)

So `MaxRetries = 2` gives **2 total attempts** for the ultimate model.

---

## 14. Review Updates Summary

> **Changes made based on [ultimate-model-retry-limit-review.md](./ultimate-model-retry-limit-review.md)**:

| Issue # | Severity | Issue | Status | Fix Applied |
|---------|----------|-------|--------|-------------|
| #1 | 🔴 CRITICAL | Behavioral change - must DELETE existing `Remove()` call | ✅ Fixed | Added explicit diff instruction in Section 3.3 |
| #2 | 🟡 MEDIUM | Frontend/backend validation mismatch (0-10 vs 0-100) | ✅ Fixed | Aligned frontend to allow 0-100 with warning for > 10 |
| #3 | 🟡 MEDIUM | `GetUltimateModelMaxRetries()` is redundant | ✅ Noted | Marked as optional in Section 1.5 |
| #4 | 🟢 MINOR | Missing edge case for hash eviction | ✅ Fixed | Added to Edge Cases table in Section 8 |
| #5 | 🟢 MINOR | Header logic issue in `SendRetryExhaustedError` | ✅ Fixed | Fixed header order in Section 3.2 |
| #6 | 🟢 MINOR | Retry counter semantics unclear | ✅ Fixed | Added new Section 13 with semantics explanation |

---

## 15. Implementation Checklist

Before merging implementation:

- [ ] **CRITICAL**: Delete existing `h.hashCache.Remove(hash)` call from failure path in `Execute()` (see Section 3.3)
- [ ] Add `retryCounter` map initialization in `NewHashCache()`
- [ ] Update `Reset()` to reinitialize `retryCounter` map
- [ ] Add `MaxRetries` to `UltimateModelConfig` with JSON tag
- [ ] Add env override `ULTIMATE_MODEL_MAX_RETRIES`
- [ ] Add validation in `Config.Validate()` (0-100)
- [ ] Update frontend types and UI components (allow 0-100, warn > 10)
- [ ] Add unit tests for `HashCache` retry counter methods
- [ ] Add integration test for retry exhaustion flow
- [ ] Test concurrent request handling
