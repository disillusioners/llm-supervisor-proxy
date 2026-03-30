# Plan: 429 Model Memory Cache

## Objective

Add an in-memory cache that remembers which models have returned HTTP 429 (Too Many Requests) errors, with a 10-minute TTL. When building the model list for a request, skip models currently in the "429 memory" and go directly to fallback models.

## Scope Assessment

**Scope: MEDIUM** — Affects 5 files, 1 new file, single package (`pkg/proxy/`), ~4-6 hours estimated.

## Context

- **Project**: llm-supervisor-proxy (Go-based LLM API proxy)
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Request Flow**: `HandleChatCompletions()` → `initRequestContext()` → `buildModelList()` → `RaceCoordinator.Start()` → `executeRequest()` → `GetFinalErrorInfo()`
- **Key Insight**: 429 is already detected in `GetFinalErrorInfo()` via `req.GetHTTPStatus() == http.StatusTooManyRequests`. We need to capture this earlier and cache it.

---

## Architecture

```
┌─────────────────────────────────────────────────────────────────┐
│ REQUEST LIFECYCLE WITH 429 CACHE                                │
├─────────────────────────────────────────────────────────────────┤
│                                                                  │
│  1. initRequestContext()                                        │
│     └─→ query ModelCache.IsBlocked()                            │
│     └─→ buildModelList() skips blocked models                   │
│                                                                  │
│  2. RaceCoordinator.Start() → manage() loop                     │
│     ├─ Case A: Request fails → check GetHTTPStatus()==429       │
│     │           └─→ ModelCache.MarkBlocked(modelID)            │
│     └─ Case B: spawn fallback → ModelCache.IsBlocked() check    │
│                                                                  │
│  3. GetFinalErrorInfo() (existing)                              │
│     └─→ Already detects 429, but cache was already updated      │
│                                                                  │
│  4. Background cleanup goroutine (every minute)                  │
│     └─→ Remove expired entries                                   │
│                                                                  │
└─────────────────────────────────────────────────────────────────┘
```

---

## Task Breakdown

### Phase 1: Config — Add cache settings

| # | Task | File | Est. Time | Risk |
|---|------|------|-----------|------|
| 1.1 | Add `ModelMemoryCacheConfig` struct to `Config` | `pkg/config/config.go` | 15 min | low |
| 1.2 | Add `ModelMemoryCache` field to `Config` | `pkg/config/config.go` | 10 min | low |
| 1.3 | Add defaults to `Defaults` variable | `pkg/config/config.go` | 10 min | low |

### Phase 2: New Package — ModelCache implementation

| # | Task | File | Est. Time | Risk |
|---|------|------|-----------|------|
| 2.1 | Create `ModelCache` struct with `map[string]time.Time` + `RWMutex` | `pkg/proxy/model_cache.go` | 30 min | low |
| 2.2 | Implement `NewModelCache()`, `IsBlocked()`, `MarkBlocked()`, `Cleanup()` | `pkg/proxy/model_cache.go` | 30 min | low |
| 2.3 | Add `ModelCache` field to `ConfigSnapshot` | `pkg/proxy/handler.go` | 15 min | low |
| 2.4 | Update `Clone()` to include cache config | `pkg/proxy/handler.go` | 15 min | low |
| 2.5 | Update `NewHandler()` to create and start cache | `pkg/proxy/handler.go` | 20 min | low |

### Phase 3: Integration — Filter models in buildModelList

| # | Task | File | Est. Time | Risk |
|---|------|------|-----------|------|
| 3.1 | Update `buildModelList()` to accept cache parameter and filter | `pkg/proxy/handler_helpers.go` | 20 min | low |
| 3.2 | Update `initRequestContext()` to pass cache to `buildModelList()` | `pkg/proxy/handler_functions.go` | 15 min | low |

### Phase 4: Integration — Mark blocked models on 429

