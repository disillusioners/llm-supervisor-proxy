# Phase 1: Backend Data Model & Persistence

## Objective
Add `secondary_upstream_model` field to the model configuration, database schema, query builder, and store layer. This is the data foundation that Phases 2 and 3 depend on.

## Coupling
- **Depends on**: None (root phase)
- **Coupling type**: —
- **Shared files with other phases**: `pkg/models/config.go` (ModelConfig struct), `pkg/store/database/store.go` (ModelsManager)
- **Shared APIs/interfaces**: `ModelsConfigInterface.ResolveInternalConfig()`, `ModelConfig` struct
- **Why this coupling**: All phases depend on the data model definition

## Context
The ModelConfig struct currently has:
- `InternalModel` — the upstream provider model name (e.g., "glm-5.0")
- `FallbackChain` — proxy model IDs to fall back to
- `PeakHourModel` — upstream model to use during peak hours

We add `SecondaryUpstreamModel` — another upstream model name for retry, in the same family as PeakHourModel.

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add field to ModelConfig struct | Add `SecondaryUpstreamModel string` with json tag `secondary_upstream_model,omitempty` | `pkg/models/config.go` |
| 2 | Add validation for secondary upstream model | Only validate when `internal=true` and field is non-empty. Must be a valid model name (non-empty string). | `pkg/models/config.go` (Validate method) |
| 3 | Create DB migration 021 | Add `secondary_upstream_model TEXT NOT NULL DEFAULT ''` to models table (SQLite + PostgreSQL) | `pkg/store/database/migrations/sqlite/021_add_secondary_upstream_model.up.sql`, `pkg/store/database/migrations/postgres/021_add_secondary_upstream_model.up.sql` |
| 4 | Register migration | Add `{"021", "021_add_secondary_upstream_model.up"}` to migrations list | `pkg/store/database/migrate.go` |
| 5 | Update query builder | Add `secondary_upstream_model` to all model queries: InsertModel, UpdateModel, GetModelByID, GetAllModels, GetEnabledModels | `pkg/store/database/querybuilder.go` |
| 6 | Update dbModelRow struct | Add `SecondaryUpstreamModel string` field | `pkg/store/database/store.go` (dbModelRow) |
| 7 | Update scanModels/GetModel | Add `coalesce(secondary_upstream_model, '')` to SELECT and scan into dbModelRow. Map to ModelConfig.SecondaryUpstreamModel | `pkg/store/database/store.go` |
| 8 | Update AddModel | Include `secondary_upstream_model` in INSERT query | `pkg/store/database/store.go` |
| 9 | Update UpdateModel | Include `secondary_upstream_model` in UPDATE query | `pkg/store/database/store.go` |
| 10 | Update dummy scan count | The duplicate/exist checks use `row.Scan(&dummy, &dummy, ...)` — increase count by 1 (from 17 to 18) in AddModel, UpdateModel, RemoveModel | `pkg/store/database/store.go` |
| 11 | Update JSON config backend | If applicable, ensure JSON file serialization includes the new field (it will auto-work via json tag, but verify) | `pkg/models/config.go` |
| 12 | Update API handler model struct & mapping in server.go | Add `SecondaryUpstreamModel` to the `Model` struct in server.go (lines ~28-46). Update 3 manual mapping sites: GET handler (~line 389), POST handler (~line 447), PUT handler (~line 533) to include `SecondaryUpstreamModel` | `pkg/ui/server.go` |
| 13 | Add API validation for secondary_upstream_model in server.go | Add validation in POST and PUT handlers that `secondary_upstream_model` requires `internal=true` (same pattern as `peak_hour_enabled` validation at ~lines 421, 509) | `pkg/ui/server.go` |

## Key Files
- `pkg/models/config.go` — ModelConfig struct, Validate()
- `pkg/store/database/migrate.go` — Migration registration
- `pkg/store/database/querybuilder.go` — SQL queries
- `pkg/store/database/store.go` — ModelsManager CRUD
- `pkg/store/database/migrations/sqlite/021_*.sql` — SQLite migration
- `pkg/store/database/migrations/postgres/021_*.sql` — PostgreSQL migration
- `pkg/ui/server.go` — API handler Model struct, field mapping, validation

