# Phase 1: Backend Data Model, Scheduling Logic & DB Layer

## Objective
Extend the Go data model with peak hour fields, implement time-window detection logic with proper validation, create database migrations, update the query builder for all CRUD SQL, and update the DB-backed store layer to read/write peak hour columns.

## Context
- No existing scheduling or time-related fields in the codebase
- Models stored as individual columns in `models` table (not JSON blob)
- Custom migration framework with numbered `NNN_*.up.sql` files (next is 018)
- **Two implementations** of `ModelsConfigInterface`:
  - JSON-backed: `pkg/models/config.go` → `ModelsConfig` struct
  - DB-backed: `pkg/store/database/store.go` → `ModelsManager` struct
- Peak-hour scheduling logic lives on `ModelConfig` struct (shared by both implementations)
- `GetModel()` in store.go has its **own inline query** separate from `scanModels()`
- Queries built in `querybuilder.go` with dialect-specific SQL (SQLite `?` vs PostgreSQL `$N`)

## Data Model Design

### New Fields on ModelConfig

```go
// Peak hour auto-switch configuration (internal upstreams only)
PeakHourEnabled  bool   `json:"peak_hour_enabled,omitempty"`
PeakHourStart    string `json:"peak_hour_start,omitempty"`     // "HH:MM" format, e.g., "13:00"
PeakHourEnd      string `json:"peak_hour_end,omitempty"`       // "HH:MM" format, e.g., "18:00"
PeakHourTimezone string `json:"peak_hour_timezone,omitempty"`  // UTC offset, e.g., "+7", "-5", "+5.5"
PeakHourModel    string `json:"peak_hour_model,omitempty"`     // Upstream model name during peak hours
```

**Design decisions:**
- **Flat fields** (not nested struct) — matches existing pattern in `ModelConfig`
- **HH:MM string format** — simple to parse, human-readable, precise to the minute
- **UTC offset as signed decimal string** (`"+7"`, `"-5"`, `"+5.5"`) — avoids DST complexity; parseable via `strconv.ParseFloat`; stored as-is in DB
- **Separate enabled flag** — allows configuring without activating, easy toggle
- **One window per model** — per requirements: "each model can have one peak hour window"

### Peak Hour Resolution Logic

```go
// ResolvePeakHourModel checks if the current time falls within the model's peak hour window.
// Returns the peak-hour model name if active, empty string otherwise.
// Only effective when PeakHourEnabled is true AND model is Internal.
func (m *ModelConfig) ResolvePeakHourModel(now time.Time) string {
    if !m.PeakHourEnabled || !m.Internal || m.PeakHourModel == "" {
        return ""
    }

    offset, err := parseUTCOffset(m.PeakHourTimezone)
    if err != nil {
        return ""
    }

    startH, startM, err := parseTime(m.PeakHourStart)
    if err != nil {
        return ""
    }

    endH, endM, err := parseTime(m.PeakHourEnd)
    if err != nil {
        return ""
    }

    localNow := now.UTC().Add(time.Duration(offset) * time.Hour)
    currentMinutes := localNow.Hour()*60 + localNow.Minute()

    startMinutes := startH*60 + startM
    endMinutes := endH*60 + endM

    if isWithinWindow(currentMinutes, startMinutes, endMinutes) {
        return m.PeakHourModel
    }
    return ""
}

func isWithinWindow(current, start, end int) bool {
    if start < end {
        // Normal window: e.g., 13:00-18:00
        return current >= start && current < end
    }
    if start > end {
        // Cross-midnight window: e.g., 22:00-06:00
        return current >= start || current < end
    }
    // start == end is rejected by validation, but defensively return false
    return false
}
```

## Tasks

