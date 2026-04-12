# Phase 2: Core Retry Logic — Use Secondary Upstream Model

## Objective
Modify the race coordinator and executor so that when a `modelTypeSecond` request is spawned (idle timeout parallel race), it uses the secondary upstream model if configured, instead of the same primary upstream model.

## Coupling
- **Depends on**: Phase 1 (needs `SecondaryUpstreamModel` field in ModelConfig + store queries)
- **Coupling type**: tight — reads the new field from ModelConfig
- **Shared files with other phases**: `pkg/models/config.go` (reads SecondaryUpstreamModel), `pkg/store/database/store.go` (ResolveInternalConfig)
- **Shared APIs/interfaces**: `ModelsConfigInterface.ResolveInternalConfig()`
- **Why this coupling**: Must read the new field from the model config at request execution time

## Context
Phase 1 delivered: `ModelConfig.SecondaryUpstreamModel` field is stored and retrieved.

Current retry flow in `race_coordinator.go`:
```
spawn(modelTypeMain)   → models[0] → executeRequest() → ResolveInternalConfig(modelID) → "glm-5.0"
spawn(modelTypeSecond) → models[0] → executeRequest() → ResolveInternalConfig(modelID) → "glm-5.0"  ← SAME
spawn(modelTypeFallback) → models[1] → executeRequest() → ResolveInternalConfig(fallbackID) → different
```

The problem: `modelTypeSecond` uses `models[0]` (same proxy model ID), so `ResolveInternalConfig` returns the same upstream model.

## Design: Two Approaches Considered

### Approach A: Pass "use secondary" flag through coordinator → executor → ResolveInternalConfig
- Add a flag to `upstreamRequest` like `useSecondaryUpstream bool`
- In `spawn()`, set it to `true` for `modelTypeSecond`
- In `executeInternalRequest()`, pass it to `ResolveInternalConfig`
- `ResolveInternalConfig` checks the flag and returns SecondaryUpstreamModel instead of InternalModel

**Pros:** Clean separation, no new model list entry needed
**Cons:** Requires modifying the ResolveInternalConfig interface signature

### Approach B: Create a "virtual" model config that overrides InternalModel
- At the point where modelID is assigned in `spawn()`, create an override mechanism
- The executor sees a different effective InternalModel

**Cons:** Would require duplicating model configs or adding override state

### **Chosen: Approach A** — Flag-based, minimal surface area change

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add `useSecondaryUpstream` flag to `upstreamRequest` | Add field and setter method | `pkg/proxy/race_request.go` |
| 2 | Set flag in `spawn()` for `modelTypeSecond` | When spawning a second request, set `req.SetUseSecondaryUpstream(true)` | `pkg/proxy/race_coordinator.go` |
| 3 | Modify `ResolveInternalConfig` interface | Add optional `secondary bool` parameter or create `ResolveInternalConfigSecondary()` method | `pkg/models/config.go`, `pkg/store/database/store.go` |
| 4 | Implement secondary model resolution | When `secondary=true`, use `SecondaryUpstreamModel` if non-empty, else fall back to `InternalModel` (unchanged behavior) | `pkg/models/config.go`, `pkg/store/database/store.go` |
| 5 | Pass flag through `executeRequest` → `executeInternalRequest` | In `executeInternalRequest`, check `req.UseSecondaryUpstream()` and pass to ResolveInternalConfig | `pkg/proxy/race_executor.go` |
| 6 | Add logging for secondary model usage | Log when secondary upstream model is used: "using secondary upstream model X instead of Y" | `pkg/proxy/race_executor.go` |
| 7 | Publish event for secondary model usage | Publish `race_secondary_model_used` event so frontend can show it | `pkg/proxy/race_executor.go` or `race_coordinator.go` |

## Key Files
- `pkg/proxy/race_request.go` — Add flag
- `pkg/proxy/race_coordinator.go` — Set flag on spawn
- `pkg/proxy/race_executor.go` — Pass flag to resolver
- `pkg/models/config.go` — ResolveInternalConfig changes
- `pkg/store/database/store.go` — ResolveInternalConfig implementation

## Detailed Implementation Notes

### Task 1: upstreamRequest flag
```go
// In upstreamRequest struct:
useSecondaryUpstream bool

// Methods:
func (r *upstreamRequest) SetUseSecondaryUpstream(val bool) {
    r.mu.Lock()
    defer r.mu.Unlock()
    r.useSecondaryUpstream = val
}

func (r *upstreamRequest) UseSecondaryUpstream() bool {
    r.mu.RLock()
    defer r.mu.RUnlock()
    return r.useSecondaryUpstream
}
```

### Task 2: spawn() modification
In `race_coordinator.go`, inside `spawn()`, after creating the request:
```go
if mType == modelTypeSecond {
    req.SetUseSecondaryUpstream(true)
}
```

### Task 3: ResolveInternalConfig changes

**Option A (preferred):** Add a new method to the interface:
```go
// Add to ModelsConfigInterface:
ResolveSecondaryConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool)
```