| # | Task | File | Est. Time | Risk |
|---|------|------|-----------|------|
| 4.1 | Update `ConfigSnapshot` to include `ModelCache` reference | `pkg/proxy/handler.go` | 10 min | low |
| 4.2 | Add 429 detection in `manage()` — when request fails with 429 | `pkg/proxy/race_coordinator.go` | 25 min | medium |
| 4.3 | Update `spawn()` to skip blocked models | `pkg/proxy/race_coordinator.go` | 20 min | medium |

### Phase 5: Testing

| # | Task | File | Est. Time | Risk |
|---|------|------|-----------|------|
| 5.1 | Write unit tests for `ModelCache` | `pkg/proxy/model_cache_test.go` | 45 min | low |
| 5.2 | Verify `go build` passes | — | 10 min | low |

---

## Implementation Details

### Step 1.1: Add ModelMemoryCacheConfig struct to Config

**File**: `pkg/config/config.go`
**Location**: After line 95 (after existing config fields)

```go
// ModelMemoryCacheConfig controls the in-memory cache for rate-limited models.
// When a model returns 429, it's cached for TTL duration to skip it in subsequent requests.
type ModelMemoryCacheConfig struct {
    Enabled bool          `json:"enabled"`
    TTL     Duration      `json:"ttl"`
}
```

### Step 1.2: Add config field

**File**: `pkg/config/config.go`
**Location**: Inside `Config` struct (around line 94)

Add at end of `Config` struct:

```go
// 429 Model Memory Cache
ModelMemoryCache ModelMemoryCacheConfig `json:"model_memory_cache"`
```

### Step 1.3: Add defaults

**File**: `pkg/config/config.go`
**Location**: Inside `Defaults` variable (after `LogRawUpstreamMaxKB` line)

```go
ModelMemoryCache: ModelMemoryCacheConfig{
    Enabled: true,
    TTL:     Duration(10 * time.Minute),
},
```

### Step 2.1-2.2: Create ModelCache implementation

**File**: `pkg/proxy/model_cache.go` (NEW FILE)

```go
package proxy

import (
    "sync"
    "time"
)

// ModelCache tracks models that have returned 429 errors.
// Thread-safe using RWMutex.
type ModelCache struct {
    mu        sync.RWMutex
    blocked   map[string]time.Time  // modelID → expiry time
    ttl       time.Duration
    enabled   bool
    stopCh    chan struct{}
    stopped   bool
}

// NewModelCache creates a new ModelCache with the given TTL.
// Starts a background cleanup goroutine.
func NewModelCache(enabled bool, ttl time.Duration) *ModelCache {
    mc := &ModelCache{
        blocked: make(map[string]time.Time),
        ttl:     ttl,
        enabled: enabled,
        stopCh:  make(chan struct{}),
    }
    
    if enabled {
        go mc.cleanupLoop()
    }
    
    return mc
}

// IsBlocked returns true if the model is currently blocked due to a 429.
func (mc *ModelCache) IsBlocked(modelID string) bool {
    if !mc.enabled {
        return false
    }
    
    mc.mu.RLock()
    defer mc.mu.RUnlock()
    
    expiry, exists := mc.blocked[modelID]
    if !exists {
        return false
    }
    
    if time.Now().After(expiry) {
        return false
    }
    
    return true
}

// MarkBlocked marks a model as blocked for the configured TTL.
func (mc *ModelCache) MarkBlocked(modelID string) {
    if !mc.enabled {
        return
    }
    
    mc.mu.Lock()
    defer mc.mu.Unlock()
    
    mc.blocked[modelID] = time.Now().Add(mc.ttl)
}

// FilterModels returns a new slice containing only non-blocked models.
// Preserves order.
func (mc *ModelCache) FilterModels(models []string) []string {
    if !mc.enabled || len(models) == 0 {
        return models
    }
    
    mc.mu.RLock()
    defer mc.mu.RUnlock()
    
    now := time.Now()
    var filtered []string
    for _, model := range models {
        expiry, blocked := mc.blocked[model]
        if blocked && now.Before(expiry) {
            continue  // Skip blocked model
        }
        filtered = append(filtered, model)
    }
    
    if filtered == nil {
        return models  // Return original if all blocked (graceful degradation)
    }
    
    return filtered
}

// cleanupLoop runs every minute, removing expired entries.
func (mc *ModelCache) cleanupLoop() {
    ticker := time.NewTicker(1 * time.Minute)
    defer ticker.Stop()
    
    for {
        select {
        case <-mc.stopCh:
            return
        case <-ticker.C:
            mc.cleanup()
        }
    }
}

// cleanup removes expired entries from the cache.
func (mc *ModelCache) cleanup() {
    mc.mu.Lock()
    defer mc.mu.Unlock()
    
    now := time.Now()
    for model, expiry := range mc.blocked {
        if now.After(expiry) {
            delete(mc.blocked, model)
        }
    }
}

// Stop stops the background cleanup goroutine.
func (mc *ModelCache) Stop() {
    if mc.enabled {
        mc.mu.Lock()
        if !mc.stopped {
            mc.stopped = true
            close(mc.stopCh)
        }
        mc.mu.Unlock()
    }
}

// Len returns the number of blocked models (for testing/monitoring).
func (mc *ModelCache) Len() int {
    mc.mu.RLock()
    defer mc.mu.RUnlock()
    return len(mc.blocked)
}
```