| # | Task | Details | Key Files |
|---|------|---------|-----------|
| 1 | Add peak hour fields to `ModelConfig` struct | Add 5 new fields: `PeakHourEnabled`, `PeakHourStart`, `PeakHourEnd`, `PeakHourTimezone`, `PeakHourModel` | `pkg/models/config.go` |
| 2 | Implement time parsing & window detection helpers | `parseUTCOffset(offset string) (float64, error)`, `parseTime(s string) (h, m int, err error)`, `isWithinWindow(current, start, end int) bool` — handle cross-midnight correctly | `pkg/models/peak_hours.go` (new file) |
| 3 | Implement `ResolvePeakHourModel()` method on `ModelConfig` | Check enabled + internal + valid window, return peak model name or empty string. Use UTC-based calculation | `pkg/models/peak_hours.go` |
| 4 | Add validation in `ModelsConfig.Validate()` | When `PeakHourEnabled`: require `PeakHourStart`, `PeakHourEnd`, `PeakHourTimezone`, `PeakHourModel`; validate HH:MM format (reject `24:00`); validate UTC offset matches `^[+-]\d{1,2}(\.\d{1,2})?$`; reject `PeakHourStart == PeakHourEnd`; reject on non-internal models | `pkg/models/config.go` |
| 5 | Write unit tests for time window detection | Test cases: normal window (13:00-18:00), cross-midnight (22:00-06:00), exact start boundary (active), exact end boundary (inactive), disabled, non-internal, empty fields, timezone offsets (+7, -5, +5.5), same start/end (rejected) | `pkg/models/peak_hours_test.go` (new file) |
| 6 | Create SQLite DB migration | Add 5 new columns to `models` table with defaults | `pkg/store/database/migrations/sqlite/018_add_peak_hours.up.sql` (new) |
| 7 | Create PostgreSQL DB migration | Same columns, PostgreSQL syntax | `pkg/store/database/migrations/postgres/018_add_peak_hours.up.sql` (new) |
| 8 | Register migration in `migrate.go` | Add `{"018", "018_add_peak_hours.up"}` to migrations list | `pkg/store/database/migrations/migrate.go` |
| 9 | Update `querybuilder.go` SQL queries | Update `InsertModel()`, `UpdateModel()`, `GetModelByID()`, `GetAllModels()`, `GetEnabledModels()` — add 5 new columns to SELECT, INSERT, UPDATE for both SQLite and PostgreSQL dialects | `pkg/store/database/querybuilder.go` |
| 10 | Update `dbModelRow` struct in store.go | Add 5 new fields matching new DB columns | `pkg/store/database/store.go` (~line 437) |
| 11 | Update `scanModels()` in store.go | Add 5 new column scans and field mapping in conversion to `ModelConfig` | `pkg/store/database/store.go` (~line 513) |
| 12 | Update `GetModel()` in store.go | Update inline query to SELECT new columns, update Scan() and conversion code | `pkg/store/database/store.go` (~line 573) |
| 13 | Update `AddModel()` in store.go | Add 5 new params to INSERT ExecContext call | `pkg/store/database/store.go` (~line 736) |
| 14 | Update `UpdateModel()` in store.go | Add 5 new params to UPDATE ExecContext call | `pkg/store/database/store.go` (~line 779) |

## Key Files
- `pkg/models/config.go` — ModelConfig struct, Validate()
- `pkg/models/peak_hours.go` — **NEW**: Time parsing, window detection, ResolvePeakHourModel()
- `pkg/models/peak_hours_test.go` — **NEW**: Unit tests
- `pkg/store/database/migrations/sqlite/018_add_peak_hours.up.sql` — **NEW**: SQLite migration
- `pkg/store/database/migrations/postgres/018_add_peak_hours.up.sql` — **NEW**: PostgreSQL migration
- `pkg/store/database/migrations/migrate.go` — Migration registry
- `pkg/store/database/querybuilder.go` — Dialect-specific SQL for all CRUD
- `pkg/store/database/store.go` — dbModelRow, scanModels(), GetModel(), AddModel(), UpdateModel()

## Validation Rules (Detailed)

```go
// In ModelsConfig.Validate(), for each model:
if model.PeakHourEnabled {
    // Peak hour requires internal upstream
    if !model.Internal {
        return fmt.Errorf("model %s: peak_hour_enabled requires internal upstream", model.ID)
    }
    // Required fields
    if model.PeakHourStart == "" || model.PeakHourEnd == "" {
        return fmt.Errorf("model %s: peak_hour_start and peak_hour_end are required when peak hour is enabled", model.ID)
    }
    if model.PeakHourTimezone == "" {
        return fmt.Errorf("model %s: peak_hour_timezone is required when peak hour is enabled", model.ID)
    }
    if model.PeakHourModel == "" {
        return fmt.Errorf("model %s: peak_hour_model is required when peak hour is enabled", model.ID)
    }
    // HH:MM format validation (reject 24:00 and above)
    if err := validateTimeFormat(model.PeakHourStart); err != nil {
        return fmt.Errorf("model %s: invalid peak_hour_start: %w", model.ID, err)
    }
    if err := validateTimeFormat(model.PeakHourEnd); err != nil {
        return fmt.Errorf("model %s: invalid peak_hour_end: %w", model.ID, err)
    }
    // Start != End
    if model.PeakHourStart == model.PeakHourEnd {
        return fmt.Errorf("model %s: peak_hour_start and peak_hour_end cannot be equal", model.ID)
    }
    // UTC offset format: "+7", "-5", "+5.5", etc.
    if err := validateUTCOffset(model.PeakHourTimezone); err != nil {
        return fmt.Errorf("model %s: invalid peak_hour_timezone: %w", model.ID, err)
    }
}
```