This keeps `ResolveInternalConfig` unchanged and adds a separate method. Implementation:
```go
func (mc *ModelsConfig) ResolveSecondaryConfig(modelID string) (provider, apiKey, baseURL, model string, ok bool) {
    // Same as ResolveInternalConfig but uses SecondaryUpstreamModel instead of InternalModel
    // If SecondaryUpstreamModel is empty, falls back to InternalModel (unchanged behavior)
    modelConfig := mc.GetModel(modelID)
    if modelConfig == nil || !modelConfig.Internal {
        return "", "", "", "", false
    }
    // ... credential resolution same as ResolveInternalConfig ...
    
    actualModel := modelConfig.InternalModel
    // Check secondary
    if modelConfig.SecondaryUpstreamModel != "" {
        actualModel = modelConfig.SecondaryUpstreamModel
    }
    // Peak hour check still applies to the resolved model? 
    // Actually NO — secondary model is for retries, peak hour is for the initial model selection.
    // But it depends on the design decision. Let's NOT apply peak hour to secondary.
    // Secondary is explicitly chosen by user as the retry target.
    
    return provider, cred.APIKey, baseURL, actualModel, true
}
```

**Alternative (interface-preserving):** Don't change the interface. Instead, handle it entirely in the executor by getting the model config directly:
```go
// In executeInternalRequest:
var provider, apiKey, baseURL, internalModel string
var ok bool

if req.UseSecondaryUpstream() && cfg.ModelsConfig != nil {
    modelConfig := cfg.ModelsConfig.GetModel(req.modelID)
    if modelConfig != nil && modelConfig.SecondaryUpstreamModel != "" {
        // Use secondary model with same credential
        provider, apiKey, baseURL, _, ok = cfg.ModelsConfig.ResolveInternalConfig(req.modelID)
        if ok {
            internalModel = modelConfig.SecondaryUpstreamModel
            log.Printf("[SECONDARY] Using secondary upstream model %s instead of %s", internalModel, modelConfig.InternalModel)
        }
    }
}

if !ok {
    provider, apiKey, baseURL, internalModel, ok = cfg.ModelsConfig.ResolveInternalConfig(req.modelID)
}
```

**Recommendation:** Use the **Alternative approach** (no interface change). It's simpler, keeps the interface stable, and the logic is contained in one place. The ResolveInternalConfig gives us the credential/provider/baseURL, and we just swap the model name.

### Task 5: Executor changes
In `executeInternalRequest()` (race_executor.go line 49), before calling ResolveInternalConfig:

```go
func executeInternalRequest(ctx context.Context, cfg *ConfigSnapshot, rawBody []byte, req *upstreamRequest) error {
    var provider, apiKey, baseURL, internalModel string
    var ok bool
    
    // Check if secondary upstream model should be used
    useSecondary := req.UseSecondaryUpstream()
    
    if useSecondary && cfg.ModelsConfig != nil {
        modelConfig := cfg.ModelsConfig.GetModel(req.modelID)
        if modelConfig != nil && modelConfig.SecondaryUpstreamModel != "" {
            // Resolve credential/provider from primary config, but use secondary model name
            provider, apiKey, baseURL, _, ok = cfg.ModelsConfig.ResolveInternalConfig(req.modelID)
            if ok {
                internalModel = modelConfig.SecondaryUpstreamModel
                log.Printf("[SECONDARY] Using secondary upstream model %s instead of %s for model %s", 
                    internalModel, modelConfig.InternalModel, req.modelID)
            }
        }
    }
    
    if !ok {
        provider, apiKey, baseURL, internalModel, ok = cfg.ModelsConfig.ResolveInternalConfig(req.modelID)
    }
    
    if !ok {
        return fmt.Errorf("failed to resolve internal config for model %s", req.modelID)
    }
    // ... rest of function unchanged ...
}
```

Note: We log `InternalModel` (the configured primary) as the comparison target, NOT the peak-hour-resolved model. Peak hour does not apply to secondary model selection (per Decision #4). The secondary model is always used as-configured when retrying.

### Task 7: Event publishing
When secondary model is used, publish event:
```go
if useSecondary && cfg.EventBus != nil {
    cfg.EventBus.Publish(events.Event{
        Type:      "race_secondary_model_used",
        Timestamp: time.Now().Unix(),
        Data: map[string]interface{}{
            "id":                fmt.Sprintf("%d", req.id),
            "model_id":          req.modelID,
            "primary_model":     modelConfig.InternalModel,
            "secondary_model":   modelConfig.SecondaryUpstreamModel,
        },
    })
}
```

## Edge Cases
1. **No secondary configured** → `UseSecondaryUpstream()=true` but `SecondaryUpstreamModel=""` → falls through to normal ResolveInternalConfig → unchanged behavior ✓
2. **Non-internal model** → `GetModel()` returns model without `Internal=true` → ResolveInternalConfig returns `ok=false` → falls through to normal → unchanged ✓  
3. **External upstream model** → `executeRequest()` goes to `executeExternalRequest()` path → `UseSecondaryUpstream()` flag is never checked → unchanged ✓
4. **Peak hour active + secondary** → Secondary model takes precedence (user explicitly chose it for retry). Peak hour only affects primary model selection.
5. **modelTypeFallback** → `UseSecondaryUpstream()` is NOT set → uses the fallback model's own ResolveInternalConfig → unchanged ✓

## Constraints
- Must NOT change `ResolveInternalConfig` interface signature (too many callers)
- Must NOT change fallback model behavior
- Must be a no-op for non-internal models
- Must be a no-op when secondary is not configured

## Deliverables
- [ ] `upstreamRequest` has `useSecondaryUpstream` flag
- [ ] `spawn()` sets flag for `modelTypeSecond`
- [ ] `executeInternalRequest()` uses secondary model when flag is set
- [ ] Logging shows when secondary is used
- [ ] Event published for frontend tracking
- [ ] Existing tests pass