### Step 2.3-2.5: Update ConfigSnapshot and NewHandler

**File**: `pkg/proxy/handler.go`

**Add to ConfigSnapshot struct** (after `EventBus` field):

```go
// 429 Model Memory Cache
ModelCache *ModelCache
```

**Update Clone()** (add after `EventBus` line in return statement):

```go
ModelCache: c.modelCache,  // Handler needs modelCache field
```

**Update Handler struct** to add:

```go
type Handler struct {
    config          *Config
    modelCache      *ModelCache  // ADD THIS
    // ... rest unchanged
}
```

**Update NewHandler()**:

```go
func NewHandler(config *Config, bus *events.Bus, store *store.RequestStore, bufferStore *bufferstore.BufferStore, tokenStore *auth.TokenStore) *Handler {
    h := &Handler{
        config:      config,
        bus:         bus,
        store:       store,
        client:      &http.Client{ /* existing transport */ },
        bufferStore: bufferStore,
        tokenStore:  tokenStore,
        modelCache:  NewModelCache(config.ModelMemoryCache.Enabled, config.ModelMemoryCache.TTL.Duration()),
    }
    // ... rest unchanged
}
```

### Step 3.1: Update buildModelList()

**File**: `pkg/proxy/handler_helpers.go`

Change function signature from:

```go
func buildModelList(originalModel string, modelsConfig models.ModelsConfigInterface) []string
```

To:

```go
func buildModelList(originalModel string, modelsConfig models.ModelsConfigInterface, modelCache *ModelCache) []string
```

Add filtering after building the list (before return):

```go
func buildModelList(originalModel string, modelsConfig models.ModelsConfigInterface, modelCache *ModelCache) []string {
    var allModels []string
    if modelsConfig != nil {
        fallbackChain := modelsConfig.GetFallbackChain(originalModel)
        if len(fallbackChain) > 0 {
            allModels = fallbackChain[1:]
        }
    }
    if allModels == nil {
        allModels = []string{}
    }
    modelList := []string{originalModel}
    modelList = append(modelList, allModels...)
    
    // Filter out models that are currently rate-limited
    if modelCache != nil && modelCache.enabled {
        modelList = modelCache.FilterModels(modelList)
    }
    
    return modelList
}
```

### Step 3.2: Update initRequestContext()

**File**: `pkg/proxy/handler_functions.go`

Change line 80 from:

```go
modelList := buildModelList(originalModel, conf.ModelsConfig)
```

To:

```go
modelList := buildModelList(originalModel, conf.ModelsConfig, h.modelCache)
```

### Step 4.1: Ensure ConfigSnapshot has ModelCache reference

Already covered in Step 2.3-2.5 above.

### Step 4.2: Add 429 detection in manage()

**File**: `pkg/proxy/race_coordinator.go`

