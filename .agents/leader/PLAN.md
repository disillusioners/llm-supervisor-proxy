# Plan: 429 Model Memory Cache

## Overview

Add an in-memory cache that remembers models returning 429 errors, with a 10-minute TTL. When building the model list, skip models currently in the "429 memory" and use fallbacks directly.

---

## Objective

Improve UX by avoiding models that have recently rate-limited, reducing unnecessary request attempts and faster fallback to available models.

---

## Architecture

```
Request arrives
      ↓
buildModelList()
      ↓
┌─────────────────────────────────────┐
│ 429 Model Memory Cache (new)        │
│ - In-memory map[modelID]expiry      │
│ - TTL: 10 minutes (configurable)    │
│ - Thread-safe with RWMutex          │
└─────────────────────────────────────┘
      ↓
Filter out 429'd models from primary chain
      ↓
Continue with race coordinator (only non-429'd models)
```

---

## Changes

### 1. New Component: 429 Model Memory Cache

**File:** `pkg/ratelimit/model_memory.go` (new)

**Purpose:** Thread-safe in-memory cache with TTL

**Interface:**
```go
type ModelMemory interface {
    Is429d(modelID string) bool          // Check if model is marked 429
    Mark429(modelID string)              // Mark model as 429 (sets TTL)
    Cleanup()                             // Remove expired entries
    Size() int                            // Debug: cache size
}
```

**Implementation:**
- `map[string]time.Time` — stores modelID → expiry time
- `sync.RWMutex` for thread safety
- `time.Now()` comparison for TTL checks
- `Cleanup()` called periodically or on access

**Config options:**
```go
RateLimitModelMemoryEnabled bool          // Feature toggle (default: true)
RateLimitModelMemoryTTL     time.Duration // TTL (default: 10 min)
```

---

### 2. Config Extension

**File:** `pkg/config/config.go`

**Add fields:**
```go
RateLimitModelMemoryEnabled bool          `koanf:"ratelimit.modelmemory.enabled"`
RateLimitModelMemoryTTL     time.Duration `koanf:"ratelimit.modelmemory.ttl"`
```

**Default values in config:**
```go
koanf.SetDefault("ratelimit.modelmemory.enabled", true)
koanf.SetDefault("ratelimit.modelmemory.ttl", 10*time.Minute)
```

---

### 3. Integration in Model Selection

**File:** `pkg/proxy/handler_helpers.go`

**Modify `buildModelList()` (line ~163):**

```go
// NEW: Filter out models currently in 429 memory
func (h *HandlerHelpers) buildModelList(req *RequestContext) ([]ModelTarget, error) {
    // ... existing logic ...
    
    // Filter models based on 429 memory
    if h.modelMemory != nil && h.config.RateLimitModelMemoryEnabled {
        filtered := make([]ModelTarget, 0, len(targets))
        for _, target := range targets {
            if !h.modelMemory.Is429d(target.Model) {
                filtered = append(filtered, target)
            }
        }
        targets = filtered
    }
    
    // ... rest of existing logic ...
}
```

---

### 4. Integration in 429 Detection

**File:** `pkg/proxy/race_executor.go`

**Modify `executeRequest()` or `handleResponse()`:**

```go
// When 429 is detected, mark the model
if resp.StatusCode == 429 {
    if h.modelMemory != nil && h.config.RateLimitModelMemoryEnabled {
        h.modelMemory.Mark429(requestedModel)
    }
}
```

**Note:** This should happen in the executor when it receives a 429 from upstream.

---

### 5. Wire Up Dependencies

**File:** `pkg/proxy/handler.go`

**Modify `NewHandler()` constructor:**

```go
type Handler struct {
    // ... existing fields ...
    modelMemory *ratelimit.ModelMemoryCache // NEW
}

// Constructor update
func NewHandler(..., modelMemory *ratelimit.ModelMemoryCache) *Handler {
    return &Handler{
        // ... existing init ...
        modelMemory: modelMemory,
    }
}
```

**File:** `cmd/main.go`

Wire the new component:
```go
modelMemory := ratelimit.NewModelMemoryCache(cfg)
defer modelMemory.Stop() // Background cleanup goroutine

handler := proxy.NewHandler(..., modelMemory)
```

---

## File Changes Summary

| File | Action | Purpose |
|------|--------|---------|
| `pkg/ratelimit/model_memory.go` | **NEW** | 429 memory cache implementation |
| `pkg/config/config.go` | MODIFY | Add feature toggle and TTL config |
| `pkg/proxy/handler_helpers.go` | MODIFY | Filter 429'd models in `buildModelList()` |
| `pkg/proxy/race_executor.go` | MODIFY | Mark model as 429 on detection |
| `pkg/proxy/handler.go` | MODIFY | Add modelMemory to Handler struct |
| `cmd/main.go` | MODIFY | Wire up new component |

---

## Testing Considerations

1. **Unit tests** for `ModelMemory`:
   - `Is429d()` returns false for unmarked model
   - `Mark429()` causes `Is429d()` to return true
   - After TTL expires, `Is429d()` returns false
   - Concurrent access safety

2. **Integration test**:
   - Mock 429 response, verify model is skipped on next request
   - Wait for TTL, verify model is tried again

---

## Edge Cases

| Case | Handling |
|------|----------|
| All models in chain are 429'd | Proceed anyway — let race coordinator handle errors |
| Config disabled | ModelMemory ops are no-ops |
| Cleanup race | Use atomic operations, mutex protection |
| Very short TTL (< 1s) | Allow but document recommendation |
| Model not found in cache | `Is429d()` returns false (not found = not 429'd) |

---

## Scope: MEDIUM

- Single focused feature
- Clear requirements
- Well-defined integration points
- No database changes

---

## Estimated Effort

- Config + ModelMemory: 2 hours
- Integration + tests: 2 hours
- Total: ~4 hours