## Detailed Implementation Notes

### Task 1: ModelConfig struct change
```go
// In ModelConfig struct, add after PeakHourModel:
SecondaryUpstreamModel string `json:"secondary_upstream_model,omitempty"` // Alternative upstream model for retries (internal only)
```

### Task 2: Validation
In `Validate()` method, after peak hour validation block:
```go
// Validate secondary upstream model
if model.SecondaryUpstreamModel != "" {
    if !model.Internal {
        return fmt.Errorf("model %s: secondary_upstream_model requires internal to be true", model.ID)
    }
}
```
Note: We DON'T require secondary to be different from InternalModel — user might want it the same (no-op but valid config).

### Task 3: Migration SQL
SQLite:
```sql
ALTER TABLE models ADD COLUMN secondary_upstream_model TEXT NOT NULL DEFAULT '';
```
PostgreSQL:
```sql
ALTER TABLE models ADD COLUMN secondary_upstream_model TEXT NOT NULL DEFAULT '';
```

### Task 5: Query Builder Changes
Every query that touches the models table needs `coalesce(secondary_upstream_model, '')` added to the SELECT clause, and the field added to INSERT/UPDATE parameter lists. Follow the exact pattern used by `peak_hour_model`.

### Task 7: Scan Mapping
In `scanModels()` and `GetModel()`, add scan target and mapping:
```go
// In Scan():
&dbModel.SecondaryUpstreamModel,

// In mapping:
SecondaryUpstreamModel: dbModel.SecondaryUpstreamModel,
```

### Task 12: API Handler Struct & Mapping (server.go)
The model API handlers in `pkg/ui/server.go` use a separate `Model` struct (NOT `models.ModelConfig`) for serialization. This requires manual field mapping:

**Struct update** (~lines 28-46):
```go
type Model struct {
    // ... existing fields ...
    SecondaryUpstreamModel string `json:"secondary_upstream_model,omitempty"`
}
```

**GET handler mapping** (~line 389):
```go
SecondaryUpstreamModel: mc.SecondaryUpstreamModel,
```

**POST handler mapping** (~line 447):
```go
SecondaryUpstreamModel: newModel.SecondaryUpstreamModel,
```

**PUT handler mapping** (~line 533):
```go
SecondaryUpstreamModel: updatedModel.SecondaryUpstreamModel,
```

### Task 13: API Validation (server.go)
In the POST (~line 421) and PUT (~line 509) handlers, add validation that mirrors `pkg/models/config.go` validation. Follow the same pattern as the existing `peak_hour_enabled` validation:

```go
if newModel.SecondaryUpstreamModel != "" && !newModel.Internal {
    http.Error(w, "secondary_upstream_model requires internal to be true", http.StatusBadRequest)
    return
}
```

### Task 10: Dummy Scan Count
Search for `row.Scan(&dummy, &dummy, ...)` patterns in store.go. Only 3 locations use the dummy-scan pattern for existence checks: `AddModel` (line ~817), `UpdateModel` (line ~868), `RemoveModel` (line ~909). Each has 17 `&dummy` args (corresponding to 17 columns). Add one more to make 18. Note: `GetTruncateParams` and `GetFallbackChain` scan into real `dbModelRow` fields — they are NOT dummy scans and don't need updating here (their scan lists are updated in Task 7).

## Constraints
- Must be backward compatible: existing models without this field must work perfectly
- Field is optional (empty string = not configured = current behavior)
- Only meaningful for `internal=true` models
- Default value in DB is empty string

## Deliverables
- [ ] ModelConfig has `SecondaryUpstreamModel` field
- [ ] DB migration 021 created and registered
- [ ] Query builder updated for all CRUD operations
- [ ] Store layer reads/writes the new field
- [ ] Validation rejects secondary on non-internal models
- [ ] server.go Model struct updated with field mapping in GET/POST/PUT
- [ ] server.go API validation rejects secondary without internal=true
- [ ] Existing tests pass