**Location**: Inside `manage()` function, in the "Case 1: Latest request FAILED" block (around line 280-290).

Change from:

```go
if latestReq.IsDone() && latestReq.GetError() != nil {
    errMsg := latestReq.GetError().Error()
    log.Printf("[RACE] Latest request %d failed: %s, spawning fallback directly", latestReq.id, errMsg)
    shouldSpawn = true
    triggerInfo = spawnTriggerInfo{
        trigger:       triggerMainError,
        errorMessage:  errMsg,
        failedRequest: latestReq.id,
    }
}
```

To:

```go
if latestReq.IsDone() && latestReq.GetError() != nil {
    errMsg := latestReq.GetError().Error()
    
    // Mark model as blocked if it returned 429
    if latestReq.GetHTTPStatus() == http.StatusTooManyRequests && c.cfg.ModelCache != nil {
        c.cfg.ModelCache.MarkBlocked(latestReq.modelID)
        log.Printf("[RACE] Model %s returned 429, added to memory cache", latestReq.modelID)
    }
    
    log.Printf("[RACE] Latest request %d failed: %s, spawning fallback directly", latestReq.id, errMsg)
    shouldSpawn = true
    triggerInfo = spawnTriggerInfo{
        trigger:       triggerMainError,
        errorMessage:  errMsg,
        failedRequest: latestReq.id,
    }
}
```

Also update the "All failed" check (around line 347-382) to mark all 429'd models:

```go
if c.winner == nil && len(c.requests) >= len(c.models) {
    allFailed := true
    for _, r := range c.requests {
        if !r.IsDone() || r.GetError() == nil {
            allFailed = false
            break
        }
        // Mark any 429 models in cache before closing
        if r.GetHTTPStatus() == http.StatusTooManyRequests && c.cfg.ModelCache != nil {
            c.cfg.ModelCache.MarkBlocked(r.modelID)
        }
    }
    if allFailed {
        // ... rest unchanged
    }
}
```

### Step 4.3: Update spawn() to skip blocked models

**File**: `pkg/proxy/race_coordinator.go`

**Location**: Inside `spawn()` function, after determining modelID.

Add check before proceeding with the request:

```go
// In spawn(), after determining modelID based on mType
// Check cache before proceeding
if c.cfg.ModelCache != nil && c.cfg.ModelCache.IsBlocked(modelID) {
    log.Printf("[RACE] Model %s is blocked (429 memory), skipping", modelID)
    return  // Don't spawn - manage() will handle checking for more models
}
```

---

## Dependencies

1. `ModelCache` → used by `Handler`, `buildModelList()`, `raceCoordinator`
2. Config changes → need defaults for graceful degradation
3. Model filtering in `buildModelList()` → must preserve original behavior when cache disabled

---

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Cache not thread-safe | High | Use RWMutex as specified |
| All models blocked → no requests | Medium | `FilterModels()` returns original list if all blocked |
| Memory leak from expired entries | Medium | Background cleanup goroutine (1-min interval) |
| Cache enabled but cleanup goroutine dies | Low | Handle in `Stop()` method |
| Config TTL not set → division by zero | Low | Defaults guarantee TTL > 0 |

---

## Success Criteria

- [ ] `pkg/proxy/model_cache.go` created with thread-safe `ModelCache` implementation
- [ ] `pkg/config/config.go` has `ModelMemoryCache` config with `Enabled` (default: true) and `TTL` (default: 10 min)
- [ ] `buildModelList()` filters blocked models before returning
- [ ] When a model returns 429, it's added to the cache
- [ ] Subsequent requests skip the cached model for TTL duration
- [ ] If ALL models are blocked, graceful degradation proceeds with original list
- [ ] Background cleanup removes expired entries every minute
- [ ] `go build` passes with no errors
- [ ] Unit tests cover `ModelCache` core functionality

---

## Tracking

- **Created**: 2026-03-22
- **Last Updated**: 2026-03-22
- **Status**: draft
- **Plan File**: `.agents/shared/working/429-model-memory-cache/plan.md`