## Unit Test Cases (Detailed)

| Test Case | Start | End | Timezone | Current Time (UTC) | Expected |
|-----------|-------|-----|----------|-------------------|----------|
| Normal window inside | 13:00 | 18:00 | +0 | 15:00 UTC | peak_model |
| Normal window boundary start | 13:00 | 18:00 | +0 | 13:00 UTC | peak_model |
| Normal window boundary end | 13:00 | 18:00 | +0 | 18:00 UTC | "" (inactive) |
| Normal window outside | 13:00 | 18:00 | +0 | 10:00 UTC | "" (inactive) |
| Cross-midnight inside (before midnight) | 22:00 | 06:00 | +0 | 23:00 UTC | peak_model |
| Cross-midnight inside (after midnight) | 22:00 | 06:00 | +0 | 03:00 UTC | peak_model |
| Cross-midnight boundary start | 22:00 | 06:00 | +0 | 22:00 UTC | peak_model |
| Cross-midnight boundary end | 22:00 | 06:00 | +0 | 06:00 UTC | "" (inactive) |
| Cross-midnight outside | 22:00 | 06:00 | +0 | 12:00 UTC | "" (inactive) |
| Positive timezone offset | 13:00 | 18:00 | +7 | 08:00 UTC (=15:00 +7) | peak_model |
| Negative timezone offset | 13:00 | 18:00 | -5 | 18:00 UTC (=13:00 -5) | peak_model |
| Fractional offset | 13:00 | 18:00 | +5.5 | 08:30 UTC (=14:00 +5.5) | peak_model |
| Disabled | 13:00 | 18:00 | +0 | 15:00 UTC | "" (disabled) |
| Non-internal model | 13:00 | 18:00 | +0 | 15:00 UTC | "" (not internal) |
| Empty peak model | 13:00 | 18:00 | +0 | 15:00 UTC | "" (no model set) |
| Invalid time format | 25:00 | 18:00 | +0 | — | validation error |
| 24:00 rejected | 24:00 | 18:00 | +0 | — | validation error |
| Same start/end | 13:00 | 13:00 | +0 | — | validation error |

## Constraints
- Peak hour only applies to `Internal: true` models — validation enforces this
- Timezone uses fixed UTC offset (`"+7"`, `"-5"`, `"+5.5"`), NOT IANA timezone names
- `PeakHourStart` and `PeakHourEnd` use `"HH:MM"` 24-hour format, range 00:00-23:59
- Same start/end time is explicitly rejected
- Time format `24:00` is explicitly rejected
- All new fields use `omitempty` JSON tags
- DB columns have sensible defaults (enabled=0/false, strings empty)
- Both SQLite and PostgreSQL migrations must be identical in semantics

## Deliverables
- [ ] `ModelConfig` struct extended with 5 peak hour fields
- [ ] `peak_hours.go` with time parsing, UTC offset parsing, window detection, and `ResolvePeakHourModel()` method
- [ ] Validation rules in `Validate()` with all edge cases covered
- [ ] Unit tests with 17+ test cases covering all scenarios
- [ ] SQLite migration file with 5 new columns
- [ ] PostgreSQL migration file with 5 new columns
- [ ] Migration registered in `migrate.go`
- [ ] `querybuilder.go` updated: all 5 query methods for both dialects
- [ ] `store.go` dbModelRow struct extended
- [ ] `store.go` scanModels() updated with new columns
- [ ] `store.go` GetModel() updated with inline query and conversion
- [ ] `store.go` AddModel() updated with new parameters
- [ ] `store.go` UpdateModel() updated with new parameters
