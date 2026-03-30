# Phase 2: API & Proxy Integration

## Objective
Wire the peak hour fields through the API layer (UI server handlers, model struct mapping) and integrate the scheduling logic into **both** `ResolveInternalConfig()` implementations (JSON-backed and DB-backed) so that peak-hour model switching happens at runtime.

## Context
- Phase 1 delivered: ModelConfig fields, peak hour logic on `ModelConfig.ResolvePeakHourModel()`, validation, DB migration, query builder, store.go CRUD
- `pkg/ui/server.go` has a local `Model` struct for API serialization — needs matching fields
- Handlers map between `models.ModelConfig` ↔ `server.Model` in GET/POST/PUT handlers
- **Two implementations of `ResolveInternalConfig()`**:
  - `pkg/models/config.go` (JSON-backed `ModelsConfig`) — returns `modelConfig.InternalModel`
  - `pkg/store/database/store.go` (DB-backed `ModelsManager`) — returns `modelConfig.InternalModel`
- `ConfigSnapshot.ModelsConfig` is a live pointer — `time.Now()` at request time is correct
- `race_executor.go` calls `ResolveInternalConfig()` — no changes needed there

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add peak hour fields to UI `Model` struct | Mirror the 5 new fields with JSON tags | `pkg/ui/server.go` (~line 26) |
| 2 | Update GET mapping in `handleModels` | Map new fields from `models.ModelConfig` → `server.Model` | `pkg/ui/server.go` (~line 362) |
| 3 | Update POST mapping in `handleModels` | Map new fields from `server.Model` → `models.ModelConfig` | `pkg/ui/server.go` (~line 407) |
| 4 | Update PUT mapping in `handleModelDetail` | Map new fields from `server.Model` → `models.ModelConfig` | `pkg/ui/server.go` (~line 478) |
| 5 | Add API validation in POST/PUT handlers | If `PeakHourEnabled: true`, validate model is internal. Return 400 with clear error message: "peak_hour_enabled requires internal upstream" | `pkg/ui/server.go` |
| 6 | Integrate peak-hour into JSON-backed `ResolveInternalConfig()` | Before returning `modelConfig.InternalModel`, call `modelConfig.ResolvePeakHourModel(time.Now())`. If non-empty, substitute. | `pkg/models/config.go` (~line 521) |
| 7 | Integrate peak-hour into DB-backed `ResolveInternalConfig()` | Same logic as Task 6: call `modelConfig.ResolvePeakHourModel(time.Now())` before returning. | `pkg/store/database/store.go` (~line 1087) |
| 8 | Add logging for peak-hour switches | When peak model is substituted, log at info level: `"peak hour active for model %s: using %s instead of %s"` | `pkg/models/config.go`, `pkg/store/database/store.go` |
| 9 | Write integration test | Test `ResolveInternalConfig()` returns peak model during window and normal model outside window, for both implementations | `pkg/models/peak_hours_test.go` (extend) |

## Key Files
- `pkg/ui/server.go` — UI Model struct, handleModels(), handleModelDetail()
- `pkg/models/config.go` — JSON-backed ResolveInternalConfig()
- `pkg/store/database/store.go` — DB-backed ResolveInternalConfig()

## Detailed Implementation: Tasks 6 & 7 (ResolveInternalConfig Integration)

Both implementations follow the same pattern. The peak-hour substitution is identical:

```go
// Determine actual model: check peak hour first
actualModel := modelConfig.InternalModel
if peakModel := modelConfig.ResolvePeakHourModel(time.Now()); peakModel != "" {
    log.Printf("peak hour active for model %s: using %s instead of %s",
        modelConfig.ID, peakModel, modelConfig.InternalModel)
    actualModel = peakModel
}
return provider, cred.APIKey, baseURL, actualModel, true
```

**JSON-backed** (`pkg/models/config.go:ResolveInternalConfig()`):
- Insert the block above right before the final `return` statement
- Uses `log` from standard library (already imported in this file)
- Read-lock (`mc.mu.RLock()`) is not affected — `ResolvePeakHourModel()` is a pure method on `ModelConfig`

**DB-backed** (`pkg/store/database/store.go:ResolveInternalConfig()`):
- Same insertion point: right before the final `return` statement
- `modelConfig` is already a `*models.ModelConfig` returned by `m.GetModel()`, which now includes peak-hour fields (from Phase 1 Task 11/12)
- Uses the same `log` package

**Why this approach:**
- Minimal change — single `if` block before each return
- `time.Now()` evaluated at each request time (live pointer benefit)
- Falls back to `InternalModel` seamlessly when not in peak hour
- No changes needed to `race_executor.go` — it already calls `ResolveInternalConfig()`
- Both implementations call `ResolvePeakHourModel()` on the shared `ModelConfig` struct — zero logic duplication

## Detailed Implementation: Task 5 (API Validation)

```go
// In handleModels POST and handleModelDetail PUT, after decoding:
if updatedModel.PeakHourEnabled && !updatedModel.Internal {
    w.WriteHeader(http.StatusBadRequest)
    json.NewEncoder(w).Encode(map[string]string{
        "error": "peak_hour_enabled requires internal upstream to be enabled",
    })
    return
}
```

This provides an immediate, clear 400 response before any further processing. Deeper validation (HH:MM format, timezone format, start ≠ end) runs through `ModelsConfig.Validate()` which is called by `Save()` / `AddModel()` / `UpdateModel()`.

## Constraints
- Backward compatible: models without peak hour config behave identically to before
- API must reject `PeakHourEnabled: true` on non-internal models (400 response)
- All 5 fields must roundtrip through GET/POST/PUT without data loss
- Both `ResolveInternalConfig()` implementations must have identical peak-hour behavior
- `ResolvePeakHourModel()` is a pure method on `ModelConfig` — no interface changes needed
- Read-lock pattern in JSON-backed `ResolveInternalConfig()` must not be broken

## Deliverables
- [ ] UI `Model` struct extended with 5 peak hour fields
- [ ] GET/POST/PUT handlers correctly map all 5 new fields
- [ ] API validates: peak hour only on internal models (400 response)
- [ ] JSON-backed `ResolveInternalConfig()` returns peak model during window
- [ ] DB-backed `ResolveInternalConfig()` returns peak model during window
- [ ] Logging for peak-hour model switches
- [ ] Integration test confirms: peak model during window, normal model outside window
- [ ] No regression for models without peak hour config
