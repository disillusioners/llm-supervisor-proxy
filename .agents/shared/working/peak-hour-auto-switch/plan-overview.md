# Plan Overview: Auto Switch Model on Peak Hour

## Objective
Add peak hour scheduling to model configuration, allowing internal upstream models to automatically switch to a different upstream model name during a configurable time window with timezone support.

## Scope Assessment
**LARGE** — Full-stack feature spanning DB schema migration, Go data model + validation + scheduling logic, **two** implementations of ModelsConfigInterface (JSON-backed and DB-backed), API endpoint mapping, query builder, and Preact frontend UI. Touches ~15 files across 5 layers with cross-cutting concerns.

## Context
- **Project**: llm-supervisor-proxy
- **Working Directory**: `/Users/nguyenminhkha/All/Code/opensource-projects/llm-supervisor-proxy`
- **Requested by**: Leader
- **Key constraint**: Only applies to internal upstream models (`model.Internal == true`)

## Architecture Summary (from exploration)

### Two Implementations of ModelsConfigInterface
The codebase has **TWO** implementations that must both be updated:
1. **`pkg/models/config.go`** — JSON-backed (`ModelsConfig` struct, file-based storage)
2. **`pkg/store/database/store.go`** — DB-backed (`ModelsManager` struct, SQLite/PostgreSQL via query builder)

Both implement `ModelsConfigInterface` including `ResolveInternalConfig()`. The DB-backed implementation is the production path, but both must be kept in sync.

### Current Model → Upstream Resolution Flow
```
Client Request → buildModelList() → race_executor.executeRequest()
    → if model.Internal: executeInternalRequest()
        → ResolveInternalConfig(modelID) → returns (provider, apiKey, baseURL, internalModel)
            → convertToProviderRequest(body, internalModel) → HTTP call with internalModel
```

### Key Interception Point
`ResolveInternalConfig()` exists in **both** `pkg/models/config.go` (JSON) and `pkg/store/database/store.go` (DB). Each returns `modelConfig.InternalModel`. Peak-hour logic must be added to **both**.

### Data Storage (DB-backed path)
- Models stored as **individual columns** in `models` table
- Custom migration framework with numbered `NNN_*.up.sql` files (next is 018)
- Queries built in `querybuilder.go` with dialect-specific SQL (SQLite vs PostgreSQL)
- `GetModel()` in store.go has its **own inline query** — separate from `scanModels()`
- `ConfigSnapshot.ModelsConfig` is a **live pointer** (not snapshotted)

### UTC Offset Format Convention
- **Stored format**: `"+7"`, `"-5"`, `"+5.5"` (signed decimal string parseable by `strconv.ParseFloat`)
- **Display format**: `UTC+7`, `UTC-5`, `UTC+5:30` (in frontend dropdown labels)
- **Conversion**: Frontend sends `"+7"`, backend stores `"+7"`, frontend displays as `UTC+7`
- **Validation**: Must match regex `^[+-]\d{1,2}(\.\d{1,2})?$`

## Phase Index

| Phase | Name | Objective | Dependencies | Est. Time |
|-------|------|-----------|-------------|-----------|
| 1 | Backend Data Model, Scheduling Logic & DB Layer | Add peak hour fields to ModelConfig, time-window detection, validation, DB migration, query builder, and store.go CRUD | None | 5-6h |
| 2 | API & Proxy Integration | Wire peak hour fields into API handlers, integrate scheduling into both ResolveInternalConfig() implementations | Phase 1 | 2-3h |
| 3 | Frontend Peak Hour UI | Add peak hour configuration form in ModelForm.tsx with time inputs, timezone selector, and live status indicator | Phase 2 | 3-4h |

## Risks & Mitigations

| Risk | Impact | Mitigation |
|------|--------|------------|
| Two implementations must stay in sync (JSON-backed + DB-backed) | High | Phase 1 explicitly lists tasks for both; peak-hour logic lives in shared `peak_hours.go` on `ModelConfig` struct, both `ResolveInternalConfig()` call the same method |
| Cross-midnight peak windows (e.g., 22:00-06:00) | Medium | Explicit wraparound logic in `isWithinWindow()` with boundary test cases |
| Same-time edge case (start == end) | Medium | Explicit validation rejects equal start/end in `Validate()` |
| `GetModel()` in store.go has inline query separate from `scanModels()` | High | Both listed explicitly as tasks; recommend extracting `dbRowToModel()` helper |
| Client-side clock inaccuracy for status indicator | Low | Use UTC-based calculation: `new Date().getUTCHours() + parsedOffset` |
| Regression in existing model resolution | High | Phase 2 includes explicit test: non-peak-hour models behave identically |

## Success Criteria
- [ ] Model config supports peak hour window (start, end) with UTC offset
- [ ] Peak hour fields validated: HH:MM format, start ≠ end, UTC offset format, peak model name required when enabled
- [ ] DB migration applies cleanly for both SQLite and PostgreSQL
- [ ] **Both** `ResolveInternalConfig()` implementations (JSON-backed + DB-backed) return peak model during window
- [ ] `querybuilder.go` SQL updated for Insert/Update/Get with 5 new columns
- [ ] `store.go` CRUD operations (`AddModel`, `UpdateModel`, `scanModels`, `GetModel`) handle new columns
- [ ] During peak hours, internal requests route to the peak-hour model instead of primary model
- [ ] Outside peak hours, behavior is identical to current (no regression)
- [ ] Feature only activates for `internal: true` models
- [ ] Frontend allows enabling/configuring peak hour per model with time inputs and timezone selector
- [ ] Frontend shows live UTC-based status indicator

## Tracking
- Created: 2026-03-22
- Last Updated: 2026-03-22
- Status: draft
